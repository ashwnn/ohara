// Package store implements the persistent memory engine for Ohara.
//
// It uses SQLite with FTS5 full-text search to store and retrieve
// memories from AI coding sessions. This is the core of Ohara —
// everything else (HTTP server, MCP server, CLI, plugins) talks to this.
package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ashwnn/ohara/internal/token"
	"github.com/ashwnn/ohara/internal/util"
	sqlite "modernc.org/sqlite"
)

var openDB = sql.Open

// sqliteConstraintForeignKey is the extended SQLite result code for a foreign-key
// constraint violation (SQLITE_CONSTRAINT_FOREIGNKEY = 787).
// See https://www.sqlite.org/rescode.html#constraint_foreignkey
const sqliteConstraintForeignKey = 787

// Sentinel errors returned by delete operations so callers can use errors.Is.
var (
	ErrSessionNotFound = errors.New("session not found")
	ErrPromptNotFound  = errors.New("prompt not found")
)

type Session struct {
	ID        string  `json:"id"`
	Project   string  `json:"project"`
	Directory string  `json:"directory"`
	StartedAt string  `json:"started_at"`
	EndedAt   *string `json:"ended_at,omitempty"`
	Summary   *string `json:"summary,omitempty"`
}

type SessionSummary struct {
	ID          string  `json:"id"`
	Project     string  `json:"project"`
	StartedAt   string  `json:"started_at"`
	EndedAt     *string `json:"ended_at,omitempty"`
	Summary     *string `json:"summary,omitempty"`
	MemoryCount int     `json:"memory_count"`
}

type Stats struct {
	TotalSessions int      `json:"total_sessions"`
	TotalMemories int      `json:"total_memories"`
	TotalPrompts  int      `json:"total_prompts"`
	Projects      []string `json:"projects"`
}

type Prompt struct {
	ID        int64  `json:"id"`
	SyncID    string `json:"sync_id"`
	SessionID string `json:"session_id"`
	Content   string `json:"content"`
	Project   string `json:"project,omitempty"`
	CreatedAt string `json:"created_at"`
}

type AddPromptParams struct {
	SessionID string `json:"session_id"`
	Content   string `json:"content"`
	Project   string `json:"project,omitempty"`
}

const (
	DefaultSyncTargetKey = "cloud"

	SyncLifecycleIdle     = "idle"
	SyncLifecyclePending  = "pending"
	SyncLifecycleRunning  = "running"
	SyncLifecycleHealthy  = "healthy"
	SyncLifecycleDegraded = "degraded"

	SyncEntitySession = "session"
	SyncEntityPrompt  = "prompt"

	SyncOpUpsert = "upsert"
	SyncOpDelete = "delete"

	SyncSourceLocal  = "local"
	SyncSourceRemote = "remote"
)

type SyncState struct {
	TargetKey           string  `json:"target_key"`
	Lifecycle           string  `json:"lifecycle"`
	LastEnqueuedSeq     int64   `json:"last_enqueued_seq"`
	LastAckedSeq        int64   `json:"last_acked_seq"`
	LastPulledSeq       int64   `json:"last_pulled_seq"`
	ConsecutiveFailures int     `json:"consecutive_failures"`
	BackoffUntil        *string `json:"backoff_until,omitempty"`
	LeaseOwner          *string `json:"lease_owner,omitempty"`
	LeaseUntil          *string `json:"lease_until,omitempty"`
	LastError           *string `json:"last_error,omitempty"`
	UpdatedAt           string  `json:"updated_at"`
}

type SyncMutation struct {
	Seq        int64   `json:"seq"`
	TargetKey  string  `json:"target_key"`
	Entity     string  `json:"entity"`
	EntityKey  string  `json:"entity_key"`
	Op         string  `json:"op"`
	Payload    string  `json:"payload"`
	Source     string  `json:"source"`
	Project    string  `json:"project"`
	OccurredAt string  `json:"occurred_at"`
	AckedAt    *string `json:"acked_at,omitempty"`
}

// EnrolledProject represents a project enrolled for cloud sync.
type EnrolledProject struct {
	Project    string `json:"project"`
	EnrolledAt string `json:"enrolled_at"`
}

type syncSessionPayload struct {
	ID        string  `json:"id"`
	Project   string  `json:"project"`
	Directory string  `json:"directory"`
	EndedAt   *string `json:"ended_at,omitempty"`
	Summary   *string `json:"summary,omitempty"`
}

type syncPromptPayload struct {
	SyncID    string  `json:"sync_id"`
	SessionID string  `json:"session_id"`
	Content   string  `json:"content"`
	Project   *string `json:"project,omitempty"`
}

// ExportData is the full serializable dump of the Ohara database.
type ExportData struct {
	Version    string    `json:"version"`
	ExportedAt string    `json:"exported_at"`
	Sessions   []Session `json:"sessions"`
	Prompts    []Prompt  `json:"prompts"`
}

// Memory items are the curated, typed, versioned memory records defined in the
// Ohara v2 spec. They replaced the legacy observations system entirely.

const (
	// MemoryKind values — the 10 typed categories from the spec.
	MemoryKindIdentity       = "identity"
	MemoryKindUserPreference = "user_preference"
	MemoryKindGlossary       = "glossary"
	MemoryKindDecision       = "decision"
	MemoryKindPattern        = "pattern"
	MemoryKindBugfix         = "bugfix"
	MemoryKindDiscovery      = "discovery"
	MemoryKindProcedure      = "procedure"
	MemoryKindConfig         = "config"
	MemoryKindPostmortem     = "postmortem"
)

// ValidMemoryKinds is the set of all valid memory kind values.
var ValidMemoryKinds = map[string]bool{
	MemoryKindIdentity:       true,
	MemoryKindUserPreference: true,
	MemoryKindGlossary:       true,
	MemoryKindDecision:       true,
	MemoryKindPattern:        true,
	MemoryKindBugfix:         true,
	MemoryKindDiscovery:      true,
	MemoryKindProcedure:      true,
	MemoryKindConfig:         true,
	MemoryKindPostmortem:     true,
}

// MemoryScope values — global or project-scoped.
const (
	MemoryScopeGlobal  = "global"
	MemoryScopeProject = "project"
)

// MemoryStatus values.
const (
	MemoryStatusActive     = "active"
	MemoryStatusArchived   = "archived"
	MemoryStatusSuperseded = "superseded"
	MemoryStatusCandidate  = "candidate"
)

// MemoryItem represents a curated, typed, versioned memory record.
// This is the primary memory type in the Ohara v2 spec.
type MemoryItem struct {
	ID           int64    `json:"id"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
	ProjectID    string   `json:"project_id"`
	ActorID      string   `json:"actor_id"`
	Kind         string   `json:"kind"`
	Scope        string   `json:"scope"`
	Title        string   `json:"title"`
	Body         string   `json:"body"`
	Tags         []string `json:"tags"`
	Source       string   `json:"source"`
	Status       string   `json:"status"`
	SupersededBy *int64   `json:"superseded_by,omitempty"`
	ExpiresAt    *string  `json:"expires_at,omitempty"`

	// P0 fields (migrations 001-003)
	Domain         string `json:"domain,omitempty"`          // domain/namespace scoping
	EvidenceJSON   string `json:"evidence_json,omitempty"`   // { "commit": "...", "issue": "...", "file": "...", "url": "..." }
	AppliesToJSON  string `json:"applies_to_json,omitempty"` // { "files": [...], "paths": [...], "commands": [...] }
	RelatedJSON    string `json:"related_json,omitempty"`    // { "relates_to": [...], "supersedes": [...], "derived_from": [...] }
	Classification string `json:"classification,omitempty"`  // foundational | tactical | observational

	// P1 fields (migrations 004-007)
	AccessCount  int     `json:"access_count,omitempty"`  // number of times retrieved
	LastAccessed *string `json:"last_accessed,omitempty"` // last retrieval timestamp
	ValidFrom    *string `json:"valid_from,omitempty"`    // when knowledge became true
	ValidTo      *string `json:"valid_to,omitempty"`      // when knowledge stopped being true
	SupersededAt *string `json:"superseded_at,omitempty"`
	SessionID    string  `json:"session_id,omitempty"`  // session that created this memory
	TrustLevel   string  `json:"trust_level,omitempty"` // user | system | tool | untrusted
	IngestedAt   string  `json:"ingested_at,omitempty"` // when record was ingested (immutable — never updated)
	WrittenBy    string  `json:"written_by,omitempty"`  // user | agent | consolidation | import | system

	// P2 fields
	TriggerCondition string `json:"trigger_condition,omitempty"` // trigger for procedure kind

	// P3 fields
	UtilityWeight    float64 `json:"utility_weight,omitempty"`    // RL-influenced weight
	ConsolidatedFrom string  `json:"consolidated_from,omitempty"` // comma-separated source obs_ids

	// Computed at query time — not stored
	RelevanceScore float64 `json:"relevance_score,omitempty"` // FTS composite score (0 for non-search queries)
}

// VisibleTrustLevelsForLowTrust defines which trust levels are visible to
// low-trust principals (RoleRead-only). Higher-trust memories ("user", "system")
// are filtered out so that low-trust remote clients cannot read sensitive data
// created by users or the system itself.
var VisibleTrustLevelsForLowTrust = map[string]bool{
	"tool":      true,
	"untrusted": true,
}

// Redacted returns a copy of the memory item with sensitive or internal
// metadata fields removed. This is used when serving responses to low-trust
// principals (RoleRead-only) so they cannot access evidence URLs, related
// memory IDs, or full body content.
func (m MemoryItem) Redacted() MemoryItem {
	if len(m.Body) > 200 {
		m.Body = m.Body[:200] + "..."
	}
	m.EvidenceJSON = ""
	m.RelatedJSON = ""
	m.AppliesToJSON = ""
	return m
}

// FilterByTrustLevel filters and redacts memory items based on the caller's
// trust level. When isLowTrust is true, only memories with trust levels in
// VisibleTrustLevelsForLowTrust are returned, and each is redacted.
// When isLowTrust is false, all items are returned unchanged.
func FilterByTrustLevel(items []MemoryItem, isLowTrust bool) []MemoryItem {
	if !isLowTrust || len(items) == 0 {
		return items
	}
	result := make([]MemoryItem, 0, len(items))
	for _, m := range items {
		if VisibleTrustLevelsForLowTrust[m.TrustLevel] {
			result = append(result, m.Redacted())
		}
	}
	if result == nil {
		result = []MemoryItem{}
	}
	return result
}

// FilterByTrustLevelPack applies FilterByTrustLevel to the MemoryItems inside
// a PackResult, returning a copy with filtered/redacted items.
func FilterByTrustLevelPack(pr *PackResult, isLowTrust bool) *PackResult {
	if pr == nil || !isLowTrust {
		return pr
	}
	pr.MemoryItems = FilterByTrustLevel(pr.MemoryItems, isLowTrust)
	return pr
}

// MemoryRelation represents a typed, directional link between two memory items.
// This implements the relation graph from Ohara v2 spec P2 (Section 6.3).
type MemoryRelation struct {
	ID        int64  `json:"id"`
	FromID    int64  `json:"from_id"`  // source memory item ID
	ToID      int64  `json:"to_id"`    // target memory item ID
	Relation  string `json:"relation"` // one of the valid relation types
	CreatedAt string `json:"created_at"`
}

// ValidRelationTypes maps relation type strings to their display names.
const (
	RelationCaused      = "caused"
	RelationResolves    = "resolves"
	RelationSupersedes  = "supersedes"
	RelationRelatedTo   = "related_to"
	RelationImplements  = "implements"
	RelationContradicts = "contradicts"
)

// ValidRelations is the set of all valid memory relation type values.
var ValidRelations = map[string]bool{
	RelationCaused:      true,
	RelationResolves:    true,
	RelationSupersedes:  true,
	RelationRelatedTo:   true,
	RelationImplements:  true,
	RelationContradicts: true,
}

// RelationLabel returns a human-readable label for a relation type.
func RelationLabel(relation string) string {
	labels := map[string]string{
		RelationCaused:      "caused",
		RelationResolves:    "resolves",
		RelationSupersedes:  "supersedes",
		RelationRelatedTo:   "related to",
		RelationImplements:  "implements",
		RelationContradicts: "contradicts",
	}
	if label, ok := labels[relation]; ok {
		return label
	}
	return relation
}

// MemoryRevision represents an append-only version history entry for a memory item.
// Tracks individual field changes with reason.
type MemoryRevision struct {
	ID       int64   `json:"id"`
	MemoryID int64   `json:"memory_id"`
	TS       string  `json:"ts"`
	ActorID  string  `json:"actor_id"`
	Field    string  `json:"field"`
	OldValue *string `json:"old_value,omitempty"`
	NewValue *string `json:"new_value,omitempty"`
	Reason   *string `json:"reason,omitempty"`
}

// MemoryItemStatus constants for body size limits per kind (spec-defined).
var memoryBodyLimits = map[string]int{
	MemoryKindIdentity:       500,
	MemoryKindUserPreference: 300,
	MemoryKindGlossary:       200,
	MemoryKindDecision:       1000,
	MemoryKindPattern:        500,
	MemoryKindBugfix:         1000,
	MemoryKindDiscovery:      500,
	MemoryKindProcedure:      2000,
	MemoryKindConfig:         500,
	MemoryKindPostmortem:     2000,
}

// MemoryBodyLimit returns the max body length for a given kind, or 0 if unlimited.
func MemoryBodyLimit(kind string) int {
	if limit, ok := memoryBodyLimits[kind]; ok {
		return limit
	}
	return 0
}

// TruncateBodyToTokenLimit truncates text to fit within the token budget for the
// given memory kind. It uses token.CountStrict() for accurate BPE token counting,
// with a safe fallback if the tokenizer is unavailable. Returns the original text
// if it already fits within the limit.
//
// The returned body includes "... [truncated]" at the end if truncation was applied.
func TruncateBodyToTokenLimit(text string, kind string) (body string) {
	limit := MemoryBodyLimit(kind)
	if limit <= 0 {
		return text
	}

	if token.CountStrict(text) <= limit {
		return text
	}

	// Binary search for the maximum character prefix that fits within the limit.
	// token.CountStrict is monotonically non-decreasing, so binary search is valid.
	// Use a generous safety margin (5 tokens) to account for tokenizer estimate error
	// and the overhead of the "... [truncated]" suffix (~3 tokens).
	safetyMargin := 5
	effectiveLimit := limit - safetyMargin
	if effectiveLimit < 1 {
		effectiveLimit = 1
	}

	lo, hi := 0, len(text)
	for lo < hi {
		mid := (lo + hi + 1) / 2 // round up to bias toward accepting more
		if token.CountStrict(text[:mid]) <= effectiveLimit {
			lo = mid
		} else {
			hi = mid - 1
		}
	}

	if lo == 0 {
		return text
	}

	return text[:lo] + "... [truncated]"
}

// Memory kind TTLs (spec-defined expiry windows from creation).
var memoryKindTTL = map[string]int{
	MemoryKindDiscovery:  90, // discovery memories expire after 90 days
	MemoryKindPostmortem: 30, // postmortem memories expire after 30 days
}

// MemoryTTL returns the TTL in days for a given memory kind, or 0 if the kind never expires.
func MemoryTTL(kind string) int {
	if ttl, ok := memoryKindTTL[kind]; ok {
		return ttl
	}
	return 0
}

// MemoryExpiresAt computes the expires_at timestamp for a newly created memory.
// Returns empty string if the kind has no expiry (nil pointer in SQL).
func MemoryExpiresAt(kind string) *string {
	ttl := MemoryTTL(kind)
	if ttl <= 0 {
		return nil
	}
	expires := time.Now().UTC().AddDate(0, 0, ttl).Format(time.RFC3339)
	return &expires
}

// PackResult is the output of building a context pack.
type PackResult struct {
	Pack         string             `json:"pack"`
	TokenCount   int                `json:"token_count"`
	Truncated    bool               `json:"truncated"`
	ItemCount    int                `json:"item_count"`
	MemoryItems  []MemoryItem       `json:"memory_items,omitempty"`
	Explain      []PackExplainEntry `json:"explain,omitempty"`
	BudgetTokens int                `json:"budget_tokens"`
}

// PackExplainEntry captures score components for pack assembly decisions.
type PackExplainEntry struct {
	MemoryID        int64              `json:"memory_id"`
	Title           string             `json:"title"`
	Kind            string             `json:"kind"`
	Scope           string             `json:"scope"`
	Classification  string             `json:"classification"`
	Score           float64            `json:"score"`
	ScoreComponents map[string]float64 `json:"score_components"`
	TokenEstimate   int                `json:"token_estimate"`
	Included        bool               `json:"included"`
	Reason          string             `json:"reason"`
}

// PackParams holds the parameters for building a context pack.
type PackParams struct {
	ProjectID    string `json:"project_id"`
	SessionID    string `json:"session_id,omitempty"`
	BudgetTokens int    `json:"budget_tokens"`
	Domain       string `json:"domain,omitempty"`
	Asof         string `json:"asof,omitempty"`
	Explain      bool   `json:"explain,omitempty"`
}

// AddMemoryParams holds the parameters for creating a new memory item.
type AddMemoryParams struct {
	ProjectID string   `json:"project_id"`
	Kind      string   `json:"kind"`
	Scope     string   `json:"scope"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Tags      []string `json:"tags"`
	Source    string   `json:"source"`
	ActorID   string   `json:"actor_id"`

	// P0 fields
	Domain        string `json:"domain,omitempty"`
	EvidenceJSON  string `json:"evidence_json,omitempty"`
	AppliesToJSON string `json:"applies_to_json,omitempty"`
	RelatedJSON   string `json:"related_json,omitempty"`

	// P1 fields
	SessionID        string  `json:"session_id,omitempty"`
	TrustLevel       string  `json:"trust_level,omitempty"`
	Classification   string  `json:"classification,omitempty"`
	WrittenBy        string  `json:"written_by,omitempty"`
	ExpiresAt        string  `json:"expires_at,omitempty"`
	TriggerCondition string  `json:"trigger_condition,omitempty"`
	UtilityWeight    float64 `json:"utility_weight,omitempty"`
	ConsolidatedFrom string  `json:"consolidated_from,omitempty"`
}

// UpdateMemoryParams holds the parameters for updating a memory item.
type UpdateMemoryParams struct {
	Title        *string  `json:"title,omitempty"`
	Body         *string  `json:"body,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	Status       *string  `json:"status,omitempty"`
	SupersededBy *int64   `json:"superseded_by,omitempty"`
	Reason       string   `json:"reason,omitempty"`
	ActorID      string   `json:"actor_id"`

	// P0 fields
	Domain         *string `json:"domain,omitempty"`
	EvidenceJSON   *string `json:"evidence_json,omitempty"`
	AppliesToJSON  *string `json:"applies_to_json,omitempty"`
	RelatedJSON    *string `json:"related_json,omitempty"`
	Classification *string `json:"classification,omitempty"`

	// P1 fields
	SessionID        *string  `json:"session_id,omitempty"`
	TrustLevel       *string  `json:"trust_level,omitempty"`
	WrittenBy        *string  `json:"written_by,omitempty"`
	ExpiresAt        *string  `json:"expires_at,omitempty"`
	TriggerCondition *string  `json:"trigger_condition,omitempty"`
	UtilityWeight    *float64 `json:"utility_weight,omitempty"`
}

type Config struct {
	DataDir           string
	MaxContextResults int
	MaxSearchResults  int
	DedupeWindow      time.Duration
	RetrievalMode     string
	EmbeddingBackend  string
	EmbeddingModel    string
	EmbeddingDim      int
	HybridAlpha       float64
	OllamaURL         string
}

func DefaultConfig() (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, fmt.Errorf("ohara: determine home directory: %w", err)
	}
	cfg := Config{
		DataDir:           filepath.Join(home, ".local/share/ohara"),
		MaxContextResults: 20,
		MaxSearchResults:  20,
		DedupeWindow:      15 * time.Minute,
		RetrievalMode:     "fts5",
		EmbeddingBackend:  "ollama",
		EmbeddingModel:    "nomic-embed-text",
		EmbeddingDim:      768,
		HybridAlpha:       0.6,
		OllamaURL:         "http://localhost:11434",
	}
	if mode := strings.TrimSpace(os.Getenv("OHARA_RETRIEVAL_MODE")); mode != "" {
		cfg.RetrievalMode = mode
	}
	if backend := strings.TrimSpace(os.Getenv("OHARA_EMBEDDING_BACKEND")); backend != "" {
		cfg.EmbeddingBackend = backend
	}
	if model := strings.TrimSpace(os.Getenv("OHARA_EMBEDDING_MODEL")); model != "" {
		cfg.EmbeddingModel = model
	}
	if dimStr := strings.TrimSpace(os.Getenv("OHARA_EMBEDDING_DIM")); dimStr != "" {
		if dim, convErr := strconv.Atoi(dimStr); convErr == nil && dim > 0 {
			cfg.EmbeddingDim = dim
		}
	}
	if alphaStr := strings.TrimSpace(os.Getenv("OHARA_HYBRID_ALPHA")); alphaStr != "" {
		if alpha, convErr := strconv.ParseFloat(alphaStr, 64); convErr == nil && alpha >= 0 && alpha <= 1 {
			cfg.HybridAlpha = alpha
		}
	}
	if ollamaURL := strings.TrimSpace(os.Getenv("OHARA_OLLAMA_URL")); ollamaURL != "" {
		cfg.OllamaURL = ollamaURL
	}
	return cfg, nil
}

// FallbackConfig returns a Config with the given DataDir and default values.
// Use this when DefaultConfig fails and you have resolved the home directory
// through alternative means.
func FallbackConfig(dataDir string) Config {
	return Config{
		DataDir:           dataDir,
		MaxContextResults: 20,
		MaxSearchResults:  20,
		DedupeWindow:      15 * time.Minute,
		RetrievalMode:     "fts5",
		EmbeddingBackend:  "ollama",
		EmbeddingModel:    "nomic-embed-text",
		EmbeddingDim:      768,
		HybridAlpha:       0.6,
		OllamaURL:         "http://localhost:11434",
	}
}

type Store struct {
	db       *sql.DB
	cfg      Config
	hooks    storeHooks
	jobsStop chan struct{}
	jobsDone chan struct{}
	closeMu  sync.Mutex
	closed   bool
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

type queryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

type rowScanner interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close() error
}

type sqlRowScanner struct {
	rows *sql.Rows
}

func (r sqlRowScanner) Next() bool {
	return r.rows.Next()
}

func (r sqlRowScanner) Scan(dest ...any) error {
	return r.rows.Scan(dest...)
}

func (r sqlRowScanner) Err() error {
	return r.rows.Err()
}

func (r sqlRowScanner) Close() error {
	return r.rows.Close()
}

type storeHooks struct {
	exec    func(db execer, query string, args ...any) (sql.Result, error)
	query   func(db queryer, query string, args ...any) (*sql.Rows, error)
	queryIt func(db queryer, query string, args ...any) (rowScanner, error)
	beginTx func(db *sql.DB) (*sql.Tx, error)
	commit  func(tx *sql.Tx) error
}

func defaultStoreHooks() storeHooks {
	return storeHooks{
		exec: func(db execer, query string, args ...any) (sql.Result, error) {
			return db.Exec(query, args...)
		},
		query: func(db queryer, query string, args ...any) (*sql.Rows, error) {
			return db.Query(query, args...)
		},
		queryIt: func(db queryer, query string, args ...any) (rowScanner, error) {
			rows, err := db.Query(query, args...)
			if err != nil {
				return nil, err
			}
			return sqlRowScanner{rows: rows}, nil
		},
		beginTx: func(db *sql.DB) (*sql.Tx, error) {
			return db.Begin()
		},
		commit: func(tx *sql.Tx) error {
			return tx.Commit()
		},
	}
}

func (s *Store) execHook(db execer, query string, args ...any) (sql.Result, error) {
	if s.hooks.exec != nil {
		return s.hooks.exec(db, query, args...)
	}
	return db.Exec(query, args...)
}

// Exec runs a query against the database. Exposed so packages like maintain
// can use the Store as a generic DB interface.
func (s *Store) Exec(query string, args ...any) (sql.Result, error) {
	return s.execHook(s.db, query, args...)
}

// Query runs a query and returns rows. Exposed so packages like maintain
// can use the Store as a generic DB interface.
func (s *Store) Query(query string, args ...any) (*sql.Rows, error) {
	return s.queryHook(s.db, query, args...)
}

// QueryRow runs a query and returns a single row. Exposed so packages like
// maintain can use the Store as a generic DB interface.
func (s *Store) QueryRow(query string, args ...any) *sql.Row {
	return s.db.QueryRow(query, args...)
}

func (s *Store) queryHook(db queryer, query string, args ...any) (*sql.Rows, error) {
	if s.hooks.query != nil {
		return s.hooks.query(db, query, args...)
	}
	return db.Query(query, args...)
}

func (s *Store) queryItHook(db queryer, query string, args ...any) (rowScanner, error) {
	if s.hooks.queryIt != nil {
		return s.hooks.queryIt(db, query, args...)
	}
	rows, err := s.queryHook(db, query, args...)
	if err != nil {
		return nil, err
	}
	return sqlRowScanner{rows: rows}, nil
}

func (s *Store) beginTxHook() (*sql.Tx, error) {
	if s.hooks.beginTx != nil {
		return s.hooks.beginTx(s.db)
	}
	return s.db.Begin()
}

func (s *Store) commitHook(tx *sql.Tx) error {
	if s.hooks.commit != nil {
		return s.hooks.commit(tx)
	}
	return tx.Commit()
}

func New(cfg Config) (*Store, error) {
	if !filepath.IsAbs(cfg.DataDir) {
		return nil, fmt.Errorf("ohara: data directory must be an absolute path, got %q — set OHARA_DATA_DIR or ensure your home directory is resolvable", cfg.DataDir)
	}
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("ohara: create data dir: %w", err)
	}

	dbPath := filepath.Join(cfg.DataDir, "ohara.db")
	db, err := openDB("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("ohara: open database: %w", err)
	}

	// SQLite performance pragmas
	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA foreign_keys = ON",
		// WAL auto-checkpoint at 1000 pages — per Ohara v2 spec Phase 2.
		// This ensures the WAL file doesn't grow unbounded on a busy machine.
		"PRAGMA wal_autocheckpoint = 1000",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			return nil, fmt.Errorf("ohara: pragma %q: %w", p, err)
		}
	}

	s := &Store{db: db, cfg: cfg, hooks: defaultStoreHooks()}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("ohara: migration: %w", err)
	}
	if err := s.repairEnrolledProjectSyncMutations(); err != nil {
		return nil, fmt.Errorf("ohara: repair enrolled sync journal: %w", err)
	}

	s.startJobWorker()

	return s, nil
}

func (s *Store) Close() error {
	s.closeMu.Lock()
	if s.closed {
		s.closeMu.Unlock()
		return nil
	}
	s.closed = true
	jobsStop := s.jobsStop
	jobsDone := s.jobsDone
	s.closeMu.Unlock()

	if jobsStop != nil {
		close(jobsStop)
		if jobsDone != nil {
			<-jobsDone
		}
	}
	s.closeMu.Lock()
	s.jobsStop = nil
	s.jobsDone = nil
	s.closeMu.Unlock()
	return s.db.Close()
}

// Current schema version — increment by 1 for each new migration.
const currentSchemaVersion = 27

func (s *Store) migrate() error {
	// Bootstrap schema_version table first so we can track applied migrations.
	if _, err := s.execHook(s.db, `
		CREATE TABLE IF NOT EXISTS schema_version (
			version    INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f','now'))
		)`); err != nil {
		return fmt.Errorf("create schema_version table: %w", err)
	}

	// Bootstrap core schema (memory_items is created after this block via runMigrations)
	schema := `
			CREATE TABLE IF NOT EXISTS sessions (
				id         TEXT PRIMARY KEY,
			project    TEXT NOT NULL,
			directory  TEXT NOT NULL,
			started_at TEXT NOT NULL DEFAULT (datetime('now')),
			ended_at   TEXT,
			summary    TEXT
		);

			CREATE TABLE IF NOT EXISTS user_prompts (
				id         INTEGER PRIMARY KEY AUTOINCREMENT,
				sync_id    TEXT,
				session_id TEXT    NOT NULL,
			content    TEXT    NOT NULL,
			project    TEXT,
			created_at TEXT    NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (session_id) REFERENCES sessions(id)
		);

		CREATE INDEX IF NOT EXISTS idx_prompts_session ON user_prompts(session_id);
		CREATE INDEX IF NOT EXISTS idx_prompts_project ON user_prompts(project);
		CREATE INDEX IF NOT EXISTS idx_prompts_created ON user_prompts(created_at DESC);

		CREATE VIRTUAL TABLE IF NOT EXISTS prompts_fts USING fts5(
			content,
			project,
			content='user_prompts',
			content_rowid='id'
		);

			CREATE TABLE IF NOT EXISTS sync_chunks (
				chunk_id    TEXT PRIMARY KEY,
				imported_at TEXT NOT NULL DEFAULT (datetime('now'))
			);

			CREATE TABLE IF NOT EXISTS sync_state (
				target_key           TEXT PRIMARY KEY,
				lifecycle            TEXT NOT NULL DEFAULT 'idle',
				last_enqueued_seq    INTEGER NOT NULL DEFAULT 0,
				last_acked_seq       INTEGER NOT NULL DEFAULT 0,
				last_pulled_seq      INTEGER NOT NULL DEFAULT 0,
				consecutive_failures INTEGER NOT NULL DEFAULT 0,
				backoff_until        TEXT,
				lease_owner          TEXT,
				lease_until          TEXT,
				last_error           TEXT,
				updated_at           TEXT NOT NULL DEFAULT (datetime('now'))
			);

			CREATE TABLE IF NOT EXISTS sync_mutations (
				seq         INTEGER PRIMARY KEY AUTOINCREMENT,
				target_key  TEXT NOT NULL,
				entity      TEXT NOT NULL,
				entity_key  TEXT NOT NULL,
				op          TEXT NOT NULL,
				payload     TEXT NOT NULL,
				source      TEXT NOT NULL DEFAULT 'local',
				occurred_at TEXT NOT NULL DEFAULT (datetime('now')),
				acked_at    TEXT,
				FOREIGN KEY (target_key) REFERENCES sync_state(target_key)
			);
		`
	if _, err := s.execHook(s.db, schema); err != nil {
		return err
	}

	if err := s.addColumnIfNotExists("user_prompts", "sync_id", "TEXT"); err != nil {
		return err
	}

	if _, err := s.execHook(s.db, `
		CREATE INDEX IF NOT EXISTS idx_prompts_sync_id ON user_prompts(sync_id);
		CREATE INDEX IF NOT EXISTS idx_sync_mutations_target_seq ON sync_mutations(target_key, seq);
		CREATE INDEX IF NOT EXISTS idx_sync_mutations_pending ON sync_mutations(target_key, acked_at, seq);
	`); err != nil {
		return err
	}

	// Project-scoped sync: add project column to sync_mutations and enrollment table.
	if err := s.addColumnIfNotExists("sync_mutations", "project", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if _, err := s.execHook(s.db, `
		CREATE TABLE IF NOT EXISTS sync_enrolled_projects (
			project     TEXT PRIMARY KEY,
			enrolled_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
		CREATE INDEX IF NOT EXISTS idx_sync_mutations_project ON sync_mutations(project);
	`); err != nil {
		return err
	}
	// Backfill: extract project from JSON payload for existing rows with empty project.
	if _, err := s.execHook(s.db, `
		UPDATE sync_mutations
		SET project = COALESCE(json_extract(payload, '$.project'), '')
		WHERE project = '' AND payload != ''
	`); err != nil {
		return err
	}

	if _, err := s.execHook(s.db, `UPDATE user_prompts SET project = '' WHERE project IS NULL`); err != nil {
		return err
	}
	if _, err := s.execHook(s.db, `UPDATE user_prompts SET sync_id = 'prompt-' || lower(hex(randomblob(16))) WHERE sync_id IS NULL OR sync_id = ''`); err != nil {
		return err
	}
	if _, err := s.execHook(s.db, `INSERT OR IGNORE INTO sync_state (target_key, lifecycle, updated_at) VALUES ('cloud', 'idle', datetime('now'))`); err != nil {
		return err
	}

	// Prompts FTS triggers (idempotent check)
	var promptTrigger string
	err := s.db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='trigger' AND name='prompt_fts_insert'",
	).Scan(&promptTrigger)

	if err == sql.ErrNoRows {
		promptTriggers := `
			CREATE TRIGGER prompt_fts_insert AFTER INSERT ON user_prompts BEGIN
				INSERT INTO prompts_fts(rowid, content, project)
				VALUES (new.id, new.content, new.project);
			END;

			CREATE TRIGGER prompt_fts_delete AFTER DELETE ON user_prompts BEGIN
				INSERT INTO prompts_fts(prompts_fts, rowid, content, project)
				VALUES ('delete', old.id, old.content, old.project);
			END;

			CREATE TRIGGER prompt_fts_update AFTER UPDATE ON user_prompts BEGIN
				INSERT INTO prompts_fts(prompts_fts, rowid, content, project)
				VALUES ('delete', old.id, old.content, old.project);
				INSERT INTO prompts_fts(rowid, content, project)
				VALUES (new.id, new.content, new.project);
			END;
		`
		if _, err := s.execHook(s.db, promptTriggers); err != nil {
			return err
		}
	}

	// ── Memory Items schema (Ohara v2 spec) ─────────────────────────────────
	if _, err := s.execHook(s.db, `
		CREATE TABLE IF NOT EXISTS memory_items (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f','now')),
			updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f','now')),
			project_id      TEXT NOT NULL,
			actor_id        TEXT NOT NULL DEFAULT 'agent',
			kind            TEXT NOT NULL,
			scope           TEXT NOT NULL DEFAULT 'project',
			title           TEXT NOT NULL,
			body            TEXT NOT NULL,
			tags            TEXT NOT NULL DEFAULT '[]',
			source          TEXT NOT NULL DEFAULT 'agent',
			status          TEXT NOT NULL DEFAULT 'active',
			superseded_by   INTEGER REFERENCES memory_items(id),
			expires_at      TEXT,
			ingested_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f','now')),
			written_by      TEXT NOT NULL DEFAULT 'agent'
		);

		CREATE INDEX IF NOT EXISTS idx_mem_project ON memory_items(project_id, status);
		CREATE INDEX IF NOT EXISTS idx_mem_kind ON memory_items(kind, status);
		CREATE INDEX IF NOT EXISTS idx_mem_scope ON memory_items(scope, status);
		CREATE INDEX IF NOT EXISTS idx_mem_updated ON memory_items(updated_at);

		CREATE TABLE IF NOT EXISTS memory_revisions (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			memory_id   INTEGER NOT NULL REFERENCES memory_items(id),
			ts          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f','now')),
			actor_id    TEXT NOT NULL,
			field       TEXT NOT NULL,
			old_value   TEXT,
			new_value   TEXT,
			reason      TEXT
		);

		CREATE INDEX IF NOT EXISTS idx_rev_memory ON memory_revisions(memory_id, ts);
	`); err != nil {
		return err
	}

	// Backfill: ensure existing memory_items rows have valid scope
	if _, err := s.execHook(s.db, `UPDATE memory_items SET scope = 'project' WHERE scope IS NULL OR scope = ''`); err != nil {
		return err
	}
	if _, err := s.execHook(s.db, `UPDATE memory_items SET status = 'active' WHERE status IS NULL OR status = ''`); err != nil {
		return err
	}
	if _, err := s.execHook(s.db, `UPDATE memory_items SET source = 'agent' WHERE source IS NULL OR source = ''`); err != nil {
		return err
	}
	if _, err := s.execHook(s.db, `UPDATE memory_items SET actor_id = 'agent' WHERE actor_id IS NULL OR actor_id = ''`); err != nil {
		return err
	}

	// Create FTS5 virtual table for memory_items (idempotent check)
	var memFTSTrigger string
	err = s.db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='trigger' AND name='mem_fts_insert'",
	).Scan(&memFTSTrigger)

	if err == sql.ErrNoRows {
		if _, err := s.execHook(s.db, `
			CREATE VIRTUAL TABLE IF NOT EXISTS memory_items_fts USING fts5(
				title,
				body,
				tags,
				content='memory_items',
				content_rowid='id',
				tokenize='porter unicode61'
			);

			CREATE TRIGGER mem_fts_insert AFTER INSERT ON memory_items BEGIN
				INSERT INTO memory_items_fts(rowid, title, body, tags)
				VALUES (new.id, new.title, new.body, new.tags);
			END;

			CREATE TRIGGER mem_fts_delete AFTER DELETE ON memory_items BEGIN
				INSERT INTO memory_items_fts(memory_items_fts, rowid, title, body, tags)
				VALUES ('delete', old.id, old.title, old.body, old.tags);
			END;

			CREATE TRIGGER mem_fts_update AFTER UPDATE ON memory_items BEGIN
				INSERT INTO memory_items_fts(memory_items_fts, rowid, title, body, tags)
				VALUES ('delete', old.id, old.title, old.body, old.tags);
				INSERT INTO memory_items_fts(rowid, title, body, tags)
				VALUES (new.id, new.title, new.body, new.tags);
			END;
		`); err != nil {
			return err
		}
	}

	// Run all migrations in order (after base memory_items table exists).
	if err := s.runMigrations(); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	return nil
}

// runMigrations applies all pending schema migrations in order.
// Each migration is idempotent: it checks the schema_version table and only
// applies migrations that haven't been recorded yet.
// Logs migration progress only when migrations are actually needed.
func (s *Store) runMigrations() error {

	var currentVersion int
	err := s.db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&currentVersion)
	if err != nil {
		return fmt.Errorf("get schema version: %w", err)
	}

	// No-op: DB is at current schema — return silently.
	if currentVersion >= currentSchemaVersion {
		return nil
	}

	// At least one migration is needed: log the upgrade path.
	log.Printf("[ohara] migrating schema %d → %d", currentVersion, currentSchemaVersion)

	// Apply migrations 1 through currentSchemaVersion
	for v := currentVersion + 1; v <= currentSchemaVersion; v++ {
		if err := s.applyMigration(v); err != nil {
			return fmt.Errorf("migration %d: %w", v, err)
		}
		log.Printf("[ohara] applied migration %d", v)
	}
	return nil
}

// applyMigration runs a single numbered migration. All migrations are additive.
func (s *Store) applyMigration(version int) error {
	switch version {
	case 1:
		// Migration 001: Domain field (P0 - 4.1)
		if err := s.addColumnIfNotExists("memory_items", "domain", "TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
		if _, err := s.execHook(s.db, `CREATE INDEX IF NOT EXISTS idx_mem_project_domain ON memory_items(project_id, domain)`); err != nil {
			return err
		}
		// Backfill empty domain for existing rows
		if _, err := s.execHook(s.db, `UPDATE memory_items SET domain = '' WHERE domain IS NULL`); err != nil {
			return err
		}

	case 2:
		// Migration 002: Evidence and provenance (P0 - 4.2)
		if err := s.addColumnIfNotExists("memory_items", "evidence_json", "TEXT NOT NULL DEFAULT '{}'"); err != nil {
			return err
		}
		if err := s.addColumnIfNotExists("memory_items", "applies_to_json", "TEXT NOT NULL DEFAULT '{}'"); err != nil {
			return err
		}
		if err := s.addColumnIfNotExists("memory_items", "related_json", "TEXT NOT NULL DEFAULT '{}'"); err != nil {
			return err
		}

	case 3:
		// Migration 003: Classification tiers (P0 - 4.5)
		if err := s.addColumnIfNotExists("memory_items", "classification", "TEXT NOT NULL DEFAULT 'tactical'"); err != nil {
			return err
		}
		// Default classification mapping per kind
		type kindClass struct{ kind, class string }
		defaults := []kindClass{
			{MemoryKindDecision, "foundational"},
			{MemoryKindProcedure, "foundational"},
			{MemoryKindPattern, "tactical"},
			{MemoryKindBugfix, "tactical"},
			{MemoryKindDiscovery, "observational"},
		}
		for _, dc := range defaults {
			if _, err := s.execHook(s.db,
				`UPDATE memory_items SET classification = ? WHERE kind = ? AND classification = 'tactical'`,
				dc.class, dc.kind); err != nil {
				return err
			}
		}

	case 4:
		// Migration 004: Temporal decay fields (P1 - 5.1)
		if err := s.addColumnIfNotExists("memory_items", "access_count", "INTEGER NOT NULL DEFAULT 0"); err != nil {
			return err
		}
		if err := s.addColumnIfNotExists("memory_items", "last_accessed", "TEXT"); err != nil {
			return err
		}

	case 5:
		// Migration 005: Usage tracking table (P1 - 5.1)
		if _, err := s.execHook(s.db, `
			CREATE TABLE IF NOT EXISTS memory_usage (
				id         INTEGER PRIMARY KEY AUTOINCREMENT,
				memory_id  INTEGER NOT NULL REFERENCES memory_items(id),
				event      TEXT NOT NULL CHECK(event IN ('retrieved', 'used')),
				session_id TEXT,
				ts         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f','now'))
			)`); err != nil {
			return err
		}
		if _, err := s.execHook(s.db, `CREATE INDEX IF NOT EXISTS idx_usage_memory ON memory_usage(memory_id)`); err != nil {
			return err
		}

	case 6:
		// Migration 006: Outcome tracking table (P1 - 5.2)
		if _, err := s.execHook(s.db, `
			CREATE TABLE IF NOT EXISTS memory_outcomes (
				id        INTEGER PRIMARY KEY AUTOINCREMENT,
				memory_id INTEGER NOT NULL REFERENCES memory_items(id),
				status    TEXT NOT NULL CHECK(status IN ('success', 'failure', 'unknown')),
				notes     TEXT,
				actor_id  TEXT,
				ts        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f','now'))
			)`); err != nil {
			return err
		}
		if _, err := s.execHook(s.db, `CREATE INDEX IF NOT EXISTS idx_outcomes_memory ON memory_outcomes(memory_id)`); err != nil {
			return err
		}

	case 7:
		// Migration 007: Temporal fields (P1 - 5.5)
		if err := s.addColumnIfNotExists("memory_items", "valid_from", "TEXT"); err != nil {
			return err
		}
		if err := s.addColumnIfNotExists("memory_items", "valid_to", "TEXT"); err != nil {
			return err
		}
		if err := s.addColumnIfNotExists("memory_items", "superseded_at", "TEXT"); err != nil {
			return err
		}
		if err := s.addColumnIfNotExists("memory_items", "session_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
		// Set valid_from = created_at for existing rows
		if _, err := s.execHook(s.db,
			`UPDATE memory_items SET valid_from = created_at WHERE valid_from IS NULL AND created_at IS NOT NULL`); err != nil {
			return err
		}

	case 8:
		// Migration 008: Security (P1 - section 8)
		if err := s.addColumnIfNotExists("memory_items", "trust_level", "TEXT NOT NULL DEFAULT 'system'"); err != nil {
			return err
		}
		if _, err := s.execHook(s.db, `
			CREATE TABLE IF NOT EXISTS audit_log (
				id         INTEGER PRIMARY KEY AUTOINCREMENT,
				obs_id     TEXT NOT NULL,
				action     TEXT NOT NULL,
				actor_id   TEXT,
				session_id TEXT,
				trust_level TEXT,
				ts         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f','now')),
				snapshot   TEXT
			)`); err != nil {
			return err
		}
		if _, err := s.execHook(s.db, `CREATE INDEX IF NOT EXISTS idx_audit_obs ON audit_log(obs_id)`); err != nil {
			return err
		}
		if _, err := s.execHook(s.db, `CREATE INDEX IF NOT EXISTS idx_audit_session ON audit_log(session_id)`); err != nil {
			return err
		}

	case 9:
		// Migration 009: P2 fields — trigger_condition (P2 - trigger for procedure kind)
		if err := s.addColumnIfNotExists("memory_items", "trigger_condition", "TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}

	case 10:
		// Migration 010: P3 fields — utility_weight and consolidated_from (P3)
		if err := s.addColumnIfNotExists("memory_items", "utility_weight", "REAL NOT NULL DEFAULT 0.0"); err != nil {
			return err
		}
		if err := s.addColumnIfNotExists("memory_items", "consolidated_from", "TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}

	case 11:
		// Migration 011: Consolidation provenance (P3 - 7.1) — source column already in base table
		// No-op: source column was added in the base CREATE TABLE

	case 12:
		// Migration 012: RL utility weight (P3 - 7.4) — utility_weight already added in migration 10
		// No-op: utility_weight column already exists

	case 13:
		// Migration 013: Embedding sidecar (P3 - 7.2, optional)
		// No-op: implemented only when hybrid retrieval is enabled via config

	case 14:
		// Migration 014: Entity graph (P3 - 7.3, optional)
		// No-op: implemented only if mem_related proves insufficient at scale

	case 15:
		// Migration 015: Bi-temporal model (P1 - 5.7)
		// ingested_at: when this record was ingested into the DB (immutable — never updated)
		if err := s.addColumnIfNotExists("memory_items", "ingested_at", "TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f','now'))"); err != nil {
			return err
		}
		// Backfill: set ingested_at = created_at for existing rows
		if _, err := s.execHook(s.db, `UPDATE memory_items SET ingested_at = created_at WHERE ingested_at IS NULL OR ingested_at = ''`); err != nil {
			return err
		}

	case 16:
		// Migration 016: Actor-aware writes (P1 - 5.8)
		if err := s.addColumnIfNotExists("memory_items", "written_by", "TEXT NOT NULL DEFAULT 'agent'"); err != nil {
			return err
		}
		// Valid values: user, agent, consolidation, import, system
		// Backfill: existing rows were written by 'agent'
		if _, err := s.execHook(s.db, `UPDATE memory_items SET written_by = 'agent' WHERE written_by IS NULL OR written_by = ''`); err != nil {
			return err
		}

	case 17:
		// Migration 017: Store-time TTL (P1 - 5.9)
		// expires_at already exists in the base CREATE TABLE — no column add needed.
		// No-op: expires_at column already exists

	case 18:
		// Migration 018: memory_relations table for typed inter-memory relationships
		if _, err := s.execHook(s.db, `
			CREATE TABLE IF NOT EXISTS memory_relations (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				from_obs_id TEXT NOT NULL,
				to_obs_id TEXT NOT NULL,
				relation TEXT NOT NULL,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				UNIQUE(from_obs_id, to_obs_id, relation)
			)`); err != nil {
			return err
		}
		if _, err := s.execHook(s.db, `CREATE INDEX IF NOT EXISTS idx_relations_from ON memory_relations(from_obs_id)`); err != nil {
			return err
		}
		if _, err := s.execHook(s.db, `CREATE INDEX IF NOT EXISTS idx_relations_to ON memory_relations(to_obs_id)`); err != nil {
			return err
		}

	case 19:
		// Migration 019: Add trigger_condition to memory_items FTS5 index.
		// FTS5 virtual tables do not support ALTER TABLE ADD COLUMN,
		// so we must rebuild the table if the column is not yet present.
		if err := s.migrateMemFTSTriggerCondition(); err != nil {
			return err
		}

	case 20:
		// Migration 020: Embedding sidecar for hybrid retrieval (P3 - 7.2)
		if _, err := s.execHook(s.db, `
			CREATE TABLE IF NOT EXISTS obs_embeddings (
				obs_id INTEGER PRIMARY KEY REFERENCES memory_items(id),
				embedding BLOB NOT NULL,
				model TEXT NOT NULL,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP
			)`); err != nil {
			return err
		}
		if _, err := s.execHook(s.db, `CREATE INDEX IF NOT EXISTS idx_obs_embeddings_model ON obs_embeddings(model)`); err != nil {
			return err
		}

	case 21:
		// Migration 021: Entity graph tables (P3 - 7.3 optional index)
		if _, err := s.execHook(s.db, `
			CREATE TABLE IF NOT EXISTS entities (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				name TEXT NOT NULL,
				type TEXT NOT NULL,
				project_key TEXT NOT NULL,
				UNIQUE(name, type, project_key)
			)`); err != nil {
			return err
		}
		if _, err := s.execHook(s.db, `
			CREATE TABLE IF NOT EXISTS obs_entities (
				obs_id INTEGER NOT NULL REFERENCES memory_items(id),
				entity_id INTEGER NOT NULL REFERENCES entities(id),
				PRIMARY KEY (obs_id, entity_id)
			)`); err != nil {
			return err
		}

	case 22:
		// Migration 022: Feedback table for RL-informed utility updates (P3 - 7.4 groundwork)
		if _, err := s.execHook(s.db, `
			CREATE TABLE IF NOT EXISTS memory_feedback (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				memory_id INTEGER NOT NULL REFERENCES memory_items(id),
				reward REAL NOT NULL,
				notes TEXT,
				actor_id TEXT,
				ts DATETIME DEFAULT CURRENT_TIMESTAMP
			)`); err != nil {
			return err
		}
		if _, err := s.execHook(s.db, `CREATE INDEX IF NOT EXISTS idx_feedback_memory ON memory_feedback(memory_id)`); err != nil {
			return err
		}

	case 23:
		// Migration 023: Port legacy observations into memory_items (one-time backfill).
		// Only inserts rows that don't already exist (idempotent).
		if _, err := s.execHook(s.db, `
			INSERT OR IGNORE INTO memory_items (id, created_at, updated_at, project_id, actor_id, kind, scope, title, body, source, status, session_id, written_by)
			SELECT
				o.id,
				o.created_at,
				o.updated_at,
				COALESCE(LOWER(o.project), 'default'),
				'agent',
				CASE
					WHEN o.type IN ('decision', 'procedure', 'pattern', 'bugfix', 'discovery', 'learned', 'config') THEN o.type
					ELSE 'note'
				END,
				COALESCE(NULLIF(o.scope, ''), 'project'),
				o.title,
				o.content,
				'import',
				CASE WHEN o.deleted_at IS NOT NULL THEN 'archived' ELSE 'active' END,
				o.session_id,
				'import'
			FROM observations o
			WHERE NOT EXISTS (SELECT 1 FROM memory_items m WHERE m.id = o.id)
		`); err != nil {
			// Non-fatal: table may not exist or columns may differ.
			// Log but continue so migration doesn't block startup.
		}

	case 24:
		// Migration 024: Final cleanup of legacy observations schema.
		// Runs after migration 023 backfill completes.
		// Safe to re-run: uses DROP IF EXISTS for all objects.
		// Fresh DBs never created these objects (removed from bootstrap), so
		// DROP IF EXISTS is a no-op on fresh DBs.
		//
		// Drop FTS triggers first (must drop triggers before FTS table).
		if _, err := s.execHook(s.db, `DROP TRIGGER IF EXISTS obs_fts_insert`); err != nil {
			return fmt.Errorf("migration 024 drop obs_fts_insert: %w", err)
		}
		if _, err := s.execHook(s.db, `DROP TRIGGER IF EXISTS obs_fts_update`); err != nil {
			return fmt.Errorf("migration 024 drop obs_fts_update: %w", err)
		}
		if _, err := s.execHook(s.db, `DROP TRIGGER IF EXISTS obs_fts_delete`); err != nil {
			return fmt.Errorf("migration 024 drop obs_fts_delete: %w", err)
		}
		// Drop the FTS virtual table.
		if _, err := s.execHook(s.db, `DROP TABLE IF EXISTS observations_fts`); err != nil {
			return fmt.Errorf("migration 024 drop observations_fts: %w", err)
		}
		// Drop the base observations table (cascades to all idx_obs_* indexes).
		if _, err := s.execHook(s.db, `DROP TABLE IF EXISTS observations`); err != nil {
			return fmt.Errorf("migration 024 drop observations: %w", err)
		}

	case 25:
		// Migration 025: Phase 2.5 — no schema changes.
		// Reserved for MCP Streamable HTTP auth/transport compatibility.
		// All required auth and role infrastructure is app-level (no DB schema needed).

	case 26:
		// Migration 026: Raw session-scoped observation capture lane.
		if _, err := s.execHook(s.db, `
			CREATE TABLE IF NOT EXISTS session_observations (
				id            INTEGER PRIMARY KEY AUTOINCREMENT,
				session_id    TEXT NOT NULL,
				project_id    TEXT NOT NULL,
				event_type    TEXT NOT NULL,
				capture_level TEXT NOT NULL DEFAULT 'metadata',
				source        TEXT NOT NULL DEFAULT 'opencode',
				title         TEXT NOT NULL DEFAULT '',
				body          TEXT NOT NULL DEFAULT '',
				payload_json  TEXT NOT NULL DEFAULT '{}',
				created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f','now'))
			)`); err != nil {
			return err
		}
		if _, err := s.execHook(s.db, `CREATE INDEX IF NOT EXISTS idx_session_observations_session ON session_observations(session_id, created_at DESC)`); err != nil {
			return err
		}
		if _, err := s.execHook(s.db, `CREATE INDEX IF NOT EXISTS idx_session_observations_project ON session_observations(project_id, created_at DESC)`); err != nil {
			return err
		}
		if _, err := s.execHook(s.db, `CREATE INDEX IF NOT EXISTS idx_session_observations_event ON session_observations(event_type, created_at DESC)`); err != nil {
			return err
		}

	case 27:
		// Migration 027: Durable derived-memory job outbox.
		if _, err := s.execHook(s.db, `
			CREATE TABLE IF NOT EXISTS memory_jobs (
				id           INTEGER PRIMARY KEY AUTOINCREMENT,
				memory_id    INTEGER NOT NULL REFERENCES memory_items(id),
				job_type     TEXT NOT NULL,
				status       TEXT NOT NULL DEFAULT 'pending',
				attempts     INTEGER NOT NULL DEFAULT 0,
				last_error   TEXT,
				available_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f','now')),
				created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f','now')),
				updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f','now')),
				UNIQUE(memory_id, job_type)
			)`); err != nil {
			return err
		}
		if _, err := s.execHook(s.db, `CREATE INDEX IF NOT EXISTS idx_memory_jobs_status_available ON memory_jobs(status, available_at)`); err != nil {
			return err
		}
		if _, err := s.execHook(s.db, `CREATE INDEX IF NOT EXISTS idx_memory_jobs_memory ON memory_jobs(memory_id)`); err != nil {
			return err
		}

	default:
		return fmt.Errorf("unknown migration version %d (current: %d)", version, currentSchemaVersion)
	}

	// Record the migration in schema_version.
	if _, err := s.execHook(s.db,
		`INSERT INTO schema_version (version, applied_at) VALUES (?, strftime('%Y-%m-%dT%H:%M:%f','now'))`,
		version); err != nil {
		return fmt.Errorf("record migration %d: %w", version, err)
	}
	return nil
}

// migrateMemFTSTriggerCondition rebuilds memory_items_fts to include the
// trigger_condition column. FTS5 does not support ALTER TABLE ADD COLUMN,
// so this uses the drop-create-backfill approach within a single transaction.
func (s *Store) migrateMemFTSTriggerCondition() error {
	// Check if the FTS table already has trigger_condition.
	var colCount int
	if err := s.db.QueryRow(
		"SELECT COUNT(*) FROM pragma_table_xinfo('memory_items_fts') WHERE name = 'trigger_condition'",
	).Scan(&colCount); err != nil {
		return fmt.Errorf("check trigger_condition column: %w", err)
	}
	if colCount > 0 {
		return nil // Already migrated
	}

	// Rebuild FTS table within a transaction so it is atomic.
	tx, err := s.beginTxHook()
	if err != nil {
		return fmt.Errorf("migrate mem fts trigger_condition: begin tx: %w", err)
	}
	defer tx.Rollback()

	// Drop old triggers and FTS table.
	if _, err := s.execHook(tx, `
		DROP TRIGGER IF EXISTS mem_fts_insert;
		DROP TRIGGER IF EXISTS mem_fts_delete;
		DROP TRIGGER IF EXISTS mem_fts_update;
		DROP TABLE IF EXISTS memory_items_fts;
	`); err != nil {
		return fmt.Errorf("migrate mem fts trigger_condition: drop old fts: %w", err)
	}

	// Create new FTS table with trigger_condition as 4th indexed column.
	if _, err := s.execHook(tx, `
		CREATE VIRTUAL TABLE memory_items_fts USING fts5(
			title,
			body,
			tags,
			trigger_condition,
			content='memory_items',
			content_rowid='id',
			tokenize='porter unicode61'
		);
	`); err != nil {
		return fmt.Errorf("migrate mem fts trigger_condition: create new fts: %w", err)
	}

	// Backfill all existing memory_items rows into the new FTS table.
	if _, err := s.execHook(tx, `
		INSERT INTO memory_items_fts(rowid, title, body, tags, trigger_condition)
		SELECT id, title, body, tags, trigger_condition FROM memory_items
	`); err != nil {
		return fmt.Errorf("migrate mem fts trigger_condition: backfill: %w", err)
	}

	// Recreate the three triggers with trigger_condition.
	if _, err := s.execHook(tx, `
		CREATE TRIGGER mem_fts_insert AFTER INSERT ON memory_items BEGIN
			INSERT INTO memory_items_fts(rowid, title, body, tags, trigger_condition)
			VALUES (new.id, new.title, new.body, new.tags, new.trigger_condition);
		END;

		CREATE TRIGGER mem_fts_delete AFTER DELETE ON memory_items BEGIN
			INSERT INTO memory_items_fts(memory_items_fts, rowid, title, body, tags, trigger_condition)
			VALUES ('delete', old.id, old.title, old.body, old.tags, old.trigger_condition);
		END;

		CREATE TRIGGER mem_fts_update AFTER UPDATE ON memory_items BEGIN
			INSERT INTO memory_items_fts(memory_items_fts, rowid, title, body, tags, trigger_condition)
			VALUES ('delete', old.id, old.title, old.body, old.tags, old.trigger_condition);
			INSERT INTO memory_items_fts(rowid, title, body, tags, trigger_condition)
			VALUES (new.id, new.title, new.body, new.tags, new.trigger_condition);
		END;
	`); err != nil {
		return fmt.Errorf("migrate mem fts trigger_condition: create triggers: %w", err)
	}

	if err := s.commitHook(tx); err != nil {
		return fmt.Errorf("migrate mem fts trigger_condition: commit: %w", err)
	}
	return nil
}

func (s *Store) CreateSession(id, project, directory string) error {
	project, _ = NormalizeProject(project)

	return s.withTx(func(tx *sql.Tx) error {
		if err := s.createSessionTx(tx, id, project, directory); err != nil {
			return err
		}
		return s.enqueueSyncMutationTx(tx, SyncEntitySession, id, SyncOpUpsert, syncSessionPayload{
			ID:        id,
			Project:   project,
			Directory: directory,
		})
	})
}

func (s *Store) EndSession(id string, summary string) error {
	return s.withTx(func(tx *sql.Tx) error {
		res, err := s.execHook(tx,
			`UPDATE sessions SET ended_at = datetime('now'), summary = ? WHERE id = ?`,
			nullableString(summary), id,
		)
		if err != nil {
			return err
		}
		rows, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if rows == 0 {
			return nil
		}

		var endedAt string
		var project, directory string
		var storedSummary *string
		if err := tx.QueryRow(
			`SELECT project, directory, ended_at, summary FROM sessions WHERE id = ?`,
			id,
		).Scan(&project, &directory, &endedAt, &storedSummary); err != nil {
			return err
		}

		return s.enqueueSyncMutationTx(tx, SyncEntitySession, id, SyncOpUpsert, syncSessionPayload{
			ID:        id,
			Project:   project,
			Directory: directory,
			EndedAt:   &endedAt,
			Summary:   storedSummary,
		})
	})
}

func (s *Store) GetSession(id string) (*Session, error) {
	row := s.db.QueryRow(
		`SELECT id, project, directory, started_at, ended_at, summary FROM sessions WHERE id = ?`, id,
	)
	var sess Session
	if err := row.Scan(&sess.ID, &sess.Project, &sess.Directory, &sess.StartedAt, &sess.EndedAt, &sess.Summary); err != nil {
		return nil, err
	}
	return &sess, nil
}

func (s *Store) RecentSessions(project string, limit int) ([]SessionSummary, error) {
	// Normalize project filter for case-insensitive matching
	project, _ = NormalizeProject(project)

	if limit <= 0 {
		limit = 5
	}

	query := `
		SELECT s.id, s.project, s.started_at, s.ended_at, s.summary,
		       COUNT(m.id) as memory_count
		FROM sessions s
		LEFT JOIN memory_items m ON m.session_id = s.id AND m.status = 'active'
		WHERE 1=1
	`
	args := []any{}

	if project != "" {
		query += " AND s.project = ?"
		args = append(args, project)
	}

	query += " GROUP BY s.id ORDER BY MAX(COALESCE(m.created_at, s.started_at)) DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.queryItHook(s.db, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SessionSummary
	for rows.Next() {
		var ss SessionSummary
		if err := rows.Scan(&ss.ID, &ss.Project, &ss.StartedAt, &ss.EndedAt, &ss.Summary, &ss.MemoryCount); err != nil {
			return nil, err
		}
		results = append(results, ss)
	}
	return results, rows.Err()
}

// AllSessions returns recent sessions ordered by most recent first (for TUI browsing).
func (s *Store) AllSessions(project string, limit int) ([]SessionSummary, error) {
	if limit <= 0 {
		limit = 50
	}

	query := `
		SELECT s.id, s.project, s.started_at, s.ended_at, s.summary,
		       COUNT(m.id) as memory_count
		FROM sessions s
		LEFT JOIN memory_items m ON m.session_id = s.id AND m.status = 'active'
		WHERE 1=1
	`
	args := []any{}

	if project != "" {
		query += " AND s.project = ?"
		args = append(args, project)
	}

	query += " GROUP BY s.id ORDER BY MAX(COALESCE(m.created_at, s.started_at)) DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.queryItHook(s.db, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SessionSummary
	for rows.Next() {
		var ss SessionSummary
		if err := rows.Scan(&ss.ID, &ss.Project, &ss.StartedAt, &ss.EndedAt, &ss.Summary, &ss.MemoryCount); err != nil {
			return nil, err
		}
		results = append(results, ss)
	}
	return results, rows.Err()
}

// GetPrompt retrieves a single prompt by ID.
func (s *Store) GetPrompt(id int64) (*Prompt, error) {
	row := s.db.QueryRow(
		`SELECT id, sync_id, session_id, content, project, created_at FROM user_prompts WHERE id = ?`, id,
	)
	var p Prompt
	if err := row.Scan(&p.ID, &p.SyncID, &p.SessionID, &p.Content, &p.Project, &p.CreatedAt); err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Store) AddPrompt(p AddPromptParams) (int64, error) {
	p.Project, _ = NormalizeProject(p.Project)

	content := stripPrivateTags(p.Content)
	if len(content) > 50000 {
		content = content[:50000] + "... [truncated]"
	}

	var promptID int64
	err := s.withTx(func(tx *sql.Tx) error {
		syncID := newSyncID("prompt")
		res, err := s.execHook(tx,
			`INSERT INTO user_prompts (sync_id, session_id, content, project) VALUES (?, ?, ?, ?)`,
			syncID, p.SessionID, content, nullableString(p.Project),
		)
		if err != nil {
			return err
		}
		promptID, err = res.LastInsertId()
		if err != nil {
			return err
		}
		return s.enqueueSyncMutationTx(tx, SyncEntityPrompt, syncID, SyncOpUpsert, syncPromptPayload{
			SyncID:    syncID,
			SessionID: p.SessionID,
			Content:   content,
			Project:   nullableString(p.Project),
		})
	})
	if err != nil {
		return 0, err
	}
	return promptID, nil
}

func (s *Store) RecentPrompts(project string, limit int) ([]Prompt, error) {
	// Normalize project filter for case-insensitive matching
	project, _ = NormalizeProject(project)

	if limit <= 0 {
		limit = 20
	}

	query := `SELECT id, ifnull(sync_id, '') as sync_id, session_id, content, ifnull(project, '') as project, created_at FROM user_prompts`
	args := []any{}

	if project != "" {
		query += " WHERE project = ?"
		args = append(args, project)
	}

	query += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.queryItHook(s.db, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Prompt
	for rows.Next() {
		var p Prompt
		if err := rows.Scan(&p.ID, &p.SyncID, &p.SessionID, &p.Content, &p.Project, &p.CreatedAt); err != nil {
			return nil, err
		}
		results = append(results, p)
	}
	return results, rows.Err()
}

func (s *Store) SearchPrompts(query string, project string, limit int) ([]Prompt, error) {
	if limit <= 0 {
		limit = 10
	}

	ftsQuery := sanitizeFTS(query)

	sql := `
		SELECT p.id, ifnull(p.sync_id, '') as sync_id, p.session_id, p.content, ifnull(p.project, '') as project, p.created_at
		FROM prompts_fts fts
		JOIN user_prompts p ON p.id = fts.rowid
		WHERE prompts_fts MATCH ?
	`
	args := []any{ftsQuery}

	if project != "" {
		sql += " AND p.project = ?"
		args = append(args, project)
	}

	sql += " ORDER BY fts.rank LIMIT ?"
	args = append(args, limit)

	rows, err := s.queryItHook(s.db, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("search prompts: %w", err)
	}
	defer rows.Close()

	var results []Prompt
	for rows.Next() {
		var p Prompt
		if err := rows.Scan(&p.ID, &p.SyncID, &p.SessionID, &p.Content, &p.Project, &p.CreatedAt); err != nil {
			return nil, err
		}
		results = append(results, p)
	}
	return results, rows.Err()
}

// DeleteSession hard-deletes a session and its prompts.
// It returns fmt.Errorf("session has memories") if the session has any memory_items
// to prevent orphaned rows.
// It returns ErrSessionNotFound if no session with that ID exists.
//
// Note: this delete only removes local rows. It does not enqueue a delete
// sync mutation, but any previously enqueued mutations for the session or its
// prompts may still be synced later if autosync is enabled, and a later pull
// may recreate the deleted rows locally.
func (s *Store) DeleteSession(id string) error {
	return s.withTx(func(tx *sql.Tx) error {
		// Guard: refuse to delete session if it has associated memory items.
		var count int
		rows, err := s.queryItHook(tx, `SELECT COUNT(*) FROM memory_items WHERE session_id = ?`, id)
		if err != nil {
			return fmt.Errorf("delete session: count memories: %w", err)
		}
		if rows.Next() {
			if err := rows.Scan(&count); err != nil {
				_ = rows.Close()
				return fmt.Errorf("delete session: count memories: %w", err)
			}
		}
		_ = rows.Close()
		if count > 0 {
			return fmt.Errorf("%w: session %q has %d memory item(s)", fmt.Errorf("session has memories"), id, count)
		}

		if _, err := s.execHook(tx, `DELETE FROM user_prompts WHERE session_id = ?`, id); err != nil {
			return fmt.Errorf("delete session: remove prompts: %w", err)
		}

		res, err := s.execHook(tx, `DELETE FROM sessions WHERE id = ?`, id)
		if err != nil {
			var sqliteErr *sqlite.Error
			if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqliteConstraintForeignKey {
				return fmt.Errorf("%w: session %q has memory item(s)", fmt.Errorf("session has memories"), id)
			}
			return fmt.Errorf("delete session: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("delete session: rows affected: %w", err)
		}
		if n == 0 {
			return fmt.Errorf("%w: %q", ErrSessionNotFound, id)
		}

		return nil
	})
}

// DeletePrompt hard-deletes a single prompt by ID.
// It returns ErrPromptNotFound if no prompt with that ID exists.
//
// Note: this delete only removes local rows. It does not enqueue a delete
// sync mutation, but any previously enqueued mutations for the prompt
// may still be synced later if autosync is enabled, and a later pull
// may recreate the deleted row locally.
func (s *Store) DeletePrompt(id int64) error {
	return s.withTx(func(tx *sql.Tx) error {
		res, err := s.execHook(tx, `DELETE FROM user_prompts WHERE id = ?`, id)
		if err != nil {
			return fmt.Errorf("delete prompt: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("delete prompt: rows affected: %w", err)
		}
		if n == 0 {
			return fmt.Errorf("%w: prompt #%d", ErrPromptNotFound, id)
		}
		return nil
	})
}

func (s *Store) Stats() (*Stats, error) {
	stats := &Stats{}

	s.db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&stats.TotalSessions)
	s.db.QueryRow("SELECT COUNT(*) FROM memory_items WHERE status = 'active'").Scan(&stats.TotalMemories)
	s.db.QueryRow("SELECT COUNT(*) FROM user_prompts").Scan(&stats.TotalPrompts)

	rows, err := s.queryItHook(s.db, "SELECT DISTINCT project_id AS project FROM memory_items WHERE status = 'active' AND project_id IS NOT NULL AND project_id != '' ORDER BY project")
	if err != nil {
		return stats, nil
	}
	defer rows.Close()

	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err == nil {
			stats.Projects = append(stats.Projects, p)
		}
	}

	return stats, nil
}

func (s *Store) FormatContext(project, scope string) (string, error) {
	sessions, err := s.RecentSessions(project, 5)
	if err != nil {
		return "", err
	}

	memories, err := s.GetMemories(project, scope, "", MemoryStatusActive, s.cfg.MaxContextResults)
	if err != nil {
		return "", err
	}

	prompts, err := s.RecentPrompts(project, 10)
	if err != nil {
		return "", err
	}

	if len(sessions) == 0 && len(memories) == 0 && len(prompts) == 0 {
		return "", nil
	}

	var b strings.Builder
	b.WriteString("## Memory from Previous Sessions\n\n")

	if len(sessions) > 0 {
		b.WriteString("### Recent Sessions\n")
		for _, sess := range sessions {
			summary := ""
			if sess.Summary != nil {
				summary = fmt.Sprintf(": %s", util.Truncate(*sess.Summary, 200))
			}
			fmt.Fprintf(&b, "- **%s** (%s)%s [%d memories]\n",
				sess.Project, sess.StartedAt, summary, sess.MemoryCount)
		}
		b.WriteString("\n")
	}

	if len(prompts) > 0 {
		b.WriteString("### Recent User Prompts\n")
		for _, p := range prompts {
			fmt.Fprintf(&b, "- %s: %s\n", p.CreatedAt, util.Truncate(p.Content, 200))
		}
		b.WriteString("\n")
	}

	if len(memories) > 0 {
		b.WriteString("### Recent Memories\n")
		for _, m := range memories {
			fmt.Fprintf(&b, "- [%s] **%s**: %s\n",
				m.Kind, m.Title, util.Truncate(m.Body, 300))
		}
		b.WriteString("\n")
	}

	return b.String(), nil
}

func (s *Store) Export() (*ExportData, error) {
	data := &ExportData{
		Version:    "0.1.0",
		ExportedAt: Now(),
	}

	// Sessions
	rows, err := s.queryItHook(s.db,
		"SELECT id, project, directory, started_at, ended_at, summary FROM sessions ORDER BY started_at",
	)
	if err != nil {
		return nil, fmt.Errorf("export sessions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var sess Session
		if err := rows.Scan(&sess.ID, &sess.Project, &sess.Directory, &sess.StartedAt, &sess.EndedAt, &sess.Summary); err != nil {
			return nil, err
		}
		data.Sessions = append(data.Sessions, sess)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Observations
	// Prompts
	promptRows, err := s.queryItHook(s.db,
		"SELECT id, ifnull(sync_id, '') as sync_id, session_id, content, ifnull(project, '') as project, created_at FROM user_prompts ORDER BY id",
	)
	if err != nil {
		return nil, fmt.Errorf("export prompts: %w", err)
	}
	defer promptRows.Close()
	for promptRows.Next() {
		var p Prompt
		if err := promptRows.Scan(&p.ID, &p.SyncID, &p.SessionID, &p.Content, &p.Project, &p.CreatedAt); err != nil {
			return nil, err
		}
		data.Prompts = append(data.Prompts, p)
	}
	if err := promptRows.Err(); err != nil {
		return nil, err
	}

	return data, nil
}

func (s *Store) Import(data *ExportData) (*ImportResult, error) {
	tx, err := s.beginTxHook()
	if err != nil {
		return nil, fmt.Errorf("import: begin tx: %w", err)
	}
	defer tx.Rollback()

	result := &ImportResult{}

	// Import sessions (skip duplicates)
	for _, sess := range data.Sessions {
		res, err := s.execHook(tx,
			`INSERT OR IGNORE INTO sessions (id, project, directory, started_at, ended_at, summary)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			sess.ID, sess.Project, sess.Directory, sess.StartedAt, sess.EndedAt, sess.Summary,
		)
		if err != nil {
			return nil, fmt.Errorf("import session %s: %w", sess.ID, err)
		}
		n, _ := res.RowsAffected()
		result.SessionsImported += int(n)
	}

	// Import prompts
	for _, p := range data.Prompts {
		_, err := s.execHook(tx,
			`INSERT INTO user_prompts (sync_id, session_id, content, project, created_at)
			 VALUES (?, ?, ?, ?, ?)`,
			normalizeExistingSyncID(p.SyncID, "prompt"), p.SessionID, p.Content, p.Project, p.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("import prompt %d: %w", p.ID, err)
		}
		result.PromptsImported++
	}

	if err := s.commitHook(tx); err != nil {
		return nil, fmt.Errorf("import: commit: %w", err)
	}

	return result, nil
}

type ImportResult struct {
	SessionsImported     int `json:"sessions_imported"`
	ObservationsImported int `json:"observations_imported"`
	PromptsImported      int `json:"prompts_imported"`
}

// GetSyncedChunks returns a set of chunk IDs that have been imported/exported.
func (s *Store) GetSyncedChunks() (map[string]bool, error) {
	rows, err := s.queryItHook(s.db, "SELECT chunk_id FROM sync_chunks")
	if err != nil {
		return nil, fmt.Errorf("get synced chunks: %w", err)
	}
	defer rows.Close()

	chunks := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		chunks[id] = true
	}
	return chunks, rows.Err()
}

// RecordSyncedChunk marks a chunk as imported/exported so it won't be processed again.
func (s *Store) RecordSyncedChunk(chunkID string) error {
	_, err := s.execHook(s.db,
		"INSERT OR IGNORE INTO sync_chunks (chunk_id) VALUES (?)",
		chunkID,
	)
	return err
}

func (s *Store) GetSyncState(targetKey string) (*SyncState, error) {
	targetKey = normalizeSyncTargetKey(targetKey)
	if err := s.ensureSyncState(targetKey); err != nil {
		return nil, err
	}
	return s.getSyncState(targetKey)
}

func (s *Store) ListPendingSyncMutations(targetKey string, limit int) ([]SyncMutation, error) {
	targetKey = normalizeSyncTargetKey(targetKey)
	if limit <= 0 {
		limit = 100
	}
	// Only return mutations for enrolled projects or empty-project (global) mutations.
	// Empty-project mutations always sync regardless of enrollment.
	rows, err := s.queryItHook(s.db, `
		SELECT sm.seq, sm.target_key, sm.entity, sm.entity_key, sm.op, sm.payload, sm.source, sm.project, sm.occurred_at, sm.acked_at
		FROM sync_mutations sm
		LEFT JOIN sync_enrolled_projects sep ON sm.project = sep.project
		WHERE sm.target_key = ? AND sm.acked_at IS NULL
		  AND (sm.project = '' OR sep.project IS NOT NULL)
		ORDER BY sm.seq ASC
		LIMIT ?`, targetKey, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var mutations []SyncMutation
	for rows.Next() {
		var mutation SyncMutation
		if err := rows.Scan(&mutation.Seq, &mutation.TargetKey, &mutation.Entity, &mutation.EntityKey, &mutation.Op, &mutation.Payload, &mutation.Source, &mutation.Project, &mutation.OccurredAt, &mutation.AckedAt); err != nil {
			return nil, err
		}
		mutations = append(mutations, mutation)
	}
	return mutations, rows.Err()
}

// SkipAckNonEnrolledMutations acks (marks as skipped) all pending mutations
// that belong to non-enrolled projects, preventing journal bloat. Empty-project
// mutations are never skipped — they always sync regardless of enrollment.
func (s *Store) SkipAckNonEnrolledMutations(targetKey string) (int64, error) {
	targetKey = normalizeSyncTargetKey(targetKey)
	res, err := s.execHook(s.db, `
		UPDATE sync_mutations
		SET acked_at = datetime('now')
		WHERE target_key = ?
		  AND acked_at IS NULL
		  AND project != ''
		  AND project NOT IN (SELECT project FROM sync_enrolled_projects)`,
		targetKey,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) AckSyncMutations(targetKey string, lastAckedSeq int64) error {
	if lastAckedSeq <= 0 {
		return nil
	}
	targetKey = normalizeSyncTargetKey(targetKey)
	return s.withTx(func(tx *sql.Tx) error {
		state, err := s.getSyncStateTx(tx, targetKey)
		if err != nil {
			return err
		}
		if _, err := s.execHook(tx,
			`UPDATE sync_mutations SET acked_at = datetime('now') WHERE target_key = ? AND seq <= ? AND acked_at IS NULL`,
			targetKey, lastAckedSeq,
		); err != nil {
			return err
		}
		acked := state.LastAckedSeq
		if lastAckedSeq > acked {
			acked = lastAckedSeq
		}
		lifecycle := SyncLifecyclePending
		if acked >= state.LastEnqueuedSeq {
			lifecycle = SyncLifecycleHealthy
		}
		_, err = s.execHook(tx,
			`UPDATE sync_state
			 SET last_acked_seq = ?, lifecycle = ?, updated_at = datetime('now')
			 WHERE target_key = ?`,
			acked, lifecycle, targetKey,
		)
		return err
	})
}

// AckSyncMutationSeqs acknowledges specific mutation sequence numbers without
// requiring them to be contiguous.
func (s *Store) AckSyncMutationSeqs(targetKey string, seqs []int64) error {
	if len(seqs) == 0 {
		return nil
	}
	targetKey = normalizeSyncTargetKey(targetKey)
	return s.withTx(func(tx *sql.Tx) error {
		state, err := s.getSyncStateTx(tx, targetKey)
		if err != nil {
			return err
		}
		maxSeq := state.LastAckedSeq
		for _, seq := range seqs {
			if seq <= 0 {
				continue
			}
			if _, err := s.execHook(tx,
				`UPDATE sync_mutations SET acked_at = datetime('now') WHERE target_key = ? AND seq = ? AND acked_at IS NULL`,
				targetKey, seq,
			); err != nil {
				return err
			}
			if seq > maxSeq {
				maxSeq = seq
			}
		}
		var remaining int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM sync_mutations WHERE target_key = ? AND acked_at IS NULL`, targetKey).Scan(&remaining); err != nil {
			return err
		}
		lifecycle := SyncLifecyclePending
		if remaining == 0 {
			lifecycle = SyncLifecycleHealthy
		}
		_, err = s.execHook(tx,
			`UPDATE sync_state SET last_acked_seq = ?, lifecycle = ?, updated_at = datetime('now') WHERE target_key = ?`,
			maxSeq, lifecycle, targetKey,
		)
		return err
	})
}

func (s *Store) AcquireSyncLease(targetKey, owner string, ttl time.Duration, now time.Time) (bool, error) {
	targetKey = normalizeSyncTargetKey(targetKey)
	if ttl <= 0 {
		ttl = time.Minute
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	var acquired bool
	err := s.withTx(func(tx *sql.Tx) error {
		state, err := s.getSyncStateTx(tx, targetKey)
		if err != nil {
			return err
		}
		if state.LeaseUntil != nil {
			leaseUntil, err := time.Parse(time.RFC3339, *state.LeaseUntil)
			if err == nil && leaseUntil.After(now) && derefString(state.LeaseOwner) != "" && derefString(state.LeaseOwner) != owner {
				acquired = false
				return nil
			}
		}
		leaseUntil := now.Add(ttl).UTC().Format(time.RFC3339)
		_, err = s.execHook(tx,
			`UPDATE sync_state
			 SET lease_owner = ?, lease_until = ?, updated_at = datetime('now')
			 WHERE target_key = ?`,
			owner, leaseUntil, targetKey,
		)
		if err == nil {
			acquired = true
		}
		return err
	})
	return acquired, err
}

func (s *Store) ReleaseSyncLease(targetKey, owner string) error {
	targetKey = normalizeSyncTargetKey(targetKey)
	_, err := s.execHook(s.db,
		`UPDATE sync_state
		 SET lease_owner = NULL, lease_until = NULL, updated_at = datetime('now')
		 WHERE target_key = ? AND (lease_owner = ? OR lease_owner IS NULL OR lease_owner = '')`,
		targetKey, owner,
	)
	return err
}

func (s *Store) MarkSyncFailure(targetKey, message string, backoffUntil time.Time) error {
	targetKey = normalizeSyncTargetKey(targetKey)
	backoff := backoffUntil.UTC().Format(time.RFC3339)
	return s.withTx(func(tx *sql.Tx) error {
		state, err := s.getSyncStateTx(tx, targetKey)
		if err != nil {
			return err
		}
		_, err = s.execHook(tx,
			`UPDATE sync_state
			 SET lifecycle = ?, consecutive_failures = ?, backoff_until = ?, last_error = ?, updated_at = datetime('now')
			 WHERE target_key = ?`,
			SyncLifecycleDegraded, state.ConsecutiveFailures+1, backoff, message, targetKey,
		)
		return err
	})
}

func (s *Store) MarkSyncHealthy(targetKey string) error {
	targetKey = normalizeSyncTargetKey(targetKey)
	_, err := s.execHook(s.db,
		`UPDATE sync_state
		 SET lifecycle = ?, consecutive_failures = 0, backoff_until = NULL, last_error = NULL, updated_at = datetime('now')
		 WHERE target_key = ?`,
		SyncLifecycleHealthy, targetKey,
	)
	return err
}

func (s *Store) ApplyPulledMutation(targetKey string, mutation SyncMutation) error {
	targetKey = normalizeSyncTargetKey(targetKey)
	return s.withTx(func(tx *sql.Tx) error {
		state, err := s.getSyncStateTx(tx, targetKey)
		if err != nil {
			return err
		}
		if mutation.Seq <= state.LastPulledSeq {
			return nil
		}

		switch mutation.Entity {
		case SyncEntitySession:
			var payload syncSessionPayload
			if err := decodeSyncPayload([]byte(mutation.Payload), &payload); err != nil {
				return err
			}
			if err := s.applySessionPayloadTx(tx, payload); err != nil {
				return err
			}
		case SyncEntityPrompt:
			var payload syncPromptPayload
			if err := decodeSyncPayload([]byte(mutation.Payload), &payload); err != nil {
				return err
			}
			if err := s.applyPromptUpsertTx(tx, payload); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown sync entity %q", mutation.Entity)
		}

		_, err = s.execHook(tx,
			`UPDATE sync_state
			 SET last_pulled_seq = ?, lifecycle = ?, consecutive_failures = 0, backoff_until = NULL, last_error = NULL, updated_at = datetime('now')
			 WHERE target_key = ?`,
			mutation.Seq, SyncLifecycleHealthy, targetKey,
		)
		return err
	})
}

// EnrollProject registers a project for cloud sync. Idempotent — re-enrolling
// an already-enrolled project is a no-op.
func (s *Store) EnrollProject(project string) error {
	if project == "" {
		return fmt.Errorf("project name must not be empty")
	}
	return s.withTx(func(tx *sql.Tx) error {
		res, err := s.execHook(tx,
			`INSERT OR IGNORE INTO sync_enrolled_projects (project) VALUES (?)`,
			project,
		)
		if err != nil {
			return err
		}
		rowsAffected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if rowsAffected == 0 {
			return nil
		}
		return s.backfillProjectSyncMutationsTx(tx, project)
	})
}

// UnenrollProject removes a project from cloud sync enrollment. Idempotent —
// unenrolling a non-enrolled project is a no-op.
func (s *Store) UnenrollProject(project string) error {
	if project == "" {
		return fmt.Errorf("project name must not be empty")
	}
	_, err := s.execHook(s.db,
		`DELETE FROM sync_enrolled_projects WHERE project = ?`,
		project,
	)
	return err
}

// ListEnrolledProjects returns all projects currently enrolled for cloud sync,
// ordered alphabetically by project name.
func (s *Store) ListEnrolledProjects() ([]EnrolledProject, error) {
	rows, err := s.queryItHook(s.db,
		`SELECT project, enrolled_at FROM sync_enrolled_projects ORDER BY project ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []EnrolledProject
	for rows.Next() {
		var ep EnrolledProject
		if err := rows.Scan(&ep.Project, &ep.EnrolledAt); err != nil {
			return nil, err
		}
		projects = append(projects, ep)
	}
	return projects, rows.Err()
}

// IsProjectEnrolled returns true if the given project is enrolled for cloud sync.
func (s *Store) IsProjectEnrolled(project string) (bool, error) {
	var exists int
	err := s.db.QueryRow(
		`SELECT 1 FROM sync_enrolled_projects WHERE project = ? LIMIT 1`,
		project,
	).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

type MigrateResult struct {
	Migrated        bool  `json:"migrated"`
	SessionsUpdated int64 `json:"sessions_updated"`
	PromptsUpdated  int64 `json:"prompts_updated"`
}

func (s *Store) MigrateProject(oldName, newName string) (*MigrateResult, error) {
	if oldName == "" || newName == "" || oldName == newName {
		return &MigrateResult{}, nil
	}

	// Check if old project has any records (short-circuit on first match)
	var exists bool
	err := s.db.QueryRow(
		`SELECT EXISTS(
			SELECT 1 FROM sessions WHERE project = ?
			UNION ALL
			SELECT 1 FROM user_prompts WHERE project = ?
			UNION ALL
			SELECT 1 FROM memory_items WHERE project_id = ?
		)`, oldName, oldName, oldName,
	).Scan(&exists)
	if err != nil {
		return nil, fmt.Errorf("check old project: %w", err)
	}
	if !exists {
		return &MigrateResult{}, nil
	}

	result := &MigrateResult{Migrated: true}

	err = s.withTx(func(tx *sql.Tx) error {
		res, err := s.execHook(tx, `UPDATE sessions SET project = ? WHERE project = ?`, newName, oldName)
		if err != nil {
			return fmt.Errorf("migrate sessions: %w", err)
		}
		result.SessionsUpdated, _ = res.RowsAffected()

		res, err = s.execHook(tx, `UPDATE user_prompts SET project = ? WHERE project = ?`, newName, oldName)
		if err != nil {
			return fmt.Errorf("migrate prompts: %w", err)
		}
		result.PromptsUpdated, _ = res.RowsAffected()

		_, err = s.execHook(tx, `UPDATE memory_items SET project_id = ? WHERE project_id = ?`, newName, oldName)
		if err != nil {
			return fmt.Errorf("migrate memory_items: %w", err)
		}

		// Enqueue sync mutations so cloud sync picks up the migrated records.
		// Same pattern used by EnrollProject and MergeProjects.
		return s.backfillProjectSyncMutationsTx(tx, newName)
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// ProjectNameCount holds a project name and how many memory items it has.
type ProjectNameCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// ListProjectNames returns all distinct project names from sessions and
// memory_items, ordered alphabetically. Used for fuzzy matching and consolidation.
func (s *Store) ListProjectNames() ([]string, error) {
	rows, err := s.queryItHook(s.db,
		`SELECT DISTINCT project FROM (
			 SELECT DISTINCT project FROM sessions
			 WHERE project IS NOT NULL AND project != ''
			 UNION
			 SELECT DISTINCT project_id AS project FROM memory_items
			 WHERE status = 'active' AND project_id IS NOT NULL AND project_id != ''
		 )
		 ORDER BY project`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		results = append(results, name)
	}
	return results, rows.Err()
}

// ProjectStats holds aggregate statistics for a single project.
type ProjectStats struct {
	Name         string   `json:"name"`
	MemoryCount  int      `json:"memory_count"`
	SessionCount int      `json:"session_count"`
	PromptCount  int      `json:"prompt_count"`
	Directories  []string `json:"directories"` // unique directories from sessions
}

// ListProjectsWithStats returns all projects with aggregated counts.
// Ordered by memory count descending.
func (s *Store) ListProjectsWithStats() ([]ProjectStats, error) {
	// Memory item counts per project
	memRows, err := s.queryItHook(s.db,
		`SELECT project_id, COUNT(*) as cnt
		 FROM memory_items
		 WHERE status = 'active' AND project_id IS NOT NULL AND project_id != ''
		 GROUP BY project_id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list projects mem items: %w", err)
	}
	defer memRows.Close()

	statsMap := make(map[string]*ProjectStats)
	for memRows.Next() {
		var name string
		var cnt int
		if err := memRows.Scan(&name, &cnt); err != nil {
			return nil, err
		}
		statsMap[name] = &ProjectStats{Name: name, MemoryCount: cnt}
	}
	if err := memRows.Err(); err != nil {
		return nil, err
	}

	// Session counts + directories per project
	sessRows, err := s.queryItHook(s.db,
		`SELECT project, COUNT(*) as cnt, directory
		 FROM sessions
		 WHERE project IS NOT NULL AND project != ''
		 GROUP BY project, directory`,
	)
	if err != nil {
		return nil, fmt.Errorf("list projects sessions: %w", err)
	}
	defer sessRows.Close()

	type projDir struct {
		count int
		dirs  map[string]bool
	}
	sessData := make(map[string]*projDir)
	for sessRows.Next() {
		var name, dir string
		var cnt int
		if err := sessRows.Scan(&name, &cnt, &dir); err != nil {
			return nil, err
		}
		if sessData[name] == nil {
			sessData[name] = &projDir{dirs: make(map[string]bool)}
		}
		sessData[name].count += cnt
		if dir != "" {
			sessData[name].dirs[dir] = true
		}
	}
	if err := sessRows.Err(); err != nil {
		return nil, err
	}

	for name, sd := range sessData {
		if statsMap[name] == nil {
			statsMap[name] = &ProjectStats{Name: name}
		}
		statsMap[name].SessionCount = sd.count
		for d := range sd.dirs {
			statsMap[name].Directories = append(statsMap[name].Directories, d)
		}
	}

	// Prompt counts per project
	promptRows, err := s.queryItHook(s.db,
		`SELECT project, COUNT(*) as cnt
		 FROM user_prompts
		 WHERE project IS NOT NULL AND project != ''
		 GROUP BY project`,
	)
	if err != nil {
		return nil, fmt.Errorf("list projects prompts: %w", err)
	}
	defer promptRows.Close()

	for promptRows.Next() {
		var name string
		var cnt int
		if err := promptRows.Scan(&name, &cnt); err != nil {
			return nil, err
		}
		if statsMap[name] == nil {
			statsMap[name] = &ProjectStats{Name: name}
		}
		statsMap[name].PromptCount = cnt
	}
	if err := promptRows.Err(); err != nil {
		return nil, err
	}

	// Convert to slice, sorted by observation count descending
	results := make([]ProjectStats, 0, len(statsMap))
	for _, ps := range statsMap {
		results = append(results, *ps)
	}
	// Simple insertion sort — project lists are small
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j].MemoryCount > results[j-1].MemoryCount; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}

	return results, nil
}

// MergeResult summarizes the result of merging multiple project name variants
// into a single canonical project name.
type MergeResult struct {
	Canonical           string   `json:"canonical"`
	SourcesMerged       []string `json:"sources_merged"`
	ObservationsUpdated int64    `json:"observations_updated"`
	SessionsUpdated     int64    `json:"sessions_updated"`
	PromptsUpdated      int64    `json:"prompts_updated"`
}

// MergeProjects migrates all records from each source project name into the
// canonical name. Sources that equal the canonical (after normalization) or
// have no records are silently skipped — the operation is idempotent.
// All updates are performed inside a single transaction for atomicity.
func (s *Store) MergeProjects(sources []string, canonical string) (*MergeResult, error) {
	canonical, _ = NormalizeProject(canonical)
	if canonical == "" {
		return nil, fmt.Errorf("canonical project name must not be empty")
	}

	result := &MergeResult{Canonical: canonical}

	err := s.withTx(func(tx *sql.Tx) error {
		for _, src := range sources {
			src, _ = NormalizeProject(src)
			if src == "" || src == canonical {
				continue
			}

			res, err := s.execHook(tx, `UPDATE sessions SET project = ? WHERE project = ?`, canonical, src)
			if err != nil {
				return fmt.Errorf("merge sessions %q → %q: %w", src, canonical, err)
			}
			n, _ := res.RowsAffected()
			result.SessionsUpdated += n

			res, err = s.execHook(tx, `UPDATE user_prompts SET project = ? WHERE project = ?`, canonical, src)
			if err != nil {
				return fmt.Errorf("merge prompts %q → %q: %w", src, canonical, err)
			}
			n, _ = res.RowsAffected()
			result.PromptsUpdated += n

			_, err = s.execHook(tx, `UPDATE memory_items SET project_id = ? WHERE project_id = ?`, canonical, src)
			if err != nil {
				return fmt.Errorf("merge memory_items %q → %q: %w", src, canonical, err)
			}

			result.SourcesMerged = append(result.SourcesMerged, src)
		}
		// Enqueue sync mutations so cloud sync picks up the merged records.
		// Same pattern used by EnrollProject.
		return s.backfillProjectSyncMutationsTx(tx, canonical)
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// PruneResult holds the outcome of pruning a single project.
type PruneResult struct {
	Project         string `json:"project"`
	SessionsDeleted int64  `json:"sessions_deleted"`
	PromptsDeleted  int64  `json:"prompts_deleted"`
}

// PruneProject removes all sessions and prompts for a project that has zero
// active memory items. Returns an error if the project still has
// memory items — the caller must verify first.
func (s *Store) PruneProject(project string) (*PruneResult, error) {
	if project == "" {
		return nil, fmt.Errorf("project name must not be empty")
	}

	// Safety check: refuse to prune if memory items exist.
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM memory_items WHERE project_id = ? AND status = 'active'`, project).Scan(&count)
	if err != nil {
		return nil, fmt.Errorf("count memories: %w", err)
	}
	if count > 0 {
		return nil, fmt.Errorf("project %q still has %d active memories — cannot prune", project, count)
	}

	result := &PruneResult{Project: project}

	err = s.withTx(func(tx *sql.Tx) error {
		res, err := s.execHook(tx, `DELETE FROM user_prompts WHERE project = ?`, project)
		if err != nil {
			return fmt.Errorf("prune prompts: %w", err)
		}
		result.PromptsDeleted, _ = res.RowsAffected()

		res, err = s.execHook(tx, `DELETE FROM sessions WHERE project = ?`, project)
		if err != nil {
			return fmt.Errorf("prune sessions: %w", err)
		}
		result.SessionsDeleted, _ = res.RowsAffected()

		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

func (s *Store) withTx(fn func(tx *sql.Tx) error) error {
	tx, err := s.beginTxHook()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return s.commitHook(tx)
}

func (s *Store) createSessionTx(tx *sql.Tx, id, project, directory string) error {
	_, err := s.execHook(tx,
		`INSERT INTO sessions (id, project, directory) VALUES (?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   project   = CASE WHEN sessions.project = '' THEN excluded.project ELSE sessions.project END,
		   directory = CASE WHEN sessions.directory = '' THEN excluded.directory ELSE sessions.directory END`,
		id, project, directory,
	)
	return err
}

func (s *Store) ensureSyncState(targetKey string) error {
	_, err := s.execHook(s.db,
		`INSERT OR IGNORE INTO sync_state (target_key, lifecycle, updated_at) VALUES (?, ?, datetime('now'))`,
		targetKey, SyncLifecycleIdle,
	)
	return err
}

func (s *Store) getSyncState(targetKey string) (*SyncState, error) {
	row := s.db.QueryRow(`
		SELECT target_key, lifecycle, last_enqueued_seq, last_acked_seq, last_pulled_seq,
		       consecutive_failures, backoff_until, lease_owner, lease_until, last_error, updated_at
		FROM sync_state WHERE target_key = ?`, targetKey)
	var state SyncState
	if err := row.Scan(&state.TargetKey, &state.Lifecycle, &state.LastEnqueuedSeq, &state.LastAckedSeq, &state.LastPulledSeq, &state.ConsecutiveFailures, &state.BackoffUntil, &state.LeaseOwner, &state.LeaseUntil, &state.LastError, &state.UpdatedAt); err != nil {
		return nil, err
	}
	return &state, nil
}

func (s *Store) getSyncStateTx(tx *sql.Tx, targetKey string) (*SyncState, error) {
	if _, err := s.execHook(tx,
		`INSERT OR IGNORE INTO sync_state (target_key, lifecycle, updated_at) VALUES (?, ?, datetime('now'))`,
		targetKey, SyncLifecycleIdle,
	); err != nil {
		return nil, err
	}
	row := tx.QueryRow(`
		SELECT target_key, lifecycle, last_enqueued_seq, last_acked_seq, last_pulled_seq,
		       consecutive_failures, backoff_until, lease_owner, lease_until, last_error, updated_at
		FROM sync_state WHERE target_key = ?`, targetKey)
	var state SyncState
	if err := row.Scan(&state.TargetKey, &state.Lifecycle, &state.LastEnqueuedSeq, &state.LastAckedSeq, &state.LastPulledSeq, &state.ConsecutiveFailures, &state.BackoffUntil, &state.LeaseOwner, &state.LeaseUntil, &state.LastError, &state.UpdatedAt); err != nil {
		return nil, err
	}
	return &state, nil
}

func (s *Store) backfillProjectSyncMutationsTx(tx *sql.Tx, project string) error {
	if err := s.backfillSessionSyncMutationsTx(tx, project); err != nil {
		return err
	}
	return s.backfillPromptSyncMutationsTx(tx, project)
}

func (s *Store) repairEnrolledProjectSyncMutations() error {
	return s.withTx(func(tx *sql.Tx) error {
		rows, err := s.queryItHook(tx,
			`SELECT project FROM sync_enrolled_projects ORDER BY project ASC`,
		)
		if err != nil {
			return err
		}
		defer rows.Close()

		var projects []string
		for rows.Next() {
			var project string
			if err := rows.Scan(&project); err != nil {
				return err
			}
			projects = append(projects, project)
		}
		if err := rows.Err(); err != nil {
			return err
		}

		for _, project := range projects {
			if err := s.backfillProjectSyncMutationsTx(tx, project); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) backfillSessionSyncMutationsTx(tx *sql.Tx, project string) error {
	rows, err := s.queryItHook(tx, `
		SELECT id, project, directory, ended_at, summary
		FROM sessions
		WHERE project = ?
		  AND NOT EXISTS (
			SELECT 1
			FROM sync_mutations sm
			WHERE sm.target_key = ?
			  AND sm.entity = ?
			  AND sm.entity_key = sessions.id
			  AND sm.source = ?
		  )
		ORDER BY started_at ASC, id ASC`,
		project, DefaultSyncTargetKey, SyncEntitySession, SyncSourceLocal,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var payload syncSessionPayload
		if err := rows.Scan(&payload.ID, &payload.Project, &payload.Directory, &payload.EndedAt, &payload.Summary); err != nil {
			return err
		}
		if err := s.enqueueSyncMutationTx(tx, SyncEntitySession, payload.ID, SyncOpUpsert, payload); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Store) backfillPromptSyncMutationsTx(tx *sql.Tx, project string) error {
	rows, err := s.queryItHook(tx, `
		SELECT sync_id, session_id, content, project
		FROM user_prompts
		WHERE ifnull(project, '') = ?
		  AND NOT EXISTS (
			SELECT 1
			FROM sync_mutations sm
			WHERE sm.target_key = ?
			  AND sm.entity = ?
			  AND sm.entity_key = user_prompts.sync_id
			  AND sm.source = ?
		  )
		ORDER BY id ASC`,
		project, DefaultSyncTargetKey, SyncEntityPrompt, SyncSourceLocal,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var payload syncPromptPayload
		if err := rows.Scan(&payload.SyncID, &payload.SessionID, &payload.Content, &payload.Project); err != nil {
			return err
		}
		if err := s.enqueueSyncMutationTx(tx, SyncEntityPrompt, payload.SyncID, SyncOpUpsert, payload); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Store) enqueueSyncMutationTx(tx *sql.Tx, entity, entityKey, op string, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	project := extractProjectFromPayload(payload)
	if _, err := s.execHook(tx,
		`INSERT OR IGNORE INTO sync_state (target_key, lifecycle, updated_at) VALUES (?, ?, datetime('now'))`,
		DefaultSyncTargetKey, SyncLifecycleIdle,
	); err != nil {
		return err
	}
	res, err := s.execHook(tx,
		`INSERT INTO sync_mutations (target_key, entity, entity_key, op, payload, source, project)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		DefaultSyncTargetKey, entity, entityKey, op, string(encoded), SyncSourceLocal, project,
	)
	if err != nil {
		return err
	}
	seq, err := res.LastInsertId()
	if err != nil {
		return err
	}
	_, err = s.execHook(tx,
		`UPDATE sync_state
		 SET lifecycle = ?, last_enqueued_seq = ?, updated_at = datetime('now')
		 WHERE target_key = ?`,
		SyncLifecyclePending, seq, DefaultSyncTargetKey,
	)
	return err
}

// extractProjectFromPayload returns the project string from a sync payload struct.
// It handles both string and *string Project fields across all entity payload types.
// Returns empty string if the payload has no project or project is nil.
func extractProjectFromPayload(payload any) string {
	switch p := payload.(type) {
	case syncSessionPayload:
		return p.Project
	case syncPromptPayload:
		if p.Project != nil {
			return *p.Project
		}
		return ""
	default:
		// Fallback: marshal to JSON and extract $.project via json.Unmarshal.
		data, err := json.Marshal(payload)
		if err != nil {
			return ""
		}
		var generic struct {
			Project *string `json:"project"`
		}
		if err := json.Unmarshal(data, &generic); err != nil || generic.Project == nil {
			return ""
		}
		return *generic.Project
	}
}

func decodeSyncPayload(payload []byte, dest any) error {
	trimmed := strings.TrimSpace(string(payload))
	if trimmed == "" {
		return fmt.Errorf("empty payload")
	}
	if trimmed[0] != '"' {
		return json.Unmarshal([]byte(trimmed), dest)
	}
	var encoded string
	if err := json.Unmarshal([]byte(trimmed), &encoded); err != nil {
		return err
	}
	return json.Unmarshal([]byte(encoded), dest)
}

func (s *Store) applySessionPayloadTx(tx *sql.Tx, payload syncSessionPayload) error {
	_, err := s.execHook(tx,
		`INSERT INTO sessions (id, project, directory, ended_at, summary)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   project = excluded.project,
		   directory = excluded.directory,
		   ended_at = COALESCE(excluded.ended_at, sessions.ended_at),
		   summary = COALESCE(excluded.summary, sessions.summary)`,
		payload.ID, payload.Project, payload.Directory, payload.EndedAt, payload.Summary,
	)
	return err
}

func (s *Store) applyPromptUpsertTx(tx *sql.Tx, payload syncPromptPayload) error {
	var existingID int64
	err := tx.QueryRow(`SELECT id FROM user_prompts WHERE sync_id = ? ORDER BY id DESC LIMIT 1`, payload.SyncID).Scan(&existingID)
	if err == sql.ErrNoRows {
		_, err = s.execHook(tx,
			`INSERT INTO user_prompts (sync_id, session_id, content, project) VALUES (?, ?, ?, ?)`,
			payload.SyncID, payload.SessionID, payload.Content, payload.Project,
		)
		return err
	}
	if err != nil {
		return err
	}
	_, err = s.execHook(tx,
		`UPDATE user_prompts SET session_id = ?, content = ?, project = ? WHERE id = ?`,
		payload.SessionID, payload.Content, payload.Project, existingID,
	)
	return err
}

func (s *Store) addColumnIfNotExists(tableName, columnName, definition string) error {
	rows, err := s.queryItHook(s.db, fmt.Sprintf("PRAGMA table_info(%s)", tableName))
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if name == columnName {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	_, err = s.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", tableName, columnName, definition))
	return err
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func normalizeScope(scope string) string {
	v := strings.TrimSpace(strings.ToLower(scope))
	if v == "personal" {
		return "personal"
	}
	return "project"
}

// NormalizeProject applies canonical project name normalization:
// lowercase + trim whitespace + collapse consecutive hyphens/underscores.
// Returns the normalized name and a warning message if the name was changed
// (empty string if no change was needed).
// Exported so MCP and CLI handlers can surface the warning to users.
func NormalizeProject(project string) (normalized string, warning string) {
	if project == "" {
		return "", ""
	}
	n := strings.TrimSpace(strings.ToLower(project))
	// Collapse multiple consecutive hyphens
	for strings.Contains(n, "--") {
		n = strings.ReplaceAll(n, "--", "-")
	}
	// Collapse multiple consecutive underscores
	for strings.Contains(n, "__") {
		n = strings.ReplaceAll(n, "__", "_")
	}
	if n == project {
		return n, ""
	}
	return n, fmt.Sprintf("⚠️ Project name normalized: %q → %q", project, n)
}

// SuggestTopicKey generates a stable topic key suggestion from type/title/content.
// It infers a topic family (e.g. architecture/*, bug/*) and then appends
// a normalized segment from title/content for stable cross-session keys.
func SuggestTopicKey(typ, title, content string) string {
	family := inferTopicFamily(typ, title, content)
	cleanTitle := stripPrivateTags(title)
	segment := normalizeTopicSegment(cleanTitle)

	if segment == "" {
		cleanContent := stripPrivateTags(content)
		words := strings.Fields(strings.ToLower(cleanContent))
		if len(words) > 8 {
			words = words[:8]
		}
		segment = normalizeTopicSegment(strings.Join(words, " "))
	}

	if segment == "" {
		segment = "general"
	}

	if strings.HasPrefix(segment, family+"-") {
		segment = strings.TrimPrefix(segment, family+"-")
	}
	if segment == "" || segment == family {
		segment = "general"
	}

	return family + "/" + segment
}

func inferTopicFamily(typ, title, content string) string {
	t := strings.TrimSpace(strings.ToLower(typ))
	switch t {
	case "architecture", "design", "adr", "refactor":
		return "architecture"
	case "bug", "bugfix", "fix", "incident", "hotfix":
		return "bug"
	case "decision":
		return "decision"
	case "pattern", "convention", "guideline":
		return "pattern"
	case "config", "setup", "infra", "infrastructure", "ci":
		return "config"
	case "discovery", "investigation", "root_cause", "root-cause":
		return "discovery"
	case "learning", "learn":
		return "learning"
	case "session_summary":
		return "session"
	}

	text := strings.ToLower(title + " " + content)
	if hasAny(text, "bug", "fix", "panic", "error", "crash", "regression", "incident", "hotfix") {
		return "bug"
	}
	if hasAny(text, "architecture", "design", "adr", "boundary", "hexagonal", "refactor") {
		return "architecture"
	}
	if hasAny(text, "decision", "tradeoff", "chose", "choose", "decide") {
		return "decision"
	}
	if hasAny(text, "pattern", "convention", "naming", "guideline") {
		return "pattern"
	}
	if hasAny(text, "config", "setup", "environment", "env", "docker", "pipeline") {
		return "config"
	}
	if hasAny(text, "discovery", "investigate", "investigation", "found", "root cause") {
		return "discovery"
	}
	if hasAny(text, "learned", "learning") {
		return "learning"
	}

	if t != "" && t != "manual" {
		return normalizeTopicSegment(t)
	}

	return "topic"
}

func hasAny(text string, words ...string) bool {
	for _, w := range words {
		if strings.Contains(text, w) {
			return true
		}
	}
	return false
}

func normalizeTopicSegment(s string) string {
	v := strings.ToLower(strings.TrimSpace(s))
	if v == "" {
		return ""
	}
	re := regexp.MustCompile(`[^a-z0-9]+`)
	v = re.ReplaceAllString(v, " ")
	v = strings.Join(strings.Fields(v), "-")
	if len(v) > 100 {
		v = v[:100]
	}
	return v
}

func normalizeTopicKey(topic string) string {
	v := strings.TrimSpace(strings.ToLower(topic))
	if v == "" {
		return ""
	}
	v = strings.Join(strings.Fields(v), "-")
	if len(v) > 120 {
		v = v[:120]
	}
	return v
}

func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func hashNormalized(content string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(content), " "))
	h := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(h[:])
}

func dedupeWindowExpression(window time.Duration) string {
	if window <= 0 {
		window = 15 * time.Minute
	}
	minutes := int(window.Minutes())
	if minutes < 1 {
		minutes = 1
	}
	return "-" + strconv.Itoa(minutes) + " minutes"
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func normalizeSyncTargetKey(targetKey string) string {
	if strings.TrimSpace(targetKey) == "" {
		return DefaultSyncTargetKey
	}
	return strings.TrimSpace(strings.ToLower(targetKey))
}

func newSyncID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UTC().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(b)
}

func normalizeExistingSyncID(existing, prefix string) string {
	if strings.TrimSpace(existing) != "" {
		return existing
	}
	return newSyncID(prefix)
}

// privateTagRegex matches <private>...</private> tags and their contents.
// Supports multiline and nested content. Case-insensitive.
var privateTagRegex = regexp.MustCompile(`(?is)<private>.*?</private>`)

// stripPrivateTags removes all <private>...</private> content from a string.
// This ensures sensitive information (API keys, passwords, personal data)
// is never persisted to the memory database.
func stripPrivateTags(s string) string {
	result := privateTagRegex.ReplaceAllString(s, "[REDACTED]")
	result = strings.TrimSpace(result)
	return result
}

// sanitizeFTS wraps each word in quotes so FTS5 doesn't choke on special chars.
// "fix auth bug" → `"fix" "auth" "bug"`
func sanitizeFTS(query string) string {
	words := strings.Fields(query)
	for i, w := range words {
		// Strip existing quotes to avoid double-quoting
		w = strings.Trim(w, `"`)
		words[i] = `"` + w + `"`
	}
	return strings.Join(words, " ")
}

func sanitizeFTSOR(query string) string {
	words := strings.Fields(query)
	for i, w := range words {
		w = strings.Trim(w, `"`)
		words[i] = `"` + w + `"`
	}
	return strings.Join(words, " OR ")
}

// PassiveCaptureParams holds the input for passive memory capture.
type PassiveCaptureParams struct {
	SessionID string `json:"session_id"`
	Content   string `json:"content"`
	Project   string `json:"project,omitempty"`
	Source    string `json:"source,omitempty"` // e.g. "subagent-stop", "session-end"
}

// PassiveCaptureResult holds the output of passive memory capture.
type PassiveCaptureResult struct {
	Extracted  int `json:"extracted"`  // Total learnings found in text
	Saved      int `json:"saved"`      // New memories created
	Duplicates int `json:"duplicates"` // Skipped because already existed
}

// learningHeaderPattern matches section headers for learnings in both English and Spanish.
var learningHeaderPattern = regexp.MustCompile(
	`(?im)^#{2,3}\s+(?:Aprendizajes(?:\s+Clave)?|Key\s+Learnings?|Learnings?):?\s*$`,
)

const (
	minLearningLength = 20
	minLearningWords  = 4
)

// ExtractLearnings parses structured learning items from text.
// It looks for sections like "## Key Learnings:" or "## Aprendizajes Clave:"
// and extracts numbered (1. text) or bullet (- text) items.
// Returns learnings from the LAST matching section (most recent output).
func ExtractLearnings(text string) []string {
	matches := learningHeaderPattern.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return nil
	}

	// Process sections in reverse — use first valid one (most recent)
	for i := len(matches) - 1; i >= 0; i-- {
		sectionStart := matches[i][1]
		sectionText := text[sectionStart:]

		// Cut off at next major section header
		if nextHeader := regexp.MustCompile(`\n#{1,3} `).FindStringIndex(sectionText); nextHeader != nil {
			sectionText = sectionText[:nextHeader[0]]
		}

		var learnings []string

		// Try numbered items: "1. text" or "1) text"
		numbered := regexp.MustCompile(`(?m)^\s*\d+[.)]\s+(.+)`).FindAllStringSubmatch(sectionText, -1)
		if len(numbered) > 0 {
			for _, m := range numbered {
				cleaned := cleanMarkdown(m[1])
				if len(cleaned) >= minLearningLength && len(strings.Fields(cleaned)) >= minLearningWords {
					learnings = append(learnings, cleaned)
				}
			}
		}

		// Fall back to bullet items: "- text" or "* text"
		if len(learnings) == 0 {
			bullets := regexp.MustCompile(`(?m)^\s*[-*]\s+(.+)`).FindAllStringSubmatch(sectionText, -1)
			for _, m := range bullets {
				cleaned := cleanMarkdown(m[1])
				if len(cleaned) >= minLearningLength && len(strings.Fields(cleaned)) >= minLearningWords {
					learnings = append(learnings, cleaned)
				}
			}
		}

		if len(learnings) > 0 {
			return learnings
		}
	}

	return nil
}

// cleanMarkdown strips basic markdown formatting and collapses whitespace.
func cleanMarkdown(text string) string {
	text = regexp.MustCompile(`\*\*([^*]+)\*\*`).ReplaceAllString(text, "$1") // bold
	text = regexp.MustCompile("`([^`]+)`").ReplaceAllString(text, "$1")       // inline code
	text = regexp.MustCompile(`\*([^*]+)\*`).ReplaceAllString(text, "$1")     // italic
	return strings.TrimSpace(strings.Join(strings.Fields(text), " "))
}

func (s *Store) PassiveCapture(p PassiveCaptureParams) (*PassiveCaptureResult, error) {
	p.Project, _ = NormalizeProject(p.Project)
	result := &PassiveCaptureResult{}
	learnings := ExtractLearnings(p.Content)
	result.Extracted = len(learnings)
	if len(learnings) == 0 {
		return result, nil
	}
	for _, learning := range learnings {
		var existingID int64
		err := s.db.QueryRow(
			`SELECT id FROM memory_items
			 WHERE kind = 'learning'
			   AND project_id = ?
			   AND status = 'active'
			   AND substr(body, 1, 64) = substr(?, 1, 64)
			 LIMIT 1`,
			p.Project, learning,
		).Scan(&existingID)
		if err == nil {
			result.Duplicates++
			continue
		}
		title := learning
		if len(title) > 60 {
			title = title[:60] + "..."
		}
		_, err = s.AddMemory(AddMemoryParams{
			ProjectID: p.Project,
			Kind:      "discovery",
			Title:     title,
			Body:      learning,
			Scope:     "project",
			Source:    "passive",
			SessionID: p.SessionID,
			ActorID:   "system",
		})
		if err != nil {
			return result, fmt.Errorf("passive capture save: %w", err)
		}
		result.Saved++
	}
	return result, nil
}

// ClassifyTool returns the observation type for a given tool name.
func ClassifyTool(toolName string) string {
	switch toolName {
	case "write", "edit", "patch":
		return "file_change"
	case "bash":
		return "command"
	case "read", "view":
		return "file_read"
	case "grep", "glob", "ls":
		return "search"
	default:
		return "tool_use"
	}
}

// Now returns the current time formatted for SQLite.
func Now() string {
	return time.Now().UTC().Format("2006-01-02 15:04:05")
}

// SchemaVersion returns the current schema version recorded in schema_version.
// Returns 0 if the schema_version table is empty (fresh DB with no migrations applied).
func (s *Store) SchemaVersion() int {
	var v int
	if err := s.db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&v); err != nil {
		return 0
	}
	return v
}
