package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ashwnn/ohara/internal/token"
	"github.com/ashwnn/ohara/internal/util"
	_ "modernc.org/sqlite"
)

func mustDefaultConfig(t *testing.T) Config {
	t.Helper()
	cfg, err := DefaultConfig()
	if err != nil {
		t.Fatalf("DefaultConfig: %v", err)
	}
	return cfg
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	cfg := mustDefaultConfig(t)
	cfg.DataDir = t.TempDir()
	cfg.DedupeWindow = time.Hour

	s, err := New(cfg)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() {
		_ = s.Close()
	})
	return s
}

type fakeRows struct {
	next    []bool
	scanErr error
	err     error
}

func (f *fakeRows) Next() bool {
	if len(f.next) == 0 {
		return false
	}
	v := f.next[0]
	f.next = f.next[1:]
	return v
}

func (f *fakeRows) Scan(dest ...any) error {
	return f.scanErr
}

func (f *fakeRows) Err() error {
	return f.err
}

func (f *fakeRows) Close() error {
	return nil
}

func TestAddObservationDeduplicatesWithinWindow(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s1", "ohara", "/tmp/ohara"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	firstID, err := s.AddObservation(AddObservationParams{
		SessionID: "s1",
		Type:      "bugfix",
		Title:     "Fixed tokenizer",
		Content:   "Normalized tokenizer panic on edge case",
		Project:   "ohara",
		Scope:     "project",
	})
	if err != nil {
		t.Fatalf("add first observation: %v", err)
	}

	secondID, err := s.AddObservation(AddObservationParams{
		SessionID: "s1",
		Type:      "bugfix",
		Title:     "Fixed tokenizer",
		Content:   "normalized   tokenizer panic on EDGE case",
		Project:   "ohara",
		Scope:     "project",
	})
	if err != nil {
		t.Fatalf("add duplicate observation: %v", err)
	}

	if firstID != secondID {
		t.Fatalf("expected duplicate to reuse same id, got %d and %d", firstID, secondID)
	}

	obs, err := s.GetObservation(firstID)
	if err != nil {
		t.Fatalf("get deduped observation: %v", err)
	}
	if obs.DuplicateCount != 2 {
		t.Fatalf("expected duplicate_count=2, got %d", obs.DuplicateCount)
	}
}

func TestScopeFiltersSearchAndContext(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s1", "ohara", "/tmp/ohara"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	_, err := s.AddObservation(AddObservationParams{
		SessionID: "s1",
		Type:      "decision",
		Title:     "Project auth",
		Content:   "Keep auth middleware in project memory",
		Project:   "ohara",
		Scope:     "project",
	})
	if err != nil {
		t.Fatalf("add project observation: %v", err)
	}

	_, err = s.AddObservation(AddObservationParams{
		SessionID: "s1",
		Type:      "decision",
		Title:     "Personal note",
		Content:   "Use this regex trick later",
		Project:   "ohara",
		Scope:     "personal",
	})
	if err != nil {
		t.Fatalf("add personal observation: %v", err)
	}

	projectResults, err := s.Search("regex", SearchOptions{Project: "ohara", Scope: "project", Limit: 10})
	if err != nil {
		t.Fatalf("search project scope: %v", err)
	}
	if len(projectResults) != 0 {
		t.Fatalf("expected no project-scope regex results, got %d", len(projectResults))
	}

	personalResults, err := s.Search("regex", SearchOptions{Project: "ohara", Scope: "personal", Limit: 10})
	if err != nil {
		t.Fatalf("search personal scope: %v", err)
	}
	if len(personalResults) != 1 {
		t.Fatalf("expected 1 personal-scope result, got %d", len(personalResults))
	}

	ctx, err := s.FormatContext("ohara", "personal")
	if err != nil {
		t.Fatalf("format context personal: %v", err)
	}
	if !strings.Contains(ctx, "Personal note") {
		t.Fatalf("expected personal context to include personal observation")
	}
	if strings.Contains(ctx, "Project auth") {
		t.Fatalf("expected personal context to exclude project observation")
	}
}

func TestUpdateAndSoftDeleteExcludedFromSearchAndTimeline(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s1", "ohara", "/tmp/ohara"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	firstID, err := s.AddObservation(AddObservationParams{
		SessionID: "s1",
		Type:      "bugfix",
		Title:     "first",
		Content:   "first event",
		Project:   "ohara",
		Scope:     "project",
	})
	if err != nil {
		t.Fatalf("add first: %v", err)
	}

	middleID, err := s.AddObservation(AddObservationParams{
		SessionID: "s1",
		Type:      "bugfix",
		Title:     "middle",
		Content:   "to be deleted",
		Project:   "ohara",
		Scope:     "project",
	})
	if err != nil {
		t.Fatalf("add middle: %v", err)
	}

	lastID, err := s.AddObservation(AddObservationParams{
		SessionID: "s1",
		Type:      "bugfix",
		Title:     "last",
		Content:   "last event",
		Project:   "ohara",
		Scope:     "project",
	})
	if err != nil {
		t.Fatalf("add last: %v", err)
	}

	newTitle := "last-updated"
	newContent := "updated content"
	newScope := "personal"
	updated, err := s.UpdateObservation(lastID, UpdateObservationParams{
		Title:   &newTitle,
		Content: &newContent,
		Scope:   &newScope,
	})
	if err != nil {
		t.Fatalf("update observation: %v", err)
	}
	if updated.Title != newTitle || updated.Scope != "personal" {
		t.Fatalf("update did not apply; got title=%q scope=%q", updated.Title, updated.Scope)
	}

	if err := s.DeleteObservation(middleID, false); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	if _, err := s.GetObservation(middleID); err == nil {
		t.Fatalf("expected deleted observation to be hidden from GetObservation")
	}

	searchResults, err := s.Search("deleted", SearchOptions{Project: "ohara", Limit: 10})
	if err != nil {
		t.Fatalf("search after delete: %v", err)
	}
	if len(searchResults) != 0 {
		t.Fatalf("expected deleted observation excluded from search")
	}

	timeline, err := s.Timeline(firstID, 5, 5)
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	if len(timeline.After) != 1 || timeline.After[0].ID != lastID {
		t.Fatalf("expected timeline to skip deleted observation")
	}

	if err := s.DeleteObservation(lastID, true); err != nil {
		t.Fatalf("hard delete: %v", err)
	}
	if _, err := s.GetObservation(lastID); err == nil {
		t.Fatalf("expected hard-deleted observation to be missing")
	}
}

func TestTopicKeyUpsertUpdatesSameTopicWithoutCreatingNewRow(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s1", "ohara", "/tmp/ohara"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	firstID, err := s.AddObservation(AddObservationParams{
		SessionID: "s1",
		Type:      "architecture",
		Title:     "Auth architecture",
		Content:   "Use middleware for JWT validation.",
		Project:   "ohara",
		Scope:     "project",
		TopicKey:  "architecture auth model",
	})
	if err != nil {
		t.Fatalf("add first architecture: %v", err)
	}

	secondID, err := s.AddObservation(AddObservationParams{
		SessionID: "s1",
		Type:      "architecture",
		Title:     "Auth architecture",
		Content:   "Move auth to gateway + middleware chain.",
		Project:   "ohara",
		Scope:     "project",
		TopicKey:  "ARCHITECTURE   AUTH  MODEL",
	})
	if err != nil {
		t.Fatalf("upsert architecture: %v", err)
	}

	if firstID != secondID {
		t.Fatalf("expected topic upsert to reuse id, got %d and %d", firstID, secondID)
	}

	obs, err := s.GetObservation(firstID)
	if err != nil {
		t.Fatalf("get upserted observation: %v", err)
	}
	if obs.RevisionCount != 2 {
		t.Fatalf("expected revision_count=2, got %d", obs.RevisionCount)
	}
	if obs.TopicKey == nil || *obs.TopicKey != "architecture-auth-model" {
		t.Fatalf("expected normalized topic key, got %v", obs.TopicKey)
	}
	if !strings.Contains(obs.Content, "gateway") {
		t.Fatalf("expected latest content after upsert, got %q", obs.Content)
	}
}

func TestDifferentTopicsDoNotReplaceEachOther(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s1", "ohara", "/tmp/ohara"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	archID, err := s.AddObservation(AddObservationParams{
		SessionID: "s1",
		Type:      "architecture",
		Title:     "Auth architecture",
		Content:   "Architecture decision",
		Project:   "ohara",
		Scope:     "project",
		TopicKey:  "architecture/auth",
	})
	if err != nil {
		t.Fatalf("add architecture observation: %v", err)
	}

	bugID, err := s.AddObservation(AddObservationParams{
		SessionID: "s1",
		Type:      "bugfix",
		Title:     "Fix auth nil panic",
		Content:   "Bugfix details",
		Project:   "ohara",
		Scope:     "project",
		TopicKey:  "bug/auth-nil-panic",
	})
	if err != nil {
		t.Fatalf("add bug observation: %v", err)
	}

	if archID == bugID {
		t.Fatalf("expected different topic keys to create different observations")
	}

	observations, err := s.AllObservations("ohara", "project", 10)
	if err != nil {
		t.Fatalf("all observations: %v", err)
	}
	if len(observations) != 2 {
		t.Fatalf("expected 2 observations, got %d", len(observations))
	}
}

func TestNewMigratesLegacyObservationIDSchema(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "ohara.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}

	_, err = db.Exec(`
		CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			project TEXT NOT NULL,
			directory TEXT NOT NULL,
			started_at TEXT NOT NULL DEFAULT (datetime('now')),
			ended_at TEXT,
			summary TEXT
		);
		CREATE TABLE observations (
			id INT,
			session_id TEXT,
			type TEXT,
			title TEXT,
			content TEXT,
			tool_name TEXT,
			project TEXT,
			created_at TEXT
		);
		INSERT INTO sessions (id, project, directory) VALUES ('s1', 'ohara', '/tmp/ohara');
		INSERT INTO observations (id, session_id, type, title, content, project, created_at)
		VALUES
			(NULL, 's1', 'bugfix', 'legacy null', 'legacy null content', 'ohara', datetime('now')),
			(7, 's1', 'bugfix', 'legacy fixed', 'legacy fixed content', 'ohara', datetime('now')),
			(7, 's1', 'bugfix', 'legacy duplicate', 'legacy duplicate content', 'ohara', datetime('now'));
	`)
	if err != nil {
		_ = db.Close()
		t.Fatalf("seed legacy db: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	cfg := mustDefaultConfig(t)
	cfg.DataDir = dataDir

	s, err := New(cfg)
	if err != nil {
		t.Fatalf("new store after legacy schema: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	obs, err := s.AllObservations("ohara", "", 20)
	if err != nil {
		t.Fatalf("all observations after migration: %v", err)
	}
	if len(obs) != 3 {
		t.Fatalf("expected 3 migrated observations, got %d", len(obs))
	}

	seen := make(map[int64]bool)
	for _, o := range obs {
		if o.ID <= 0 {
			t.Fatalf("expected migrated observation id > 0, got %d", o.ID)
		}
		if seen[o.ID] {
			t.Fatalf("expected unique migrated ids, duplicate %d", o.ID)
		}
		seen[o.ID] = true
	}

	results, err := s.Search("legacy", SearchOptions{Project: "ohara", Limit: 10})
	if err != nil {
		t.Fatalf("search after migration: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected search results after migration")
	}

	newID, err := s.AddObservation(AddObservationParams{
		SessionID: "s1",
		Type:      "bugfix",
		Title:     "post migration",
		Content:   "new row should get id",
		Project:   "ohara",
		Scope:     "project",
	})
	if err != nil {
		t.Fatalf("add observation after migration: %v", err)
	}
	if newID <= 0 {
		t.Fatalf("expected autoincrement id after migration, got %d", newID)
	}
}

func TestNewMigratesLegacyUserPromptsSyncIDSchema(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "ohara.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}

	_, err = db.Exec(`
		CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			project TEXT NOT NULL,
			directory TEXT NOT NULL,
			started_at TEXT NOT NULL DEFAULT (datetime('now')),
			ended_at TEXT,
			summary TEXT
		);
		CREATE TABLE user_prompts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			content TEXT NOT NULL,
			project TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (session_id) REFERENCES sessions(id)
		);
		INSERT INTO sessions (id, project, directory) VALUES ('s1', 'ohara', '/tmp/ohara');
		INSERT INTO user_prompts (session_id, content, project) VALUES ('s1', 'legacy prompt', 'ohara');
	`)
	if err != nil {
		_ = db.Close()
		t.Fatalf("seed legacy db: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	cfg := mustDefaultConfig(t)
	cfg.DataDir = dataDir

	s, err := New(cfg)
	if err != nil {
		t.Fatalf("new store after legacy prompt schema: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	var syncID string
	if err := s.db.QueryRow("SELECT sync_id FROM user_prompts WHERE content = ?", "legacy prompt").Scan(&syncID); err != nil {
		t.Fatalf("query migrated prompt sync_id: %v", err)
	}
	if syncID == "" {
		t.Fatalf("expected migrated prompt sync_id to be backfilled")
	}

	var hasSyncIDColumn bool
	rows, err := s.db.Query("PRAGMA table_info(user_prompts)")
	if err != nil {
		t.Fatalf("query prompt columns: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan prompt column: %v", err)
		}
		if name == "sync_id" {
			hasSyncIDColumn = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate prompt columns: %v", err)
	}
	if !hasSyncIDColumn {
		t.Fatalf("expected user_prompts.sync_id column after migration")
	}

	var indexName string
	if err := s.db.QueryRow("SELECT name FROM sqlite_master WHERE type = 'index' AND name = 'idx_prompts_sync_id'").Scan(&indexName); err != nil {
		t.Fatalf("query prompt sync index: %v", err)
	}
	if indexName != "idx_prompts_sync_id" {
		t.Fatalf("expected idx_prompts_sync_id to exist, got %q", indexName)
	}
}

func TestSuggestTopicKeyNormalizesDeterministically(t *testing.T) {
	got := SuggestTopicKey("Architecture", "  Auth Model  ", "ignored")
	if got != "architecture/auth-model" {
		t.Fatalf("expected architecture/auth-model, got %q", got)
	}

	fallback := SuggestTopicKey("bugfix", "", "Fix nil panic in auth middleware on empty token")
	if fallback != "bug/fix-nil-panic-in-auth-middleware-on-empty" {
		t.Fatalf("unexpected fallback topic key: %q", fallback)
	}
}

func TestSuggestTopicKeyInfersFamilyFromTextWhenTypeIsGeneric(t *testing.T) {
	bug := SuggestTopicKey("manual", "", "Fix regression in auth login flow")
	if bug != "bug/fix-regression-in-auth-login-flow" {
		t.Fatalf("expected bug family inference, got %q", bug)
	}

	arch := SuggestTopicKey("", "ADR: Split API gateway boundary", "")
	if arch != "architecture/adr-split-api-gateway-boundary" {
		t.Fatalf("expected architecture family inference, got %q", arch)
	}
}

func TestTopicKeyUpsertIsScopedByProjectAndScope(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s1", "ohara", "/tmp/ohara"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	baseID, err := s.AddObservation(AddObservationParams{
		SessionID: "s1",
		Type:      "architecture",
		Title:     "Auth model",
		Content:   "Initial architecture",
		Project:   "ohara",
		Scope:     "project",
		TopicKey:  "architecture/auth-model",
	})
	if err != nil {
		t.Fatalf("add base observation: %v", err)
	}

	personalID, err := s.AddObservation(AddObservationParams{
		SessionID: "s1",
		Type:      "architecture",
		Title:     "Auth model",
		Content:   "Personal take",
		Project:   "ohara",
		Scope:     "personal",
		TopicKey:  "architecture/auth-model",
	})
	if err != nil {
		t.Fatalf("add personal scoped observation: %v", err)
	}

	otherProjectID, err := s.AddObservation(AddObservationParams{
		SessionID: "s1",
		Type:      "architecture",
		Title:     "Auth model",
		Content:   "Other project",
		Project:   "another-project",
		Scope:     "project",
		TopicKey:  "architecture/auth-model",
	})
	if err != nil {
		t.Fatalf("add other project observation: %v", err)
	}

	if baseID == personalID || baseID == otherProjectID || personalID == otherProjectID {
		t.Fatalf("expected topic upsert boundaries by project+scope, got ids base=%d personal=%d other=%d", baseID, personalID, otherProjectID)
	}
}

func TestPromptProjectNullScan(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s1", "ohara", "/tmp/ohara"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Manually insert a prompt with NULL project to simulate legacy data or external changes
	_, err := s.db.Exec(
		"INSERT INTO user_prompts (session_id, content, project) VALUES (?, ?, NULL)",
		"s1", "prompt with null project",
	)
	if err != nil {
		t.Fatalf("manual insert: %v", err)
	}

	// 1. Test RecentPrompts
	prompts, err := s.RecentPrompts("", 10)
	if err != nil {
		t.Fatalf("RecentPrompts failed with null project: %v", err)
	}
	if len(prompts) != 1 || prompts[0].Project != "" {
		t.Errorf("expected empty string for null project, got %q", prompts[0].Project)
	}

	// 2. Test SearchPrompts
	searchResult, err := s.SearchPrompts("null", "", 10)
	if err != nil {
		t.Fatalf("SearchPrompts failed with null project: %v", err)
	}
	if len(searchResult) != 1 || searchResult[0].Project != "" {
		t.Errorf("expected empty string for null project in search, got %q", searchResult[0].Project)
	}

	// 3. Test Export
	data, err := s.Export()
	if err != nil {
		t.Fatalf("Export failed with null project: %v", err)
	}
	found := false
	for _, p := range data.Prompts {
		if p.Content == "prompt with null project" {
			found = true
			if p.Project != "" {
				t.Errorf("expected empty string for null project in export, got %q", p.Project)
			}
		}
	}
	if !found {
		t.Error("exported prompts missing the test prompt")
	}
}

// ─── Passive Capture Tests ───────────────────────────────────────────────────

func TestExtractLearningsNumberedList(t *testing.T) {
	text := `Some preamble text here.

## Key Learnings:

1. bcrypt cost=12 is the right balance for our server performance
2. JWT refresh tokens need atomic rotation to prevent race conditions
3. Always validate the audience claim in JWT tokens before trusting them

## Next Steps
- something else
`
	learnings := ExtractLearnings(text)
	if len(learnings) != 3 {
		t.Fatalf("expected 3 learnings, got %d: %v", len(learnings), learnings)
	}
	if !strings.Contains(learnings[0], "bcrypt") {
		t.Fatalf("expected first learning about bcrypt, got %q", learnings[0])
	}
}

func TestExtractLearningsSpanishHeader(t *testing.T) {
	text := `## Aprendizajes Clave:

1. El costo de bcrypt=12 es el balance correcto para nuestro servidor
2. Los refresh tokens de JWT necesitan rotacion atomica
`
	learnings := ExtractLearnings(text)
	if len(learnings) != 2 {
		t.Fatalf("expected 2 learnings, got %d: %v", len(learnings), learnings)
	}
}

func TestExtractLearningsBulletList(t *testing.T) {
	text := `### Learnings:

- bcrypt cost=12 is the right balance for our server performance
- JWT refresh tokens need atomic rotation to prevent race conditions
`
	learnings := ExtractLearnings(text)
	if len(learnings) != 2 {
		t.Fatalf("expected 2 learnings, got %d: %v", len(learnings), learnings)
	}
}

func TestExtractLearningsIgnoresShortItems(t *testing.T) {
	text := `## Key Learnings:

1. too short
2. bcrypt cost=12 is the right balance for our server performance
3. also short
`
	learnings := ExtractLearnings(text)
	if len(learnings) != 1 {
		t.Fatalf("expected 1 learning (short ones filtered), got %d: %v", len(learnings), learnings)
	}
}

func TestExtractLearningsNoSection(t *testing.T) {
	text := `This is just regular text without any learning section headers.
It has multiple lines but no ## Key Learnings or similar.
`
	learnings := ExtractLearnings(text)
	if len(learnings) != 0 {
		t.Fatalf("expected 0 learnings, got %d: %v", len(learnings), learnings)
	}
}

func TestExtractLearningsSectionPresentButNoValidItems(t *testing.T) {
	text := `## Key Learnings:

1. short
2. tiny
`
	learnings := ExtractLearnings(text)
	if len(learnings) != 0 {
		t.Fatalf("expected 0 learnings when section has no valid items, got %d: %v", len(learnings), learnings)
	}
}

func TestExtractLearningsUsesLastSection(t *testing.T) {
	text := `## Key Learnings:

1. This is from the first section and should be ignored

Some other text here.

## Key Learnings:

1. This is from the last section and should be captured as the real one
`
	learnings := ExtractLearnings(text)
	if len(learnings) != 1 {
		t.Fatalf("expected 1 learning from last section, got %d: %v", len(learnings), learnings)
	}
	if !strings.Contains(learnings[0], "last section") {
		t.Fatalf("expected learning from last section, got %q", learnings[0])
	}
}

func TestExtractLearningsFallsBackWhenLastSectionHasNoValidItems(t *testing.T) {
	text := `## Key Learnings:

1. This is long enough and should be captured from the previous section

## Key Learnings:

1. short
2. tiny
`
	learnings := ExtractLearnings(text)
	if len(learnings) != 1 {
		t.Fatalf("expected fallback to previous valid section, got %d: %v", len(learnings), learnings)
	}
	if !strings.Contains(learnings[0], "previous section") {
		t.Fatalf("expected learning from previous section, got %q", learnings[0])
	}
}

func TestExtractLearningsCleansMarkdown(t *testing.T) {
	text := "## Key Learnings:\n\n1. **Use** `context.Context` in *all* handlers to support cancellation correctly\n"
	learnings := ExtractLearnings(text)
	if len(learnings) != 1 {
		t.Fatalf("expected 1 learning, got %d: %v", len(learnings), learnings)
	}
	if strings.Contains(learnings[0], "**") || strings.Contains(learnings[0], "`") || strings.Contains(learnings[0], "*") {
		t.Fatalf("expected markdown to be stripped, got %q", learnings[0])
	}
}

func TestPassiveCaptureStoresLearnings(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s1", "ohara", "/tmp/ohara"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	text := `## Key Learnings:

1. bcrypt cost=12 is the right balance for our server performance
2. JWT refresh tokens need atomic rotation to prevent race conditions
`
	result, err := s.PassiveCapture(PassiveCaptureParams{
		SessionID: "s1",
		Content:   text,
		Project:   "ohara",
		Source:    "test",
	})
	if err != nil {
		t.Fatalf("passive capture: %v", err)
	}
	if result.Extracted != 2 {
		t.Fatalf("expected 2 extracted, got %d", result.Extracted)
	}
	if result.Saved != 2 {
		t.Fatalf("expected 2 saved, got %d", result.Saved)
	}

	obs, err := s.AllObservations("ohara", "", 10)
	if err != nil {
		t.Fatalf("all observations: %v", err)
	}
	if len(obs) != 2 {
		t.Fatalf("expected 2 observations, got %d", len(obs))
	}
	for _, o := range obs {
		if o.Type != "passive" {
			t.Fatalf("expected type=passive, got %q", o.Type)
		}
	}
	if obs[0].ToolName == nil || *obs[0].ToolName != "test" {
		t.Fatalf("expected tool_name source to be stored as 'test', got %+v", obs[0].ToolName)
	}
}

func TestPassiveCaptureEmptyContent(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s1", "ohara", "/tmp/ohara"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	result, err := s.PassiveCapture(PassiveCaptureParams{
		SessionID: "s1",
		Content:   "",
		Project:   "ohara",
		Source:    "test",
	})
	if err != nil {
		t.Fatalf("passive capture: %v", err)
	}
	if result.Extracted != 0 || result.Saved != 0 {
		t.Fatalf("expected 0 extracted and 0 saved, got %d/%d", result.Extracted, result.Saved)
	}
}

func TestPassiveCaptureDedupesAgainstExistingObservations(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s1", "ohara", "/tmp/ohara"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// First: agent saves actively via mem_save
	_, err := s.AddObservation(AddObservationParams{
		SessionID: "s1",
		Type:      "decision",
		Title:     "bcrypt cost",
		Content:   "bcrypt cost=12 is the right balance for our server performance",
		Project:   "ohara",
		Scope:     "project",
	})
	if err != nil {
		t.Fatalf("add active observation: %v", err)
	}

	// Then: passive capture fires with overlapping content
	text := `## Key Learnings:

1. bcrypt cost=12 is the right balance for our server performance
2. JWT refresh tokens need atomic rotation to prevent race conditions
`
	result, err := s.PassiveCapture(PassiveCaptureParams{
		SessionID: "s1",
		Content:   text,
		Project:   "ohara",
		Source:    "test",
	})
	if err != nil {
		t.Fatalf("passive capture: %v", err)
	}
	if result.Extracted != 2 {
		t.Fatalf("expected 2 extracted, got %d", result.Extracted)
	}
	if result.Saved != 1 {
		t.Fatalf("expected 1 saved (1 deduped), got %d", result.Saved)
	}
	if result.Duplicates != 1 {
		t.Fatalf("expected 1 duplicate, got %d", result.Duplicates)
	}
}

func TestPassiveCaptureReturnsErrorWhenSessionDoesNotExist(t *testing.T) {
	s := newTestStore(t)

	text := `## Key Learnings:

1. This learning is long enough to attempt insert and fail without session
`
	_, err := s.PassiveCapture(PassiveCaptureParams{
		SessionID: "missing-session",
		Content:   text,
		Project:   "ohara",
		Source:    "test",
	})
	if err == nil {
		t.Fatalf("expected error when session does not exist")
	}
}

func TestStatsProjectsOrderedByMostRecentObservation(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s1", "ohara", "/tmp/ohara"); err != nil {
		t.Fatalf("create session s1: %v", err)
	}
	if err := s.CreateSession("s2", "ohara", "/tmp/ohara"); err != nil {
		t.Fatalf("create session s2: %v", err)
	}

	_, err := s.db.Exec(
		`INSERT INTO observations (session_id, type, title, content, project, scope, normalized_hash, revision_count, duplicate_count, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 1, 1, ?, ?),
		        (?, ?, ?, ?, ?, ?, ?, 1, 1, ?, ?)`,
		"s1", "note", "older", "older alpha", "alpha", "project", hashNormalized("older alpha"), "2026-02-01 10:00:00", "2026-02-01 10:00:00",
		"s2", "note", "newer", "newer beta", "beta", "project", hashNormalized("newer beta"), "2026-02-02 10:00:00", "2026-02-02 10:00:00",
	)
	if err != nil {
		t.Fatalf("insert observations: %v", err)
	}

	stats, err := s.Stats()
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if len(stats.Projects) < 2 {
		t.Fatalf("expected at least 2 projects, got %d", len(stats.Projects))
	}

	if stats.Projects[0] != "beta" || stats.Projects[1] != "alpha" {
		t.Fatalf("expected recency order [beta alpha], got %v", stats.Projects[:2])
	}
}

func TestSessionsOrderedByMostRecentActivity(t *testing.T) {
	s := newTestStore(t)

	_, err := s.db.Exec(
		`INSERT INTO sessions (id, project, directory, started_at) VALUES
		 (?, ?, ?, ?),
		 (?, ?, ?, ?)`,
		"s-older", "ohara", "/tmp/ohara", "2026-02-01 09:00:00",
		"s-newer", "ohara", "/tmp/ohara", "2026-02-02 09:00:00",
	)
	if err != nil {
		t.Fatalf("insert sessions: %v", err)
	}

	_, err = s.db.Exec(
		`INSERT INTO observations (session_id, type, title, content, project, scope, normalized_hash, revision_count, duplicate_count, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 1, 1, ?, ?)`,
		"s-older", "note", "latest", "session old got new activity", "ohara", "project", hashNormalized("session old got new activity"), "2026-02-03 09:00:00", "2026-02-03 09:00:00",
	)
	if err != nil {
		t.Fatalf("insert latest observation: %v", err)
	}

	all, err := s.AllSessions("", 10)
	if err != nil {
		t.Fatalf("all sessions: %v", err)
	}
	if len(all) < 2 {
		t.Fatalf("expected at least 2 sessions, got %d", len(all))
	}
	if all[0].ID != "s-older" {
		t.Fatalf("expected s-older first in all sessions, got %s", all[0].ID)
	}

	recent, err := s.RecentSessions("", 10)
	if err != nil {
		t.Fatalf("recent sessions: %v", err)
	}
	if len(recent) < 2 {
		t.Fatalf("expected at least 2 recent sessions, got %d", len(recent))
	}
	if recent[0].ID != "s-older" {
		t.Fatalf("expected s-older first in recent sessions, got %s", recent[0].ID)
	}
}

func TestSessionObservationsAddPromptImportAndSyncChunks(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s1", "ohara", "/tmp/ohara"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	_, err := s.AddObservation(AddObservationParams{
		SessionID: "s1",
		Type:      "decision",
		Title:     "Auth",
		Content:   "Use middleware chain",
		Project:   "ohara",
		Scope:     "project",
	})
	if err != nil {
		t.Fatalf("add observation: %v", err)
	}

	longPrompt := strings.Repeat("x", s.cfg.MaxObservationLength+25)
	promptID, err := s.AddPrompt(AddPromptParams{SessionID: "s1", Content: longPrompt, Project: "ohara"})
	if err != nil {
		t.Fatalf("add prompt: %v", err)
	}
	if promptID <= 0 {
		t.Fatalf("expected valid prompt id, got %d", promptID)
	}

	sessionObs, err := s.SessionObservations("s1", 0)
	if err != nil {
		t.Fatalf("session observations: %v", err)
	}
	if len(sessionObs) != 1 {
		t.Fatalf("expected 1 session observation, got %d", len(sessionObs))
	}

	exported, err := s.Export()
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	cfg := mustDefaultConfig(t)
	cfg.DataDir = t.TempDir()
	dst, err := New(cfg)
	if err != nil {
		t.Fatalf("new destination store: %v", err)
	}
	t.Cleanup(func() { _ = dst.Close() })

	imported, err := dst.Import(exported)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if imported.SessionsImported < 1 || imported.ObservationsImported < 1 || imported.PromptsImported < 1 {
		t.Fatalf("expected non-zero import counts, got %+v", imported)
	}

	if err := dst.RecordSyncedChunk("chunk-1"); err != nil {
		t.Fatalf("record synced chunk: %v", err)
	}
	chunks, err := dst.GetSyncedChunks()
	if err != nil {
		t.Fatalf("get synced chunks: %v", err)
	}
	if !chunks["chunk-1"] {
		t.Fatalf("expected chunk-1 to be marked as synced")
	}
}

func TestStoreLocalSyncFoundationEnqueuesCoreMutations(t *testing.T) {
	s := newTestStore(t)

	// Enroll "ohara" so mutations are visible via ListPendingSyncMutations.
	if err := s.EnrollProject("ohara"); err != nil {
		t.Fatalf("enroll: %v", err)
	}

	if err := s.CreateSession("sync-session", "ohara", "/tmp/ohara"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	obsID, err := s.AddObservation(AddObservationParams{
		SessionID: "sync-session",
		Type:      "decision",
		Title:     "Initial title",
		Content:   "Initial content",
		Project:   "ohara",
		Scope:     "project",
	})
	if err != nil {
		t.Fatalf("add observation: %v", err)
	}

	updatedTitle := "Updated title"
	updatedContent := "Updated content"
	if _, err := s.UpdateObservation(obsID, UpdateObservationParams{
		Title:   &updatedTitle,
		Content: &updatedContent,
	}); err != nil {
		t.Fatalf("update observation: %v", err)
	}

	if err := s.DeleteObservation(obsID, false); err != nil {
		t.Fatalf("soft delete observation: %v", err)
	}

	promptID, err := s.AddPrompt(AddPromptParams{
		SessionID: "sync-session",
		Content:   "How do we keep this local-first?",
		Project:   "ohara",
	})
	if err != nil {
		t.Fatalf("add prompt: %v", err)
	}

	if err := s.EndSession("sync-session", "done"); err != nil {
		t.Fatalf("end session: %v", err)
	}

	state, err := s.GetSyncState(DefaultSyncTargetKey)
	if err != nil {
		t.Fatalf("get sync state: %v", err)
	}
	if state.TargetKey != DefaultSyncTargetKey {
		t.Fatalf("expected target %q, got %q", DefaultSyncTargetKey, state.TargetKey)
	}
	if state.Lifecycle != SyncLifecyclePending {
		t.Fatalf("expected pending lifecycle after local writes, got %q", state.Lifecycle)
	}
	if state.LastEnqueuedSeq != 6 {
		t.Fatalf("expected 6 enqueued mutations, got %d", state.LastEnqueuedSeq)
	}

	mutations, err := s.ListPendingSyncMutations(DefaultSyncTargetKey, 10)
	if err != nil {
		t.Fatalf("list pending sync mutations: %v", err)
	}
	if len(mutations) != 6 {
		t.Fatalf("expected 6 pending mutations, got %d", len(mutations))
	}

	var observationSyncID string
	if err := s.db.QueryRow("SELECT sync_id FROM observations WHERE id = ?", obsID).Scan(&observationSyncID); err != nil {
		t.Fatalf("lookup observation sync id: %v", err)
	}
	if observationSyncID == "" {
		t.Fatalf("expected observation sync id to be persisted")
	}

	var promptSyncID string
	if err := s.db.QueryRow("SELECT sync_id FROM user_prompts WHERE id = ?", promptID).Scan(&promptSyncID); err != nil {
		t.Fatalf("lookup prompt sync id: %v", err)
	}
	if promptSyncID == "" {
		t.Fatalf("expected prompt sync id to be persisted")
	}

	if mutations[0].Entity != SyncEntitySession || mutations[0].EntityKey != "sync-session" || mutations[0].Op != SyncOpUpsert {
		t.Fatalf("unexpected session mutation: %+v", mutations[0])
	}
	if mutations[1].Entity != SyncEntityObservation || mutations[1].EntityKey != observationSyncID || mutations[1].Op != SyncOpUpsert {
		t.Fatalf("unexpected observation insert mutation: %+v", mutations[1])
	}
	if mutations[2].Entity != SyncEntityObservation || mutations[2].EntityKey != observationSyncID || mutations[2].Op != SyncOpUpsert {
		t.Fatalf("unexpected observation update mutation: %+v", mutations[2])
	}
	if mutations[3].Entity != SyncEntityObservation || mutations[3].EntityKey != observationSyncID || mutations[3].Op != SyncOpDelete {
		t.Fatalf("unexpected observation delete mutation: %+v", mutations[3])
	}
	if mutations[4].Entity != SyncEntityPrompt || mutations[4].EntityKey != promptSyncID || mutations[4].Op != SyncOpUpsert {
		t.Fatalf("unexpected prompt mutation: %+v", mutations[4])
	}
	if mutations[5].Entity != SyncEntitySession || mutations[5].EntityKey != "sync-session" || mutations[5].Op != SyncOpUpsert {
		t.Fatalf("unexpected end session mutation: %+v", mutations[5])
	}

	var deletedPayload map[string]any
	if err := json.Unmarshal([]byte(mutations[3].Payload), &deletedPayload); err != nil {
		t.Fatalf("decode delete payload: %v", err)
	}
	if deletedPayload["sync_id"] != observationSyncID {
		t.Fatalf("expected delete payload sync id %q, got %#v", observationSyncID, deletedPayload["sync_id"])
	}
	if deletedPayload["deleted"] != true {
		t.Fatalf("expected delete payload to mark deleted=true, got %#v", deletedPayload["deleted"])
	}

	if err := s.AckSyncMutations(DefaultSyncTargetKey, mutations[3].Seq); err != nil {
		t.Fatalf("ack sync mutations: %v", err)
	}
	remaining, err := s.ListPendingSyncMutations(DefaultSyncTargetKey, 10)
	if err != nil {
		t.Fatalf("list remaining sync mutations: %v", err)
	}
	if len(remaining) != 2 || remaining[0].Entity != SyncEntityPrompt || remaining[1].Entity != SyncEntitySession {
		t.Fatalf("expected prompt and end-session mutations to remain pending, got %+v", remaining)
	}
}

func TestStoreLocalSyncFoundationStateHelpers(t *testing.T) {
	s := newTestStore(t)

	state, err := s.GetSyncState(DefaultSyncTargetKey)
	if err != nil {
		t.Fatalf("get initial sync state: %v", err)
	}
	if state.Lifecycle != SyncLifecycleIdle {
		t.Fatalf("expected idle lifecycle, got %q", state.Lifecycle)
	}

	acquired, err := s.AcquireSyncLease(DefaultSyncTargetKey, "worker-a", 2*time.Minute, time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("acquire first lease: %v", err)
	}
	if !acquired {
		t.Fatalf("expected first lease acquisition to succeed")
	}

	acquired, err = s.AcquireSyncLease(DefaultSyncTargetKey, "worker-b", 2*time.Minute, time.Date(2026, 3, 7, 12, 1, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("acquire conflicting lease: %v", err)
	}
	if acquired {
		t.Fatalf("expected conflicting lease acquisition to fail")
	}

	if err := s.ReleaseSyncLease(DefaultSyncTargetKey, "worker-a"); err != nil {
		t.Fatalf("release lease: %v", err)
	}

	acquired, err = s.AcquireSyncLease(DefaultSyncTargetKey, "worker-b", 2*time.Minute, time.Date(2026, 3, 7, 12, 2, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("acquire released lease: %v", err)
	}
	if !acquired {
		t.Fatalf("expected lease acquisition after release to succeed")
	}

	if err := s.MarkSyncFailure(DefaultSyncTargetKey, "timeout talking to cloud", time.Date(2026, 3, 7, 12, 10, 0, 0, time.UTC)); err != nil {
		t.Fatalf("mark sync failure: %v", err)
	}

	state, err = s.GetSyncState(DefaultSyncTargetKey)
	if err != nil {
		t.Fatalf("get degraded sync state: %v", err)
	}
	if state.Lifecycle != SyncLifecycleDegraded {
		t.Fatalf("expected degraded lifecycle, got %q", state.Lifecycle)
	}
	if state.ConsecutiveFailures != 1 {
		t.Fatalf("expected failure count 1, got %d", state.ConsecutiveFailures)
	}
	if state.LastError == nil || *state.LastError != "timeout talking to cloud" {
		t.Fatalf("expected last error to be stored, got %+v", state.LastError)
	}
	if state.BackoffUntil == nil || *state.BackoffUntil != "2026-03-07T12:10:00Z" {
		t.Fatalf("expected backoff timestamp to be stored, got %+v", state.BackoffUntil)
	}

	if err := s.MarkSyncHealthy(DefaultSyncTargetKey); err != nil {
		t.Fatalf("mark sync healthy: %v", err)
	}

	state, err = s.GetSyncState(DefaultSyncTargetKey)
	if err != nil {
		t.Fatalf("get healthy sync state: %v", err)
	}
	if state.Lifecycle != SyncLifecycleHealthy {
		t.Fatalf("expected healthy lifecycle, got %q", state.Lifecycle)
	}
	if state.ConsecutiveFailures != 0 || state.LastError != nil || state.BackoffUntil != nil {
		t.Fatalf("expected healthy state to clear failure metadata, got %+v", state)
	}
}

func TestApplyRemoteMutationIdempotent(t *testing.T) {
	s := newTestStore(t)

	create := SyncMutation{
		Seq:       41,
		TargetKey: DefaultSyncTargetKey,
		Entity:    SyncEntitySession,
		EntityKey: "remote-session",
		Op:        SyncOpUpsert,
		Payload:   `{"id":"remote-session","project":"ohara","directory":"/remote"}`,
	}
	if err := s.ApplyPulledMutation(DefaultSyncTargetKey, create); err != nil {
		t.Fatalf("apply session mutation: %v", err)
	}
	if err := s.ApplyPulledMutation(DefaultSyncTargetKey, create); err != nil {
		t.Fatalf("reapply session mutation: %v", err)
	}

	obsMutation := SyncMutation{
		Seq:       42,
		TargetKey: DefaultSyncTargetKey,
		Entity:    SyncEntityObservation,
		EntityKey: "obs-remote-1",
		Op:        SyncOpUpsert,
		Payload:   `{"sync_id":"obs-remote-1","session_id":"remote-session","type":"decision","title":"Remote","content":"Pulled from cloud","project":"ohara","scope":"project"}`,
	}
	if err := s.ApplyPulledMutation(DefaultSyncTargetKey, obsMutation); err != nil {
		t.Fatalf("apply observation mutation: %v", err)
	}
	if err := s.ApplyPulledMutation(DefaultSyncTargetKey, obsMutation); err != nil {
		t.Fatalf("reapply observation mutation: %v", err)
	}

	var rowCount int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM observations WHERE sync_id = ?", "obs-remote-1").Scan(&rowCount); err != nil {
		t.Fatalf("count remote observation rows: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("expected one remote observation row after idempotent upsert, got %d", rowCount)
	}

	deleteMutation := SyncMutation{
		Seq:       43,
		TargetKey: DefaultSyncTargetKey,
		Entity:    SyncEntityObservation,
		EntityKey: "obs-remote-1",
		Op:        SyncOpDelete,
		Payload:   `{"sync_id":"obs-remote-1","deleted":true}`,
	}
	if err := s.ApplyPulledMutation(DefaultSyncTargetKey, deleteMutation); err != nil {
		t.Fatalf("apply delete mutation: %v", err)
	}
	if err := s.ApplyPulledMutation(DefaultSyncTargetKey, deleteMutation); err != nil {
		t.Fatalf("reapply delete mutation: %v", err)
	}

	if _, err := s.GetObservationBySyncID("obs-remote-1"); err == nil {
		t.Fatalf("expected pulled delete to hide observation")
	}

	pending, err := s.ListPendingSyncMutations(DefaultSyncTargetKey, 10)
	if err != nil {
		t.Fatalf("list pending after pulled apply: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("expected pulled apply helpers to avoid local re-enqueue, got %+v", pending)
	}

	state, err := s.GetSyncState(DefaultSyncTargetKey)
	if err != nil {
		t.Fatalf("get sync state after pulled apply: %v", err)
	}
	if state.LastPulledSeq != 43 {
		t.Fatalf("expected last pulled seq 43, got %d", state.LastPulledSeq)
	}
}

func TestApplyPulledMutationAcceptsStringifiedSessionPayload(t *testing.T) {
	s := newTestStore(t)

	mutation := SyncMutation{
		Seq:       1,
		TargetKey: DefaultSyncTargetKey,
		Entity:    SyncEntitySession,
		EntityKey: "remote-session",
		Op:        SyncOpUpsert,
		Payload:   `"{\"id\":\"remote-session\",\"project\":\"ohara\",\"directory\":\"/remote\"}"`,
	}
	if err := s.ApplyPulledMutation(DefaultSyncTargetKey, mutation); err != nil {
		t.Fatalf("apply stringified session mutation: %v", err)
	}

	session, err := s.GetSession("remote-session")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.Project != "ohara" || session.Directory != "/remote" {
		t.Fatalf("unexpected session after pulled apply: %+v", session)
	}
}

func TestUtilityHelpersCoverage(t *testing.T) {
	if got := derefString(nil); got != "" {
		t.Fatalf("expected empty string for nil pointer, got %q", got)
	}
	v := "value"
	if got := derefString(&v); got != "value" {
		t.Fatalf("expected dereferenced value, got %q", got)
	}

	if got := maxInt(10, 5); got != 10 {
		t.Fatalf("expected maxInt(10,5)=10, got %d", got)
	}
	if got := maxInt(3, 7); got != 7 {
		t.Fatalf("expected maxInt(3,7)=7, got %d", got)
	}

	if got := dedupeWindowExpression(0); got != "-15 minutes" {
		t.Fatalf("expected default dedupe window, got %q", got)
	}
	if got := dedupeWindowExpression(20 * time.Second); got != "-1 minutes" {
		t.Fatalf("expected minimum 1 minute window, got %q", got)
	}

	cases := map[string]string{
		"write":   "file_change",
		"patch":   "file_change",
		"bash":    "command",
		"read":    "file_read",
		"glob":    "search",
		"unknown": "tool_use",
	}
	for in, want := range cases {
		if got := ClassifyTool(in); got != want {
			t.Fatalf("ClassifyTool(%q): expected %q, got %q", in, want, got)
		}
	}
}

func TestEndSessionAndTimelineDefaults(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s-end", "ohara", "/tmp/ohara"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	firstID, err := s.AddObservation(AddObservationParams{
		SessionID: "s-end",
		Type:      "note",
		Title:     "first",
		Content:   "first note",
		Project:   "ohara",
	})
	if err != nil {
		t.Fatalf("add first observation: %v", err)
	}
	_, err = s.AddObservation(AddObservationParams{
		SessionID: "s-end",
		Type:      "note",
		Title:     "second",
		Content:   "second note",
		Project:   "ohara",
	})
	if err != nil {
		t.Fatalf("add second observation: %v", err)
	}

	if err := s.EndSession("s-end", "finished session"); err != nil {
		t.Fatalf("end session: %v", err)
	}

	sess, err := s.GetSession("s-end")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if sess.EndedAt == nil {
		t.Fatalf("expected ended_at to be set")
	}
	if sess.Summary == nil || *sess.Summary != "finished session" {
		t.Fatalf("expected summary to be stored, got %+v", sess.Summary)
	}

	timeline, err := s.Timeline(firstID, 0, -1)
	if err != nil {
		t.Fatalf("timeline with default before/after: %v", err)
	}
	if timeline.SessionInfo == nil {
		t.Fatalf("expected session info in timeline")
	}
	if timeline.TotalInRange != 2 {
		t.Fatalf("expected total_in_range=2, got %d", timeline.TotalInRange)
	}
}

func TestInferTopicFamilyCoverage(t *testing.T) {
	cases := []struct {
		name    string
		typ     string
		title   string
		content string
		want    string
	}{
		{name: "type architecture", typ: "architecture", want: "architecture"},
		{name: "type bugfix", typ: "bugfix", want: "bug"},
		{name: "type decision", typ: "decision", want: "decision"},
		{name: "type pattern", typ: "pattern", want: "pattern"},
		{name: "type config", typ: "config", want: "config"},
		{name: "type discovery", typ: "discovery", want: "discovery"},
		{name: "type learning", typ: "learning", want: "learning"},
		{name: "type session summary", typ: "session_summary", want: "session"},
		{name: "text bug", title: "", content: "this caused a crash regression", want: "bug"},
		{name: "text architecture", title: "", content: "new boundary design", want: "architecture"},
		{name: "text decision", title: "", content: "we chose this tradeoff", want: "decision"},
		{name: "text pattern", title: "", content: "naming convention for handlers", want: "pattern"},
		{name: "text config", title: "", content: "docker env setup", want: "config"},
		{name: "text discovery", title: "", content: "root cause found", want: "discovery"},
		{name: "text learning", title: "", content: "key learning from this issue", want: "learning"},
		{name: "fallback type", typ: "Custom Type", want: "custom-type"},
		{name: "default topic", typ: "manual", title: "", content: "", want: "topic"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := inferTopicFamily(tc.typ, tc.title, tc.content)
			if got != tc.want {
				t.Fatalf("inferTopicFamily(%q,%q,%q): expected %q, got %q", tc.typ, tc.title, tc.content, tc.want, got)
			}
		})
	}
}

func TestStoreAdditionalQueryAndMutationBranches(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s-q", "ohara", "/tmp/ohara"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	longContent := strings.Repeat("x", s.cfg.MaxObservationLength+100)
	obsID, err := s.AddObservation(AddObservationParams{
		SessionID: "s-q",
		Type:      "note",
		Title:     "Private <private>secret</private> title",
		Content:   longContent + " <private>token</private>",
		Project:   "ohara",
		Scope:     "project",
	})
	if err != nil {
		t.Fatalf("add observation: %v", err)
	}
	obs, err := s.GetObservation(obsID)
	if err != nil {
		t.Fatalf("get observation: %v", err)
	}
	if !strings.Contains(obs.Title, "[REDACTED]") {
		t.Fatalf("expected private tags redacted in title, got %q", obs.Title)
	}
	if !strings.Contains(obs.Content, "... [truncated]") {
		t.Fatalf("expected truncated content marker, got %q", obs.Content)
	}

	newProject := ""
	newTopic := ""
	updated, err := s.UpdateObservation(obsID, UpdateObservationParams{Project: &newProject, TopicKey: &newTopic})
	if err != nil {
		t.Fatalf("update observation: %v", err)
	}
	if updated.Project != nil {
		t.Fatalf("expected nil project after empty update")
	}
	if updated.TopicKey != nil {
		t.Fatalf("expected nil topic key after empty update")
	}

	if _, err := s.AddPrompt(AddPromptParams{SessionID: "s-q", Content: "alpha prompt", Project: "alpha"}); err != nil {
		t.Fatalf("add alpha prompt: %v", err)
	}
	if _, err := s.AddPrompt(AddPromptParams{SessionID: "s-q", Content: "beta prompt", Project: "beta"}); err != nil {
		t.Fatalf("add beta prompt: %v", err)
	}

	recentPrompts, err := s.RecentPrompts("beta", 1)
	if err != nil {
		t.Fatalf("recent prompts with project filter: %v", err)
	}
	if len(recentPrompts) != 1 || recentPrompts[0].Project != "beta" {
		t.Fatalf("expected one beta prompt, got %+v", recentPrompts)
	}

	searchPrompts, err := s.SearchPrompts("prompt", "alpha", 0)
	if err != nil {
		t.Fatalf("search prompts with project filter/default limit: %v", err)
	}
	if len(searchPrompts) != 1 || searchPrompts[0].Project != "alpha" {
		t.Fatalf("expected one alpha prompt search result, got %+v", searchPrompts)
	}

	searchResults, err := s.Search("title", SearchOptions{Scope: "project", Limit: 9999})
	if err != nil {
		t.Fatalf("search with clamped limit: %v", err)
	}
	if len(searchResults) == 0 {
		t.Fatalf("expected search results")
	}

	ctx, err := s.FormatContext("", "project")
	if err != nil {
		t.Fatalf("format context: %v", err)
	}
	if !strings.Contains(ctx, "Recent User Prompts") {
		t.Fatalf("expected prompts section in context output")
	}
}

func TestStoreErrorBranchesWithClosedDatabase(t *testing.T) {
	s := newTestStore(t)

	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	if _, err := s.GetSession("missing"); err == nil {
		t.Fatalf("expected GetSession error when db is closed")
	}
	if _, err := s.AllSessions("", 1); err == nil {
		t.Fatalf("expected AllSessions error when db is closed")
	}
	if _, err := s.RecentSessions("", 1); err == nil {
		t.Fatalf("expected RecentSessions error when db is closed")
	}
	if _, err := s.SearchPrompts("x", "", 1); err == nil {
		t.Fatalf("expected SearchPrompts error when db is closed")
	}
	if _, err := s.Search("x", SearchOptions{}); err == nil {
		t.Fatalf("expected Search error when db is closed")
	}
	if _, err := s.Export(); err == nil {
		t.Fatalf("expected Export error when db is closed")
	}
	if _, err := s.Timeline(1, 1, 1); err == nil {
		t.Fatalf("expected Timeline error when db is closed")
	}
}

func TestEndSessionEdgeCases(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s-edge", "ohara", "/tmp/ohara"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	if err := s.EndSession("missing", "ignored"); err != nil {
		t.Fatalf("end missing session should be no-op: %v", err)
	}

	if err := s.EndSession("s-edge", ""); err != nil {
		t.Fatalf("end session with empty summary: %v", err)
	}

	sess, err := s.GetSession("s-edge")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if sess.EndedAt == nil {
		t.Fatalf("expected ended_at to be set")
	}
	if sess.Summary != nil {
		t.Fatalf("expected empty summary to persist as NULL, got %q", *sess.Summary)
	}
}

func TestTimelineHandlesMissingSessionRecord(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.db.Exec("PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatalf("disable fk: %v", err)
	}
	defer func() {
		_, _ = s.db.Exec("PRAGMA foreign_keys = ON")
	}()

	res, err := s.db.Exec(
		`INSERT INTO observations (session_id, type, title, content, project, scope, normalized_hash, revision_count, duplicate_count, last_seen_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 1, 1, datetime('now'), datetime('now'))`,
		"manual-save", "manual", "orphan", "orphan content", "ohara", "project", hashNormalized("orphan content"),
	)
	if err != nil {
		t.Fatalf("insert orphan observation: %v", err)
	}
	obsID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}

	timeline, err := s.Timeline(obsID, 1, 1)
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	if timeline.SessionInfo != nil {
		t.Fatalf("expected nil session info for missing session, got %+v", timeline.SessionInfo)
	}
	if timeline.TotalInRange != 1 {
		t.Fatalf("expected total in range=1, got %d", timeline.TotalInRange)
	}
}

func TestQueryObservationsScanError(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.queryObservations("SELECT 1"); err == nil {
		t.Fatalf("expected scan error for mismatched projection")
	}
}

func TestMigrationAndHelperEdgeBranches(t *testing.T) {
	t.Run("migrate is idempotent with existing triggers", func(t *testing.T) {
		s := newTestStore(t)
		if err := s.migrate(); err != nil {
			t.Fatalf("second migrate should succeed: %v", err)
		}
	})

	t.Run("legacy migrate skips table without id column", func(t *testing.T) {
		s := newTestStore(t)

		if _, err := s.db.Exec(`
			DROP TRIGGER IF EXISTS obs_fts_insert;
			DROP TRIGGER IF EXISTS obs_fts_update;
			DROP TRIGGER IF EXISTS obs_fts_delete;
			DROP TABLE IF EXISTS observations_fts;
			DROP TABLE observations;
			CREATE TABLE observations (
				session_id TEXT,
				type TEXT,
				title TEXT,
				content TEXT
			);
		`); err != nil {
			t.Fatalf("recreate observations without id: %v", err)
		}

		if err := s.migrateLegacyObservationsTable(); err != nil {
			t.Fatalf("legacy migrate should skip tables without id: %v", err)
		}
	})

	t.Run("topic helpers normalize edge cases", func(t *testing.T) {
		if got := SuggestTopicKey("decision", "decision", ""); got != "decision/general" {
			t.Fatalf("expected decision/general, got %q", got)
		}
		if got := SuggestTopicKey("bugfix", "bug-auth-panic", ""); got != "bug/auth-panic" {
			t.Fatalf("expected bug/auth-panic, got %q", got)
		}
		if got := SuggestTopicKey("manual", "!!!", "..."); got != "topic/general" {
			t.Fatalf("expected topic/general fallback, got %q", got)
		}

		longSegment := normalizeTopicSegment(strings.Repeat("abc", 50))
		if len(longSegment) != 100 {
			t.Fatalf("expected topic segment truncation to 100, got %d", len(longSegment))
		}

		longKey := normalizeTopicKey(strings.Repeat("k", 200))
		if len(longKey) != 120 {
			t.Fatalf("expected topic key truncation to 120, got %d", len(longKey))
		}
	})

	t.Run("format context empty returns empty string", func(t *testing.T) {
		s := newTestStore(t)
		ctx, err := s.FormatContext("", "")
		if err != nil {
			t.Fatalf("format context: %v", err)
		}
		if ctx != "" {
			t.Fatalf("expected empty context when no data, got %q", ctx)
		}
	})
}

func TestExportImportEdgeBranches(t *testing.T) {
	t.Run("export fails when observations query fails", func(t *testing.T) {
		s := newTestStore(t)

		if _, err := s.db.Exec(`
			DROP TRIGGER IF EXISTS obs_fts_insert;
			DROP TRIGGER IF EXISTS obs_fts_update;
			DROP TRIGGER IF EXISTS obs_fts_delete;
			DROP TABLE IF EXISTS observations_fts;
			DROP TABLE observations;
		`); err != nil {
			t.Fatalf("drop observations: %v", err)
		}

		_, err := s.Export()
		if err == nil || !strings.Contains(err.Error(), "export observations") {
			t.Fatalf("expected observations export error, got %v", err)
		}
	})

	t.Run("export fails when prompts query fails", func(t *testing.T) {
		s := newTestStore(t)

		if _, err := s.db.Exec(`
			DROP TRIGGER IF EXISTS prompt_fts_insert;
			DROP TRIGGER IF EXISTS prompt_fts_update;
			DROP TRIGGER IF EXISTS prompt_fts_delete;
			DROP TABLE IF EXISTS prompts_fts;
			DROP TABLE user_prompts;
		`); err != nil {
			t.Fatalf("drop prompts: %v", err)
		}

		_, err := s.Export()
		if err == nil || !strings.Contains(err.Error(), "export prompts") {
			t.Fatalf("expected prompts export error, got %v", err)
		}
	})

	t.Run("import begin tx fails on closed db", func(t *testing.T) {
		s := newTestStore(t)
		if err := s.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}

		_, err := s.Import(&ExportData{})
		if err == nil || !strings.Contains(err.Error(), "begin tx") {
			t.Fatalf("expected begin tx import error, got %v", err)
		}
	})

	t.Run("import fails on observation fk error", func(t *testing.T) {
		s := newTestStore(t)
		_, err := s.Import(&ExportData{
			Observations: []Observation{{
				ID:        1,
				SessionID: "missing-session",
				Type:      "bugfix",
				Title:     "x",
				Content:   "y",
				Scope:     "project",
				CreatedAt: Now(),
				UpdatedAt: Now(),
			}},
		})
		if err == nil || !strings.Contains(err.Error(), "import observation") {
			t.Fatalf("expected observation import error, got %v", err)
		}
	})

	t.Run("import fails on prompt fk error", func(t *testing.T) {
		s := newTestStore(t)
		_, err := s.Import(&ExportData{
			Prompts: []Prompt{{
				ID:        1,
				SessionID: "missing-session",
				Content:   "prompt",
				Project:   "ohara",
				CreatedAt: Now(),
			}},
		})
		if err == nil || !strings.Contains(err.Error(), "import prompt") {
			t.Fatalf("expected prompt import error, got %v", err)
		}
	})
}

func TestNewErrorBranches(t *testing.T) {
	t.Run("fails when data dir is a file", func(t *testing.T) {
		base := t.TempDir()
		badPath := filepath.Join(base, "not-a-dir")
		if err := os.WriteFile(badPath, []byte("x"), 0600); err != nil {
			t.Fatalf("write file: %v", err)
		}

		cfg := mustDefaultConfig(t)
		cfg.DataDir = badPath

		_, err := New(cfg)
		if err == nil || !strings.Contains(err.Error(), "create data dir") {
			t.Fatalf("expected create data dir error, got %v", err)
		}
	})

	t.Run("fails when db path is a directory", func(t *testing.T) {
		dataDir := t.TempDir()
		dbAsDir := filepath.Join(dataDir, "ohara.db")
		if err := os.Mkdir(dbAsDir, 0755); err != nil {
			t.Fatalf("mkdir db path: %v", err)
		}

		cfg := mustDefaultConfig(t)
		cfg.DataDir = dataDir

		_, err := New(cfg)
		if err == nil {
			t.Fatalf("expected New to fail when db path is a directory")
		}
	})

	t.Run("fails when migration encounters conflicting object", func(t *testing.T) {
		dataDir := t.TempDir()
		dbPath := filepath.Join(dataDir, "ohara.db")

		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatalf("open db: %v", err)
		}
		_, err = db.Exec(`
			CREATE TABLE sessions (
				id TEXT PRIMARY KEY,
				project TEXT NOT NULL,
				directory TEXT NOT NULL,
				started_at TEXT NOT NULL,
				ended_at TEXT,
				summary TEXT
			);
			CREATE TABLE user_prompts (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				session_id TEXT NOT NULL,
				content TEXT NOT NULL,
				created_at TEXT NOT NULL
			);
		`)
		if err != nil {
			_ = db.Close()
			t.Fatalf("create conflicting view: %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}

		cfg := mustDefaultConfig(t)
		cfg.DataDir = dataDir

		_, err = New(cfg)
		if err == nil || !strings.Contains(err.Error(), "migration") {
			t.Fatalf("expected migration error, got %v", err)
		}
	})
}

func TestMigrationInternalErrorAndNoopBranches(t *testing.T) {
	t.Run("addColumnIfNotExists adds then noops", func(t *testing.T) {
		s := newTestStore(t)
		if _, err := s.db.Exec(`CREATE TABLE extra_table (id INTEGER)`); err != nil {
			t.Fatalf("create extra table: %v", err)
		}

		if err := s.addColumnIfNotExists("extra_table", "name", "TEXT"); err != nil {
			t.Fatalf("add column: %v", err)
		}
		if err := s.addColumnIfNotExists("extra_table", "name", "TEXT"); err != nil {
			t.Fatalf("add existing column should noop: %v", err)
		}

		if err := s.addColumnIfNotExists("missing_table", "x", "TEXT"); err == nil {
			t.Fatalf("expected missing table error")
		}
	})

	t.Run("legacy migrate noops when id is primary key", func(t *testing.T) {
		s := newTestStore(t)
		if err := s.migrateLegacyObservationsTable(); err != nil {
			t.Fatalf("expected noop for modern schema: %v", err)
		}
	})

	t.Run("legacy migrate fails if temp table already exists", func(t *testing.T) {
		s := newTestStore(t)
		if _, err := s.db.Exec(`
			DROP TRIGGER IF EXISTS obs_fts_insert;
			DROP TRIGGER IF EXISTS obs_fts_update;
			DROP TRIGGER IF EXISTS obs_fts_delete;
			DROP TABLE IF EXISTS observations_fts;
			DROP TABLE observations;
			CREATE TABLE observations (
				id INT,
				session_id TEXT,
				type TEXT,
				title TEXT,
				content TEXT,
				created_at TEXT
			);
			CREATE TABLE observations_migrated (id INTEGER PRIMARY KEY);
		`); err != nil {
			t.Fatalf("prepare legacy schema: %v", err)
		}

		err := s.migrateLegacyObservationsTable()
		if err == nil || !strings.Contains(err.Error(), "create table") {
			t.Fatalf("expected create table error, got %v", err)
		}
	})

	t.Run("migrate returns deterministic exec hook errors", func(t *testing.T) {
		s := newTestStore(t)

		origExec := s.hooks.exec
		s.hooks.exec = func(db execer, query string, args ...any) (sql.Result, error) {
			if strings.Contains(query, "UPDATE observations SET scope = 'project'") {
				return nil, errors.New("forced migrate update failure")
			}
			return origExec(db, query, args...)
		}

		err := s.migrate()
		if err == nil || !strings.Contains(err.Error(), "forced migrate update failure") {
			t.Fatalf("expected forced migrate failure, got %v", err)
		}
	})

	t.Run("migrate fails when creating missing triggers", func(t *testing.T) {
		s := newTestStore(t)

		if _, err := s.db.Exec(`
			DROP TRIGGER IF EXISTS obs_fts_insert;
			DROP TRIGGER IF EXISTS obs_fts_update;
			DROP TRIGGER IF EXISTS obs_fts_delete;
		`); err != nil {
			t.Fatalf("drop obs triggers: %v", err)
		}

		origExec := s.hooks.exec
		s.hooks.exec = func(db execer, query string, args ...any) (sql.Result, error) {
			if strings.Contains(query, "CREATE TRIGGER obs_fts_insert") {
				return nil, errors.New("forced obs trigger failure")
			}
			return origExec(db, query, args...)
		}

		err := s.migrate()
		if err == nil || !strings.Contains(err.Error(), "forced obs trigger failure") {
			t.Fatalf("expected forced trigger failure, got %v", err)
		}
	})

	t.Run("legacy migrate surfaces begin and commit hook failures", func(t *testing.T) {
		prepareLegacyStore := func(t *testing.T) *Store {
			t.Helper()
			s := newTestStore(t)
			if _, err := s.db.Exec(`
				DROP TRIGGER IF EXISTS obs_fts_insert;
				DROP TRIGGER IF EXISTS obs_fts_update;
				DROP TRIGGER IF EXISTS obs_fts_delete;
				DROP TABLE IF EXISTS observations_fts;
				DROP TABLE observations;
				INSERT OR IGNORE INTO sessions (id, project, directory) VALUES ('s1', 'ohara', '/tmp/ohara');
				CREATE TABLE observations (
					id INT,
					session_id TEXT,
					type TEXT,
					title TEXT,
					content TEXT,
					tool_name TEXT,
					project TEXT,
					scope TEXT,
					topic_key TEXT,
					normalized_hash TEXT,
					revision_count INTEGER,
					duplicate_count INTEGER,
					last_seen_at TEXT,
					created_at TEXT,
					updated_at TEXT,
					deleted_at TEXT
				);
				INSERT INTO observations (id, session_id, type, title, content, project, created_at, updated_at)
				VALUES (1, 's1', 'bugfix', 'legacy', 'legacy row', 'ohara', datetime('now'), datetime('now'));
			`); err != nil {
				t.Fatalf("prepare legacy table: %v", err)
			}
			return s
		}

		t.Run("begin tx", func(t *testing.T) {
			s := prepareLegacyStore(t)
			s.hooks.beginTx = func(_ *sql.DB) (*sql.Tx, error) {
				return nil, errors.New("forced begin failure")
			}

			err := s.migrateLegacyObservationsTable()
			if err == nil || !strings.Contains(err.Error(), "forced begin failure") {
				t.Fatalf("expected begin failure, got %v", err)
			}
		})

		t.Run("commit", func(t *testing.T) {
			s := prepareLegacyStore(t)
			s.hooks.commit = func(_ *sql.Tx) error {
				return errors.New("forced legacy commit failure")
			}

			err := s.migrateLegacyObservationsTable()
			if err == nil || !strings.Contains(err.Error(), "forced legacy commit failure") {
				t.Fatalf("expected commit failure, got %v", err)
			}
		})
	})
}

func TestImportExportSeamErrors(t *testing.T) {
	t.Run("export query hooks", func(t *testing.T) {
		s := newTestStore(t)

		origQueryIt := s.hooks.queryIt
		s.hooks.queryIt = func(db queryer, query string, args ...any) (rowScanner, error) {
			if strings.Contains(query, "FROM sessions") {
				return nil, errors.New("forced sessions export query error")
			}
			return origQueryIt(db, query, args...)
		}
		if _, err := s.Export(); err == nil || !strings.Contains(err.Error(), "export sessions") {
			t.Fatalf("expected sessions export error, got %v", err)
		}

		s.hooks.queryIt = func(db queryer, query string, args ...any) (rowScanner, error) {
			if strings.Contains(query, "FROM observations") {
				return nil, errors.New("forced observations export query error")
			}
			return origQueryIt(db, query, args...)
		}
		if _, err := s.Export(); err == nil || !strings.Contains(err.Error(), "export observations") {
			t.Fatalf("expected observations export error, got %v", err)
		}

		s.hooks.queryIt = func(db queryer, query string, args ...any) (rowScanner, error) {
			if strings.Contains(query, "FROM user_prompts") {
				return nil, errors.New("forced prompts export query error")
			}
			return origQueryIt(db, query, args...)
		}
		if _, err := s.Export(); err == nil || !strings.Contains(err.Error(), "export prompts") {
			t.Fatalf("expected prompts export error, got %v", err)
		}
	})

	t.Run("import tx and exec hooks", func(t *testing.T) {
		s := newTestStore(t)

		s.hooks.beginTx = func(_ *sql.DB) (*sql.Tx, error) {
			return nil, errors.New("forced import begin failure")
		}
		if _, err := s.Import(&ExportData{}); err == nil || !strings.Contains(err.Error(), "begin tx") {
			t.Fatalf("expected begin tx error, got %v", err)
		}

		s.hooks = defaultStoreHooks()
		origExec := s.hooks.exec
		s.hooks.exec = func(db execer, query string, args ...any) (sql.Result, error) {
			if strings.Contains(query, "INSERT OR IGNORE INTO sessions") {
				return nil, errors.New("forced import session insert failure")
			}
			return origExec(db, query, args...)
		}
		if _, err := s.Import(&ExportData{Sessions: []Session{{ID: "s-x", Project: "p", Directory: "/tmp", StartedAt: Now()}}}); err == nil || !strings.Contains(err.Error(), "import session") {
			t.Fatalf("expected session import error, got %v", err)
		}

		s.hooks = defaultStoreHooks()
		s.hooks.commit = func(_ *sql.Tx) error {
			return errors.New("forced import commit failure")
		}
		if _, err := s.Import(&ExportData{}); err == nil || !strings.Contains(err.Error(), "import: commit") {
			t.Fatalf("expected commit error, got %v", err)
		}
	})
}

func TestHookFallbacksAndAdditionalBranches(t *testing.T) {
	t.Run("hook fallbacks call default DB methods", func(t *testing.T) {
		s := newTestStore(t)
		s.hooks = storeHooks{}

		if _, err := s.execHook(s.db, "SELECT 1"); err != nil {
			t.Fatalf("exec hook fallback: %v", err)
		}
		rows, err := s.queryHook(s.db, "SELECT 1")
		if err != nil {
			t.Fatalf("query hook fallback: %v", err)
		}
		_ = rows.Close()

		iter, err := s.queryItHook(s.db, "SELECT 1")
		if err != nil {
			t.Fatalf("query iterator fallback: %v", err)
		}
		_ = iter.Close()

		tx, err := s.beginTxHook()
		if err != nil {
			t.Fatalf("begin tx hook fallback: %v", err)
		}
		if err := s.commitHook(tx); err != nil {
			t.Fatalf("commit hook fallback: %v", err)
		}

		s2 := newTestStore(t)
		rows2, err := s2.queryHook(s2.db, "SELECT 1")
		if err != nil {
			t.Fatalf("query hook default closure: %v", err)
		}
		_ = rows2.Close()

		s.hooks.query = func(db queryer, query string, args ...any) (*sql.Rows, error) {
			return nil, errors.New("forced query hook error")
		}
		s.hooks.queryIt = nil
		if _, err := s.queryItHook(s.db, "SELECT 1"); err == nil {
			t.Fatalf("expected queryItHook error through queryHook fallback")
		}
	})

	t.Run("sessions and observations filters with default limits", func(t *testing.T) {
		s := newTestStore(t)
		if err := s.CreateSession("s-p", "proj-a", "/tmp/proj-a"); err != nil {
			t.Fatalf("create session proj-a: %v", err)
		}
		if err := s.CreateSession("s-q", "proj-b", "/tmp/proj-b"); err != nil {
			t.Fatalf("create session proj-b: %v", err)
		}
		if _, err := s.AddObservation(AddObservationParams{SessionID: "s-p", Type: "note", Title: "a", Content: "a", Project: "proj-a", Scope: "project"}); err != nil {
			t.Fatalf("add observation proj-a: %v", err)
		}
		if _, err := s.AddObservation(AddObservationParams{SessionID: "s-q", Type: "note", Title: "b", Content: "b", Project: "proj-b", Scope: "project"}); err != nil {
			t.Fatalf("add observation proj-b: %v", err)
		}

		recent, err := s.RecentSessions("proj-a", 0)
		if err != nil {
			t.Fatalf("recent sessions filtered: %v", err)
		}
		if len(recent) != 1 || recent[0].Project != "proj-a" {
			t.Fatalf("expected one proj-a recent session, got %+v", recent)
		}

		all, err := s.AllSessions("proj-b", -1)
		if err != nil {
			t.Fatalf("all sessions filtered: %v", err)
		}
		if len(all) != 1 || all[0].Project != "proj-b" {
			t.Fatalf("expected one proj-b session, got %+v", all)
		}

		obs, err := s.AllObservations("proj-a", "project", 0)
		if err != nil {
			t.Fatalf("all observations defaults: %v", err)
		}
		if len(obs) != 1 || obs[0].SessionID != "s-p" {
			t.Fatalf("expected one proj-a observation, got %+v", obs)
		}

		sessionObs, err := s.SessionObservations("s-p", 0)
		if err != nil {
			t.Fatalf("session observations default limit: %v", err)
		}
		if len(sessionObs) != 1 {
			t.Fatalf("expected one session observation, got %d", len(sessionObs))
		}

		recentObs, err := s.RecentObservations("proj-a", "project", 0)
		if err != nil {
			t.Fatalf("recent observations default limit: %v", err)
		}
		if len(recentObs) != 1 {
			t.Fatalf("expected one recent observation, got %d", len(recentObs))
		}

		recentPrompts, err := s.RecentPrompts("", 0)
		if err != nil {
			t.Fatalf("recent prompts default limit: %v", err)
		}
		if len(recentPrompts) != 0 {
			t.Fatalf("expected zero prompts, got %d", len(recentPrompts))
		}
	})

	t.Run("timeline includes before and after in chronological order", func(t *testing.T) {
		s := newTestStore(t)
		if err := s.CreateSession("s-tl", "ohara", "/tmp/ohara"); err != nil {
			t.Fatalf("create session: %v", err)
		}

		firstID, err := s.AddObservation(AddObservationParams{SessionID: "s-tl", Type: "note", Title: "1", Content: "one", Project: "ohara"})
		if err != nil {
			t.Fatalf("add first observation: %v", err)
		}
		middleID, err := s.AddObservation(AddObservationParams{SessionID: "s-tl", Type: "note", Title: "2", Content: "two", Project: "ohara"})
		if err != nil {
			t.Fatalf("add middle observation: %v", err)
		}
		lastID, err := s.AddObservation(AddObservationParams{SessionID: "s-tl", Type: "note", Title: "3", Content: "three", Project: "ohara"})
		if err != nil {
			t.Fatalf("add last observation: %v", err)
		}

		tl, err := s.Timeline(middleID, 5, 5)
		if err != nil {
			t.Fatalf("timeline middle: %v", err)
		}
		if len(tl.Before) != 1 || tl.Before[0].ID != firstID {
			t.Fatalf("expected first in before list, got %+v", tl.Before)
		}
		if len(tl.After) != 1 || tl.After[0].ID != lastID {
			t.Fatalf("expected last in after list, got %+v", tl.After)
		}
	})

	t.Run("format context returns specific query stage errors", func(t *testing.T) {
		t.Run("recent sessions error", func(t *testing.T) {
			s := newTestStore(t)
			_ = s.Close()
			if _, err := s.FormatContext("", ""); err == nil {
				t.Fatalf("expected format context to fail from recent sessions")
			}
		})

		t.Run("recent observations error", func(t *testing.T) {
			s := newTestStore(t)
			if err := s.CreateSession("s-ctx", "ohara", "/tmp/ohara"); err != nil {
				t.Fatalf("create session: %v", err)
			}
			if _, err := s.db.Exec("DROP TABLE observations"); err != nil {
				t.Fatalf("drop observations: %v", err)
			}
			if _, err := s.FormatContext("", ""); err == nil {
				t.Fatalf("expected format context to fail from recent observations")
			}
		})

		t.Run("recent prompts error", func(t *testing.T) {
			s := newTestStore(t)
			if err := s.CreateSession("s-ctx2", "ohara", "/tmp/ohara"); err != nil {
				t.Fatalf("create session: %v", err)
			}
			if _, err := s.db.Exec("DROP TABLE user_prompts"); err != nil {
				t.Fatalf("drop prompts: %v", err)
			}
			if _, err := s.FormatContext("", ""); err == nil {
				t.Fatalf("expected format context to fail from recent prompts")
			}
		})
	})
}

func TestStoreUncoveredBranchesPushToHundred(t *testing.T) {
	t.Run("new open database hook error", func(t *testing.T) {
		orig := openDB
		t.Cleanup(func() { openDB = orig })
		openDB = func(driverName, dataSourceName string) (*sql.DB, error) {
			return nil, errors.New("forced open error")
		}

		cfg := mustDefaultConfig(t)
		cfg.DataDir = t.TempDir()
		if _, err := New(cfg); err == nil || !strings.Contains(err.Error(), "open database") {
			t.Fatalf("expected open database error, got %v", err)
		}
	})

	t.Run("migrate forced failures for remaining exec branches", func(t *testing.T) {
		failCases := []string{
			"CREATE INDEX IF NOT EXISTS idx_obs_scope",
			"UPDATE observations SET topic_key = NULL",
			"UPDATE observations SET revision_count = 1",
			"UPDATE observations SET duplicate_count = 1",
			"UPDATE observations SET updated_at = created_at",
			"UPDATE user_prompts SET project = ''",
			"CREATE TRIGGER prompt_fts_insert",
		}
		for _, needle := range failCases {
			t.Run(needle, func(t *testing.T) {
				s := newTestStore(t)
				if strings.Contains(needle, "CREATE TRIGGER prompt_fts_insert") {
					if _, err := s.db.Exec(`
						DROP TRIGGER IF EXISTS prompt_fts_insert;
						DROP TRIGGER IF EXISTS prompt_fts_update;
						DROP TRIGGER IF EXISTS prompt_fts_delete;
					`); err != nil {
						t.Fatalf("drop prompt triggers: %v", err)
					}
				}
				origExec := s.hooks.exec
				s.hooks.exec = func(db execer, query string, args ...any) (sql.Result, error) {
					if strings.Contains(query, needle) {
						return nil, errors.New("forced migrate failure")
					}
					return origExec(db, query, args...)
				}
				if err := s.migrate(); err == nil {
					t.Fatalf("expected migrate error for %q", needle)
				}
			})
		}
	})

	t.Run("migrate addColumn and legacy-call propagation", func(t *testing.T) {
		t.Run("propagates addColumn error", func(t *testing.T) {
			s := newTestStore(t)
			origQueryIt := s.hooks.queryIt
			called := 0
			s.hooks.queryIt = func(db queryer, query string, args ...any) (rowScanner, error) {
				if strings.Contains(query, "PRAGMA table_info(observations)") {
					called++
					if called == 1 {
						return nil, errors.New("forced addColumn failure")
					}
				}
				return origQueryIt(db, query, args...)
			}
			if err := s.migrate(); err == nil {
				t.Fatalf("expected migrate to propagate addColumn failure")
			}
		})

		t.Run("propagates legacy migrate error", func(t *testing.T) {
			s := newTestStore(t)
			origQueryIt := s.hooks.queryIt
			called := 0
			s.hooks.queryIt = func(db queryer, query string, args ...any) (rowScanner, error) {
				if strings.Contains(query, "PRAGMA table_info(observations)") {
					called++
					if called == 9 {
						return nil, errors.New("forced legacy call failure")
					}
				}
				return origQueryIt(db, query, args...)
			}
			if err := s.migrate(); err == nil {
				t.Fatalf("expected migrate to propagate legacy migrate failure")
			}
		})
	})

	t.Run("add observation, prompt, update forced errors", func(t *testing.T) {
		s := newTestStore(t)
		if err := s.CreateSession("s-e", "ohara", "/tmp/ohara"); err != nil {
			t.Fatalf("create session: %v", err)
		}

		if _, err := s.AddObservation(AddObservationParams{SessionID: "s-e", Type: "note", Title: "top", Content: "x", Project: "ohara", TopicKey: "x"}); err != nil {
			t.Fatalf("seed topic observation: %v", err)
		}
		origExec := s.hooks.exec
		s.hooks.exec = func(db execer, query string, args ...any) (sql.Result, error) {
			if strings.Contains(query, "SET type = ?") {
				return nil, errors.New("forced topic update error")
			}
			return origExec(db, query, args...)
		}
		if _, err := s.AddObservation(AddObservationParams{SessionID: "s-e", Type: "note", Title: "top", Content: "x", Project: "ohara", TopicKey: "x"}); err == nil {
			t.Fatalf("expected topic upsert exec error")
		}

		s.hooks = defaultStoreHooks()
		if _, err := s.AddObservation(AddObservationParams{SessionID: "s-e", Type: "note", Title: "dup", Content: "dup content", Project: "ohara"}); err != nil {
			t.Fatalf("seed dedupe observation: %v", err)
		}
		origExec = s.hooks.exec
		s.hooks.exec = func(db execer, query string, args ...any) (sql.Result, error) {
			if strings.Contains(query, "SET duplicate_count = duplicate_count + 1") {
				return nil, errors.New("forced dedupe update error")
			}
			return origExec(db, query, args...)
		}
		if _, err := s.AddObservation(AddObservationParams{SessionID: "s-e", Type: "note", Title: "dup", Content: "dup content", Project: "ohara"}); err == nil {
			t.Fatalf("expected dedupe exec error")
		}

		if err := s.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
		if _, err := s.AddObservation(AddObservationParams{SessionID: "s-e", Type: "note", Title: "x", Content: "y", Project: "ohara", TopicKey: "t"}); err == nil {
			t.Fatalf("expected topic query error on closed db")
		}
		if _, err := s.AddObservation(AddObservationParams{SessionID: "s-e", Type: "note", Title: "x", Content: "y", Project: "ohara"}); err == nil {
			t.Fatalf("expected dedupe query error on closed db")
		}
		if _, err := s.AddPrompt(AddPromptParams{SessionID: "s-e", Content: "x"}); err == nil {
			t.Fatalf("expected add prompt error on closed db")
		}
	})

	t.Run("update observation remaining branches", func(t *testing.T) {
		s := newTestStore(t)
		if err := s.CreateSession("s-u", "ohara", "/tmp/ohara"); err != nil {
			t.Fatalf("create session: %v", err)
		}
		id, err := s.AddObservation(AddObservationParams{SessionID: "s-u", Type: "old", Title: "t", Content: "c", Project: "ohara", TopicKey: "topic/key"})
		if err != nil {
			t.Fatalf("seed observation: %v", err)
		}

		if _, err := s.UpdateObservation(999999, UpdateObservationParams{}); err == nil {
			t.Fatalf("expected update missing observation error")
		}

		newType := "new-type"
		longContent := strings.Repeat("z", s.cfg.MaxObservationLength+50)
		if _, err := s.UpdateObservation(id, UpdateObservationParams{Type: &newType, Content: &longContent}); err != nil {
			t.Fatalf("update with type+truncation: %v", err)
		}

		origExec := s.hooks.exec
		s.hooks.exec = func(db execer, query string, args ...any) (sql.Result, error) {
			if strings.Contains(query, "UPDATE observations") {
				return nil, errors.New("forced update exec error")
			}
			return origExec(db, query, args...)
		}
		if _, err := s.UpdateObservation(id, UpdateObservationParams{}); err == nil {
			t.Fatalf("expected update exec error")
		}
	})

	t.Run("query iterator scan and rows.Err branches", func(t *testing.T) {
		s := newTestStore(t)
		origQueryIt := s.hooks.queryIt

		setScanErr := func(match string) {
			s.hooks.queryIt = func(db queryer, query string, args ...any) (rowScanner, error) {
				if strings.Contains(query, match) {
					return &fakeRows{next: []bool{true, false}, scanErr: errors.New("forced scan error")}, nil
				}
				return origQueryIt(db, query, args...)
			}
		}

		setRowsErr := func(match string) {
			s.hooks.queryIt = func(db queryer, query string, args ...any) (rowScanner, error) {
				if strings.Contains(query, match) {
					return &fakeRows{next: []bool{false}, err: errors.New("forced rows err")}, nil
				}
				return origQueryIt(db, query, args...)
			}
		}

		if err := s.CreateSession("s-iter", "ohara", "/tmp/ohara"); err != nil {
			t.Fatalf("create session: %v", err)
		}
		if _, err := s.AddObservation(AddObservationParams{SessionID: "s-iter", Type: "note", Title: "one", Content: "one", Project: "ohara"}); err != nil {
			t.Fatalf("add observation: %v", err)
		}
		if _, err := s.AddPrompt(AddPromptParams{SessionID: "s-iter", Content: "prompt", Project: "ohara"}); err != nil {
			t.Fatalf("add prompt: %v", err)
		}

		setScanErr("FROM sessions s")
		if _, err := s.RecentSessions("", 10); err == nil {
			t.Fatalf("expected recent sessions scan error")
		}

		setScanErr("FROM sessions s")
		if _, err := s.AllSessions("", 10); err == nil {
			t.Fatalf("expected all sessions scan error")
		}

		setScanErr("FROM user_prompts")
		if _, err := s.RecentPrompts("", 10); err == nil {
			t.Fatalf("expected recent prompts scan error")
		}

		setScanErr("FROM prompts_fts")
		if _, err := s.SearchPrompts("prompt", "", 10); err == nil {
			t.Fatalf("expected search prompts scan error")
		}

		setScanErr("FROM observations_fts")
		if _, err := s.Search("one", SearchOptions{}); err == nil {
			t.Fatalf("expected search scan error")
		}

		setRowsErr("FROM observations_fts")
		if _, err := s.Search("one", SearchOptions{}); err == nil {
			t.Fatalf("expected search rows err")
		}

		setScanErr("SELECT id, project, directory")
		if _, err := s.Export(); err == nil {
			t.Fatalf("expected export sessions scan error")
		}

		setRowsErr("SELECT id, project, directory")
		if _, err := s.Export(); err == nil {
			t.Fatalf("expected export sessions rows err")
		}

		setScanErr("FROM observations ORDER BY id")
		if _, err := s.Export(); err == nil {
			t.Fatalf("expected export observations scan error")
		}

		setRowsErr("FROM observations ORDER BY id")
		if _, err := s.Export(); err == nil {
			t.Fatalf("expected export observations rows err")
		}

		setScanErr("FROM user_prompts ORDER BY id")
		if _, err := s.Export(); err == nil {
			t.Fatalf("expected export prompts scan error")
		}

		setRowsErr("FROM user_prompts ORDER BY id")
		if _, err := s.Export(); err == nil {
			t.Fatalf("expected export prompts rows err")
		}

		setScanErr("FROM sync_chunks")
		if _, err := s.GetSyncedChunks(); err == nil {
			t.Fatalf("expected synced chunks scan error")
		}

		setRowsErr("PRAGMA table_info(extra_table)")
		if _, err := s.db.Exec(`CREATE TABLE extra_table (id INTEGER)`); err != nil {
			t.Fatalf("create extra table: %v", err)
		}
		if err := s.addColumnIfNotExists("extra_table", "n", "TEXT"); err == nil {
			t.Fatalf("expected add column rows err")
		}

		setScanErr("PRAGMA table_info(extra_table)")
		if err := s.addColumnIfNotExists("extra_table", "n2", "TEXT"); err == nil {
			t.Fatalf("expected add column scan error")
		}

		setRowsErr("PRAGMA table_info(observations)")
		if err := s.migrateLegacyObservationsTable(); err == nil {
			t.Fatalf("expected legacy migrate pragma rows err")
		}

		setScanErr("PRAGMA table_info(observations)")
		if err := s.migrateLegacyObservationsTable(); err == nil {
			t.Fatalf("expected legacy migrate pragma scan error")
		}

		s.hooks.queryIt = origQueryIt
	})

	t.Run("timeline and search type filter branches", func(t *testing.T) {
		s := newTestStore(t)
		if err := s.CreateSession("s-t2", "ohara", "/tmp/ohara"); err != nil {
			t.Fatalf("create session: %v", err)
		}
		first, _ := s.AddObservation(AddObservationParams{SessionID: "s-t2", Type: "decision", Title: "a", Content: "a", Project: "ohara"})
		_, _ = s.AddObservation(AddObservationParams{SessionID: "s-t2", Type: "decision", Title: "aa", Content: "aa", Project: "ohara"})
		focus, _ := s.AddObservation(AddObservationParams{SessionID: "s-t2", Type: "decision", Title: "b", Content: "b", Project: "ohara"})
		_, _ = s.AddObservation(AddObservationParams{SessionID: "s-t2", Type: "decision", Title: "c", Content: "c", Project: "ohara"})

		if _, err := s.Search("b", SearchOptions{Type: "decision", Project: "ohara", Scope: "project", Limit: 5}); err != nil {
			t.Fatalf("search with type filter: %v", err)
		}

		origQueryIt := s.hooks.queryIt
		s.hooks.queryIt = func(db queryer, query string, args ...any) (rowScanner, error) {
			if strings.Contains(query, "id < ?") {
				return nil, errors.New("forced before query error")
			}
			return origQueryIt(db, query, args...)
		}
		if _, err := s.Timeline(focus, 2, 2); err == nil {
			t.Fatalf("expected timeline before query error")
		}

		s.hooks.queryIt = func(db queryer, query string, args ...any) (rowScanner, error) {
			if strings.Contains(query, "id < ?") {
				return &fakeRows{next: []bool{true, false}, scanErr: errors.New("forced before scan error")}, nil
			}
			return origQueryIt(db, query, args...)
		}
		if _, err := s.Timeline(focus, 2, 2); err == nil {
			t.Fatalf("expected timeline before scan error")
		}

		s.hooks.queryIt = func(db queryer, query string, args ...any) (rowScanner, error) {
			if strings.Contains(query, "id < ?") {
				return &fakeRows{next: []bool{false}, err: errors.New("forced before rows err")}, nil
			}
			return origQueryIt(db, query, args...)
		}
		if _, err := s.Timeline(focus, 2, 2); err == nil {
			t.Fatalf("expected timeline before rows err")
		}

		s.hooks.queryIt = func(db queryer, query string, args ...any) (rowScanner, error) {
			if strings.Contains(query, "id > ?") {
				return nil, errors.New("forced after query error")
			}
			return origQueryIt(db, query, args...)
		}
		if _, err := s.Timeline(focus, 2, 2); err == nil {
			t.Fatalf("expected timeline after query error")
		}

		s.hooks.queryIt = func(db queryer, query string, args ...any) (rowScanner, error) {
			if strings.Contains(query, "id > ?") {
				return &fakeRows{next: []bool{true, false}, scanErr: errors.New("forced after scan error")}, nil
			}
			return origQueryIt(db, query, args...)
		}
		if _, err := s.Timeline(focus, 2, 2); err == nil {
			t.Fatalf("expected timeline after scan error")
		}

		s.hooks.queryIt = func(db queryer, query string, args ...any) (rowScanner, error) {
			if strings.Contains(query, "id > ?") {
				return &fakeRows{next: []bool{false}, err: errors.New("forced after rows err")}, nil
			}
			return origQueryIt(db, query, args...)
		}
		if _, err := s.Timeline(focus, 2, 2); err == nil {
			t.Fatalf("expected timeline after rows err")
		}

		s.hooks.queryIt = origQueryIt
		tl, err := s.Timeline(first, 5, 5)
		if err != nil {
			t.Fatalf("timeline reverse branch run: %v", err)
		}
		if len(tl.After) == 0 {
			t.Fatalf("expected timeline after entries")
		}
	})

	t.Run("format context and stats remaining branches", func(t *testing.T) {
		s := newTestStore(t)
		if err := s.CreateSession("s-c", "ohara", "/tmp/ohara"); err != nil {
			t.Fatalf("create session: %v", err)
		}
		if _, err := s.AddObservation(AddObservationParams{SessionID: "s-c", Type: "note", Title: "n", Content: "n", Project: "ohara"}); err != nil {
			t.Fatalf("add obs: %v", err)
		}

		origQueryIt := s.hooks.queryIt
		s.hooks.queryIt = func(db queryer, query string, args ...any) (rowScanner, error) {
			if strings.Contains(query, "FROM observations o") && strings.Contains(query, "WHERE o.deleted_at IS NULL") {
				return nil, errors.New("forced recent observations error")
			}
			return origQueryIt(db, query, args...)
		}
		if _, err := s.FormatContext("ohara", "project"); err == nil {
			t.Fatalf("expected format context observations error")
		}

		s.hooks.queryIt = func(db queryer, query string, args ...any) (rowScanner, error) {
			if strings.Contains(query, "GROUP BY project") {
				return nil, errors.New("forced stats query error")
			}
			return origQueryIt(db, query, args...)
		}
		if _, err := s.Stats(); err != nil {
			t.Fatalf("stats should swallow project query errors: %v", err)
		}

		if err := s.EndSession("s-c", "has summary"); err != nil {
			t.Fatalf("end session: %v", err)
		}
		s.hooks.queryIt = origQueryIt
		ctx, err := s.FormatContext("ohara", "project")
		if err != nil {
			t.Fatalf("format context with summary: %v", err)
		}
		if !strings.Contains(ctx, "has summary") {
			t.Fatalf("expected session summary included in context")
		}
	})

	t.Run("helper query errors and legacy migration late-stage failures", func(t *testing.T) {
		s := newTestStore(t)
		if err := s.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
		if _, err := s.GetSyncedChunks(); err == nil {
			t.Fatalf("expected synced chunks query error")
		}
		if _, err := s.queryObservations("SELECT id FROM observations"); err == nil {
			t.Fatalf("expected queryObservations query error")
		}
		if err := s.addColumnIfNotExists("observations", "x", "TEXT"); err == nil {
			t.Fatalf("expected addColumn query error")
		}
		if err := s.migrateLegacyObservationsTable(); err == nil {
			t.Fatalf("expected legacy migrate query error")
		}

		s2 := newTestStore(t)
		if _, err := s2.db.Exec(`
			DROP TRIGGER IF EXISTS obs_fts_insert;
			DROP TRIGGER IF EXISTS obs_fts_update;
			DROP TRIGGER IF EXISTS obs_fts_delete;
			DROP TABLE IF EXISTS observations_fts;
			DROP TABLE observations;
			INSERT OR IGNORE INTO sessions (id, project, directory) VALUES ('s1', 'ohara', '/tmp/ohara');
			CREATE TABLE observations (
				id INT,
				session_id TEXT,
				type TEXT,
				title TEXT,
				content TEXT,
				tool_name TEXT,
				project TEXT,
				scope TEXT,
				topic_key TEXT,
				normalized_hash TEXT,
				revision_count INTEGER,
				duplicate_count INTEGER,
				last_seen_at TEXT,
				created_at TEXT,
				updated_at TEXT,
				deleted_at TEXT
			);
			INSERT INTO observations (id, session_id, type, title, content, project, created_at, updated_at)
			VALUES (1, 's1', 'bugfix', 'legacy', 'legacy row', 'ohara', datetime('now'), datetime('now'));
		`); err != nil {
			t.Fatalf("prepare legacy table: %v", err)
		}

		lateFail := []string{"INSERT INTO observations_migrated", "DROP TABLE observations", "RENAME TO observations", "CREATE VIRTUAL TABLE observations_fts"}
		for _, needle := range lateFail {
			t.Run(needle, func(t *testing.T) {
				s3 := newTestStore(t)
				if _, err := s3.db.Exec(`
					DROP TRIGGER IF EXISTS obs_fts_insert;
					DROP TRIGGER IF EXISTS obs_fts_update;
					DROP TRIGGER IF EXISTS obs_fts_delete;
					DROP TABLE IF EXISTS observations_fts;
					DROP TABLE observations;
			INSERT OR IGNORE INTO sessions (id, project, directory) VALUES ('s1', 'ohara', '/tmp/ohara');
					CREATE TABLE observations (
						id INT,
						session_id TEXT,
						type TEXT,
						title TEXT,
						content TEXT,
						tool_name TEXT,
						project TEXT,
						scope TEXT,
						topic_key TEXT,
						normalized_hash TEXT,
						revision_count INTEGER,
						duplicate_count INTEGER,
						last_seen_at TEXT,
						created_at TEXT,
						updated_at TEXT,
						deleted_at TEXT
					);
					INSERT INTO observations (id, session_id, type, title, content, project, created_at, updated_at)
					VALUES (1, 's1', 'bugfix', 'legacy', 'legacy row', 'ohara', datetime('now'), datetime('now'));
				`); err != nil {
					t.Fatalf("prepare legacy schema: %v", err)
				}

				origExec := s3.hooks.exec
				s3.hooks.exec = func(db execer, query string, args ...any) (sql.Result, error) {
					if strings.Contains(query, needle) {
						return nil, errors.New("forced legacy late failure")
					}
					return origExec(db, query, args...)
				}
				if err := s3.migrateLegacyObservationsTable(); err == nil {
					t.Fatalf("expected legacy migrate error for %q", needle)
				}
			})
		}
	})
}

// ─── Issue #25: Session collision regression tests ──────────────────────────

func TestCreateSessionUpsertsEmptyProjectAndDirectory(t *testing.T) {
	s := newTestStore(t)

	// Create session with empty project/directory (simulates first MCP call without context)
	if err := s.CreateSession("sess-upsert", "", ""); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Second call with real project/directory should fill in the blanks.
	// Project names are normalized to lowercase, so "projectA" becomes "projecta".
	if err := s.CreateSession("sess-upsert", "projectA", "/tmp/a"); err != nil {
		t.Fatalf("upsert session: %v", err)
	}

	sess, err := s.GetSession("sess-upsert")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if sess.Project != "projecta" {
		t.Fatalf("expected project=projecta after upsert (normalized), got %q", sess.Project)
	}
	if sess.Directory != "/tmp/a" {
		t.Fatalf("expected directory=/tmp/a after upsert, got %q", sess.Directory)
	}
}

func TestCreateSessionDoesNotOverwriteExistingProject(t *testing.T) {
	s := newTestStore(t)

	// Create session with project A (normalized to "projecta")
	if err := s.CreateSession("sess-preserve", "projectA", "/tmp/a"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Second call with project B should NOT overwrite
	if err := s.CreateSession("sess-preserve", "projectB", "/tmp/b"); err != nil {
		t.Fatalf("upsert session: %v", err)
	}

	sess, err := s.GetSession("sess-preserve")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	// Project names are normalized to lowercase, so "projectA" is stored as "projecta"
	if sess.Project != "projecta" {
		t.Fatalf("expected project=projecta (preserved, normalized), got %q", sess.Project)
	}
	if sess.Directory != "/tmp/a" {
		t.Fatalf("expected directory=/tmp/a (preserved), got %q", sess.Directory)
	}
}

func TestCreateSessionPartialUpsert(t *testing.T) {
	s := newTestStore(t)

	t.Run("fills directory when project already set", func(t *testing.T) {
		if err := s.CreateSession("sess-partial-1", "myproject", ""); err != nil {
			t.Fatalf("create: %v", err)
		}
		// Second call fills directory but project stays
		if err := s.CreateSession("sess-partial-1", "other", "/new/dir"); err != nil {
			t.Fatalf("upsert: %v", err)
		}
		sess, err := s.GetSession("sess-partial-1")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if sess.Project != "myproject" {
			t.Fatalf("project should be preserved, got %q", sess.Project)
		}
		if sess.Directory != "/new/dir" {
			t.Fatalf("directory should be filled, got %q", sess.Directory)
		}
	})

	t.Run("fills project when directory already set", func(t *testing.T) {
		if err := s.CreateSession("sess-partial-2", "", "/existing/dir"); err != nil {
			t.Fatalf("create: %v", err)
		}
		if err := s.CreateSession("sess-partial-2", "newproject", ""); err != nil {
			t.Fatalf("upsert: %v", err)
		}
		sess, err := s.GetSession("sess-partial-2")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if sess.Project != "newproject" {
			t.Fatalf("project should be filled, got %q", sess.Project)
		}
		if sess.Directory != "/existing/dir" {
			t.Fatalf("directory should be preserved, got %q", sess.Directory)
		}
	})

	t.Run("both empty stays empty", func(t *testing.T) {
		if err := s.CreateSession("sess-partial-3", "", ""); err != nil {
			t.Fatalf("create: %v", err)
		}
		if err := s.CreateSession("sess-partial-3", "", ""); err != nil {
			t.Fatalf("upsert: %v", err)
		}
		sess, err := s.GetSession("sess-partial-3")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if sess.Project != "" {
			t.Fatalf("project should stay empty, got %q", sess.Project)
		}
		if sess.Directory != "" {
			t.Fatalf("directory should stay empty, got %q", sess.Directory)
		}
	})
}

func TestTruncateUTF8(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{name: "short ascii", in: "abc", max: 10, want: "abc"},
		{name: "exact length", in: "hello", max: 5, want: "hello"},
		{name: "long ascii", in: "abcdef", max: 3, want: "abc..."},
		{name: "spanish accents", in: "Decisión de arquitectura", max: 8, want: "Decisión..."},
		{name: "emoji", in: "🐛🔧🚀✨🎉💡", max: 3, want: "🐛🔧🚀..."},
		{name: "mixed ascii and multibyte", in: "café☕latte", max: 5, want: "café☕..."},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := util.Truncate(tc.in, tc.max)
			if got != tc.want {
				t.Fatalf("truncate(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
		})
	}
}

// ─── Project Enrollment CRUD Tests ───────────────────────────────────────────

func TestEnrollProjectBasic(t *testing.T) {
	s := newTestStore(t)

	// Enroll a project.
	if err := s.EnrollProject("ohara"); err != nil {
		t.Fatalf("enroll project: %v", err)
	}

	// Verify it shows up in the list.
	projects, err := s.ListEnrolledProjects()
	if err != nil {
		t.Fatalf("list enrolled projects: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected 1 enrolled project, got %d", len(projects))
	}
	if projects[0].Project != "ohara" {
		t.Fatalf("expected project 'ohara', got %q", projects[0].Project)
	}
	if projects[0].EnrolledAt == "" {
		t.Fatal("expected enrolled_at to be set")
	}

	// Verify IsProjectEnrolled returns true.
	enrolled, err := s.IsProjectEnrolled("ohara")
	if err != nil {
		t.Fatalf("is project enrolled: %v", err)
	}
	if !enrolled {
		t.Fatal("expected project to be enrolled")
	}
}

func TestEnrollProjectIdempotent(t *testing.T) {
	s := newTestStore(t)

	// Enroll twice — should not error.
	if err := s.EnrollProject("ohara"); err != nil {
		t.Fatalf("first enroll: %v", err)
	}
	if err := s.EnrollProject("ohara"); err != nil {
		t.Fatalf("second enroll (idempotent): %v", err)
	}

	// Should still be exactly one row.
	projects, err := s.ListEnrolledProjects()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected 1 enrolled project after double-enroll, got %d", len(projects))
	}
}

func TestEnrollProjectBackfillsHistoricalMutations(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.db.Exec(
		`INSERT INTO sessions (id, project, directory, ended_at, summary) VALUES (?, ?, ?, datetime('now'), ?)`,
		"legacy-session", "legacy-proj", "/tmp/legacy", "done",
	); err != nil {
		t.Fatalf("insert session: %v", err)
	}

	if _, err := s.db.Exec(
		`INSERT INTO observations (sync_id, session_id, type, title, content, project, scope, normalized_hash, revision_count, duplicate_count, last_seen_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, 1, datetime('now'), datetime('now'))`,
		"obs-legacy", "legacy-session", "decision", "Legacy obs", "Historical content", "legacy-proj", "project", hashNormalized("Historical content"),
	); err != nil {
		t.Fatalf("insert observation: %v", err)
	}

	if _, err := s.db.Exec(
		`INSERT INTO user_prompts (sync_id, session_id, content, project) VALUES (?, ?, ?, ?)`,
		"prompt-legacy", "legacy-session", "What happened before enterprise?", "legacy-proj",
	); err != nil {
		t.Fatalf("insert prompt: %v", err)
	}

	var before int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sync_mutations`).Scan(&before); err != nil {
		t.Fatalf("count mutations before enroll: %v", err)
	}
	if before != 0 {
		t.Fatalf("expected 0 sync mutations before enroll, got %d", before)
	}

	if err := s.EnrollProject("legacy-proj"); err != nil {
		t.Fatalf("enroll project: %v", err)
	}

	mutations, err := s.ListPendingSyncMutations(DefaultSyncTargetKey, 10)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(mutations) != 3 {
		t.Fatalf("expected 3 backfilled mutations, got %d", len(mutations))
	}

	expected := map[string]string{
		SyncEntitySession:     "legacy-session",
		SyncEntityObservation: "obs-legacy",
		SyncEntityPrompt:      "prompt-legacy",
	}
	for _, mutation := range mutations {
		entityKey, ok := expected[mutation.Entity]
		if !ok {
			t.Fatalf("unexpected mutation entity %q", mutation.Entity)
		}
		if mutation.EntityKey != entityKey {
			t.Fatalf("expected entity_key %q for %s, got %q", entityKey, mutation.Entity, mutation.EntityKey)
		}
		if mutation.Project != "legacy-proj" {
			t.Fatalf("expected project legacy-proj, got %q", mutation.Project)
		}
	}
	state, err := s.GetSyncState(DefaultSyncTargetKey)
	if err != nil {
		t.Fatalf("get sync state: %v", err)
	}
	if state.LastEnqueuedSeq != 3 {
		t.Fatalf("expected last_enqueued_seq 3 after backfill, got %d", state.LastEnqueuedSeq)
	}
}

func TestEnrollProjectBackfillIsIdempotentAndSkipsExistingMutations(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.db.Exec(
		`INSERT INTO sessions (id, project, directory) VALUES (?, ?, ?)`,
		"legacy-session", "legacy-proj", "/tmp/legacy",
	); err != nil {
		t.Fatalf("insert session: %v", err)
	}

	if _, err := s.db.Exec(
		`INSERT INTO observations (sync_id, session_id, type, title, content, project, scope, normalized_hash, revision_count, duplicate_count, last_seen_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, 1, datetime('now'), datetime('now'))`,
		"obs-legacy", "legacy-session", "decision", "Legacy obs", "Historical content", "legacy-proj", "project", hashNormalized("Historical content"),
	); err != nil {
		t.Fatalf("insert observation: %v", err)
	}

	if _, err := s.db.Exec(
		`INSERT INTO user_prompts (sync_id, session_id, content, project) VALUES (?, ?, ?, ?)`,
		"prompt-legacy", "legacy-session", "Historical prompt", "legacy-proj",
	); err != nil {
		t.Fatalf("insert prompt: %v", err)
	}

	if _, err := s.db.Exec(
		`INSERT INTO sync_mutations (target_key, entity, entity_key, op, payload, source, project)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		DefaultSyncTargetKey, SyncEntityObservation, "obs-legacy", SyncOpUpsert, `{"sync_id":"obs-legacy","session_id":"legacy-session","project":"legacy-proj"}`, SyncSourceLocal, "legacy-proj",
	); err != nil {
		t.Fatalf("insert existing mutation: %v", err)
	}

	if err := s.EnrollProject("legacy-proj"); err != nil {
		t.Fatalf("first enroll: %v", err)
	}

	var afterFirst int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sync_mutations`).Scan(&afterFirst); err != nil {
		t.Fatalf("count after first enroll: %v", err)
	}
	if afterFirst != 3 {
		t.Fatalf("expected 3 total mutations after first enroll, got %d", afterFirst)
	}

	var observationMutations int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sync_mutations WHERE entity = ? AND entity_key = ?`, SyncEntityObservation, "obs-legacy").Scan(&observationMutations); err != nil {
		t.Fatalf("count observation mutations: %v", err)
	}
	if observationMutations != 1 {
		t.Fatalf("expected existing observation mutation to remain single, got %d rows", observationMutations)
	}

	if err := s.EnrollProject("legacy-proj"); err != nil {
		t.Fatalf("second enroll: %v", err)
	}

	var afterSecond int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sync_mutations`).Scan(&afterSecond); err != nil {
		t.Fatalf("count after second enroll: %v", err)
	}
	if afterSecond != afterFirst {
		t.Fatalf("expected no duplicate backfill on re-enroll, got %d mutations after second enroll vs %d after first", afterSecond, afterFirst)
	}
}

func TestNewRepairsAlreadyEnrolledProjectsMissingHistoricalSyncMutations(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "ohara.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}

	obsHash := hashNormalized("Historical content")
	_, err = db.Exec(`
		CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			project TEXT NOT NULL,
			directory TEXT NOT NULL,
			started_at TEXT NOT NULL DEFAULT (datetime('now')),
			ended_at TEXT,
			summary TEXT
		);
		CREATE TABLE observations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			sync_id TEXT,
			session_id TEXT NOT NULL,
			type TEXT NOT NULL,
			title TEXT NOT NULL,
			content TEXT NOT NULL,
			tool_name TEXT,
			project TEXT,
			scope TEXT NOT NULL DEFAULT 'project',
			topic_key TEXT,
			normalized_hash TEXT,
			revision_count INTEGER NOT NULL DEFAULT 1,
			duplicate_count INTEGER NOT NULL DEFAULT 1,
			last_seen_at TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			deleted_at TEXT,
			FOREIGN KEY (session_id) REFERENCES sessions(id)
		);
		CREATE TABLE user_prompts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			sync_id TEXT,
			session_id TEXT NOT NULL,
			content TEXT NOT NULL,
			project TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (session_id) REFERENCES sessions(id)
		);
		CREATE TABLE sync_state (
			target_key TEXT PRIMARY KEY,
			lifecycle TEXT NOT NULL DEFAULT 'idle',
			last_enqueued_seq INTEGER NOT NULL DEFAULT 0,
			last_acked_seq INTEGER NOT NULL DEFAULT 0,
			last_pulled_seq INTEGER NOT NULL DEFAULT 0,
			consecutive_failures INTEGER NOT NULL DEFAULT 0,
			backoff_until TEXT,
			lease_owner TEXT,
			lease_until TEXT,
			last_error TEXT,
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
		CREATE TABLE sync_mutations (
			seq INTEGER PRIMARY KEY AUTOINCREMENT,
			target_key TEXT NOT NULL,
			entity TEXT NOT NULL,
			entity_key TEXT NOT NULL,
			op TEXT NOT NULL,
			payload TEXT NOT NULL,
			source TEXT NOT NULL DEFAULT 'local',
			occurred_at TEXT NOT NULL DEFAULT (datetime('now')),
			acked_at TEXT,
			project TEXT NOT NULL DEFAULT '',
			FOREIGN KEY (target_key) REFERENCES sync_state(target_key)
		);
		CREATE TABLE sync_enrolled_projects (
			project TEXT PRIMARY KEY,
			enrolled_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
		INSERT INTO sessions (id, project, directory, summary) VALUES ('legacy-session', 'legacy-proj', '/tmp/legacy', 'done');
		INSERT INTO observations (sync_id, session_id, type, title, content, project, scope, normalized_hash, revision_count, duplicate_count, last_seen_at, updated_at)
		VALUES ('obs-legacy', 'legacy-session', 'decision', 'Legacy obs', 'Historical content', 'legacy-proj', 'project', ?, 1, 1, datetime('now'), datetime('now'));
		INSERT INTO user_prompts (sync_id, session_id, content, project) VALUES ('prompt-legacy', 'legacy-session', 'Historical prompt', 'legacy-proj');
		INSERT INTO sync_state (target_key, lifecycle, updated_at) VALUES (?, 'idle', datetime('now'));
		INSERT INTO sync_enrolled_projects (project) VALUES ('legacy-proj');
	`, obsHash, DefaultSyncTargetKey)
	if err != nil {
		_ = db.Close()
		t.Fatalf("seed legacy db: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	cfg := mustDefaultConfig(t)
	cfg.DataDir = dataDir

	s, err := New(cfg)
	if err != nil {
		t.Fatalf("new store after enrolled legacy state: %v", err)
	}

	mutations, err := s.ListPendingSyncMutations(DefaultSyncTargetKey, 10)
	if err != nil {
		_ = s.Close()
		t.Fatalf("list pending after repair: %v", err)
	}
	if len(mutations) != 3 {
		_ = s.Close()
		t.Fatalf("expected 3 repaired mutations, got %d", len(mutations))
	}

	state, err := s.GetSyncState(DefaultSyncTargetKey)
	if err != nil {
		_ = s.Close()
		t.Fatalf("get sync state after repair: %v", err)
	}
	if state.LastEnqueuedSeq != 3 {
		_ = s.Close()
		t.Fatalf("expected last_enqueued_seq 3 after automatic repair, got %d", state.LastEnqueuedSeq)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("close repaired store: %v", err)
	}

	s, err = New(cfg)
	if err != nil {
		t.Fatalf("reopen repaired store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sync_mutations`).Scan(&count); err != nil {
		t.Fatalf("count repaired mutations after reopen: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected repair to stay idempotent across reopen, got %d sync mutations", count)
	}
}

func TestEnrollProjectEmptyNameReturnsError(t *testing.T) {
	s := newTestStore(t)

	if err := s.EnrollProject(""); err == nil {
		t.Fatal("expected error when enrolling empty project name")
	}
}

func TestUnenrollProjectBasic(t *testing.T) {
	s := newTestStore(t)

	if err := s.EnrollProject("ohara"); err != nil {
		t.Fatalf("enroll: %v", err)
	}

	// Unenroll.
	if err := s.UnenrollProject("ohara"); err != nil {
		t.Fatalf("unenroll: %v", err)
	}

	// Should be gone.
	enrolled, err := s.IsProjectEnrolled("ohara")
	if err != nil {
		t.Fatalf("is enrolled after unenroll: %v", err)
	}
	if enrolled {
		t.Fatal("expected project to be unenrolled")
	}

	projects, err := s.ListEnrolledProjects()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(projects) != 0 {
		t.Fatalf("expected 0 enrolled projects after unenroll, got %d", len(projects))
	}
}

func TestUnenrollProjectIdempotent(t *testing.T) {
	s := newTestStore(t)

	// Unenroll a project that was never enrolled — should not error.
	if err := s.UnenrollProject("nonexistent"); err != nil {
		t.Fatalf("unenroll non-enrolled project should be idempotent: %v", err)
	}
}

func TestUnenrollProjectEmptyNameReturnsError(t *testing.T) {
	s := newTestStore(t)

	if err := s.UnenrollProject(""); err == nil {
		t.Fatal("expected error when unenrolling empty project name")
	}
}

func TestIsProjectEnrolledReturnsFalseForUnknown(t *testing.T) {
	s := newTestStore(t)

	enrolled, err := s.IsProjectEnrolled("unknown-project")
	if err != nil {
		t.Fatalf("is enrolled: %v", err)
	}
	if enrolled {
		t.Fatal("expected false for unknown project")
	}
}

func TestListEnrolledProjectsEmpty(t *testing.T) {
	s := newTestStore(t)

	projects, err := s.ListEnrolledProjects()
	if err != nil {
		t.Fatalf("list enrolled projects: %v", err)
	}
	if projects != nil {
		t.Fatalf("expected nil for empty list, got %v", projects)
	}
}

func TestListEnrolledProjectsAlphabeticalOrder(t *testing.T) {
	s := newTestStore(t)

	// Enroll in non-alphabetical order.
	for _, p := range []string{"zebra", "alpha", "mango"} {
		if err := s.EnrollProject(p); err != nil {
			t.Fatalf("enroll %q: %v", p, err)
		}
	}

	projects, err := s.ListEnrolledProjects()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(projects) != 3 {
		t.Fatalf("expected 3 projects, got %d", len(projects))
	}
	expected := []string{"alpha", "mango", "zebra"}
	for i, ep := range projects {
		if ep.Project != expected[i] {
			t.Fatalf("position %d: expected %q, got %q", i, expected[i], ep.Project)
		}
	}
}

func TestSyncMutationProjectColumnExists(t *testing.T) {
	s := newTestStore(t)

	// Verify the project column exists on sync_mutations by inserting a row.
	_, err := s.db.Exec(
		`INSERT INTO sync_mutations (target_key, entity, entity_key, op, payload, source, project)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		DefaultSyncTargetKey, "session", "test-key", SyncOpUpsert, `{"project":"myproj"}`, SyncSourceLocal, "myproj",
	)
	if err != nil {
		t.Fatalf("insert sync_mutation with project: %v", err)
	}

	// Read it back and verify project is populated.
	var project string
	if err := s.db.QueryRow(`SELECT project FROM sync_mutations WHERE entity_key = ?`, "test-key").Scan(&project); err != nil {
		t.Fatalf("scan project: %v", err)
	}
	if project != "myproj" {
		t.Fatalf("expected project 'myproj', got %q", project)
	}
}

func TestSyncMutationProjectBackfill(t *testing.T) {
	s := newTestStore(t)

	// Insert a mutation that simulates a pre-migration row (project is empty, but payload has it).
	// The backfill runs during schema init, so we test it by inserting directly then re-running.
	// Since the store already ran migrations, let's verify backfill logic by inserting a new row
	// with empty project and manually running the backfill.
	_, err := s.db.Exec(
		`INSERT INTO sync_mutations (target_key, entity, entity_key, op, payload, source, project)
		 VALUES (?, ?, ?, ?, ?, ?, '')`,
		DefaultSyncTargetKey, "observation", "backfill-key", SyncOpUpsert, `{"project":"backfilled"}`, SyncSourceLocal,
	)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Run the backfill manually.
	_, err = s.db.Exec(`
		UPDATE sync_mutations
		SET project = COALESCE(json_extract(payload, '$.project'), '')
		WHERE project = '' AND payload != ''
	`)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}

	var project string
	if err := s.db.QueryRow(`SELECT project FROM sync_mutations WHERE entity_key = ?`, "backfill-key").Scan(&project); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if project != "backfilled" {
		t.Fatalf("expected backfilled project 'backfilled', got %q", project)
	}
}

func TestListPendingSyncMutationsIncludesProject(t *testing.T) {
	s := newTestStore(t)

	// Enroll the project so mutations are visible in ListPendingSyncMutations.
	if err := s.EnrollProject("my-project"); err != nil {
		t.Fatalf("enroll: %v", err)
	}

	if err := s.CreateSession("proj-session", "my-project", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	_, err := s.AddObservation(AddObservationParams{
		SessionID: "proj-session",
		Type:      "decision",
		Title:     "Test obs",
		Content:   "Content",
		Project:   "my-project",
		Scope:     "project",
	})
	if err != nil {
		t.Fatalf("add observation: %v", err)
	}

	mutations, err := s.ListPendingSyncMutations(DefaultSyncTargetKey, 10)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}

	// There should be mutations (session create + observation create at minimum).
	if len(mutations) == 0 {
		t.Fatal("expected at least one pending mutation")
	}

	// Phase 3: Verify the Project field is populated at enqueue time.
	foundProject := false
	for _, m := range mutations {
		if m.Project == "my-project" {
			foundProject = true
			break
		}
	}
	if !foundProject {
		t.Fatal("expected at least one mutation with project='my-project'")
	}
}

// ─── Phase 3: extractProjectFromPayload ──────────────────────────────────────

func TestExtractProjectFromSessionPayload(t *testing.T) {
	p := syncSessionPayload{ID: "s1", Project: "acme"}
	got := extractProjectFromPayload(p)
	if got != "acme" {
		t.Fatalf("expected 'acme', got %q", got)
	}
}

func TestExtractProjectFromObservationPayload(t *testing.T) {
	proj := "obs-project"
	p := syncObservationPayload{SyncID: "obs-1", Project: &proj}
	got := extractProjectFromPayload(p)
	if got != "obs-project" {
		t.Fatalf("expected 'obs-project', got %q", got)
	}
}

func TestExtractProjectFromObservationPayloadNil(t *testing.T) {
	p := syncObservationPayload{SyncID: "obs-1", Project: nil}
	got := extractProjectFromPayload(p)
	if got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

func TestExtractProjectFromPromptPayload(t *testing.T) {
	proj := "prompt-project"
	p := syncPromptPayload{SyncID: "p1", Project: &proj}
	got := extractProjectFromPayload(p)
	if got != "prompt-project" {
		t.Fatalf("expected 'prompt-project', got %q", got)
	}
}

func TestExtractProjectFromPromptPayloadNil(t *testing.T) {
	p := syncPromptPayload{SyncID: "p1", Project: nil}
	got := extractProjectFromPayload(p)
	if got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

func TestExtractProjectFromUnknownPayloadFallback(t *testing.T) {
	// Unknown struct with a project field — uses JSON fallback.
	p := struct {
		Project string `json:"project"`
		Other   string `json:"other"`
	}{Project: "fallback-proj", Other: "x"}
	got := extractProjectFromPayload(p)
	if got != "fallback-proj" {
		t.Fatalf("expected 'fallback-proj', got %q", got)
	}
}

func TestExtractProjectFromPayloadWithoutProjectField(t *testing.T) {
	// Unknown struct without a project field — returns empty.
	p := struct {
		Name string `json:"name"`
	}{Name: "test"}
	got := extractProjectFromPayload(p)
	if got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

// ─── Phase 3: enqueueSyncMutationTx populates project column ────────────────

func TestEnqueueSyncMutationPopulatesProjectFromSessionPayload(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreateSession("enq-session", "enqueued-project", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// CreateSession enqueues a sync mutation internally. Check the project column.
	var project string
	err := s.db.QueryRow(
		`SELECT project FROM sync_mutations WHERE entity = ? AND entity_key = ?`,
		SyncEntitySession, "enq-session",
	).Scan(&project)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if project != "enqueued-project" {
		t.Fatalf("expected project='enqueued-project', got %q", project)
	}
}

func TestEnqueueSyncMutationPopulatesProjectFromObservationPayload(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreateSession("obs-enq", "obs-proj", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	_, err := s.AddObservation(AddObservationParams{
		SessionID: "obs-enq",
		Type:      "decision",
		Title:     "Test",
		Content:   "Content",
		Project:   "obs-proj",
	})
	if err != nil {
		t.Fatalf("add observation: %v", err)
	}

	// Check the observation mutation's project column.
	var project string
	err = s.db.QueryRow(
		`SELECT project FROM sync_mutations WHERE entity = ? ORDER BY seq DESC LIMIT 1`,
		SyncEntityObservation,
	).Scan(&project)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if project != "obs-proj" {
		t.Fatalf("expected project='obs-proj', got %q", project)
	}
}

func TestEnqueueSyncMutationPopulatesProjectFromPromptPayload(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreateSession("prompt-enq", "prompt-proj", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	_, err := s.AddPrompt(AddPromptParams{
		SessionID: "prompt-enq",
		Content:   "What did we do?",
		Project:   "prompt-proj",
	})
	if err != nil {
		t.Fatalf("add prompt: %v", err)
	}

	var project string
	err = s.db.QueryRow(
		`SELECT project FROM sync_mutations WHERE entity = ? ORDER BY seq DESC LIMIT 1`,
		SyncEntityPrompt,
	).Scan(&project)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if project != "prompt-proj" {
		t.Fatalf("expected project='prompt-proj', got %q", project)
	}
}

// ─── Phase 4: ListPendingSyncMutations enrollment filtering ──────────────────

func TestListPendingFiltersNonEnrolledProjects(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s-enrolled", "enrolled-proj", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := s.CreateSession("s-not-enrolled", "other-proj", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Enroll only "enrolled-proj".
	if err := s.EnrollProject("enrolled-proj"); err != nil {
		t.Fatalf("enroll: %v", err)
	}

	mutations, err := s.ListPendingSyncMutations(DefaultSyncTargetKey, 100)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}

	// Only enrolled-proj mutations should appear.
	for _, m := range mutations {
		if m.Project == "other-proj" {
			t.Fatalf("non-enrolled project 'other-proj' should not appear in pending mutations")
		}
	}

	foundEnrolled := false
	for _, m := range mutations {
		if m.Project == "enrolled-proj" {
			foundEnrolled = true
			break
		}
	}
	if !foundEnrolled {
		t.Fatal("expected enrolled-proj mutations to appear")
	}
}

func TestListPendingReturnsNoMutationsWhenNoneEnrolled(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s-no-enroll", "some-proj", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	mutations, err := s.ListPendingSyncMutations(DefaultSyncTargetKey, 100)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}

	// No projects enrolled → no mutations (all have project != '').
	if len(mutations) != 0 {
		t.Fatalf("expected 0 mutations when no projects enrolled, got %d", len(mutations))
	}
}

// ─── Phase 4: SkipAckNonEnrolledMutations ────────────────────────────────────

func TestSkipAckNonEnrolledMutationsBasic(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("skip-session", "skip-proj", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Do NOT enroll "skip-proj" → mutations should be skip-acked.
	skipped, err := s.SkipAckNonEnrolledMutations(DefaultSyncTargetKey)
	if err != nil {
		t.Fatalf("skip-ack: %v", err)
	}
	if skipped == 0 {
		t.Fatal("expected at least one mutation to be skip-acked")
	}

	// After skip-ack, there should be no pending mutations left.
	mutations, err := s.ListPendingSyncMutations(DefaultSyncTargetKey, 100)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(mutations) != 0 {
		t.Fatalf("expected 0 pending mutations after skip-ack, got %d", len(mutations))
	}
}

func TestSkipAckPreservesEnrolledProjectMutations(t *testing.T) {
	s := newTestStore(t)

	if err := s.EnrollProject("enrolled"); err != nil {
		t.Fatalf("enroll: %v", err)
	}

	if err := s.CreateSession("s-enrolled", "enrolled", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := s.CreateSession("s-not-enrolled", "not-enrolled", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Count total pending before skip-ack.
	var totalBefore int
	s.db.QueryRow(`SELECT COUNT(*) FROM sync_mutations WHERE acked_at IS NULL`).Scan(&totalBefore)

	skipped, err := s.SkipAckNonEnrolledMutations(DefaultSyncTargetKey)
	if err != nil {
		t.Fatalf("skip-ack: %v", err)
	}
	if skipped == 0 {
		t.Fatal("expected at least one mutation to be skip-acked for 'not-enrolled'")
	}

	// Remaining pending should be only "enrolled" mutations.
	mutations, err := s.ListPendingSyncMutations(DefaultSyncTargetKey, 100)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	for _, m := range mutations {
		if m.Project == "not-enrolled" {
			t.Fatal("skip-acked mutation still appears as pending")
		}
	}
	if len(mutations) == 0 {
		t.Fatal("expected enrolled-project mutations to remain")
	}
}

// ─── Phase 5: Empty/global project always syncs ──────────────────────────────

func TestEmptyProjectMutationsAlwaysSync(t *testing.T) {
	s := newTestStore(t)

	// Create a session with empty project (global).
	if err := s.CreateSession("global-session", "", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// No projects enrolled, but empty-project mutations should still appear.
	mutations, err := s.ListPendingSyncMutations(DefaultSyncTargetKey, 100)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}

	if len(mutations) == 0 {
		t.Fatal("expected empty-project mutations to always sync regardless of enrollment")
	}

	// Verify they have project = ''.
	for _, m := range mutations {
		if m.Project != "" {
			t.Fatalf("expected empty project, got %q", m.Project)
		}
	}
}

func TestSkipAckDoesNotAffectEmptyProjectMutations(t *testing.T) {
	s := newTestStore(t)

	// Create a session with empty project (global).
	if err := s.CreateSession("global-session-2", "", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Count pending before skip-ack.
	beforeMutations, err := s.ListPendingSyncMutations(DefaultSyncTargetKey, 100)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	beforeCount := len(beforeMutations)

	// Skip-ack should not affect empty-project mutations.
	skipped, err := s.SkipAckNonEnrolledMutations(DefaultSyncTargetKey)
	if err != nil {
		t.Fatalf("skip-ack: %v", err)
	}
	if skipped != 0 {
		t.Fatalf("expected 0 mutations to be skip-acked (all empty project), got %d", skipped)
	}

	// Verify count unchanged.
	afterMutations, err := s.ListPendingSyncMutations(DefaultSyncTargetKey, 100)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(afterMutations) != beforeCount {
		t.Fatalf("expected %d mutations after skip-ack, got %d", beforeCount, len(afterMutations))
	}
}

func TestMixedEnrolledAndEmptyProjectMutations(t *testing.T) {
	s := newTestStore(t)

	if err := s.EnrollProject("enrolled-mix"); err != nil {
		t.Fatalf("enroll: %v", err)
	}

	// Create sessions with different project states.
	if err := s.CreateSession("mix-enrolled", "enrolled-mix", "/tmp"); err != nil {
		t.Fatalf("create enrolled session: %v", err)
	}
	if err := s.CreateSession("mix-global", "", "/tmp"); err != nil {
		t.Fatalf("create global session: %v", err)
	}
	if err := s.CreateSession("mix-unenrolled", "unenrolled-mix", "/tmp"); err != nil {
		t.Fatalf("create unenrolled session: %v", err)
	}

	mutations, err := s.ListPendingSyncMutations(DefaultSyncTargetKey, 100)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}

	// Should have enrolled-mix and empty-project mutations, but NOT unenrolled-mix.
	var hasEnrolled, hasGlobal bool
	for _, m := range mutations {
		if m.Project == "unenrolled-mix" {
			t.Fatal("unenrolled project mutations should not appear")
		}
		if m.Project == "enrolled-mix" {
			hasEnrolled = true
		}
		if m.Project == "" {
			hasGlobal = true
		}
	}
	if !hasEnrolled {
		t.Fatal("expected enrolled-mix mutations to appear")
	}
	if !hasGlobal {
		t.Fatal("expected empty-project (global) mutations to appear")
	}
}

// ─── MigrateProject ─────────────────────────────────────────────────────────

func TestMigrateProject(t *testing.T) {
	s := newTestStore(t)
	old, new_ := "old-name", "new-name"

	// Seed data under old project name
	s.CreateSession("s1", old, "/tmp/old")
	s.AddObservation(AddObservationParams{
		SessionID: "s1", Type: "decision", Title: "test obs",
		Content: "some content", Project: old, Scope: "project",
	})
	s.AddPrompt(AddPromptParams{SessionID: "s1", Content: "test prompt", Project: old})

	// Run migration
	result, err := s.MigrateProject(old, new_)
	if err != nil {
		t.Fatalf("MigrateProject: %v", err)
	}
	if !result.Migrated {
		t.Fatal("expected migration to happen")
	}
	if result.ObservationsUpdated != 1 {
		t.Fatalf("expected 1 observation migrated, got %d", result.ObservationsUpdated)
	}
	if result.SessionsUpdated != 1 {
		t.Fatalf("expected 1 session migrated, got %d", result.SessionsUpdated)
	}
	if result.PromptsUpdated != 1 {
		t.Fatalf("expected 1 prompt migrated, got %d", result.PromptsUpdated)
	}

	// Verify old project has no records
	obs, _ := s.RecentObservations(old, "", 10)
	if len(obs) != 0 {
		t.Fatalf("expected 0 observations under old name, got %d", len(obs))
	}

	// Verify new project has the records
	obs, _ = s.RecentObservations(new_, "", 10)
	if len(obs) != 1 {
		t.Fatalf("expected 1 observation under new name, got %d", len(obs))
	}

	// Verify FTS search finds it under new project
	results, _ := s.Search("test obs", SearchOptions{Project: new_, Limit: 10})
	if len(results) != 1 {
		t.Fatalf("expected FTS to find 1 result under new project, got %d", len(results))
	}
}

func TestMigrateProjectNoOp(t *testing.T) {
	s := newTestStore(t)

	// No records under "nonexistent" — should be a no-op
	result, err := s.MigrateProject("nonexistent", "anything")
	if err != nil {
		t.Fatalf("MigrateProject: %v", err)
	}
	if result.Migrated {
		t.Fatal("expected no migration for nonexistent project")
	}
}

func TestMigrateProjectIdempotent(t *testing.T) {
	s := newTestStore(t)
	old, new_ := "old-proj", "new-proj"

	s.CreateSession("s1", old, "/tmp")
	s.AddObservation(AddObservationParams{
		SessionID: "s1", Type: "decision", Title: "test",
		Content: "content", Project: old, Scope: "project",
	})

	// First migration
	r1, err := s.MigrateProject(old, new_)
	if err != nil {
		t.Fatalf("first MigrateProject: %v", err)
	}
	if !r1.Migrated {
		t.Fatal("first migration should migrate")
	}

	// Second migration — no records under old name anymore
	r2, err := s.MigrateProject(old, new_)
	if err != nil {
		t.Fatalf("second MigrateProject: %v", err)
	}
	if r2.Migrated {
		t.Fatal("second migration should be a no-op")
	}
}

// ─── Phase 2: project-name-drift — NormalizeProject, ListProjectNames,
//              ListProjectsWithStats, MergeProjects tests ─────────────────────

func TestNormalizeProjectFunction(t *testing.T) {
	tests := []struct {
		input       string
		wantName    string
		wantWarning bool
	}{
		{"ohara", "ohara", false},
		{"Ohara", "ohara", true},
		{"OHARA", "ohara", true},
		{"  ohara  ", "ohara", true},
		{"Ohara-Memory", "ohara-memory", true},
		{"ohara--memory", "ohara-memory", true},
		{"ohara__memory", "ohara_memory", true},
		{"", "", false},
		{"already-lower", "already-lower", false},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, warning := NormalizeProject(tc.input)
			if got != tc.wantName {
				t.Errorf("NormalizeProject(%q) name = %q, want %q", tc.input, got, tc.wantName)
			}
			if tc.wantWarning && warning == "" {
				t.Errorf("NormalizeProject(%q) expected a warning, got empty string", tc.input)
			}
			if !tc.wantWarning && warning != "" {
				t.Errorf("NormalizeProject(%q) expected no warning, got %q", tc.input, warning)
			}
		})
	}
}

func TestAddObservationNormalizesProject(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s1", "ohara", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Save with mixed-case project name
	id, err := s.AddObservation(AddObservationParams{
		SessionID: "s1",
		Type:      "decision",
		Title:     "Normalize test",
		Content:   "This should be stored under lowercase project",
		Project:   "Ohara",
		Scope:     "project",
	})
	if err != nil {
		t.Fatalf("AddObservation: %v", err)
	}

	obs, err := s.GetObservation(id)
	if err != nil {
		t.Fatalf("GetObservation: %v", err)
	}

	// Stored project should be normalized to lowercase
	if obs.Project == nil || *obs.Project != "ohara" {
		got := "<nil>"
		if obs.Project != nil {
			got = *obs.Project
		}
		t.Errorf("stored project = %q, want \"ohara\"", got)
	}
}

func TestSearchNormalizesProjectFilter(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s1", "ohara", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Store observation under already-lowercase project
	_, err := s.AddObservation(AddObservationParams{
		SessionID: "s1",
		Type:      "decision",
		Title:     "Search normalize test",
		Content:   "content for project filter normalization",
		Project:   "ohara",
		Scope:     "project",
	})
	if err != nil {
		t.Fatalf("AddObservation: %v", err)
	}

	// Search with UPPERCASE project filter — should still find the record
	results, err := s.Search("normalize test", SearchOptions{
		Project: "Ohara", // intentionally mixed-case
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(results) == 0 {
		t.Fatalf("expected ≥1 result when searching with normalized project filter, got 0")
	}
}

func TestRecentObservationsNormalizesProjectFilter(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s1", "ohara", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	_, err := s.AddObservation(AddObservationParams{
		SessionID: "s1",
		Type:      "decision",
		Title:     "Recent obs test",
		Content:   "some content",
		Project:   "ohara",
		Scope:     "project",
	})
	if err != nil {
		t.Fatalf("AddObservation: %v", err)
	}

	// Query with uppercase project name
	obs, err := s.RecentObservations("OHARA", "", 10)
	if err != nil {
		t.Fatalf("RecentObservations: %v", err)
	}
	if len(obs) == 0 {
		t.Fatalf("expected ≥1 result with normalized project filter, got 0")
	}
}

func TestCreateSessionNormalizesProject(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s-norm", "MyProject", "/tmp"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	sess, err := s.GetSession("s-norm")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.Project != "myproject" {
		t.Errorf("expected project=myproject (normalized), got %q", sess.Project)
	}
}

func TestListProjectNames(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s1", "alpha", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := s.CreateSession("s2", "beta", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	for _, proj := range []string{"alpha", "alpha", "beta", "gamma"} {
		_, err := s.AddObservation(AddObservationParams{
			SessionID: "s1",
			Type:      "decision",
			Title:     "test " + proj,
			Content:   "content for " + proj,
			Project:   proj,
			Scope:     "project",
		})
		if err != nil {
			t.Fatalf("AddObservation: %v", err)
		}
	}

	names, err := s.ListProjectNames()
	if err != nil {
		t.Fatalf("ListProjectNames: %v", err)
	}

	// Should return distinct names: alpha, beta, gamma
	want := map[string]bool{"alpha": true, "beta": true, "gamma": true}
	for _, n := range names {
		if !want[n] {
			t.Errorf("unexpected project name %q in results", n)
		}
		delete(want, n)
	}
	if len(want) > 0 {
		t.Errorf("missing project names: %v", want)
	}
}

func TestListProjectsWithStats(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s1", "proj-a", "/work/a"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := s.CreateSession("s2", "proj-b", "/work/b"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Add 3 observations to proj-a
	for i := 0; i < 3; i++ {
		_, err := s.AddObservation(AddObservationParams{
			SessionID: "s1",
			Type:      "decision",
			Title:     "obs a",
			Content:   strings.Repeat("x", i+1), // unique content per obs
			Project:   "proj-a",
			Scope:     "project",
		})
		if err != nil {
			t.Fatalf("AddObservation proj-a: %v", err)
		}
	}

	// Add 1 observation to proj-b
	_, err := s.AddObservation(AddObservationParams{
		SessionID: "s2",
		Type:      "decision",
		Title:     "obs b",
		Content:   "content for proj-b",
		Project:   "proj-b",
		Scope:     "project",
	})
	if err != nil {
		t.Fatalf("AddObservation proj-b: %v", err)
	}

	stats, err := s.ListProjectsWithStats()
	if err != nil {
		t.Fatalf("ListProjectsWithStats: %v", err)
	}

	if len(stats) < 2 {
		t.Fatalf("expected ≥2 project stats, got %d", len(stats))
	}

	// Find proj-a and proj-b in results
	statsMap := make(map[string]ProjectStats)
	for _, ps := range stats {
		statsMap[ps.Name] = ps
	}

	if a, ok := statsMap["proj-a"]; !ok {
		t.Error("proj-a not in ListProjectsWithStats results")
	} else {
		if a.ObservationCount != 3 {
			t.Errorf("proj-a: expected 3 observations, got %d", a.ObservationCount)
		}
		if a.SessionCount != 1 {
			t.Errorf("proj-a: expected 1 session, got %d", a.SessionCount)
		}
	}

	if b, ok := statsMap["proj-b"]; !ok {
		t.Error("proj-b not in ListProjectsWithStats results")
	} else {
		if b.ObservationCount != 1 {
			t.Errorf("proj-b: expected 1 observation, got %d", b.ObservationCount)
		}
	}

	// Results should be sorted by observation count descending
	if stats[0].Name != "proj-a" {
		t.Errorf("expected proj-a first (most observations), got %q", stats[0].Name)
	}
}

func TestMergeProjects(t *testing.T) {
	s := newTestStore(t)

	// Set up three source projects
	sources := []string{"ohara", "Ohara", "ohara-memory"}
	canonical := "ohara"

	if err := s.CreateSession("s1", "ohara", "/work"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Add observations to each source
	for _, src := range []string{"ohara", "ohara-memory"} {
		for i := 0; i < 2; i++ {
			_, err := s.AddObservation(AddObservationParams{
				SessionID: "s1",
				Type:      "decision",
				Title:     "obs from " + src,
				Content:   strings.Repeat(src, i+1),
				Project:   src,
				Scope:     "project",
			})
			if err != nil {
				t.Fatalf("AddObservation %s: %v", src, err)
			}
		}
	}

	result, err := s.MergeProjects(sources, canonical)
	if err != nil {
		t.Fatalf("MergeProjects: %v", err)
	}

	if result.Canonical != "ohara" {
		t.Errorf("canonical = %q, want \"ohara\"", result.Canonical)
	}

	// "Ohara" normalizes to "ohara" (same as canonical) → skipped
	// "ohara-memory" is different → merged
	// Only "ohara-memory" should appear in SourcesMerged (and possibly "ohara" if it had records,
	// but it equals canonical after normalization → skipped)
	for _, merged := range result.SourcesMerged {
		if merged == "ohara" {
			t.Error("canonical 'ohara' should not appear in SourcesMerged")
		}
	}

	// All records from ohara-memory should now be under "ohara"
	obs, err := s.RecentObservations("ohara", "", 20)
	if err != nil {
		t.Fatalf("RecentObservations: %v", err)
	}
	if len(obs) < 4 {
		t.Errorf("expected ≥4 observations under 'ohara' after merge, got %d", len(obs))
	}

	// ohara-memory should have 0 observations
	obsMerged, err := s.RecentObservations("ohara-memory", "", 10)
	if err != nil {
		t.Fatalf("RecentObservations ohara-memory: %v", err)
	}
	if len(obsMerged) != 0 {
		t.Errorf("expected 0 observations under 'ohara-memory' after merge, got %d", len(obsMerged))
	}
}

func TestMergeProjectsIdempotent(t *testing.T) {
	s := newTestStore(t)

	// Merge a nonexistent source — should not error
	result, err := s.MergeProjects([]string{"ghost-project"}, "ohara")
	if err != nil {
		t.Fatalf("MergeProjects with nonexistent source: %v", err)
	}
	if result.ObservationsUpdated != 0 {
		t.Errorf("expected 0 observations updated for nonexistent source, got %d", result.ObservationsUpdated)
	}
}

func TestMergeProjectsCanonicalInSources(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s1", "ohara", "/work"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Put some obs under "ohara"
	_, err := s.AddObservation(AddObservationParams{
		SessionID: "s1",
		Type:      "decision",
		Title:     "existing",
		Content:   "existing observation",
		Project:   "ohara",
		Scope:     "project",
	})
	if err != nil {
		t.Fatalf("AddObservation: %v", err)
	}

	// Sources include the canonical itself — should be silently skipped
	result, err := s.MergeProjects([]string{"ohara", "Ohara"}, "ohara")
	if err != nil {
		t.Fatalf("MergeProjects: %v", err)
	}

	// Nothing should have been changed (ohara and Ohara both normalize to "ohara" = canonical)
	if result.ObservationsUpdated != 0 {
		t.Errorf("expected 0 observations updated when sources equal canonical, got %d", result.ObservationsUpdated)
	}
	if len(result.SourcesMerged) != 0 {
		t.Errorf("expected empty SourcesMerged when all sources equal canonical, got %v", result.SourcesMerged)
	}
}

func TestCountObservationsForProject(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s1", "alpha", "/work/alpha"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// No observations yet — count should be 0
	count, err := s.CountObservationsForProject("alpha")
	if err != nil {
		t.Fatalf("CountObservationsForProject: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0, got %d", count)
	}

	// Add two observations
	for i := 0; i < 2; i++ {
		if _, err := s.AddObservation(AddObservationParams{
			SessionID: "s1",
			Type:      "decision",
			Title:     "obs " + string(rune('A'+i)),
			Content:   "unique content that is definitely unique " + string(rune('A'+i)),
			Project:   "alpha",
			Scope:     "project",
		}); err != nil {
			t.Fatalf("AddObservation: %v", err)
		}
	}

	count, err = s.CountObservationsForProject("alpha")
	if err != nil {
		t.Fatalf("CountObservationsForProject: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2, got %d", count)
	}

	// Different project should return 0
	count, err = s.CountObservationsForProject("beta")
	if err != nil {
		t.Fatalf("CountObservationsForProject for beta: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 for beta, got %d", count)
	}
}

// ─── DeleteSession tests ─────────────────────────────────────────────────────

func TestDeleteSession_EmptySession(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("sess-empty", "proj", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	if err := s.DeleteSession("sess-empty"); err != nil {
		t.Fatalf("expected no error deleting empty session, got: %v", err)
	}

	// Session should be gone.
	sessions, err := s.RecentSessions("proj", 10)
	if err != nil {
		t.Fatalf("recent sessions: %v", err)
	}
	for _, ss := range sessions {
		if ss.ID == "sess-empty" {
			t.Fatal("expected session to be deleted but it still exists")
		}
	}
}

func TestDeleteSession_NotFound(t *testing.T) {
	s := newTestStore(t)

	err := s.DeleteSession("does-not-exist")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound, got: %v", err)
	}
}

func TestDeleteSession_HasActiveObservations(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("sess-has-obs", "proj", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := s.AddObservation(AddObservationParams{
		SessionID: "sess-has-obs",
		Type:      "decision",
		Title:     "some decision",
		Content:   "content",
		Project:   "proj",
		Scope:     "project",
	}); err != nil {
		t.Fatalf("add observation: %v", err)
	}

	err := s.DeleteSession("sess-has-obs")
	if !errors.Is(err, ErrSessionHasObservations) {
		t.Fatalf("expected ErrSessionHasObservations, got: %v", err)
	}
}

func TestDeleteSession_HasSoftDeletedObservations(t *testing.T) {
	// Even soft-deleted observations must block the session delete
	// to avoid FK constraint violations.
	s := newTestStore(t)

	if err := s.CreateSession("sess-soft", "proj", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	obsID, err := s.AddObservation(AddObservationParams{
		SessionID: "sess-soft",
		Type:      "decision",
		Title:     "soft deleted obs",
		Content:   "content",
		Project:   "proj",
		Scope:     "project",
	})
	if err != nil {
		t.Fatalf("add observation: %v", err)
	}
	if err := s.DeleteObservation(obsID, false); err != nil {
		t.Fatalf("soft delete observation: %v", err)
	}

	err = s.DeleteSession("sess-soft")
	if !errors.Is(err, ErrSessionHasObservations) {
		t.Fatalf("expected ErrSessionHasObservations for soft-deleted obs, got: %v", err)
	}
}

func TestDeleteSession_DeletesPromptsAlso(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("sess-with-prompts", "proj", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := s.AddPrompt(AddPromptParams{
		SessionID: "sess-with-prompts",
		Content:   "a prompt",
		Project:   "proj",
	}); err != nil {
		t.Fatalf("add prompt: %v", err)
	}

	if err := s.DeleteSession("sess-with-prompts"); err != nil {
		t.Fatalf("delete session: %v", err)
	}

	prompts, err := s.RecentPrompts("proj", 10)
	if err != nil {
		t.Fatalf("recent prompts: %v", err)
	}
	if len(prompts) != 0 {
		t.Fatalf("expected prompts to be deleted with session, got %d", len(prompts))
	}
}

func TestDeleteSession_FKConstraintFallback(t *testing.T) {
	// Verify that a SQLite FK constraint error on the DELETE FROM sessions
	// statement is translated into ErrSessionHasObservations.
	//
	// SQLite is a single-writer database, so it is not possible to inject an
	// observation from a concurrent connection while the transaction already
	// holds the write lock. Instead we simulate the race by:
	//   1. Pre-inserting an observation directly (bypassing store logic).
	//   2. Mocking the queryIt hook so the COUNT query returns 0 (as if the
	//      observation arrived after the count).
	//   3. Letting DeleteSession proceed; the DELETE FROM sessions then fails
	//      with a real SQLite FK constraint error (SQLITE_CONSTRAINT_FOREIGNKEY).
	s := newTestStore(t)

	if err := s.CreateSession("sess-race", "proj", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Insert the observation directly, bypassing the store COUNT guard.
	if _, err := s.db.Exec(`
		INSERT INTO observations
			(session_id, type, title, content, project, scope, created_at, updated_at, sync_id, duplicate_count, revision_count)
		VALUES
			('sess-race', 'decision', 'race obs', 'content', 'proj', 'project',
			 datetime('now'), datetime('now'), 'sync-race-1', 1, 1)`); err != nil {
		t.Fatalf("pre-insert observation: %v", err)
	}

	// Mock queryIt so the COUNT returns 0, simulating the race window where the
	// observation did not exist when the count ran.
	origQueryIt := s.hooks.queryIt
	faked := false
	s.hooks.queryIt = func(db queryer, query string, args ...any) (rowScanner, error) {
		if !faked && strings.Contains(query, "COUNT(*)") && strings.Contains(query, "observations WHERE session_id") {
			faked = true
			// Return a scanner that always yields count = 0.
			return &fakeCountScanner{}, nil
		}
		return origQueryIt(db, query, args...)
	}
	defer func() { s.hooks = defaultStoreHooks() }()

	err := s.DeleteSession("sess-race")
	if !errors.Is(err, ErrSessionHasObservations) {
		t.Fatalf("expected ErrSessionHasObservations from FK constraint, got: %v", err)
	}
}

// fakeCountScanner is a rowScanner that yields a single row with value 0,
// used to simulate a COUNT(*) result of zero.
type fakeCountScanner struct {
	done bool
}

func (f *fakeCountScanner) Next() bool {
	if f.done {
		return false
	}
	f.done = true
	return true
}
func (f *fakeCountScanner) Scan(dest ...any) error {
	if len(dest) > 0 {
		if p, ok := dest[0].(*int); ok {
			*p = 0
		}
	}
	return nil
}
func (f *fakeCountScanner) Err() error   { return nil }
func (f *fakeCountScanner) Close() error { return nil }

// ─── DeletePrompt tests ──────────────────────────────────────────────────────

func TestDeletePrompt_Success(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("sess-p", "proj", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	id, err := s.AddPrompt(AddPromptParams{
		SessionID: "sess-p",
		Content:   "delete me",
		Project:   "proj",
	})
	if err != nil {
		t.Fatalf("add prompt: %v", err)
	}

	if err := s.DeletePrompt(id); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	prompts, err := s.RecentPrompts("proj", 10)
	if err != nil {
		t.Fatalf("recent prompts: %v", err)
	}
	if len(prompts) != 0 {
		t.Fatalf("expected prompt to be deleted, got %d", len(prompts))
	}
}

func TestDeletePrompt_NotFound(t *testing.T) {
	s := newTestStore(t)

	err := s.DeletePrompt(999999)
	if !errors.Is(err, ErrPromptNotFound) {
		t.Fatalf("expected ErrPromptNotFound, got: %v", err)
	}
}

// ─── DetectConflict tests ─────────────────────────────────────────────────────

func TestDetectConflict_SkipsNonCheckingKinds(t *testing.T) {
	s := newTestStore(t)

	for _, kind := range []string{MemoryKindBugfix, MemoryKindDiscovery, MemoryKindProcedure, MemoryKindPostmortem, MemoryKindIdentity, MemoryKindUserPreference, MemoryKindGlossary} {
		ci, err := s.DetectConflict(AddMemoryParams{
			ProjectID: "ohara",
			Kind:      kind,
			Title:     "Some title that would conflict",
			Body:      "Body content",
		})
		if err != nil {
			t.Fatalf("DetectConflict for kind %q: %v", kind, err)
		}
		if ci != nil {
			t.Fatalf("kind %q: expected nil conflict, got %+v", kind, ci)
		}
	}
}

func TestDetectConflict_EmptyProjectReturnsNil(t *testing.T) {
	s := newTestStore(t)

	ci, err := s.DetectConflict(AddMemoryParams{
		ProjectID: "",
		Kind:      MemoryKindDecision,
		Title:     "Auth decision",
		Body:      "Use JWT for auth",
	})
	if err != nil {
		t.Fatalf("DetectConflict: %v", err)
	}
	if ci != nil {
		t.Fatalf("empty project: expected nil conflict, got %+v", ci)
	}
}

func TestDetectConflict_EmptyTitleReturnsNil(t *testing.T) {
	s := newTestStore(t)

	_, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Auth decision",
		Body:      "Use JWT for auth",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	ci, err := s.DetectConflict(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "", // empty title
		Body:      "Different body",
	})
	if err != nil {
		t.Fatalf("DetectConflict: %v", err)
	}
	if ci != nil {
		t.Fatalf("empty title: expected nil conflict, got %+v", ci)
	}
}

func TestDetectConflict_NoExistingMemoriesReturnsNil(t *testing.T) {
	s := newTestStore(t)

	ci, err := s.DetectConflict(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "New auth decision",
		Body:      "Use OAuth2",
	})
	if err != nil {
		t.Fatalf("DetectConflict: %v", err)
	}
	if ci != nil {
		t.Fatalf("no existing memories: expected nil conflict, got %+v", ci)
	}
}

func TestDetectConflict_DissimilarTitlesReturnsNil(t *testing.T) {
	s := newTestStore(t)

	_, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Use JWT for authentication",
		Body:      "JWT-based auth is simpler to implement",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	// Very different title — should not conflict
	ci, err := s.DetectConflict(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Deploy with Docker compose",
		Body:      "Use docker-compose for local dev",
	})
	if err != nil {
		t.Fatalf("DetectConflict: %v", err)
	}
	if ci != nil {
		t.Fatalf("dissimilar titles: expected nil conflict, got %+v", ci)
	}
}

func TestDetectConflict_SimilarTitlesDetectsConflict(t *testing.T) {
	s := newTestStore(t)

	_, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Auth decision: Use JWT for session management",
		Body:      "JWT is stateless and scales well",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	// Very similar title — should detect conflict
	ci, err := s.DetectConflict(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Auth decision: JWT for session management",
		Body:      "Alternative: use OAuth2",
	})
	if err != nil {
		t.Fatalf("DetectConflict: %v", err)
	}
	if ci == nil {
		t.Fatal("similar titles: expected conflict, got nil")
	}
	if ci.ConflictType != "title_overlap" {
		t.Errorf("expected conflict type title_overlap, got %q", ci.ConflictType)
	}
	if ci.OverlapScore <= 0.6 {
		t.Errorf("expected overlap score > 0.6, got %f", ci.OverlapScore)
	}
	if ci.ExistingMemory == nil {
		t.Error("expected existing memory in conflict info")
	}
}

func TestDetectConflict_PicksHighestScore(t *testing.T) {
	s := newTestStore(t)

	// Add two memories: one weakly overlapping, one strongly overlapping
	_, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindPattern,
		Title:     "Error handling pattern for API calls",
		Body:      "Return errors immediately",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	_, err = s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindPattern,
		Title:     "API call error handling with retry",
		Body:      "Retry failed API calls up to 3 times",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	// This should match the second memory (API call error handling) more strongly
	ci, err := s.DetectConflict(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindPattern,
		Title:     "API call error handling and retry logic",
		Body:      "Consider exponential backoff",
	})
	if err != nil {
		t.Fatalf("DetectConflict: %v", err)
	}
	if ci == nil {
		t.Fatal("expected conflict with best match")
	}
	// Should match "API call error handling with retry" best
	if !strings.Contains(ci.ExistingMemory.Title, "retry") {
		t.Errorf("expected best match to be the retry memory, got %q", ci.ExistingMemory.Title)
	}
}

func TestDetectConflict_DifferentKindNoConflict(t *testing.T) {
	s := newTestStore(t)

	_, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Use JWT for authentication",
		Body:      "JWT-based auth",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	// Same title but different kind — no conflict detection for non-checking kinds
	ci, err := s.DetectConflict(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindBugfix,
		Title:     "Use JWT for authentication",
		Body:      "Fix the JWT implementation",
	})
	if err != nil {
		t.Fatalf("DetectConflict: %v", err)
	}
	if ci != nil {
		t.Fatalf("different kind: expected nil conflict, got %+v", ci)
	}
}

func TestDetectConflict_ConfigKindDetected(t *testing.T) {
	s := newTestStore(t)

	_, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindConfig,
		Title:     "Database config: use PostgreSQL for production",
		Body:      "PostgreSQL for production",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	ci, err := s.DetectConflict(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindConfig,
		Title:     "Database config: PostgreSQL for production settings",
		Body:      "Connection pool size 10",
	})
	if err != nil {
		t.Fatalf("DetectConflict: %v", err)
	}
	if ci == nil {
		t.Fatal("expected conflict for config kind with similar title")
	}
	if ci.ExistingMemory.Kind != MemoryKindConfig {
		t.Errorf("expected existing memory kind config, got %q", ci.ExistingMemory.Kind)
	}
}

// ─── significantWords and keywordOverlap tests ─────────────────────────────────

func TestSignificantWords_FiltersStopwordsAndShortWords(t *testing.T) {
	words := significantWords("The and a use JWT for authentication in production systems")
	// stopwords: the, and, a, for, in
	// short words (<3): use, may be kept (JWT is 3 chars)
	wordMap := make(map[string]bool)
	for _, w := range words {
		wordMap[w] = true
	}
	// Should contain long non-stopwords
	if !wordMap["authentication"] {
		t.Error("expected 'authentication' in significant words")
	}
	if !wordMap["production"] {
		t.Error("expected 'production' in significant words")
	}
	if !wordMap["systems"] {
		t.Error("expected 'systems' in significant words")
	}
	// Should NOT contain short words or stopwords
	if wordMap["the"] || wordMap["and"] || wordMap["for"] {
		t.Error("stopwords should be filtered out")
	}
}

func TestKeywordOverlap_JaccardSimilarity(t *testing.T) {
	// Identical sets
	a := []string{"auth", "jwt", "token"}
	b := []string{"auth", "jwt", "token"}
	score := keywordOverlap(a, b)
	if score != 1.0 {
		t.Errorf("identical sets: expected 1.0, got %f", score)
	}

	// Disjoint sets
	c := []string{"auth", "jwt"}
	d := []string{"database", "postgres"}
	score = keywordOverlap(c, d)
	if score != 0.0 {
		t.Errorf("disjoint sets: expected 0.0, got %f", score)
	}

	// Partial overlap
	e := []string{"auth", "jwt", "token", "session"}
	f := []string{"auth", "jwt", "database"}
	score = keywordOverlap(e, f)
	// intersection = 2, union = 6, score = 2/6 = 0.333
	if score < 0.3 || score > 0.4 {
		t.Errorf("partial overlap: expected ~0.333, got %f", score)
	}
}

// ─── SearchMemories ranking tests ──────────────────────────────────────────────

func TestSearchMemories_DefaultLimit(t *testing.T) {
	s := newTestStore(t)

	// No memories — should return empty slice (not error)
	results, err := s.SearchMemories("auth", "", "", "", "", MemoryStatusActive, 0, "")
	if err != nil {
		t.Fatalf("SearchMemories with zero limit: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty results with no matches, got %d", len(results))
	}
}

func TestSearchMemories_FindsMatchingItems(t *testing.T) {
	s := newTestStore(t)

	_, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Auth decision: use JWT for tokens",
		Body:      "JWT is stateless",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	results, err := s.SearchMemories("JWT", "ohara", "", "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Kind != MemoryKindDecision {
		t.Errorf("expected decision kind, got %s", results[0].Kind)
	}
}

func TestSearchMemories_KindBoost_DecisionsRankHigher(t *testing.T) {
	s := newTestStore(t)

	// Add both with equally matching titles so kind boost is the tiebreaker.
	// Both contain "JWT authentication" so FTS relevance is similar.
	_, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindProcedure,
		Title:     "JWT authentication in the auth service",
		Body:      "Use middleware to validate tokens",
	})
	if err != nil {
		t.Fatalf("AddMemory procedure: %v", err)
	}

	_, err = s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "JWT authentication decision",
		Body:      "We chose JWT for stateless auth",
	})
	if err != nil {
		t.Fatalf("AddMemory decision: %v", err)
	}

	results, err := s.SearchMemories("JWT authentication", "ohara", "", "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}
	// Decision should be first due to kind boost (1.5x vs 1.1x)
	if results[0].Kind != MemoryKindDecision {
		t.Errorf("expected first result to be decision (kind boost 1.5), got %s", results[0].Kind)
	}
}

func TestSearchMemories_KindBoost_BugfixRanksHigherThanDiscovery(t *testing.T) {
	s := newTestStore(t)

	_, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDiscovery,
		Title:     "Discovered memory leak in auth module when handling JWT",
		Body:      "The JWT refresh handler was not releasing tokens",
	})
	if err != nil {
		t.Fatalf("AddMemory discovery: %v", err)
	}

	_, err = s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindBugfix,
		Title:     "Fixed JWT token leak in auth module",
		Body:      "Added proper token release in refresh handler",
	})
	if err != nil {
		t.Fatalf("AddMemory bugfix: %v", err)
	}

	results, err := s.SearchMemories("JWT token", "ohara", "", "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}
	// Bugfix (1.3) should rank above discovery (1.2)
	if results[0].Kind != MemoryKindBugfix {
		t.Errorf("expected first result to be bugfix (kind boost 1.3), got %s", results[0].Kind)
	}
}

func TestSearchMemories_FilterByKind(t *testing.T) {
	s := newTestStore(t)

	_, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Auth decision: use JWT",
		Body:      "JWT chosen",
	})
	if err != nil {
		t.Fatalf("AddMemory decision: %v", err)
	}

	_, err = s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindPattern,
		Title:     "Pattern for JWT validation",
		Body:      "Use middleware",
	})
	if err != nil {
		t.Fatalf("AddMemory pattern: %v", err)
	}

	// Search with kind filter
	results, err := s.SearchMemories("JWT", "ohara", "", MemoryKindPattern, "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	for _, r := range results {
		if r.Kind != MemoryKindPattern {
			t.Errorf("kind filter: expected pattern, got %s", r.Kind)
		}
	}
}

func TestSearchMemories_FilterByScope(t *testing.T) {
	s := newTestStore(t)

	_, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindGlossary,
		Scope:     MemoryScopeGlobal,
		Title:     "Glossary: JWT means JSON Web Token",
		Body:      "JWT is a standard",
	})
	if err != nil {
		t.Fatalf("AddMemory global: %v", err)
	}

	_, err = s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Scope:     MemoryScopeProject,
		Title:     "Decision: use JWT for auth",
		Body:      "JWT chosen",
	})
	if err != nil {
		t.Fatalf("AddMemory project: %v", err)
	}

	// Global scope search — should only return global item
	results, err := s.SearchMemories("JWT", "", MemoryScopeGlobal, "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	for _, r := range results {
		if r.Scope != MemoryScopeGlobal {
			t.Errorf("scope filter: expected global, got %s", r.Scope)
		}
	}
}

func TestSearchMemories_TriggerConditionIsSearchable(t *testing.T) {
	s := newTestStore(t)

	// Add a procedure memory with a distinctive trigger_condition.
	procID, err := s.AddMemory(AddMemoryParams{
		ProjectID:        "ohara",
		Kind:             MemoryKindProcedure,
		Title:            "Handle user auth failures",
		Body:             "Use a lockout strategy after repeated failed attempts",
		TriggerCondition: "When the user fails login 3 times within 5 minutes",
		Scope:            MemoryScopeProject,
	})
	if err != nil {
		t.Fatalf("AddMemory procedure: %v", err)
	}
	if procID <= 0 {
		t.Fatalf("expected valid memory id, got %d", procID)
	}

	// Add a noise memory (different kind, no trigger_condition).
	_, err = s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Auth decision: rate limit the login endpoint",
		Body:      "We chose to implement rate limiting to prevent brute force",
		Scope:     MemoryScopeProject,
	})
	if err != nil {
		t.Fatalf("AddMemory decision: %v", err)
	}

	// Search by trigger_condition text — procedure should be found even though
	// the title "Handle user auth failures" doesn't match "failed attempts".
	results, err := s.SearchMemories("failed attempts", "ohara", "", "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories by trigger_condition: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected at least 1 result from trigger_condition search, got 0")
	}
	if results[0].Kind != MemoryKindProcedure {
		t.Errorf("expected procedure to rank highest from trigger_condition match, got %s", results[0].Kind)
	}
	if results[0].TriggerCondition == "" {
		t.Error("expected trigger_condition to be returned in search result")
	}
}

func TestSearchMemories_TriggerConditionUpdateReindexesFTS(t *testing.T) {
	s := newTestStore(t)

	initialTrigger := "When user uploads a file larger than 10MB"
	updatedTrigger := "When user uploads any file type"

	memID, err := s.AddMemory(AddMemoryParams{
		ProjectID:        "ohara",
		Kind:             MemoryKindProcedure,
		Title:            "File upload handling",
		Body:             "Validate file size and type before processing",
		TriggerCondition: initialTrigger,
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	// Search by initial trigger — should find it.
	results, err := s.SearchMemories("larger than 10MB", "ohara", "", "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories initial trigger: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected result from initial trigger_condition search")
	}

	// Update the trigger_condition.
	updated, err := s.UpdateMemory(memID, UpdateMemoryParams{
		TriggerCondition: &updatedTrigger,
		ActorID:          "agent",
	})
	if err != nil {
		t.Fatalf("UpdateMemory trigger_condition: %v", err)
	}
	if updated.TriggerCondition != updatedTrigger {
		t.Fatalf("expected updated trigger_condition %q, got %q", updatedTrigger, updated.TriggerCondition)
	}

	// Search by old trigger — should no longer find it.
	oldResults, err := s.SearchMemories("larger than 10MB", "ohara", "", "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories old trigger: %v", err)
	}
	if len(oldResults) != 0 {
		t.Errorf("expected no results from old trigger_condition after update, got %d", len(oldResults))
	}

	// Search by new trigger — should find it.
	newResults, err := s.SearchMemories("uploads any file", "ohara", "", "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories new trigger: %v", err)
	}
	if len(newResults) == 0 {
		t.Fatal("expected result from new trigger_condition search after update")
	}
}

func TestMigrateMemFTS_MigrationIsIdempotent(t *testing.T) {
	s := newTestStore(t)

	// Running migrate twice should be a no-op (idempotent).
	if err := s.migrateMemFTSTriggerCondition(); err != nil {
		t.Fatalf("first migrateMemFTSTriggerCondition: %v", err)
	}
	if err := s.migrateMemFTSTriggerCondition(); err != nil {
		t.Fatalf("second migrateMemFTSTriggerCondition (idempotent): %v", err)
	}

	// Verify the FTS table has trigger_condition column.
	var colCount int
	if err := s.db.QueryRow(
		"SELECT COUNT(*) FROM pragma_table_xinfo('memory_items_fts') WHERE name = 'trigger_condition'",
	).Scan(&colCount); err != nil {
		t.Fatalf("check trigger_condition column: %v", err)
	}
	if colCount != 1 {
		t.Fatalf("expected 1 trigger_condition column in memory_items_fts, got %d", colCount)
	}

	// Verify the triggers still fire correctly for a new memory.
	memID, err := s.AddMemory(AddMemoryParams{
		ProjectID:        "ohara",
		Kind:             MemoryKindProcedure,
		Title:            "Handle OOM errors",
		Body:             "Catch and log out-of-memory exceptions",
		TriggerCondition: "When the process runs out of memory",
	})
	if err != nil {
		t.Fatalf("AddMemory after migration: %v", err)
	}

	results, err := s.SearchMemories("runs out of memory", "ohara", "", "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories after migration: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected search to find memory added after migration")
	}
	if results[0].ID != memID {
		t.Errorf("expected exact memory match, got id %d vs %d", results[0].ID, memID)
	}
}

func TestMigrateMemFTS_BackfillsExistingRows(t *testing.T) {
	// Set up a pre-migration database: memory_items table with some rows
	// but the FTS table WITHOUT trigger_condition.
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "ohara.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	_, err = db.Exec(`
		CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			project TEXT NOT NULL,
			directory TEXT NOT NULL,
			started_at TEXT NOT NULL DEFAULT (datetime('now')),
			ended_at TEXT,
			summary TEXT
		);
		CREATE TABLE memory_items (
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
			superseded_by   INTEGER,
			expires_at      TEXT,
			ingested_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f','now')),
			written_by      TEXT NOT NULL DEFAULT 'agent',
			domain          TEXT NOT NULL DEFAULT '',
			evidence_json   TEXT NOT NULL DEFAULT '{}',
			applies_to_json TEXT NOT NULL DEFAULT '{}',
			related_json    TEXT NOT NULL DEFAULT '{}',
			classification  TEXT NOT NULL DEFAULT 'tactical',
			access_count    INTEGER NOT NULL DEFAULT 0,
			last_accessed   TEXT,
			valid_from      TEXT,
			valid_to        TEXT,
			superseded_at   TEXT,
			session_id      TEXT NOT NULL DEFAULT '',
			trust_level     TEXT NOT NULL DEFAULT 'system',
			trigger_condition TEXT NOT NULL DEFAULT '',
			utility_weight  REAL NOT NULL DEFAULT 0.0,
			consolidated_from TEXT NOT NULL DEFAULT ''
		);
		INSERT INTO memory_items (project_id, actor_id, kind, scope, title, body, trigger_condition)
		VALUES ('ohara', 'agent', 'procedure', 'project',
			'Handle 500 errors',
			'Log and return a friendly error',
			'When a server returns HTTP 500');
		INSERT INTO memory_items (project_id, actor_id, kind, scope, title, body, trigger_condition)
		VALUES ('ohara', 'agent', 'procedure', 'project',
			'Handle timeout',
			'Retry with backoff',
			'When request times out after 30s');
		INSERT INTO memory_items (project_id, actor_id, kind, scope, title, body, trigger_condition)
		VALUES ('ohara', 'agent', 'decision', 'project',
			'Retry strategy',
			'Use exponential backoff',
			'');
		CREATE VIRTUAL TABLE memory_items_fts USING fts5(
			title, body, tags,
			content='memory_items',
			content_rowid='id',
			tokenize='porter unicode61'
		);
		INSERT INTO memory_items_fts(rowid, title, body, tags)
		SELECT id, title, body, tags FROM memory_items;
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
		CREATE TABLE memory_revisions (
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

		CREATE TABLE audit_log (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			obs_id     TEXT NOT NULL,
			action     TEXT NOT NULL CHECK(action IN ('create', 'update', 'delete', 'archive')),
			actor_id   TEXT,
			session_id TEXT,
			trust_level TEXT,
			ts         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f','now')),
			snapshot   TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_audit_obs ON audit_log(obs_id);
		CREATE INDEX IF NOT EXISTS idx_audit_session ON audit_log(session_id);

		CREATE TABLE schema_version (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f','now')));
		INSERT INTO schema_version (version, applied_at) VALUES (18, datetime('now'));
	`)
	if err != nil {
		_ = db.Close()
		t.Fatalf("seed pre-migration db: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	// Open via Store — this should run migration 19 and rebuild the FTS table.
	cfg := mustDefaultConfig(t)
	cfg.DataDir = dataDir

	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// Verify the two procedure memories are now findable by their trigger_condition.
	results, err := s.SearchMemories("request times out", "ohara", "", "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories trigger_condition after migration: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected to find procedure by trigger_condition after migration backfill")
	}
	if results[0].TriggerCondition != "When request times out after 30s" {
		t.Errorf("expected specific trigger_condition, got %q", results[0].TriggerCondition)
	}

	// The HTTP 500 procedure should also be findable.
	results500, err := s.SearchMemories("HTTP 500", "ohara", "", "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories HTTP 500: %v", err)
	}
	if len(results500) == 0 {
		t.Fatal("expected to find HTTP 500 procedure by trigger_condition")
	}

	// New inserts after migration should also index trigger_condition.
	newID, err := s.AddMemory(AddMemoryParams{
		ProjectID:        "ohara",
		Kind:             MemoryKindProcedure,
		Title:            "Handle panic recovery",
		Body:             "Catch panics in goroutines",
		TriggerCondition: "When a goroutine panics unexpectedly",
	})
	if err != nil {
		t.Fatalf("AddMemory post-migration: %v", err)
	}

	postResults, err := s.SearchMemories("goroutine panics", "ohara", "", "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories post-migration insert: %v", err)
	}
	if len(postResults) == 0 {
		t.Fatal("expected to find post-migration insert by trigger_condition")
	}
	if postResults[0].ID != newID {
		t.Errorf("expected new memory as top result, got id %d", postResults[0].ID)
	}
}

// ─── Memory Expiry Lifecycle Tests ──────────────────────────────────────────────

func TestAddMemory_AutoExpiryDiscovery(t *testing.T) {
	s := newTestStore(t)

	id, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDiscovery,
		Title:     "Discovered SQLite quirk",
		Body:      "SQLite ignores TRIM() in certain contexts",
	})
	if err != nil {
		t.Fatalf("AddMemory discovery: %v", err)
	}

	mem, err := s.GetMemory(id)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if mem.ExpiresAt == nil {
		t.Fatal("discovery memory should have expires_at set")
	}
	if *mem.ExpiresAt == "" {
		t.Fatal("discovery memory expires_at should not be empty")
	}
	// Verify it's approximately 90 days in the future
	expiresTime, err := time.Parse(time.RFC3339, *mem.ExpiresAt)
	if err != nil {
		t.Fatalf("parse expires_at: %v", err)
	}
	expectedMin := time.Now().UTC().AddDate(0, 0, 89)
	expectedMax := time.Now().UTC().AddDate(0, 0, 91)
	if expiresTime.Before(expectedMin) || expiresTime.After(expectedMax) {
		t.Fatalf("discovery TTL should be ~90 days, got %v (expected between %v and %v)", expiresTime, expectedMin, expectedMax)
	}
}

func TestAddMemory_AutoExpiryPostmortem(t *testing.T) {
	s := newTestStore(t)

	id, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindPostmortem,
		Title:     "Incident postmortem",
		Body:      "Root cause was a race condition",
	})
	if err != nil {
		t.Fatalf("AddMemory postmortem: %v", err)
	}

	mem, err := s.GetMemory(id)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if mem.ExpiresAt == nil {
		t.Fatal("postmortem memory should have expires_at set")
	}
	if *mem.ExpiresAt == "" {
		t.Fatal("postmortem memory expires_at should not be empty")
	}
	// Verify it's approximately 30 days in the future
	expiresTime, err := time.Parse(time.RFC3339, *mem.ExpiresAt)
	if err != nil {
		t.Fatalf("parse expires_at: %v", err)
	}
	expectedMin := time.Now().UTC().AddDate(0, 0, 29)
	expectedMax := time.Now().UTC().AddDate(0, 0, 31)
	if expiresTime.Before(expectedMin) || expiresTime.After(expectedMax) {
		t.Fatalf("postmortem TTL should be ~30 days, got %v (expected between %v and %v)", expiresTime, expectedMin, expectedMax)
	}
}

func TestAddMemory_NoExpiryOtherKinds(t *testing.T) {
	s := newTestStore(t)

	// Test that non-expiry kinds don't get expires_at
	nonExpiryKinds := []string{
		MemoryKindDecision,
		MemoryKindPattern,
		MemoryKindBugfix,
		MemoryKindProcedure,
		MemoryKindConfig,
		MemoryKindIdentity,
		MemoryKindUserPreference,
		MemoryKindGlossary,
	}
	for _, kind := range nonExpiryKinds {
		id, err := s.AddMemory(AddMemoryParams{
			ProjectID: "ohara",
			Kind:      kind,
			Title:     "Test " + kind,
			Body:      "Body for " + kind,
		})
		if err != nil {
			t.Fatalf("AddMemory %s: %v", kind, err)
		}
		mem, err := s.GetMemory(id)
		if err != nil {
			t.Fatalf("GetMemory %s: %v", kind, err)
		}
		if mem.ExpiresAt != nil {
			t.Errorf("kind %s should NOT have expires_at, got %v", kind, *mem.ExpiresAt)
		}
	}
}

func TestGetMemories_ExcludesExpiredItems(t *testing.T) {
	s := newTestStore(t)

	// Add an active memory without expiry
	activeID, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Active decision",
		Body:      "This should be found",
	})
	if err != nil {
		t.Fatalf("AddMemory active: %v", err)
	}

	// Add a discovery memory (which auto-expires) and manually set its expires_at to the past
	discoveryID, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDiscovery,
		Title:     "Expired discovery",
		Body:      "This should be excluded",
	})
	if err != nil {
		t.Fatalf("AddMemory discovery: %v", err)
	}

	// Manually expire the discovery memory by setting expires_at to the past
	_, err = s.db.Exec(
		`UPDATE memory_items SET expires_at = '2020-01-01T00:00:00Z' WHERE id = ?`,
		discoveryID,
	)
	if err != nil {
		t.Fatalf("set expired: %v", err)
	}

	// GetMemories should return only the active one
	items, err := s.GetMemories("ohara", "", "", MemoryStatusActive, 10)
	if err != nil {
		t.Fatalf("GetMemories: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 active item, got %d", len(items))
	}
	if items[0].ID != activeID {
		t.Fatalf("expected active memory id %d, got %d", activeID, items[0].ID)
	}

	// Verify the expired item is not returned even if we explicitly search by its kind
	expiredItems, err := s.GetMemories("ohara", "", MemoryKindDiscovery, MemoryStatusActive, 10)
	if err != nil {
		t.Fatalf("GetMemories discovery filter: %v", err)
	}
	if len(expiredItems) != 0 {
		t.Fatalf("expected 0 expired discovery items, got %d", len(expiredItems))
	}
}

func TestSearchMemories_ExcludesExpiredItems(t *testing.T) {
	s := newTestStore(t)

	// Add an active memory that matches the search
	activeID, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindPattern,
		Title:     "Use connection pooling",
		Body:      "Pool database connections for better performance",
	})
	if err != nil {
		t.Fatalf("AddMemory active: %v", err)
	}

	// Add an expired memory that also matches the search terms
	expiredID, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindPattern,
		Title:     "Use connection pooling old",
		Body:      "Pool database connections for better performance old",
	})
	if err != nil {
		t.Fatalf("AddMemory expired: %v", err)
	}

	// Manually expire the second memory
	_, err = s.db.Exec(
		`UPDATE memory_items SET expires_at = '2020-01-01T00:00:00Z' WHERE id = ?`,
		expiredID,
	)
	if err != nil {
		t.Fatalf("set expired: %v", err)
	}

	// SearchMemories should only return the active one
	results, err := s.SearchMemories("pool", "ohara", "", "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 search result (active only), got %d", len(results))
	}
	if results[0].ID != activeID {
		t.Fatalf("expected active memory id %d, got %d", activeID, results[0].ID)
	}
}

func TestBuildPack_ExcludesExpiredItems(t *testing.T) {
	s := newTestStore(t)

	// Add active memories for pack
	activeID, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Active decision",
		Body:      "This decision is current and relevant",
	})
	if err != nil {
		t.Fatalf("AddMemory active: %v", err)
	}

	// Add expired project memory
	expiredID, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Expired decision",
		Body:      "This decision is outdated",
	})
	if err != nil {
		t.Fatalf("AddMemory expired: %v", err)
	}

	// Manually expire the second memory
	_, err = s.db.Exec(
		`UPDATE memory_items SET expires_at = '2020-01-01T00:00:00Z' WHERE id = ?`,
		expiredID,
	)
	if err != nil {
		t.Fatalf("set expired: %v", err)
	}

	// BuildPack should only include the active memory
	pack, err := s.BuildPack(PackParams{ProjectID: "ohara"})
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}

	// Check that expired item is not in pack output
	if strings.Contains(pack.Pack, "Expired decision") {
		t.Fatal("expired memory should not appear in pack")
	}
	if !strings.Contains(pack.Pack, "Active decision") {
		t.Fatal("active memory should appear in pack")
	}

	// Verify the memory items in the result
	foundActive := false
	foundExpired := false
	for _, item := range pack.MemoryItems {
		if item.ID == activeID {
			foundActive = true
		}
		if item.ID == expiredID {
			foundExpired = true
		}
	}
	if !foundActive {
		t.Fatal("active memory should be in pack.MemoryItems")
	}
	if foundExpired {
		t.Fatal("expired memory should not be in pack.MemoryItems")
	}
}

func TestGetMemories_ArchivedItemsRemainRetrievable(t *testing.T) {
	s := newTestStore(t)

	// Add an active decision memory
	activeID, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Active decision",
		Body:      "This is current",
	})
	if err != nil {
		t.Fatalf("AddMemory active: %v", err)
	}

	// Add a discovery memory and set it to archived+expired
	archivedID, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDiscovery,
		Title:     "Archived discovery",
		Body:      "This is archived and expired",
	})
	if err != nil {
		t.Fatalf("AddMemory archived: %v", err)
	}

	// Set status to archived and expire the discovery memory
	_, err = s.db.Exec(
		`UPDATE memory_items SET status = 'archived', expires_at = '2020-01-01T00:00:00Z' WHERE id = ?`,
		archivedID,
	)
	if err != nil {
		t.Fatalf("set archived and expired: %v", err)
	}

	// Verify GetMemory (by ID) can still retrieve the expired item directly
	mem, err := s.GetMemory(archivedID)
	if err != nil {
		t.Fatalf("GetMemory should retrieve expired item by ID: %v", err)
	}
	if mem.ID != archivedID {
		t.Fatalf("expected memory id %d, got %d", archivedID, mem.ID)
	}

	// Verify default GetMemories (status="") excludes expired items
	items, err := s.GetMemories("ohara", "", "", "", 10)
	if err != nil {
		t.Fatalf("GetMemories with default status: %v", err)
	}
	if len(items) != 1 || items[0].ID != activeID {
		t.Fatalf("expected 1 active item, got %d items", len(items))
	}

	// Verify GetMemories with explicit status="active" also excludes expired items
	activeItems, err := s.GetMemories("ohara", "", "", MemoryStatusActive, 10)
	if err != nil {
		t.Fatalf("GetMemories with active status: %v", err)
	}
	if len(activeItems) != 1 || activeItems[0].ID != activeID {
		t.Fatalf("expected 1 active item, got %d items", len(activeItems))
	}

	// Verify GetMemories with status="archived" CAN retrieve archived/expired items
	archivedItems, err := s.GetMemories("ohara", "", "", MemoryStatusArchived, 10)
	if err != nil {
		t.Fatalf("GetMemories with archived status: %v", err)
	}
	foundArchived := false
	for _, item := range archivedItems {
		if item.ID == archivedID {
			foundArchived = true
			break
		}
	}
	if !foundArchived {
		t.Fatalf("expected archived/expired item to be retrievable via archived status, got %d items", len(archivedItems))
	}
}

func TestSearchMemories_ArchivedItemsRemainSearchable(t *testing.T) {
	s := newTestStore(t)

	// Add an active pattern memory
	activeID, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindPattern,
		Title:     "Active pattern auth",
		Body:      "Use middleware for auth validation",
	})
	if err != nil {
		t.Fatalf("AddMemory active: %v", err)
	}

	// Add an archived pattern memory that is also expired
	archivedID, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindPattern,
		Title:     "Archived pattern auth",
		Body:      "Use middleware for old auth approach",
	})
	if err != nil {
		t.Fatalf("AddMemory archived: %v", err)
	}

	// Set status to archived and expire the memory
	_, err = s.db.Exec(
		`UPDATE memory_items SET status = 'archived', expires_at = '2020-01-01T00:00:00Z' WHERE id = ?`,
		archivedID,
	)
	if err != nil {
		t.Fatalf("set archived and expired: %v", err)
	}

	// Verify default SearchMemories (status="") excludes expired items
	results, err := s.SearchMemories("middleware", "ohara", "", "", "", "", 10, "")
	if err != nil {
		t.Fatalf("SearchMemories with default status: %v", err)
	}
	if len(results) != 1 || results[0].ID != activeID {
		t.Fatalf("expected 1 active search result, got %d", len(results))
	}

	// Verify SearchMemories with status="archived" includes archived/expired items
	archivedResults, err := s.SearchMemories("middleware", "ohara", "", "", "", MemoryStatusArchived, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories with archived status: %v", err)
	}
	foundArchived := false
	for _, item := range archivedResults {
		if item.ID == archivedID {
			foundArchived = true
			break
		}
	}
	if !foundArchived {
		t.Fatalf("expected archived/expired pattern item to be searchable via archived status, got %d results", len(archivedResults))
	}
}

func TestMemoryExpiresAt_HelperFunction(t *testing.T) {
	// Test discovery TTL (90 days)
	expires := MemoryExpiresAt(MemoryKindDiscovery)
	if expires == nil {
		t.Fatal("discovery should have TTL")
	}
	expiresTime, err := time.Parse(time.RFC3339, *expires)
	if err != nil {
		t.Fatalf("parse discovery expires_at: %v", err)
	}
	ttl := MemoryTTL(MemoryKindDiscovery)
	if ttl != 90 {
		t.Fatalf("expected discovery TTL 90 days, got %d", ttl)
	}
	expectedExpiry := time.Now().UTC().AddDate(0, 0, ttl)
	diff := expiresTime.Sub(expectedExpiry)
	if diff < -time.Minute || diff > time.Minute {
		t.Fatalf("discovery expires_at should be ~90 days from now, diff=%v", diff)
	}

	// Test postmortem TTL (30 days)
	expires = MemoryExpiresAt(MemoryKindPostmortem)
	if expires == nil {
		t.Fatal("postmortem should have TTL")
	}
	ttl = MemoryTTL(MemoryKindPostmortem)
	if ttl != 30 {
		t.Fatalf("expected postmortem TTL 30 days, got %d", ttl)
	}

	// Test non-expiring kinds
	for _, kind := range []string{MemoryKindDecision, MemoryKindPattern, MemoryKindBugfix} {
		expires = MemoryExpiresAt(kind)
		if expires != nil {
			t.Errorf("kind %s should not have TTL, got %v", kind, *expires)
		}
		if MemoryTTL(kind) != 0 {
			t.Errorf("kind %s TTL should be 0, got %d", kind, MemoryTTL(kind))
		}
	}
}

func TestMarkConsolidatedArchivesCandidateAndSources(t *testing.T) {
	s := newTestStore(t)

	var sourceIDs []int64
	for i := 1; i <= 3; i++ {
		id, err := s.AddMemory(AddMemoryParams{
			ProjectID:      "ohara",
			Kind:           MemoryKindDiscovery,
			Title:          fmt.Sprintf("Episode %d", i),
			Body:           fmt.Sprintf("raw session detail %d", i),
			Classification: "observational",
			Domain:         "api",
		})
		if err != nil {
			t.Fatalf("AddMemory source %d: %v", i, err)
		}
		sourceIDs = append(sourceIDs, id)
	}

	created, _, err := s.GenerateConsolidationCandidates("ohara", "api", false)
	if err != nil {
		t.Fatalf("GenerateConsolidationCandidates: %v", err)
	}
	if created != 1 {
		t.Fatalf("expected 1 candidate, got %d", created)
	}

	groups, err := s.GetConsolidationCandidates("ohara", "api")
	if err != nil {
		t.Fatalf("GetConsolidationCandidates: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected 1 candidate group, got %d", len(groups))
	}
	candidateID := groups[0].Candidate.ID

	consolidatedID, err := s.AddMemory(AddMemoryParams{
		ProjectID:        "ohara",
		Kind:             MemoryKindDecision,
		Title:            "Durable API learning",
		Body:             "Semantic summary",
		Source:           "consolidation",
		Classification:   "tactical",
		Domain:           "api",
		ConsolidatedFrom: groups[0].Candidate.ConsolidatedFrom,
	})
	if err != nil {
		t.Fatalf("AddMemory consolidated: %v", err)
	}

	if err := s.MarkConsolidated(candidateID, consolidatedID); err != nil {
		t.Fatalf("MarkConsolidated: %v", err)
	}

	candidate, err := s.GetMemory(candidateID)
	if err != nil {
		t.Fatalf("GetMemory candidate: %v", err)
	}
	if candidate.Status != MemoryStatusArchived {
		t.Fatalf("candidate status = %q, want %q", candidate.Status, MemoryStatusArchived)
	}
	if candidate.SupersededBy == nil || *candidate.SupersededBy != consolidatedID {
		t.Fatalf("candidate superseded_by = %v, want %d", candidate.SupersededBy, consolidatedID)
	}

	for _, sourceID := range sourceIDs {
		source, err := s.GetMemory(sourceID)
		if err != nil {
			t.Fatalf("GetMemory source %d: %v", sourceID, err)
		}
		if source.Status != MemoryStatusArchived {
			t.Fatalf("source %d status = %q, want %q", sourceID, source.Status, MemoryStatusArchived)
		}
		if source.SupersededBy == nil || *source.SupersededBy != consolidatedID {
			t.Fatalf("source %d superseded_by = %v, want %d", sourceID, source.SupersededBy, consolidatedID)
		}
	}
}

// ─── Token-Aware Body Limit Tests ──────────────────────────────────────────────

func TestTruncateBodyToTokenLimit_NoTruncationWhenUnderLimit(t *testing.T) {
	// Glossary has 200 token limit. Short text should not be truncated.
	shortText := "This is a brief glossary entry."
	result := TruncateBodyToTokenLimit(shortText, MemoryKindGlossary)
	if result != shortText {
		t.Errorf("expected no truncation for short text, got %q", result)
	}
	if strings.HasSuffix(result, "... [truncated]") {
		t.Error("short text should not have truncation suffix")
	}
}

func TestTruncateBodyToTokenLimit_TruncatesWhenOverLimit(t *testing.T) {
	// Glossary has 200 token limit. Create text that exceeds it.
	// Repeating a word many times will exceed the token budget.
	longText := strings.Repeat("tokenization ", 300) // ~300+ tokens

	result := TruncateBodyToTokenLimit(longText, MemoryKindGlossary)

	// Should be truncated
	if !strings.HasSuffix(result, "... [truncated]") {
		t.Errorf("expected truncation suffix, got %q", result)
	}

	// The result should fit within the token limit
	if token.CountStrict(result) > MemoryBodyLimit(MemoryKindGlossary) {
		t.Errorf("truncated result exceeds token limit: got %d tokens for %d limit",
			token.CountStrict(result), MemoryBodyLimit(MemoryKindGlossary))
	}

	// Original should be longer than result
	if len(result) >= len(longText) {
		t.Error("truncated result should be shorter than original")
	}
}

func TestTruncateBodyToTokenLimit_EmptyText(t *testing.T) {
	result := TruncateBodyToTokenLimit("", MemoryKindDecision)
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestTruncateBodyToTokenLimit_UnknownKindNoLimit(t *testing.T) {
	// Unknown kinds have limit=0 (unlimited), so no truncation
	longText := strings.Repeat("x", 10000)
	result := TruncateBodyToTokenLimit(longText, "unknown_kind")
	if result != longText {
		t.Errorf("unknown kind should not truncate, got %q", result)
	}
}

func TestTruncateBodyToTokenLimit_DifferentKindsRespectLimits(t *testing.T) {
	// Create text that exceeds glossary limit (200) but not decision limit (1000)
	// "word " is roughly 1 token, so 300 repetitions = ~300 tokens
	mediumText := strings.Repeat("word ", 300)

	// Test glossary (200 token limit) - should truncate
	glossaryResult := TruncateBodyToTokenLimit(mediumText, MemoryKindGlossary)
	if !strings.HasSuffix(glossaryResult, "... [truncated]") {
		t.Error("glossary should truncate text exceeding 200 token limit")
	}

	// Test decision (1000 token limit) - should NOT truncate (300 < 1000)
	decisionResult := TruncateBodyToTokenLimit(mediumText, MemoryKindDecision)
	if strings.HasSuffix(decisionResult, "... [truncated]") {
		t.Error("decision should not truncate 300-token text (1000 token limit)")
	}

	// Verify token counts
	if token.CountStrict(glossaryResult) > MemoryBodyLimit(MemoryKindGlossary) {
		t.Error("glossary result exceeds its token limit")
	}
}

func TestAddMemory_TruncatesBodyByTokenCount(t *testing.T) {
	s := newTestStore(t)

	// Create a body that exceeds the glossary token limit (200)
	longBody := strings.Repeat("tokenization test ", 300)

	id, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindGlossary,
		Title:     "Long glossary entry",
		Body:      longBody,
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	mem, err := s.GetMemory(id)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}

	// Should be truncated
	if !strings.HasSuffix(mem.Body, "... [truncated]") {
		t.Errorf("expected truncated body, got %q", mem.Body)
	}

	// Should fit within token limit
	if token.CountStrict(mem.Body) > MemoryBodyLimit(MemoryKindGlossary) {
		t.Errorf("stored body exceeds token limit: got %d tokens",
			token.CountStrict(mem.Body))
	}
}

func TestUpdateMemory_TruncatesBodyByTokenCount(t *testing.T) {
	s := newTestStore(t)

	// First create a memory
	id, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindGlossary,
		Title:     "Glossary entry",
		Body:      "Short body",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	// Now update with a long body that exceeds the limit
	longBody := strings.Repeat("tokenization update ", 300)
	updated, err := s.UpdateMemory(id, UpdateMemoryParams{
		Body: &longBody,
	})
	if err != nil {
		t.Fatalf("UpdateMemory: %v", err)
	}

	// Should be truncated
	if !strings.HasSuffix(updated.Body, "... [truncated]") {
		t.Errorf("expected truncated body after update, got %q", updated.Body)
	}

	// Should fit within token limit
	if token.CountStrict(updated.Body) > MemoryBodyLimit(MemoryKindGlossary) {
		t.Errorf("updated body exceeds token limit: got %d tokens",
			token.CountStrict(updated.Body))
	}

	// Verify revision was recorded
	revs, err := s.GetMemoryRevisions(id)
	if err != nil {
		t.Fatalf("GetMemoryRevisions: %v", err)
	}

	foundBodyRev := false
	for _, r := range revs {
		if r.Field == "body" {
			foundBodyRev = true
			break
		}
	}
	if !foundBodyRev {
		t.Error("expected body revision to be recorded")
	}
}

func TestTruncateBodyToTokenLimit_FallsBackSafely(t *testing.T) {
	// Test that the function works even with the fallback estimator
	// (when tiktoken is unavailable). CountStrict overestimates, so
	// we may truncate slightly more conservatively, but it should still work.

	text := strings.Repeat("word ", 1000) // Definitely over any limit

	// Test with a kind that has a small limit
	result := TruncateBodyToTokenLimit(text, MemoryKindGlossary)

	// Should have truncation marker
	if !strings.HasSuffix(result, "... [truncated]") {
		t.Error("should truncate even with fallback estimator")
	}

	// Result should be reasonable length (not empty, not full text)
	if len(result) == 0 || len(result) >= len(text) {
		t.Error("truncated result should have reasonable length")
	}
}

// ─── Private-tag stripping on memory save/update ────────────────────────────────

func TestAddMemory_StripsPrivateTags(t *testing.T) {
	s := newTestStore(t)

	// Memory with <private> tags in both title and body.
	id, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Use <private>super-secret-key</private> for signing",
		Body:      "The secret key is <private>SECRET123</private> and must stay private",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	mem, err := s.GetMemory(id)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}

	// Title must not contain the private tag
	if strings.Contains(mem.Title, "<private>") {
		t.Errorf("stored title should not contain <private>: %q", mem.Title)
	}
	// Title should contain the replacement
	if !strings.Contains(mem.Title, "[REDACTED]") {
		t.Errorf("stored title should contain [REDACTED]: %q", mem.Title)
	}

	// Body must not contain the private tag
	if strings.Contains(mem.Body, "<private>") {
		t.Errorf("stored body should not contain <private>: %q", mem.Body)
	}
	if !strings.Contains(mem.Body, "[REDACTED]") {
		t.Errorf("stored body should contain [REDACTED]: %q", mem.Body)
	}

	// Revision records should also be stripped
	revs, err := s.GetMemoryRevisions(id)
	if err != nil {
		t.Fatalf("GetMemoryRevisions: %v", err)
	}
	for _, r := range revs {
		if r.NewValue != nil && strings.Contains(*r.NewValue, "<private>") {
			t.Errorf("revision %d field %q new_value should not contain <private>: %s", r.ID, r.Field, *r.NewValue)
		}
	}
}

func TestAddMemory_PrivateTagInBodyOnly(t *testing.T) {
	s := newTestStore(t)

	id, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindPattern,
		Title:     "Normal title without private tags",
		Body:      "API key: <private>ak-test-12345</private> — never commit this",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	mem, err := s.GetMemory(id)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}

	if strings.Contains(mem.Body, "<private>") {
		t.Errorf("body should not contain <private> tag: %q", mem.Body)
	}
	if !strings.Contains(mem.Body, "[REDACTED]") {
		t.Errorf("body should contain [REDACTED] replacement: %q", mem.Body)
	}
	// Title should be unchanged
	if mem.Title != "Normal title without private tags" {
		t.Errorf("title should be unchanged: %q", mem.Title)
	}
}

func TestUpdateMemory_StripsPrivateTags(t *testing.T) {
	s := newTestStore(t)

	id, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindBugfix,
		Title:     "Fixed auth issue",
		Body:      "Original fix approach",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	// Update the body with a private tag
	newBody := "Updated: <private>password=hunter2</private> was the old cred"
	updated, err := s.UpdateMemory(id, UpdateMemoryParams{
		Body:   &newBody,
		Reason: "Added sensitive details to document",
	})
	if err != nil {
		t.Fatalf("UpdateMemory: %v", err)
	}

	if strings.Contains(updated.Body, "<private>") {
		t.Errorf("updated body should not contain <private>: %q", updated.Body)
	}
	if !strings.Contains(updated.Body, "[REDACTED]") {
		t.Errorf("updated body should contain [REDACTED]: %q", updated.Body)
	}

	// Verify the revision records the stripped value, not the original
	revs, err := s.GetMemoryRevisions(id)
	if err != nil {
		t.Fatalf("GetMemoryRevisions: %v", err)
	}
	var bodyRev *MemoryRevision
	for _, r := range revs {
		if r.Field == "body" {
			bodyRev = &r
			break
		}
	}
	if bodyRev == nil {
		t.Fatal("expected a body revision")
	}
	if bodyRev.NewValue != nil && strings.Contains(*bodyRev.NewValue, "<private>") {
		t.Errorf("body revision new_value should not contain <private>: %s", *bodyRev.NewValue)
	}
}

func TestUpdateMemory_StripsPrivateTagsInTitle(t *testing.T) {
	s := newTestStore(t)

	id, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Original decision title",
		Body:      "Some content",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	newTitle := "Updated: <private>internal-discussion</private> about auth"
	updated, err := s.UpdateMemory(id, UpdateMemoryParams{
		Title:  &newTitle,
		Reason: "Reflecting internal notes",
	})
	if err != nil {
		t.Fatalf("UpdateMemory: %v", err)
	}

	if strings.Contains(updated.Title, "<private>") {
		t.Errorf("updated title should not contain <private>: %q", updated.Title)
	}
	if !strings.Contains(updated.Title, "[REDACTED]") {
		t.Errorf("updated title should contain [REDACTED]: %q", updated.Title)
	}
}

func TestPrivateTagRegex_MatchesAcrossLines(t *testing.T) {
	// The regex must match <private> tags that span multiple lines.
	result := stripPrivateTags("Line1\n<private>multi\nline\nsecret</private>\nLine5")
	if strings.Contains(result, "<private>") {
		t.Error("multi-line <private> tag should be stripped")
	}
	if !strings.Contains(result, "[REDACTED]") {
		t.Error("multi-line replacement should appear")
	}
	if strings.Contains(result, "multi") || strings.Contains(result, "line") {
		t.Error("content inside <private> should be removed")
	}
}

func TestPrivateTagRegex_CaseInsensitive(t *testing.T) {
	result := stripPrivateTags("<PRIVATE>secret</PRIVATE> and <Private>more</Private>")
	if strings.Contains(result, "<PRIVATE>") || strings.Contains(result, "<Private>") {
		t.Error("case-insensitive <private> tags should be stripped")
	}
	if !strings.Contains(result, "[REDACTED]") {
		t.Error("[REDACTED] should appear for each match")
	}
}

func TestPrivateTagRegex_MultipleTags(t *testing.T) {
	result := stripPrivateTags("<private>key1</private> and <private>key2</private> remaining")
	if strings.Contains(result, "<private>") {
		t.Error("multiple <private> tags should all be stripped")
	}
	// Should have two [REDACTED] replacements
	count := strings.Count(result, "[REDACTED]")
	if count != 2 {
		t.Errorf("expected 2 [REDACTED] replacements, got %d in %q", count, result)
	}
}

// ─── WAL auto-checkpoint pragma ─────────────────────────────────────────────────

func TestWALAutocheckpointSet(t *testing.T) {
	s := newTestStore(t)

	// Verify the wal_autocheckpoint pragma was applied at store creation.
	var checkpointPages int
	err := s.db.QueryRow("PRAGMA wal_autocheckpoint").Scan(&checkpointPages)
	if err != nil {
		t.Fatalf("PRAGMA wal_autocheckpoint query: %v", err)
	}
	// Per Ohara v2 spec Phase 2: WAL auto-checkpoint at 1000 pages.
	if checkpointPages != 1000 {
		t.Errorf("expected wal_autocheckpoint=1000, got %d", checkpointPages)
	}
}

func TestWALAutocheckpoint_JournalModeIsWAL(t *testing.T) {
	s := newTestStore(t)

	var journalMode string
	err := s.db.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	if err != nil {
		t.Fatalf("PRAGMA journal_mode query: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("expected journal_mode=wal, got %q", journalMode)
	}
}

// ─── Consolidation candidate tests ───────────────────────────────────────────────

func TestConsolidationCandidates_CreatesCandidateForThreeOrMoreObservational(t *testing.T) {
	s := newTestStore(t)

	// Create 3 observational memories in the same project+domain+kind group.
	for i := 1; i <= 3; i++ {
		_, err := s.AddMemory(AddMemoryParams{
			ProjectID:      "ohara",
			Kind:           MemoryKindDiscovery,
			Title:          fmt.Sprintf("Obs item %d", i),
			Body:           fmt.Sprintf("Body of observation %d", i),
			Classification: "observational",
			Domain:         "test",
		})
		if err != nil {
			t.Fatalf("AddMemory %d: %v", i, err)
		}
	}

	created, summaries, err := s.GenerateConsolidationCandidates("ohara", "test", false)
	if err != nil {
		t.Fatalf("GenerateConsolidationCandidates: %v", err)
	}
	if created != 1 {
		t.Errorf("expected 1 candidate, got %d", created)
	}
	if len(summaries) != 1 {
		t.Errorf("expected 1 summary, got %d", len(summaries))
	}
	if !strings.Contains(summaries[0], "created:") {
		t.Errorf("summary should contain 'created:', got %q", summaries[0])
	}

	// Verify the candidate memory was created with correct status.
	cand, err := s.GetMemories("ohara", "", "", MemoryStatusCandidate, 10)
	if err != nil {
		t.Fatalf("GetMemories candidate: %v", err)
	}
	if len(cand) != 1 {
		t.Errorf("expected 1 candidate in GetMemories, got %d", len(cand))
	}
	if cand[0].Classification != "tactical" {
		t.Errorf("expected classification tactical, got %q", cand[0].Classification)
	}
	if cand[0].Source != "consolidation" {
		t.Errorf("expected source consolidation, got %q", cand[0].Source)
	}
}

func TestConsolidationCandidates_DryRunDoesNotCreate(t *testing.T) {
	s := newTestStore(t)

	for i := 1; i <= 3; i++ {
		_, err := s.AddMemory(AddMemoryParams{
			ProjectID:      "ohara",
			Kind:           MemoryKindBugfix,
			Title:          fmt.Sprintf("Bugfix obs %d", i),
			Body:           fmt.Sprintf("Body %d", i),
			Classification: "observational",
		})
		if err != nil {
			t.Fatalf("AddMemory: %v", err)
		}
	}

	created, summaries, err := s.GenerateConsolidationCandidates("ohara", "", true)
	if err != nil {
		t.Fatalf("GenerateConsolidationCandidates dry-run: %v", err)
	}
	if created != 0 {
		t.Errorf("dry-run should return 0 created, got %d", created)
	}
	if len(summaries) == 0 {
		t.Errorf("dry-run should return summaries")
	}
	if !strings.Contains(summaries[0], "[dry-run]") {
		t.Errorf("dry-run summary should contain [dry-run], got %q", summaries[0])
	}

	// Verify no candidate was actually created.
	cand, _ := s.GetMemories("ohara", "", "", MemoryStatusCandidate, 10)
	if len(cand) != 0 {
		t.Errorf("dry-run should not create candidates, got %d", len(cand))
	}
}

func TestConsolidationCandidates_DuplicateAvoidance(t *testing.T) {
	s := newTestStore(t)

	// Create a group that will produce a candidate.
	for i := 1; i <= 3; i++ {
		_, err := s.AddMemory(AddMemoryParams{
			ProjectID:      "ohara",
			Kind:           MemoryKindPattern,
			Title:          fmt.Sprintf("Pattern obs %d", i),
			Body:           fmt.Sprintf("Body %d", i),
			Classification: "observational",
			Domain:         "auth",
		})
		if err != nil {
			t.Fatalf("AddMemory: %v", err)
		}
	}

	// First run: creates candidate.
	created1, _, err := s.GenerateConsolidationCandidates("ohara", "auth", false)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if created1 != 1 {
		t.Errorf("expected 1 candidate on first run, got %d", created1)
	}

	// Second run: should not create duplicate (same source IDs).
	created2, _, err := s.GenerateConsolidationCandidates("ohara", "auth", false)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if created2 != 0 {
		t.Errorf("expected 0 candidates on second run (duplicate avoidance), got %d", created2)
	}

	// Verify only one candidate exists.
	cand, _ := s.GetMemories("ohara", "", "", MemoryStatusCandidate, 10)
	if len(cand) != 1 {
		t.Errorf("expected exactly 1 candidate total, got %d", len(cand))
	}
}

func TestConsolidationCandidates_ExcludedFromActiveRetrieval(t *testing.T) {
	s := newTestStore(t)

	// Create a regular active memory.
	_, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Regular decision",
		Body:      "A regular decision body",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	// Create 3 observational memories to trigger a candidate.
	for i := 1; i <= 3; i++ {
		_, err := s.AddMemory(AddMemoryParams{
			ProjectID:      "ohara",
			Kind:           MemoryKindDiscovery,
			Title:          fmt.Sprintf("Obs %d", i),
			Body:           fmt.Sprintf("Body %d", i),
			Classification: "observational",
			Domain:         "db",
		})
		if err != nil {
			t.Fatalf("AddMemory: %v", err)
		}
	}

	// Generate the candidate.
	_, _, err = s.GenerateConsolidationCandidates("ohara", "db", false)
	if err != nil {
		t.Fatalf("GenerateConsolidationCandidates: %v", err)
	}

	// Normal active retrieval should NOT include the candidate.
	active, err := s.GetMemories("ohara", "", "", MemoryStatusActive, 20)
	if err != nil {
		t.Fatalf("GetMemories active: %v", err)
	}
	for _, m := range active {
		if m.Status == MemoryStatusCandidate {
			t.Errorf("candidate should not appear in active retrieval, found id=%d", m.ID)
		}
	}
}

func TestCountCandidates(t *testing.T) {
	s := newTestStore(t)

	// No candidates yet.
	count, err := s.CountCandidates("")
	if err != nil {
		t.Fatalf("CountCandidates: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 candidates initially, got %d", count)
	}

	// Create 3 observational memories and generate a candidate.
	for i := 1; i <= 3; i++ {
		_, _ = s.AddMemory(AddMemoryParams{
			ProjectID:      "ohara",
			Kind:           MemoryKindBugfix,
			Title:          fmt.Sprintf("Obs %d", i),
			Body:           fmt.Sprintf("Body %d", i),
			Classification: "observational",
		})
	}
	_, _, _ = s.GenerateConsolidationCandidates("ohara", "", false)

	count, err = s.CountCandidates("")
	if err != nil {
		t.Fatalf("CountCandidates after: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 candidate, got %d", count)
	}

	// Filter by project.
	count, _ = s.CountCandidates("ohara")
	if count != 1 {
		t.Errorf("expected 1 candidate for ohara, got %d", count)
	}
	count, _ = s.CountCandidates("other-project")
	if count != 0 {
		t.Errorf("expected 0 candidates for other-project, got %d", count)
	}
}

func TestConsolidationCandidates_GroupSizeTwoOrLess(t *testing.T) {
	s := newTestStore(t)

	// Only 2 observational memories — below the threshold of 3.
	for i := 1; i <= 2; i++ {
		_, err := s.AddMemory(AddMemoryParams{
			ProjectID:      "ohara",
			Kind:           MemoryKindProcedure,
			Title:          fmt.Sprintf("Proc obs %d", i),
			Body:           fmt.Sprintf("Body %d", i),
			Classification: "observational",
		})
		if err != nil {
			t.Fatalf("AddMemory: %v", err)
		}
	}

	created, summaries, err := s.GenerateConsolidationCandidates("ohara", "", false)
	if err != nil {
		t.Fatalf("GenerateConsolidationCandidates: %v", err)
	}
	if created != 0 {
		t.Errorf("expected 0 candidates for group < 3, got %d", created)
	}
	if len(summaries) != 0 {
		t.Errorf("expected 0 summaries for group < 3, got %d", len(summaries))
	}
}
