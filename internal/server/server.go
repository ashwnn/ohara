// Package server provides the HTTP API for Ohara.
//
// This is how external clients (OpenCode plugin, Claude Code hooks,
// any agent) communicate with the memory engine. Simple JSON REST API.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ashwnn/ohara/internal/auth"
	"github.com/ashwnn/ohara/internal/store"
)

var loadServerStats = func(s *store.Store) (any, error) {
	legacy, err := s.Stats()
	if err != nil {
		return nil, err
	}
	mem, err := s.MemoryStats()
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"total_sessions":     legacy.TotalSessions,
		"total_memories":     legacy.TotalMemories,
		"total_prompts":      legacy.TotalPrompts,
		"projects":           legacy.Projects,
		"by_kind":            mem.ByKind,
		"by_scope":           mem.ByScope,
		"by_status":          mem.ByStatus,
		"by_domain":          mem.ByDomain,
		"by_classification":  mem.ByClassification,
		"total_memory_items": mem.TotalMemoryItems,
	}, nil
}

// SyncStatusProvider returns the current sync status. This is implemented
// by autosync.Manager and injected from cmd/ohara/main.go.
type SyncStatusProvider interface {
	Status() SyncStatus
}

// SyncStatus mirrors autosync.Status to avoid a direct import cycle.
type SyncStatus struct {
	Phase               string     `json:"phase"`
	LastError           string     `json:"last_error,omitempty"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	BackoffUntil        *time.Time `json:"backoff_until,omitempty"`
	LastSyncAt          *time.Time `json:"last_sync_at,omitempty"`
}

// ServerOption configures a Server.
type ServerOption func(*Server)

// WithSocketPath sets a unix socket path for the server to listen on.
// When set, the server attempts to listen on the unix socket first;
// if that fails it falls back to the TCP port.
func WithSocketPath(path string) ServerOption {
	return func(s *Server) { s.socketPath = path }
}

// PackConfig holds the configurable parameters for context pack assembly.
type PackConfig struct {
	DefaultBudgetTokens int
	MaxBudgetTokens     int
}

// WithPackConfig sets the pack assembly configuration.
func WithPackConfig(cfg PackConfig) ServerOption {
	return func(s *Server) { s.packConfig = cfg }
}

// ConflictEnabled controls whether conflict detection runs.
// Zero value is ConflictEnabledOn (conflict detection enabled by default).
//
//go:generate stringer -type=ConflictEnabled -linecomment
type ConflictEnabled int

const (
	ConflictEnabledOn  ConflictEnabled = iota // enabled (also the zero value)
	ConflictEnabledOff                        // explicitly disabled
)

// ConflictConfig holds the configurable parameters for memory contradiction detection.
// The zero value is ConflictEnabledDefault (detection enabled by default).
type ConflictConfig struct {
	Enabled   ConflictEnabled
	Threshold float64 // minimum Jaccard overlap (0.0-1.0) to report a conflict
}

// WithConflictConfig sets the conflict detection configuration.
// When Enabled is ConflictEnabledOff, no contradiction checking is performed.
// Default (zero value) or ConflictEnabledOn enables it with the configured threshold.
func WithConflictConfig(cfg ConflictConfig) ServerOption {
	return func(s *Server) { s.conflictConfig = cfg }
}

// adminOnlyPaths are HTTP routes that require the admin role regardless of method.
// DELETE methods always require admin via method check; only non-DELETE overrides
// (like GET /export, POST /import) need to be listed here.
var adminOnlyPaths = map[string]bool{
	"GET /export":            true,
	"POST /import":           true,
	"POST /projects/migrate": true,
}

type Server struct {
	store          *store.Store
	mux            *http.ServeMux
	port           int
	socketPath     string
	listen         func(network, address string) (net.Listener, error)
	serve          func(net.Listener, http.Handler) error
	onWrite        func() // called after successful local writes (for autosync notification)
	syncStatus     SyncStatusProvider
	packConfig     PackConfig
	conflictConfig ConflictConfig
	authenticator  auth.Authenticator
	mcpHandler     http.Handler
	mcpRegistered  bool
}

func New(s *store.Store, port int, opts ...ServerOption) *Server {
	srv := &Server{store: s, port: port, listen: net.Listen, serve: http.Serve}
	for _, o := range opts {
		o(srv)
	}
	srv.mux = http.NewServeMux()
	srv.routes()
	return srv
}

// SetOnWrite configures a callback invoked after every successful local write.
// This is used to notify autosync.Manager via NotifyDirty().
func (s *Server) SetOnWrite(fn func()) {
	s.onWrite = fn
}

// SetAuthConfig configures bearer token authentication for HTTP requests.
// When enabled, all requests (except health check) require an Authorization:
// Bearer <token> header using a static pre-shared token.
// Disabled by default for local-only operation.
//
// This is a convenience wrapper that creates a StaticTokenAuthenticator.
// For programmatic configuration, use SetAuthenticator directly.
func (s *Server) SetAuthConfig(enabled bool, token string) {
	if enabled {
		s.authenticator = auth.NewStaticTokenAuthenticator(token)
	} else {
		s.authenticator = nil
	}
}

// SetAuthenticator sets the authenticator used for bearer token validation.
// When nil, authentication is disabled and all requests pass through.
func (s *Server) SetAuthenticator(authr auth.Authenticator) {
	s.authenticator = authr
}

// requiredRole returns the minimum role required for the given request.
// Health is excluded (checked earlier in authMiddleware).
// Method-based defaults: DELETE→admin, POST/PATCH/PUT→write, GET/others→read.
// Fixed-path overrides in adminOnlyPaths elevate GET/POST routes to admin.
func (s *Server) requiredRole(r *http.Request) auth.Role {
	key := r.Method + " " + r.URL.Path
	if adminOnlyPaths[key] {
		return auth.RoleAdmin
	}
	switch r.Method {
	case http.MethodDelete:
		return auth.RoleAdmin
	case http.MethodPost, http.MethodPatch, http.MethodPut:
		return auth.RoleWrite
	default:
		return auth.RoleRead
	}
}

// checkProjectScope verifies that the authenticated principal (from request
// context) is allowed to access the given project. Returns nil when auth is
// disabled, claims are absent, or AllowedProjects is unrestricted.
func (s *Server) checkProjectScope(r *http.Request, project string) error {
	if s.authenticator == nil || project == "" {
		return nil
	}
	return auth.RequireProject(auth.ClaimsFromContext(r.Context()), project)
}

// needsRedaction reports whether the request came from a low-trust principal
// whose responses should have sensitive memory fields filtered and redacted.
func (s *Server) needsRedaction(r *http.Request) bool {
	claims := auth.ClaimsFromContext(r.Context())
	return claims.IsLowTrust()
}

// redactMemories filters and redacts memory items based on the request's
// auth context. Passes through unchanged for admin/write/nil-claims callers.
func (s *Server) redactMemories(r *http.Request, items []store.MemoryItem) []store.MemoryItem {
	return store.FilterByTrustLevel(items, s.needsRedaction(r))
}

// redactMemory filters and redacts a single memory item.
func (s *Server) redactMemory(r *http.Request, item *store.MemoryItem) *store.MemoryItem {
	if item == nil || !s.needsRedaction(r) {
		return item
	}
	if !store.VisibleTrustLevelsForLowTrust[item.TrustLevel] {
		return nil
	}
	redacted := item.Redacted()
	return &redacted
}

// redactTimelineResult applies trust-level filtering to a timeline result.
func (s *Server) redactTimelineResult(r *http.Request, tr *store.MemoryTimelineResult) *store.MemoryTimelineResult {
	if tr == nil || !s.needsRedaction(r) {
		return tr
	}
	// Redact the anchor
	if !store.VisibleTrustLevelsForLowTrust[tr.Anchor.TrustLevel] {
		// Anchor is blocked; return empty result
		return &store.MemoryTimelineResult{}
	}
	tr.Anchor = tr.Anchor.Redacted()
	tr.Before = store.FilterByTrustLevel(tr.Before, true)
	tr.After = store.FilterByTrustLevel(tr.After, true)
	return tr
}

// logAudit records a handler-level audit entry for a successful mutation.
// Extracts actor from request context claims if available.
func (s *Server) logAudit(r *http.Request, obsID, action, project string) {
	actor := "unknown"
	if claims := auth.ClaimsFromContext(r.Context()); claims != nil {
		actor = claims.Subject
	}
	s.store.LogAudit(obsID, action, actor, "", project)
}

// SetMCPHandler registers a Streamable HTTP MCP handler to be served at /mcp.
// Must be called before the first request (before Start or Handler).
func (s *Server) SetMCPHandler(h http.Handler) {
	s.mcpHandler = h
}

// authMiddleware returns an http.Handler that enforces bearer token authentication
// when s.authenticator is set. The health endpoint is always open.
// When authentication succeeds, the validated Claims are attached to the request
// context and can be retrieved via auth.ClaimsFromContext.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.authenticator == nil {
			next.ServeHTTP(w, r)
			return
		}
		// Health check is always open even when auth is enabled.
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			jsonError(w, http.StatusUnauthorized, "missing or malformed authorization header")
			return
		}
		token := strings.TrimPrefix(header, "Bearer ")
		claims, err := s.authenticator.Authenticate(token)
		if err != nil {
			jsonError(w, http.StatusUnauthorized, "invalid token")
			return
		}
		// Enforce route-level role requirement.
		if err := auth.RequireRole(claims, s.requiredRole(r)); err != nil {
			jsonError(w, http.StatusForbidden, "insufficient permissions")
			return
		}
		ctx := auth.ContextWithClaims(r.Context(), claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// SetSyncStatus configures the sync status provider for the /sync/status endpoint.
func (s *Server) SetSyncStatus(provider SyncStatusProvider) {
	s.syncStatus = provider
}

// notifyWrite calls the onWrite callback if configured (best-effort, non-blocking).
func (s *Server) notifyWrite() {
	if s.onWrite != nil {
		s.onWrite()
	}
}

func (s *Server) Start() error {
	listenFn := s.listen
	if listenFn == nil {
		listenFn = net.Listen
	}
	serveFn := s.serve
	if serveFn == nil {
		serveFn = http.Serve
	}

	var ln net.Listener
	var err error

	// Unix socket first if configured.
	if s.socketPath != "" {
		ln, err = s.listenUnixWithStaleCleanup(listenFn)
		if err == nil {
			log.Printf("[ohara] HTTP server listening on unix socket %s", s.socketPath)
			return serveFn(ln, s.Handler())
		}
		// Socket failed; log and fall through to TCP.
		log.Printf("[ohara] unix socket %s: %v — falling back to TCP", s.socketPath, err)
	}

	// TCP fallback.
	addr := fmt.Sprintf("127.0.0.1:%d", s.port)
	ln, err = listenFn("tcp", addr)
	if err != nil {
		return fmt.Errorf("ohara server: listen %s: %w", addr, err)
	}
	log.Printf("[ohara] HTTP server listening on %s", addr)
	return serveFn(ln, s.Handler())
}

func (s *Server) listenUnixWithStaleCleanup(listenFn func(network, address string) (net.Listener, error)) (net.Listener, error) {
	ln, err := listenFn("unix", s.socketPath)
	if err == nil {
		return ln, nil
	}

	// Common restart case: stale socket file left behind after abrupt exit.
	// If address is in use but nobody can dial it, remove stale file and retry once.
	if !errors.Is(err, syscall.EADDRINUSE) {
		return nil, err
	}

	conn, dialErr := net.DialTimeout("unix", s.socketPath, 200*time.Millisecond)
	if dialErr == nil {
		_ = conn.Close()
		return nil, err // active listener exists; do not remove
	}

	if rmErr := os.Remove(s.socketPath); rmErr != nil && !os.IsNotExist(rmErr) {
		return nil, fmt.Errorf("remove stale unix socket %s: %w", s.socketPath, rmErr)
	}
	return listenFn("unix", s.socketPath)
}

func (s *Server) Handler() http.Handler {
	if s.mcpHandler != nil && !s.mcpRegistered {
		s.mux.Handle("/mcp", s.mcpHandler)
		s.mcpRegistered = true
	}
	if s.authenticator != nil {
		return s.authMiddleware(s.mux)
	}
	return s.mux
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /health", s.handleHealth)

	// Sessions
	s.mux.HandleFunc("POST /sessions", s.handleCreateSession)
	s.mux.HandleFunc("PATCH /sessions/{id}", s.handleEndSession)    // spec-aligned
	s.mux.HandleFunc("POST /sessions/{id}/end", s.handleEndSession) // legacy alias
	s.mux.HandleFunc("GET /sessions/{id}/context", s.handleGetSessionContext)
	s.mux.HandleFunc("GET /sessions/recent", s.handleRecentSessions)
	s.mux.HandleFunc("DELETE /sessions/{id}", s.handleDeleteSession)

	// Prompts
	s.mux.HandleFunc("POST /prompts", s.handleAddPrompt)
	s.mux.HandleFunc("GET /prompts/recent", s.handleRecentPrompts)
	s.mux.HandleFunc("GET /prompts/search", s.handleSearchPrompts)
	s.mux.HandleFunc("DELETE /prompts/{id}", s.handleDeletePrompt)

	// Passive capture
	s.mux.HandleFunc("POST /capture/passive", s.handlePassiveCapture)

	// Context
	s.mux.HandleFunc("GET /context", s.handleContext)

	// Export / Import
	s.mux.HandleFunc("GET /export", s.handleExport)
	s.mux.HandleFunc("POST /import", s.handleImport)

	// Stats
	s.mux.HandleFunc("GET /stats", s.handleStats)

	// Project migration
	s.mux.HandleFunc("POST /projects/migrate", s.handleMigrateProject)

	// Sync status (degraded-state visibility for autosync)
	s.mux.HandleFunc("GET /sync/status", s.handleSyncStatus)

	// Memory Items (Ohara v2 spec) — spec-aligned aliases for the typed memory system.
	s.mux.HandleFunc("POST /memories", s.handleAddMemory)
	s.mux.HandleFunc("GET /memories", s.handleGetMemories)
	s.mux.HandleFunc("GET /memories/search", s.handleSearchMemories)
	s.mux.HandleFunc("GET /memories/{id}", s.handleGetMemory)
	s.mux.HandleFunc("PATCH /memories/{id}", s.handleUpdateMemory)
	s.mux.HandleFunc("GET /memories/{id}/timeline", s.handleMemoryTimeline)
	s.mux.HandleFunc("GET /memories/{id}/revisions", s.handleMemoryRevisions)
	s.mux.HandleFunc("DELETE /memories/{id}", s.handleDeleteMemory)

	// Pack (context pack assembly)
	s.mux.HandleFunc("POST /pack", s.handlePack)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	dbSize := int64(0)
	rows, err := s.store.Query("PRAGMA database_list")
	if err == nil && rows.Next() {
		var seq int
		var name, file string
		if err := rows.Scan(&seq, &name, &file); err == nil && file != "" {
			if info, err := os.Stat(file); err == nil {
				dbSize = info.Size()
			}
		}
		rows.Close()
	}

	memoryCount := int64(0)
	_ = s.store.QueryRow("SELECT COUNT(*) FROM memory_items").Scan(&memoryCount)

	jsonResponse(w, http.StatusOK, map[string]any{
		"status":        "ok",
		"service":       "ohara",
		"version":       "0.1.0",
		"db_size_bytes": dbSize,
		"memory_count":  memoryCount,
	})
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID        string `json:"id"`
		Project   string `json:"project"`    // legacy field name
		ProjectID string `json:"project_id"` // spec field name
		Directory string `json:"directory"`
		ActorID   string `json:"actor_id"` // spec field (not persisted)
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	// Prefer spec field name, fall back to legacy.
	project := body.ProjectID
	if project == "" {
		project = body.Project
	}
	if body.ID == "" || project == "" {
		jsonError(w, http.StatusBadRequest, "id and project are required")
		return
	}

	if err := s.checkProjectScope(r, project); err != nil {
		jsonError(w, http.StatusForbidden, "project not allowed")
		return
	}

	// Check if session already exists to return correct "created" flag.
	_, err := s.store.GetSession(body.ID)
	alreadyExists := err == nil // nil error means session exists

	if err := s.store.CreateSession(body.ID, project, body.Directory); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.logAudit(r, "session-"+body.ID, "create", project)

	s.notifyWrite()
	// Spec response: { "id": "...", "created": bool }
	// Idempotent: if session already existed, returns { "created": false }.
	jsonResponse(w, http.StatusCreated, map[string]any{
		"id":      body.ID,
		"created": !alreadyExists,
	})
}

func (s *Server) handleEndSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var body struct {
		Summary string `json:"summary"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	// Look up session to check project scope before mutating.
	sess, err := s.store.GetSession(id)
	if err != nil {
		jsonError(w, http.StatusNotFound, "session not found")
		return
	}
	if err := s.checkProjectScope(r, sess.Project); err != nil {
		jsonError(w, http.StatusForbidden, "project not allowed")
		return
	}

	if err := s.store.EndSession(id, body.Summary); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.logAudit(r, "session-"+id, "update", sess.Project)

	s.notifyWrite()
	jsonResponse(w, http.StatusOK, map[string]string{"id": id, "status": "completed"})
}

func (s *Server) handleRecentSessions(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	limit := queryInt(r, "limit", 5)

	if err := s.checkProjectScope(r, project); err != nil {
		jsonError(w, http.StatusForbidden, "project not allowed")
		return
	}

	sessions, err := s.store.RecentSessions(project, limit)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, sessions)
}

// handleGetSessionContext implements GET /sessions/:id/context per the spec.
// It returns context from the most recent completed session in the same project,
// which is used during session compaction recovery.
func (s *Server) handleGetSessionContext(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		jsonError(w, http.StatusBadRequest, "session id is required")
		return
	}

	// Get the session to find its project.
	session, err := s.store.GetSession(id)
	if err != nil {
		jsonError(w, http.StatusNotFound, "session not found")
		return
	}

	if err := s.checkProjectScope(r, session.Project); err != nil {
		jsonError(w, http.StatusForbidden, "project not allowed")
		return
	}

	// Get the most recent completed session for the same project (excluding the current one).
	// A session is "completed" if it has an ended_at timestamp.
	recent, err := s.store.RecentSessions(session.Project, 5)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var prevSummary string
	var found bool
	for _, s := range recent {
		// Skip the current session itself; find the most recent completed one.
		if s.ID == id {
			continue
		}
		if s.EndedAt != nil {
			// Found the most recent completed session.
			if s.Summary != nil {
				prevSummary = *s.Summary
			}
			found = true
			break
		}
	}

	if !found {
		// No previous completed session found.
		jsonResponse(w, http.StatusOK, map[string]string{"context": ""})
		return
	}

	jsonResponse(w, http.StatusOK, map[string]string{"context": prevSummary})
}

func (s *Server) handleAddPrompt(w http.ResponseWriter, r *http.Request) {
	var body store.AddPromptParams
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if body.SessionID == "" || body.Content == "" {
		jsonError(w, http.StatusBadRequest, "session_id and content are required")
		return
	}

	if err := s.checkProjectScope(r, body.Project); err != nil {
		jsonError(w, http.StatusForbidden, "project not allowed")
		return
	}

	id, err := s.store.AddPrompt(body)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.logAudit(r, fmt.Sprintf("prompt-%d", id), "create", body.Project)

	s.notifyWrite()
	jsonResponse(w, http.StatusCreated, map[string]any{"id": id, "status": "saved"})
}

func (s *Server) handleRecentPrompts(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	limit := queryInt(r, "limit", 20)

	if err := s.checkProjectScope(r, project); err != nil {
		jsonError(w, http.StatusForbidden, "project not allowed")
		return
	}

	prompts, err := s.store.RecentPrompts(project, limit)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, prompts)
}

func (s *Server) handleSearchPrompts(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		jsonError(w, http.StatusBadRequest, "q parameter is required")
		return
	}

	project := r.URL.Query().Get("project")
	if err := s.checkProjectScope(r, project); err != nil {
		jsonError(w, http.StatusForbidden, "project not allowed")
		return
	}

	prompts, err := s.store.SearchPrompts(
		query,
		project,
		queryInt(r, "limit", 10),
	)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, prompts)
}

func (s *Server) handlePassiveCapture(w http.ResponseWriter, r *http.Request) {
	var body store.PassiveCaptureParams
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if body.SessionID == "" || body.Content == "" {
		jsonError(w, http.StatusBadRequest, "session_id and content are required")
		return
	}

	if err := s.checkProjectScope(r, body.Project); err != nil {
		jsonError(w, http.StatusForbidden, "project not allowed")
		return
	}

	result, err := s.store.PassiveCapture(body)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if result.Saved > 0 {
		s.notifyWrite()
	}
	s.logAudit(r, "session-"+body.SessionID, "create", body.Project)

	jsonResponse(w, http.StatusOK, result)
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		jsonError(w, http.StatusBadRequest, "session id is required")
		return
	}

	// Look up session to check project scope before deleting.
	sess, err := s.store.GetSession(id)
	if err != nil {
		jsonError(w, http.StatusNotFound, "session not found")
		return
	}
	if err := s.checkProjectScope(r, sess.Project); err != nil {
		jsonError(w, http.StatusForbidden, "project not allowed")
		return
	}

	if err := s.store.DeleteSession(id); err != nil {
		switch {
		case strings.Contains(err.Error(), "session has memories"):
			jsonError(w, http.StatusConflict, err.Error())
		case errors.Is(err, store.ErrSessionNotFound):
			jsonError(w, http.StatusNotFound, err.Error())
		default:
			jsonError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	s.logAudit(r, "session-"+id, "delete", sess.Project)

	// local-only delete: do not notify autosync to avoid triggering a pull
	// that could recreate the deleted rows from a remote store.
	jsonResponse(w, http.StatusOK, map[string]string{"id": id, "status": "deleted"})
}

func (s *Server) handleDeletePrompt(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid prompt id")
		return
	}

	// Look up prompt to check project scope before deleting.
	prompt, err := s.store.GetPrompt(id)
	if err != nil {
		jsonError(w, http.StatusNotFound, "prompt not found")
		return
	}
	if err := s.checkProjectScope(r, prompt.Project); err != nil {
		jsonError(w, http.StatusForbidden, "project not allowed")
		return
	}

	if err := s.store.DeletePrompt(id); err != nil {
		if errors.Is(err, store.ErrPromptNotFound) {
			jsonError(w, http.StatusNotFound, err.Error())
			return
		}
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.logAudit(r, fmt.Sprintf("prompt-%d", id), "delete", prompt.Project)

	jsonResponse(w, http.StatusOK, map[string]any{"id": id, "status": "deleted"})
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	data, err := s.store.Export()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=ohara-export.json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(data)
}

func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	// Limit body to 50MB
	r.Body = http.MaxBytesReader(w, r.Body, 50<<20)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "failed to read body: "+err.Error())
		return
	}

	var data store.ExportData
	if err := json.Unmarshal(body, &data); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}

	result, err := s.store.Import(&data)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.notifyWrite()
	s.logAudit(r, "system", "create", "")
	jsonResponse(w, http.StatusOK, result)
}

func (s *Server) handleContext(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	scope := r.URL.Query().Get("scope")

	if err := s.checkProjectScope(r, project); err != nil {
		jsonError(w, http.StatusForbidden, "project not allowed")
		return
	}

	context, err := s.store.FormatContext(project, scope)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, map[string]string{"context": context})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats, err := loadServerStats(s.store)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, stats)
}

func (s *Server) handleSyncStatus(w http.ResponseWriter, r *http.Request) {
	if s.syncStatus == nil {
		jsonResponse(w, http.StatusOK, map[string]any{
			"enabled": false,
			"message": "background sync is not configured",
		})
		return
	}

	status := s.syncStatus.Status()
	jsonResponse(w, http.StatusOK, map[string]any{
		"enabled":              true,
		"phase":                status.Phase,
		"last_error":           status.LastError,
		"consecutive_failures": status.ConsecutiveFailures,
		"backoff_until":        status.BackoffUntil,
		"last_sync_at":         status.LastSyncAt,
	})
}

func (s *Server) handleMigrateProject(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<10) // 1 KB max
	var body struct {
		OldProject string `json:"old_project"`
		NewProject string `json:"new_project"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if body.OldProject == "" || body.NewProject == "" {
		jsonError(w, http.StatusBadRequest, "old_project and new_project are required")
		return
	}
	if body.OldProject == body.NewProject {
		jsonResponse(w, http.StatusOK, map[string]any{"status": "skipped", "reason": "names are identical"})
		return
	}

	// Both source and target projects must be in the principal's allowlist.
	if err := s.checkProjectScope(r, body.OldProject); err != nil {
		jsonError(w, http.StatusForbidden, "project not allowed")
		return
	}
	if err := s.checkProjectScope(r, body.NewProject); err != nil {
		jsonError(w, http.StatusForbidden, "project not allowed")
		return
	}

	result, err := s.store.MigrateProject(body.OldProject, body.NewProject)
	if err != nil {
		log.Printf("[ohara] project migration failed: %v", err)
		jsonError(w, http.StatusInternalServerError, "migration failed")
		return
	}

	s.logAudit(r, "project-"+body.OldProject, "update", body.NewProject)

	if !result.Migrated {
		jsonResponse(w, http.StatusOK, map[string]any{"status": "skipped", "reason": "nothing to migrate"})
		return
	}

	s.notifyWrite()

	jsonResponse(w, http.StatusOK, map[string]any{
		"status":            "migrated",
		"sessions_updated":  result.SessionsUpdated,
		"prompts_updated":   result.PromptsUpdated,
	})
}

func (s *Server) handleGetMemories(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	scope := r.URL.Query().Get("scope")
	kind := r.URL.Query().Get("kind")
	status := r.URL.Query().Get("status")
	limit := queryInt(r, "limit", 20)

	if err := s.checkProjectScope(r, projectID); err != nil {
		jsonError(w, http.StatusForbidden, "project not allowed")
		return
	}

	items, err := s.store.GetMemories(projectID, scope, kind, status, limit)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if items == nil {
		items = []store.MemoryItem{}
	}
	jsonResponse(w, http.StatusOK, s.redactMemories(r, items))
}

func (s *Server) handleSearchMemories(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		jsonError(w, http.StatusBadRequest, "q parameter is required")
		return
	}

	projectID := r.URL.Query().Get("project_id")
	scope := r.URL.Query().Get("scope")
	kind := r.URL.Query().Get("kind")
	status := r.URL.Query().Get("status")
	limit := queryInt(r, "limit", 10)

	if err := s.checkProjectScope(r, projectID); err != nil {
		jsonError(w, http.StatusForbidden, "project not allowed")
		return
	}

	items, err := s.store.SearchMemories(query, projectID, scope, kind, "", status, limit, "")
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if items == nil {
		items = []store.MemoryItem{}
	}
	jsonResponse(w, http.StatusOK, map[string]any{
		"results": s.redactMemories(r, items),
		"method":  "fts5",
	})
}

func (s *Server) handleGetMemory(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid memory id")
		return
	}

	item, err := s.store.GetMemory(id)
	if err != nil {
		jsonError(w, http.StatusNotFound, "memory not found")
		return
	}

	if err := s.checkProjectScope(r, item.ProjectID); err != nil {
		jsonError(w, http.StatusForbidden, "project not allowed")
		return
	}

	if redacted := s.redactMemory(r, item); redacted != nil {
		jsonResponse(w, http.StatusOK, redacted)
	} else {
		jsonError(w, http.StatusNotFound, "memory not found")
	}
}

func (s *Server) handleUpdateMemory(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid memory id")
		return
	}

	var body store.UpdateMemoryParams
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if body.Title == nil && body.Body == nil && body.Tags == nil && body.Status == nil && body.SupersededBy == nil {
		jsonError(w, http.StatusBadRequest, "at least one field is required")
		return
	}

	// Validate status if provided
	if body.Status != nil {
		switch *body.Status {
		case store.MemoryStatusActive, store.MemoryStatusArchived, store.MemoryStatusSuperseded:
			// Valid
		default:
			jsonError(w, http.StatusBadRequest, "status must be active, archived, or superseded")
			return
		}
	}

	// Check project scope before mutating.
	item, err := s.store.GetMemory(id)
	if err != nil {
		jsonError(w, http.StatusNotFound, "memory not found")
		return
	}
	if err := s.checkProjectScope(r, item.ProjectID); err != nil {
		jsonError(w, http.StatusForbidden, "project not allowed")
		return
	}

	updated, err := s.store.UpdateMemory(id, body)
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}

	s.logAudit(r, fmt.Sprintf("mem-%d", id), "update", item.ProjectID)
	s.notifyWrite()

	var revisionID int64
	_ = s.store.QueryRow("SELECT id FROM memory_revisions WHERE memory_id = ? ORDER BY id DESC LIMIT 1", id).Scan(&revisionID)

	jsonResponse(w, http.StatusOK, map[string]any{
		"id":          updated.ID,
		"revision_id": revisionID,
	})
}

func (s *Server) handleMemoryTimeline(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid memory id")
		return
	}

	count := queryInt(r, "count", 3)

	// Check project scope before serving.
	item, err := s.store.GetMemory(id)
	if err != nil {
		jsonError(w, http.StatusNotFound, "memory not found")
		return
	}
	if err := s.checkProjectScope(r, item.ProjectID); err != nil {
		jsonError(w, http.StatusForbidden, "project not allowed")
		return
	}

	result, err := s.store.MemoryTimeline(id, count)
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, s.redactTimelineResult(r, result))
}

func (s *Server) handleMemoryRevisions(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid memory id")
		return
	}

	// Check project scope before serving.
	item, err := s.store.GetMemory(id)
	if err != nil {
		jsonError(w, http.StatusNotFound, "memory not found")
		return
	}
	if err := s.checkProjectScope(r, item.ProjectID); err != nil {
		jsonError(w, http.StatusForbidden, "project not allowed")
		return
	}

	revisions, err := s.store.GetMemoryRevisions(id)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if revisions == nil {
		revisions = []store.MemoryRevision{}
	}
	jsonResponse(w, http.StatusOK, revisions)
}

// handleDeleteMemory implements DELETE /memories/:id per the Ohara v2 spec.
// Memories are never hard-deleted; they are archived via PATCH. This handler
// always returns 405 Method Not Allowed to enforce that policy explicitly.
func (s *Server) handleDeleteMemory(w http.ResponseWriter, r *http.Request) {
	// Deliberately do nothing with the path value — we never delete memories.
	_ = r.PathValue("id")
	jsonError(w, http.StatusMethodNotAllowed, "memories cannot be deleted; use PATCH to set status to archived")
}

func (s *Server) handlePack(w http.ResponseWriter, r *http.Request) {
	var body store.PackParams
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if body.ProjectID == "" {
		jsonError(w, http.StatusBadRequest, "project_id is required")
		return
	}

	if err := s.checkProjectScope(r, body.ProjectID); err != nil {
		jsonError(w, http.StatusForbidden, "project not allowed")
		return
	}

	// Use config-driven defaults when budget is not specified by the caller.
	defaultBudget := s.packConfig.DefaultBudgetTokens
	if defaultBudget <= 0 {
		defaultBudget = 400 // fallback to spec-safe default
	}
	maxBudget := s.packConfig.MaxBudgetTokens
	if maxBudget <= 0 {
		maxBudget = 800 // fallback to spec-safe maximum
	}
	if body.BudgetTokens <= 0 {
		body.BudgetTokens = defaultBudget
	}
	if body.BudgetTokens > maxBudget {
		body.BudgetTokens = maxBudget
	}

	result, err := s.store.BuildPack(body)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, store.FilterByTrustLevelPack(result, s.needsRedaction(r)))
}

// handleAddMemory implements POST /memories per the Ohara v2 spec.
// It creates a memory item, runs conflict detection for decision/pattern/config kinds,
// and returns the new memory ID along with optional conflict metadata.
func (s *Server) handleAddMemory(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProjectID        string   `json:"project_id"`
		Kind             string   `json:"kind"`
		Scope            string   `json:"scope"`
		Title            string   `json:"title"`
		Body             string   `json:"body"`
		Tags             []string `json:"tags"`
		Source           string   `json:"source"`
		ActorID          string   `json:"actor_id"`
		Domain           string   `json:"domain"`
		EvidenceJSON     string   `json:"evidence_json"`
		AppliesToJSON    string   `json:"applies_to_json"`
		RelatedJSON      string   `json:"related_json"`
		SessionID        string   `json:"session_id"`
		TrustLevel       string   `json:"trust_level"`
		Classification   string   `json:"classification"`
		WrittenBy        string   `json:"written_by"`
		ExpiresAt        string   `json:"expires_at"`
		TriggerCondition string   `json:"trigger_condition"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if body.ProjectID == "" || body.Kind == "" || body.Title == "" {
		jsonError(w, http.StatusBadRequest, "project_id, kind, and title are required")
		return
	}

	if err := s.checkProjectScope(r, body.ProjectID); err != nil {
		jsonError(w, http.StatusForbidden, "project not allowed")
		return
	}

	// Conflict detection for decision/pattern/config kinds (non-blocking).
	var conflictInfo *store.ConflictInfo
	detectKinds := map[string]bool{"decision": true, "pattern": true, "config": true}
	if detectKinds[body.Kind] && s.conflictConfig.Enabled != ConflictEnabledOff {
		ci, err := s.store.DetectConflict(store.AddMemoryParams{
			ProjectID: body.ProjectID,
			Kind:      body.Kind,
			Title:     body.Title,
			Body:      body.Body,
		})
		if err == nil && ci != nil {
			// Apply server-level threshold: suppress if below configured minimum.
			threshold := s.conflictConfig.Threshold
			if threshold <= 0 {
				threshold = 0.6 // default matching DetectConflict's internal threshold
			}
			if ci.OverlapScore >= threshold {
				conflictInfo = ci
			}
		}
	}

	memID, err := s.store.AddMemory(store.AddMemoryParams{
		ProjectID:        body.ProjectID,
		Kind:             body.Kind,
		Scope:            body.Scope,
		Title:            body.Title,
		Body:             body.Body,
		Tags:             body.Tags,
		Source:           body.Source,
		ActorID:          body.ActorID,
		Domain:           body.Domain,
		EvidenceJSON:     body.EvidenceJSON,
		AppliesToJSON:    body.AppliesToJSON,
		RelatedJSON:      body.RelatedJSON,
		SessionID:        body.SessionID,
		TrustLevel:       body.TrustLevel,
		Classification:   body.Classification,
		WrittenBy:        body.WrittenBy,
		ExpiresAt:        body.ExpiresAt,
		TriggerCondition: body.TriggerCondition,
	})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.logAudit(r, fmt.Sprintf("mem-%d", memID), "create", body.ProjectID)
	s.notifyWrite()

	resp := map[string]any{"id": memID}
	if conflictInfo != nil && conflictInfo.ExistingMemory != nil {
		resp["conflict"] = map[string]any{
			"existing_id":    conflictInfo.ExistingMemory.ID,
			"existing_title": conflictInfo.ExistingMemory.Title,
			"similarity":     conflictInfo.OverlapScore,
			"message":        conflictInfo.Message,
		}
	}
	jsonResponse(w, http.StatusCreated, resp)
}

func jsonResponse(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	jsonResponse(w, status, map[string]string{"error": msg})
}

func queryInt(r *http.Request, key string, defaultVal int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return n
}

func queryBool(r *http.Request, key string, defaultVal bool) bool {
	v := r.URL.Query().Get(key)
	if v == "" {
		return defaultVal
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return defaultVal
	}
	return b
}
