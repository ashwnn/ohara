package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/ashwnn/ohara/internal/store"
)

type stubListener struct{}

func (stubListener) Accept() (net.Conn, error) { return nil, errors.New("not used") }
func (stubListener) Close() error              { return nil }
func (stubListener) Addr() net.Addr            { return &net.TCPAddr{} }

func TestStartReturnsListenError(t *testing.T) {
	s := New(nil, 7777)
	s.listen = func(network, address string) (net.Listener, error) {
		return nil, errors.New("listen failed")
	}

	err := s.Start()
	if err == nil {
		t.Fatalf("expected start to fail on listen error")
	}
}

func TestStartUsesInjectedServe(t *testing.T) {
	s := New(&store.Store{}, 7777)
	s.listen = func(network, address string) (net.Listener, error) {
		return stubListener{}, nil
	}
	s.serve = func(ln net.Listener, h http.Handler) error {
		if ln == nil || h == nil {
			t.Fatalf("expected listener and handler to be provided")
		}
		return errors.New("serve stopped")
	}

	err := s.Start()
	if err == nil || err.Error() != "serve stopped" {
		t.Fatalf("expected propagated serve error, got %v", err)
	}
}

func newServerTestStore(t *testing.T) *store.Store {
	t.Helper()
	cfg, err := store.DefaultConfig()
	if err != nil {
		t.Fatalf("DefaultConfig: %v", err)
	}
	cfg.DataDir = t.TempDir()

	s, err := store.New(cfg)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() {
		_ = s.Close()
	})
	return s
}

func TestStartUsesDefaultListenWhenListenNil(t *testing.T) {
	s := New(newServerTestStore(t), 0)
	s.listen = nil
	s.serve = func(ln net.Listener, h http.Handler) error {
		if ln == nil || h == nil {
			t.Fatalf("expected non-nil listener and handler")
		}
		_ = ln.Close()
		return errors.New("serve stopped")
	}

	err := s.Start()
	if err == nil || err.Error() != "serve stopped" {
		t.Fatalf("expected propagated serve error, got %v", err)
	}
}

func TestStartUsesDefaultServeWhenServeNil(t *testing.T) {
	s := New(newServerTestStore(t), 7777)
	s.listen = func(network, address string) (net.Listener, error) {
		return stubListener{}, nil
	}
	s.serve = nil

	err := s.Start()
	if err == nil {
		t.Fatalf("expected start to fail when default http.Serve receives failing listener")
	}
}

func TestAdditionalServerErrorBranches(t *testing.T) {
	st := newServerTestStore(t)
	srv := New(st, 0)
	h := srv.Handler()

	createReq := httptest.NewRequest(http.MethodPost, "/sessions", strings.NewReader(`{"id":"s-test","project":"ohara"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	h.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected session create 201, got %d", createRec.Code)
	}

	getBadIDReq := httptest.NewRequest(http.MethodGet, "/memories/not-a-number", nil)
	getBadIDRec := httptest.NewRecorder()
	h.ServeHTTP(getBadIDRec, getBadIDReq)
	if getBadIDRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid memory id, got %d", getBadIDRec.Code)
	}

	updateNotFoundReq := httptest.NewRequest(http.MethodPatch, "/memories/99999", strings.NewReader(`{"title":"updated"}`))
	updateNotFoundReq.Header.Set("Content-Type", "application/json")
	updateNotFoundRec := httptest.NewRecorder()
	h.ServeHTTP(updateNotFoundRec, updateNotFoundReq)
	if updateNotFoundRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 updating missing memory, got %d", updateNotFoundRec.Code)
	}

	promptBadJSONReq := httptest.NewRequest(http.MethodPost, "/prompts", strings.NewReader("{"))
	promptBadJSONReq.Header.Set("Content-Type", "application/json")
	promptBadJSONRec := httptest.NewRecorder()
	h.ServeHTTP(promptBadJSONRec, promptBadJSONReq)
	if promptBadJSONRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid prompt json, got %d", promptBadJSONRec.Code)
	}

	oversizeBody := bytes.Repeat([]byte("a"), 50<<20+1)
	importTooLargeReq := httptest.NewRequest(http.MethodPost, "/import", bytes.NewReader(oversizeBody))
	importTooLargeReq.Header.Set("Content-Type", "application/json")
	importTooLargeRec := httptest.NewRecorder()
	h.ServeHTTP(importTooLargeRec, importTooLargeReq)
	if importTooLargeRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for oversize import body, got %d", importTooLargeRec.Code)
	}

	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	validImport, err := json.Marshal(store.ExportData{Version: "0.1.0", ExportedAt: "now"})
	if err != nil {
		t.Fatalf("marshal import payload: %v", err)
	}
	importClosedReq := httptest.NewRequest(http.MethodPost, "/import", bytes.NewReader(validImport))
	importClosedReq.Header.Set("Content-Type", "application/json")
	importClosedRec := httptest.NewRecorder()
	h.ServeHTTP(importClosedRec, importClosedReq)
	if importClosedRec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 importing on closed store, got %d", importClosedRec.Code)
	}
}

// ─── Sync Status Tests ───────────────────────────────────────────────────────

// stubSyncStatusProvider is a fake SyncStatusProvider for tests.
type stubSyncStatusProvider struct {
	status SyncStatus
}

func (s *stubSyncStatusProvider) Status() SyncStatus {
	return s.status
}

func TestSyncStatusNotConfigured(t *testing.T) {
	srv := New(newServerTestStore(t), 0)
	// No sync status provider set — should return enabled: false.
	req := httptest.NewRequest(http.MethodGet, "/sync/status", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["enabled"] != false {
		t.Fatalf("expected enabled=false when no provider, got %v", resp["enabled"])
	}
}

func TestSyncStatusHealthy(t *testing.T) {
	now := time.Now()
	provider := &stubSyncStatusProvider{
		status: SyncStatus{
			Phase:      "healthy",
			LastSyncAt: &now,
		},
	}

	srv := New(newServerTestStore(t), 0)
	srv.SetSyncStatus(provider)

	req := httptest.NewRequest(http.MethodGet, "/sync/status", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["enabled"] != true {
		t.Fatalf("expected enabled=true, got %v", resp["enabled"])
	}
	if resp["phase"] != "healthy" {
		t.Fatalf("expected phase=healthy, got %v", resp["phase"])
	}
}

func TestSyncStatusDegraded(t *testing.T) {
	backoff := time.Now().Add(5 * time.Minute)
	provider := &stubSyncStatusProvider{
		status: SyncStatus{
			Phase:               "push_failed",
			LastError:           "network timeout",
			ConsecutiveFailures: 3,
			BackoffUntil:        &backoff,
		},
	}

	srv := New(newServerTestStore(t), 0)
	srv.SetSyncStatus(provider)

	req := httptest.NewRequest(http.MethodGet, "/sync/status", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["phase"] != "push_failed" {
		t.Fatalf("expected phase=push_failed, got %v", resp["phase"])
	}
	if resp["last_error"] != "network timeout" {
		t.Fatalf("expected last_error=network timeout, got %v", resp["last_error"])
	}
	if resp["consecutive_failures"] != float64(3) {
		t.Fatalf("expected consecutive_failures=3, got %v", resp["consecutive_failures"])
	}
}

// ─── OnWrite Notification Tests ──────────────────────────────────────────────

func TestOnWriteCalledAfterSuccessfulWrites(t *testing.T) {
	st := newServerTestStore(t)
	srv := New(st, 0)
	h := srv.Handler()

	var writeCount atomic.Int32
	srv.SetOnWrite(func() {
		writeCount.Add(1)
	})

	// Create session → should trigger onWrite.
	createReq := httptest.NewRequest(http.MethodPost, "/sessions",
		strings.NewReader(`{"id":"s-test","project":"ohara"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	h.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("session create: expected 201, got %d", createRec.Code)
	}
	if writeCount.Load() != 1 {
		t.Fatalf("expected 1 onWrite after session create, got %d", writeCount.Load())
	}

	// End session → should trigger onWrite.
	endReq := httptest.NewRequest(http.MethodPost, "/sessions/s-test/end",
		strings.NewReader(`{"summary":"done"}`))
	endReq.Header.Set("Content-Type", "application/json")
	endRec := httptest.NewRecorder()
	h.ServeHTTP(endRec, endReq)
	if endRec.Code != http.StatusOK {
		t.Fatalf("session end: expected 200, got %d", endRec.Code)
	}
	if writeCount.Load() != 2 {
		t.Fatalf("expected 2 onWrite after session end, got %d", writeCount.Load())
	}

	// Add memory → should trigger onWrite.
	memBody := `{"project_id":"ohara","kind":"decision","title":"Test","body":"test content","scope":"project","session_id":"s-test","actor_id":"test"}`
	memReq := httptest.NewRequest(http.MethodPost, "/memories",
		strings.NewReader(memBody))
	memReq.Header.Set("Content-Type", "application/json")
	memRec := httptest.NewRecorder()
	h.ServeHTTP(memRec, memReq)
	if memRec.Code != http.StatusCreated {
		t.Fatalf("add memory: expected 201, got %d", memRec.Code)
	}
	if writeCount.Load() != 3 {
		t.Fatalf("expected 3 onWrite after add memory, got %d", writeCount.Load())
	}

	// Add prompt → should trigger onWrite.
	promptBody := `{"session_id":"s-test","content":"what did we do?"}`
	promptReq := httptest.NewRequest(http.MethodPost, "/prompts",
		strings.NewReader(promptBody))
	promptReq.Header.Set("Content-Type", "application/json")
	promptRec := httptest.NewRecorder()
	h.ServeHTTP(promptRec, promptReq)
	if promptRec.Code != http.StatusCreated {
		t.Fatalf("add prompt: expected 201, got %d", promptRec.Code)
	}
	if writeCount.Load() != 4 {
		t.Fatalf("expected 4 onWrite after add prompt, got %d", writeCount.Load())
	}
}

func TestOnWriteNotCalledOnReadOperations(t *testing.T) {
	st := newServerTestStore(t)
	srv := New(st, 0)
	h := srv.Handler()

	var writeCount atomic.Int32
	srv.SetOnWrite(func() {
		writeCount.Add(1)
	})

	// GET /health → read-only, no onWrite.
	healthReq := httptest.NewRequest(http.MethodGet, "/health", nil)
	healthRec := httptest.NewRecorder()
	h.ServeHTTP(healthRec, healthReq)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("health: expected 200, got %d", healthRec.Code)
	}

	// GET /stats → read-only, no onWrite.
	statsReq := httptest.NewRequest(http.MethodGet, "/stats", nil)
	statsRec := httptest.NewRecorder()
	h.ServeHTTP(statsRec, statsReq)

	// GET /sync/status → read-only, no onWrite.
	syncReq := httptest.NewRequest(http.MethodGet, "/sync/status", nil)
	syncRec := httptest.NewRecorder()
	h.ServeHTTP(syncRec, syncReq)

	if writeCount.Load() != 0 {
		t.Fatalf("expected 0 onWrite calls for read operations, got %d", writeCount.Load())
	}
}

func TestOnWriteNotCalledOnFailedWrites(t *testing.T) {
	st := newServerTestStore(t)
	srv := New(st, 0)
	h := srv.Handler()

	var writeCount atomic.Int32
	srv.SetOnWrite(func() {
		writeCount.Add(1)
	})

	// POST /memories with bad JSON → should NOT trigger onWrite.
	badReq := httptest.NewRequest(http.MethodPost, "/memories",
		strings.NewReader(`{invalid`))
	badReq.Header.Set("Content-Type", "application/json")
	badRec := httptest.NewRecorder()
	h.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad json, got %d", badRec.Code)
	}

	// POST /memories with missing required fields → should NOT trigger onWrite.
	missingReq := httptest.NewRequest(http.MethodPost, "/memories",
		strings.NewReader(`{"project_id":"ohara"}`))
	missingReq.Header.Set("Content-Type", "application/json")
	missingRec := httptest.NewRecorder()
	h.ServeHTTP(missingRec, missingReq)
	if missingRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing fields, got %d", missingRec.Code)
	}

	if writeCount.Load() != 0 {
		t.Fatalf("expected 0 onWrite calls for failed writes, got %d", writeCount.Load())
	}
}

func TestHandleStatsReturnsInternalServerErrorOnLoaderError(t *testing.T) {
	prev := loadServerStats
	loadServerStats = func(s *store.Store) (any, error) {
		return nil, errors.New("stats unavailable")
	}
	t.Cleanup(func() {
		loadServerStats = prev
	})

	s := New(newServerTestStore(t), 0)
	req := httptest.NewRequest(http.MethodGet, "/stats", nil)
	rec := httptest.NewRecorder()

	s.handleStats(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 stats response, got %d", rec.Code)
	}
}

// ─── DELETE /sessions/{id} tests ─────────────────────────────────────────────

func TestHandleDeleteSession_Success(t *testing.T) {
	st := newServerTestStore(t)
	srv := New(st, 0)
	h := srv.Handler()

	// Create an empty session.
	createReq := httptest.NewRequest(http.MethodPost, "/sessions",
		strings.NewReader(`{"id":"sess-del","project":"proj"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	h.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 creating session, got %d", createRec.Code)
	}

	// Delete it.
	delReq := httptest.NewRequest(http.MethodDelete, "/sessions/sess-del", nil)
	delRec := httptest.NewRecorder()
	h.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusOK {
		t.Fatalf("expected 200 deleting empty session, got %d: %s", delRec.Code, delRec.Body.String())
	}
}

func TestHandleDeleteSession_NotFound(t *testing.T) {
	srv := New(newServerTestStore(t), 0)
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodDelete, "/sessions/does-not-exist", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// ─── DELETE /prompts/{id} tests ───────────────────────────────────────────────

func TestHandleDeletePrompt_Success(t *testing.T) {
	st := newServerTestStore(t)
	srv := New(st, 0)
	h := srv.Handler()

	if err := st.CreateSession("sess-p", "proj", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	promptID, err := st.AddPrompt(store.AddPromptParams{
		SessionID: "sess-p",
		Content:   "delete me",
		Project:   "proj",
	})
	if err != nil {
		t.Fatalf("add prompt: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/prompts/%d", promptID), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 deleting prompt, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleDeletePrompt_NotFound(t *testing.T) {
	srv := New(newServerTestStore(t), 0)
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodDelete, "/prompts/999999", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHandleDeletePrompt_BadID(t *testing.T) {
	srv := New(newServerTestStore(t), 0)
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodDelete, "/prompts/not-a-number", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid prompt id, got %d", rec.Code)
	}
}

// ─── Memory conflict detection tests ─────────────────────────────────────────

func TestHandleAddMemory_NoConflict_ReturnsCreated(t *testing.T) {
	st := newServerTestStore(t)
	srv := New(st, 0)
	h := srv.Handler()

	body := `{"project_id":"ohara","kind":"decision","title":"Auth decision","body":"Use JWT"}`
	req := httptest.NewRequest(http.MethodPost, "/memories", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleAddMemory_Conflict_ReturnsCreatedWithConflict(t *testing.T) {
	st := newServerTestStore(t)
	srv := New(st, 0)
	h := srv.Handler()

	// Seed an existing decision memory
	existingID, err := st.AddMemory(store.AddMemoryParams{
		ProjectID: "ohara",
		Kind:      store.MemoryKindDecision,
		Title:     "Auth decision: Use JWT for session management",
		Body:      "JWT is stateless",
	})
	if err != nil {
		t.Fatalf("seed memory: %v", err)
	}

	// Attempt to add a conflicting decision memory — save still succeeds with conflict metadata
	body := `{"project_id":"ohara","kind":"decision","title":"Auth decision: JWT for session management","body":"Alternative approach"}`
	req := httptest.NewRequest(http.MethodPost, "/memories", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for conflicting memory (save succeeds), got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify response body contains top-level conflict metadata (spec-compliant shape)
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp["id"] == nil {
		t.Error("expected id in response")
	}
	conflict, ok := resp["conflict"].(map[string]any)
	if !ok {
		t.Fatal("expected top-level conflict field in response for conflict")
	}
	// Verify spec-compliant field names
	if conflict["existing_id"] == nil {
		t.Error("expected existing_id in conflict metadata")
	}
	if conflict["existing_title"] == nil {
		t.Error("expected existing_title in conflict metadata")
	}
	if conflict["similarity"] == nil {
		t.Error("expected similarity in conflict metadata")
	}
	if conflict["message"] == nil {
		t.Error("expected message in conflict metadata")
	}
	// Verify the existing memory ID is correct
	if conflict["existing_id"].(float64) != float64(existingID) {
		t.Errorf("expected existing_id %d, got %v", existingID, conflict["existing_id"])
	}
}

func TestHandleAddMemory_Conflict_SaveActuallyPersisted(t *testing.T) {
	// Verifies that conflicting memories are actually saved, not silently dropped.
	st := newServerTestStore(t)
	srv := New(st, 0)
	h := srv.Handler()

	// Seed an existing decision memory
	_, err := st.AddMemory(store.AddMemoryParams{
		ProjectID: "ohara",
		Kind:      store.MemoryKindDecision,
		Title:     "Auth decision: Use JWT for session management",
		Body:      "JWT is stateless",
	})
	if err != nil {
		t.Fatalf("seed memory: %v", err)
	}

	// Attempt to add a conflicting decision memory
	body := `{"project_id":"ohara","kind":"decision","title":"Auth decision: JWT for session management","body":"Alternative approach"}`
	req := httptest.NewRequest(http.MethodPost, "/memories", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	// Parse response to get the new memory ID
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	newIDFloat, ok := resp["id"].(float64)
	if !ok {
		t.Fatal("expected id as number in response")
	}
	newID := int64(newIDFloat)

	// Verify the new memory was actually persisted
	saved, err := st.GetMemory(newID)
	if err != nil {
		t.Fatalf("get saved memory: %v", err)
	}
	if saved.Title != "Auth decision: JWT for session management" {
		t.Errorf("expected saved title, got %q", saved.Title)
	}
	if saved.Body != "Alternative approach" {
		t.Errorf("expected saved body, got %q", saved.Body)
	}

	// Verify we now have 2 decision memories
	memories, err := st.GetMemories("ohara", "", "decision", "active", 10)
	if err != nil {
		t.Fatalf("get memories: %v", err)
	}
	if len(memories) != 2 {
		t.Fatalf("expected 2 decision memories after conflict save, got %d", len(memories))
	}
}

func TestHandleAddMemory_BugfixKind_BypassesConflict(t *testing.T) {
	st := newServerTestStore(t)
	srv := New(st, 0)
	h := srv.Handler()

	// Seed an existing bugfix memory
	_, err := st.AddMemory(store.AddMemoryParams{
		ProjectID: "ohara",
		Kind:      store.MemoryKindBugfix,
		Title:     "Fix tokenizer panic on edge case",
		Body:      "Added null check",
	})
	if err != nil {
		t.Fatalf("seed memory: %v", err)
	}

	// Same title, different memory — bugfix kind bypasses conflict detection
	body := `{"project_id":"ohara","kind":"bugfix","title":"Fix tokenizer panic on edge case","body":"Different fix"}`
	req := httptest.NewRequest(http.MethodPost, "/memories", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("bugfix kind: expected 201 (no conflict check), got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleAddMemory_PatternConflict_ReturnsCreatedWithConflict(t *testing.T) {
	st := newServerTestStore(t)
	srv := New(st, 0)
	h := srv.Handler()

	// Seed an existing pattern
	_, err := st.AddMemory(store.AddMemoryParams{
		ProjectID: "ohara",
		Kind:      store.MemoryKindPattern,
		Title:     "Error handling: retry pattern for API calls",
		Body:      "Retry up to 3 times",
	})
	if err != nil {
		t.Fatalf("seed memory: %v", err)
	}

	// Conflicting pattern — save succeeds with conflict metadata
	body := `{"project_id":"ohara","kind":"pattern","title":"Error handling: retry pattern for API requests","body":"Use exponential backoff"}`
	req := httptest.NewRequest(http.MethodPost, "/memories", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for conflicting pattern (save succeeds), got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp["conflict"] == nil {
		t.Fatal("expected top-level conflict field for conflicting pattern")
	}
}

// ─── Conflict Config tests ───────────────────────────────────────────────────────

func TestHandleAddMemory_ConflictConfigDisabled_NoConflictReturned(t *testing.T) {
	st := newServerTestStore(t)
	// Explicitly disable conflict detection.
	srv := New(st, 0, WithConflictConfig(ConflictConfig{Enabled: ConflictEnabledOff, Threshold: 0.6}))
	h := srv.Handler()

	// Seed an existing decision memory.
	_, err := st.AddMemory(store.AddMemoryParams{
		ProjectID: "ohara",
		Kind:      store.MemoryKindDecision,
		Title:     "Auth decision: Use JWT for session management",
		Body:      "JWT is stateless",
	})
	if err != nil {
		t.Fatalf("seed memory: %v", err)
	}

	// Attempt to add a conflicting decision memory — but conflict detection is disabled.
	body := `{"project_id":"ohara","kind":"decision","title":"Auth decision: JWT for session management","body":"Alternative approach"}`
	req := httptest.NewRequest(http.MethodPost, "/memories", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 (save still succeeds), got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp["id"] == nil {
		t.Error("expected id in response")
	}
	if resp["conflict"] != nil {
		t.Error("expected no conflict field when ConflictConfig.Enabled=false, got conflict metadata")
	}
}

func TestHandleAddMemory_ConflictConfigEnabled_SkipsLowConfidenceConflicts(t *testing.T) {
	st := newServerTestStore(t)
	// Set a very high threshold (0.99) so even high-overlap conflicts are suppressed.
	srv := New(st, 0, WithConflictConfig(ConflictConfig{Enabled: ConflictEnabledOn, Threshold: 0.99}))
	h := srv.Handler()

	// Seed an existing decision memory.
	existingID, err := st.AddMemory(store.AddMemoryParams{
		ProjectID: "ohara",
		Kind:      store.MemoryKindDecision,
		Title:     "Auth decision: Use JWT for session management",
		Body:      "JWT is stateless",
	})
	if err != nil {
		t.Fatalf("seed memory: %v", err)
	}

	// Attempt to add a conflicting decision memory.
	body := `{"project_id":"ohara","kind":"decision","title":"Auth decision: JWT for session management","body":"Alternative approach"}`
	req := httptest.NewRequest(http.MethodPost, "/memories", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 (save succeeds), got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	// Save succeeded with a different ID.
	if resp["id"] == nil {
		t.Error("expected id in response")
	}
	// Conflict is suppressed because threshold (0.99) is above the actual score (~0.67).
	if resp["conflict"] != nil {
		t.Error("expected no conflict when score < threshold, got conflict metadata")
	}
	// Verify we have 2 decision memories (the new one was saved).
	memories, err := st.GetMemories("ohara", "", "decision", "active", 10)
	if err != nil {
		t.Fatalf("get memories: %v", err)
	}
	if len(memories) != 2 {
		t.Fatalf("expected 2 decision memories, got %d", len(memories))
	}
	_ = existingID // referenced for documentation; not asserted here.
}

func TestHandleAddMemory_ConflictConfigDefaultThreshold_ReportsConflicts(t *testing.T) {
	st := newServerTestStore(t)
	// Enabled with default threshold (0.6 via server code fallback).
	srv := New(st, 0, WithConflictConfig(ConflictConfig{Enabled: ConflictEnabledOn, Threshold: 0.6}))
	h := srv.Handler()

	// Seed an existing decision memory.
	_, err := st.AddMemory(store.AddMemoryParams{
		ProjectID: "ohara",
		Kind:      store.MemoryKindDecision,
		Title:     "Auth decision: Use JWT for session management",
		Body:      "JWT is stateless",
	})
	if err != nil {
		t.Fatalf("seed memory: %v", err)
	}

	// Add a conflicting decision memory — threshold matches the algorithm default.
	body := `{"project_id":"ohara","kind":"decision","title":"Auth decision: JWT for session management","body":"Alternative approach"}`
	req := httptest.NewRequest(http.MethodPost, "/memories", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp["conflict"] == nil {
		t.Error("expected conflict metadata with Enabled=true and Threshold=0.6")
	}
}

func TestHandleAddMemory_ConflictConfigEnabled_ZeroThresholdUsesDefault(t *testing.T) {
	st := newServerTestStore(t)
	// Enabled but Threshold=0 — the server code falls back to 0.6 default.
	srv := New(st, 0, WithConflictConfig(ConflictConfig{Enabled: ConflictEnabledOn, Threshold: 0.0}))
	h := srv.Handler()

	// Seed an existing decision memory.
	_, err := st.AddMemory(store.AddMemoryParams{
		ProjectID: "ohara",
		Kind:      store.MemoryKindDecision,
		Title:     "Auth decision: Use JWT for session management",
		Body:      "JWT is stateless",
	})
	if err != nil {
		t.Fatalf("seed memory: %v", err)
	}

	// Add a conflicting decision memory.
	body := `{"project_id":"ohara","kind":"decision","title":"Auth decision: JWT for session management","body":"Alternative approach"}`
	req := httptest.NewRequest(http.MethodPost, "/memories", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	// With Threshold=0 (fallback to 0.6), conflict should be reported.
	if resp["conflict"] == nil {
		t.Error("expected conflict metadata with Threshold=0 (server defaults to 0.6)")
	}
}

func TestHandleAddMemory_ConfigConflict_ReturnsCreatedWithConflict(t *testing.T) {
	st := newServerTestStore(t)
	srv := New(st, 0)
	h := srv.Handler()

	// Seed an existing config
	_, err := st.AddMemory(store.AddMemoryParams{
		ProjectID: "ohara",
		Kind:      store.MemoryKindConfig,
		Title:     "Database config: use PostgreSQL for production",
		Body:      "Connection string in env",
	})
	if err != nil {
		t.Fatalf("seed memory: %v", err)
	}

	// Conflicting config — save succeeds with conflict metadata
	body := `{"project_id":"ohara","kind":"config","title":"Database config: PostgreSQL for production settings","body":"Pool size 10"}`
	req := httptest.NewRequest(http.MethodPost, "/memories", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for conflicting config (save succeeds), got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp["conflict"] == nil {
		t.Fatal("expected top-level conflict field for conflicting config")
	}
}

// ─── Unix socket transport tests ────────────────────────────────────────────────

func TestWithSocketPathSetsField(t *testing.T) {
	s := New(nil, 7777)
	if s.socketPath != "" {
		t.Fatalf("expected empty socketPath initially, got %q", s.socketPath)
	}

	s2 := New(nil, 7777, WithSocketPath("/tmp/ohara.sock"))
	if s2.socketPath != "/tmp/ohara.sock" {
		t.Fatalf("expected /tmp/ohara.sock, got %q", s2.socketPath)
	}
}

func TestStartPrefersUnixSocketWhenConfigured(t *testing.T) {
	var gotNetwork, gotAddr string

	s := New(nil, 7777, WithSocketPath("/tmp/ohara-transport-test.sock"))
	s.listen = func(network, address string) (net.Listener, error) {
		gotNetwork = network
		gotAddr = address
		// Return a real listener so we can call Close().
		ln, err := net.Listen("unix", "/tmp/ohara-transport-test.sock")
		return ln, err
	}
	s.serve = func(ln net.Listener, h http.Handler) error {
		// Close immediately so Start returns.
		_ = ln.Close()
		return errors.New("serve stopped")
	}

	err := s.Start()
	if err == nil || err.Error() != "serve stopped" {
		t.Fatalf("expected serve stopped error, got %v", err)
	}
	if gotNetwork != "unix" {
		t.Fatalf("expected network=unix, got %q", gotNetwork)
	}
	if gotAddr != "/tmp/ohara-transport-test.sock" {
		t.Fatalf("expected addr=/tmp/ohara-transport-test.sock, got %q", gotAddr)
	}
}

func TestStartFallsBackToTCPWhenSocketFails(t *testing.T) {
	var attempts []string // "unix:<path>" or "tcp:<addr>"

	s := New(nil, 7777, WithSocketPath("/nonexistent/ohara.sock"))
	s.listen = func(network, address string) (net.Listener, error) {
		attempts = append(attempts, network+":"+address)
		// Unix socket fails; TCP succeeds with a stub.
		if network == "unix" {
			return nil, errors.New("permission denied")
		}
		return stubListener{}, nil
	}
	s.serve = func(ln net.Listener, h http.Handler) error {
		_ = ln.Close()
		return errors.New("serve stopped")
	}

	err := s.Start()
	if err == nil || err.Error() != "serve stopped" {
		t.Fatalf("expected serve stopped error, got %v", err)
	}
	if len(attempts) != 2 {
		t.Fatalf("expected 2 listen attempts, got %d: %v", len(attempts), attempts)
	}
	if attempts[0] != "unix:/nonexistent/ohara.sock" {
		t.Fatalf("first attempt: expected unix socket, got %q", attempts[0])
	}
	if attempts[1] != "tcp:127.0.0.1:7777" {
		t.Fatalf("second attempt: expected tcp, got %q", attempts[1])
	}
}

func TestStartTCPFallbackWithSocketPathSet(t *testing.T) {
	// Even when socket path is set, if unix socket errors, falls back to TCP.
	var gotNetwork string

	s := New(nil, 8080, WithSocketPath("/tmp/fallback-test.sock"))
	s.listen = func(network, address string) (net.Listener, error) {
		gotNetwork = network
		if network == "unix" {
			return nil, errors.New("address already in use")
		}
		return stubListener{}, nil
	}
	s.serve = func(ln net.Listener, h http.Handler) error {
		_ = ln.Close()
		return errors.New("serve stopped")
	}

	err := s.Start()
	if err == nil || err.Error() != "serve stopped" {
		t.Fatalf("expected serve stopped, got %v", err)
	}
	// Should have fallen back to TCP after unix failure.
	if gotNetwork != "tcp" {
		t.Fatalf("expected fallback to tcp after unix failure, got network=%q", gotNetwork)
	}
}

func TestStartRetriesUnixAfterStaleSocketCleanup(t *testing.T) {
	socketPath := t.TempDir() + "/ohara.sock"
	if err := os.WriteFile(socketPath, []byte("stale"), 0644); err != nil {
		t.Fatalf("create stale socket placeholder: %v", err)
	}

	var attempts []string
	unixAttempts := 0

	s := New(nil, 8080, WithSocketPath(socketPath))
	s.listen = func(network, address string) (net.Listener, error) {
		attempts = append(attempts, network+":"+address)
		if network == "unix" {
			unixAttempts++
			if unixAttempts == 1 {
				return nil, &net.OpError{Op: "listen", Net: "unix", Err: syscall.EADDRINUSE}
			}
			return stubListener{}, nil
		}
		return stubListener{}, nil
	}
	s.serve = func(ln net.Listener, h http.Handler) error {
		_ = ln.Close()
		return errors.New("serve stopped")
	}

	err := s.Start()
	if err == nil || err.Error() != "serve stopped" {
		t.Fatalf("expected serve stopped, got %v", err)
	}
	if len(attempts) != 2 {
		t.Fatalf("expected 2 listen attempts (unix retry), got %d: %v", len(attempts), attempts)
	}
	if !strings.HasPrefix(attempts[0], "unix:") || !strings.HasPrefix(attempts[1], "unix:") {
		t.Fatalf("expected unix retry attempts, got %v", attempts)
	}
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("expected stale socket path removed, stat err=%v", err)
	}
}

func TestStartUsesTCPWhenSocketPathEmpty(t *testing.T) {
	var gotNetwork string

	s := New(nil, 9999) // no socket path
	s.listen = func(network, address string) (net.Listener, error) {
		gotNetwork = network
		return stubListener{}, nil
	}
	s.serve = func(ln net.Listener, h http.Handler) error {
		_ = ln.Close()
		return errors.New("serve stopped")
	}

	err := s.Start()
	if err == nil || err.Error() != "serve stopped" {
		t.Fatalf("expected serve stopped, got %v", err)
	}
	if gotNetwork != "tcp" {
		t.Fatalf("expected network=tcp when no socket path, got %q", gotNetwork)
	}
}

func TestNewSignatureVariadic(t *testing.T) {
	// Verify New accepts 0, 1, or many options without breaking.
	_ = New(nil, 7777)                                                       // no options
	_ = New(nil, 7777, WithSocketPath("/a.sock"))                            // one option
	_ = New(nil, 7777, WithSocketPath("/a.sock"), WithSocketPath("/b.sock")) // last wins
}

// ─── Session API spec-alignment tests ─────────────────────────────────────────

func TestCreateSession_ProjectIDField(t *testing.T) {
	st := newServerTestStore(t)
	srv := New(st, 0)
	h := srv.Handler()

	// Spec uses project_id, not project.
	body := `{"id":"ses-pid","project_id":"ohara-spec"}`
	req := httptest.NewRequest(http.MethodPost, "/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp["id"] != "ses-pid" {
		t.Errorf("expected id=ses-pid, got %v", resp["id"])
	}
	if resp["created"] != true {
		t.Errorf("expected created=true on first create, got %v", resp["created"])
	}
}

func TestCreateSession_IdempotentCreatedFalse(t *testing.T) {
	st := newServerTestStore(t)
	srv := New(st, 0)
	h := srv.Handler()

	// First create.
	body := `{"id":"ses-idemp","project_id":"ohara"}`
	req := httptest.NewRequest(http.MethodPost, "/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("first create: expected 201, got %d", rec.Code)
	}

	// Same ID again — should be idempotent.
	body = `{"id":"ses-idemp","project_id":"ohara"}`
	req = httptest.NewRequest(http.MethodPost, "/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("idempotent create: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp["id"] != "ses-idemp" {
		t.Errorf("expected id=ses-idemp, got %v", resp["id"])
	}
	// created should be false because the session already existed.
	if resp["created"] != false {
		t.Errorf("expected created=false on idempotent create, got %v", resp["created"])
	}
}

func TestCreateSession_LegacyProjectField(t *testing.T) {
	// Legacy field name "project" must still work for backward compat.
	st := newServerTestStore(t)
	srv := New(st, 0)
	h := srv.Handler()

	body := `{"id":"ses-legacy","project":"ohara-legacy"}`
	req := httptest.NewRequest(http.MethodPost, "/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 with legacy project field, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp["created"] != true {
		t.Errorf("expected created=true, got %v", resp["created"])
	}
}

func TestCreateSession_ActorIDIgnored(t *testing.T) {
	// Spec includes actor_id but we don't persist it — just ensure it doesn't break.
	st := newServerTestStore(t)
	srv := New(st, 0)
	h := srv.Handler()

	body := `{"id":"ses-actor","project_id":"ohara","actor_id":"human"}`
	req := httptest.NewRequest(http.MethodPost, "/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 with actor_id, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPatchSession_BehavesAsEndAlias(t *testing.T) {
	st := newServerTestStore(t)
	srv := New(st, 0)
	h := srv.Handler()

	// Create a session.
	createReq := httptest.NewRequest(http.MethodPost, "/sessions",
		strings.NewReader(`{"id":"ses-patch","project":"ohara"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	h.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d", createRec.Code)
	}

	// PATCH /sessions/{id} ends the session (same as POST /sessions/{id}/end).
	patchReq := httptest.NewRequest(http.MethodPatch, "/sessions/ses-patch",
		strings.NewReader(`{"summary":"done","status":"completed"}`))
	patchReq.Header.Set("Content-Type", "application/json")
	patchRec := httptest.NewRecorder()
	h.ServeHTTP(patchRec, patchReq)

	if patchRec.Code != http.StatusOK {
		t.Fatalf("PATCH session: expected 200, got %d: %s", patchRec.Code, patchRec.Body.String())
	}

	var patchResp map[string]any
	if err := json.NewDecoder(patchRec.Body).Decode(&patchResp); err != nil {
		t.Fatalf("parse PATCH response: %v", err)
	}
	if patchResp["status"] != "completed" {
		t.Errorf("expected status=completed, got %v", patchResp["status"])
	}
	if patchResp["id"] != "ses-patch" {
		t.Errorf("expected id=ses-patch, got %v", patchResp["id"])
	}

	// Also verify POST /sessions/{id}/end still works as legacy alias.
	endReq := httptest.NewRequest(http.MethodPost, "/sessions/ses-patch/end",
		strings.NewReader(`{"summary":"done2"}`))
	endReq.Header.Set("Content-Type", "application/json")
	endRec := httptest.NewRecorder()
	h.ServeHTTP(endRec, endReq)
	if endRec.Code != http.StatusOK {
		t.Fatalf("POST /end legacy alias: expected 200, got %d", endRec.Code)
	}
}

func TestGetSessionContext_WithPriorCompletedSession(t *testing.T) {
	st := newServerTestStore(t)
	srv := New(st, 0)
	h := srv.Handler()

	// Create and end the first session (completed).
	createReq := httptest.NewRequest(http.MethodPost, "/sessions",
		strings.NewReader(`{"id":"ses-prev","project_id":"ohara"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	h.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create prev session: expected 201, got %d", createRec.Code)
	}

	endReq := httptest.NewRequest(http.MethodPatch, "/sessions/ses-prev",
		strings.NewReader(`{"summary":"previous work done"}`))
	endReq.Header.Set("Content-Type", "application/json")
	endRec := httptest.NewRecorder()
	h.ServeHTTP(endRec, endReq)
	if endRec.Code != http.StatusOK {
		t.Fatalf("end prev session: expected 200, got %d", endRec.Code)
	}

	// Create a second session (not ended yet — current session).
	createReq2 := httptest.NewRequest(http.MethodPost, "/sessions",
		strings.NewReader(`{"id":"ses-current","project_id":"ohara"}`))
	createReq2.Header.Set("Content-Type", "application/json")
	createRec2 := httptest.NewRecorder()
	h.ServeHTTP(createRec2, createReq2)
	if createRec2.Code != http.StatusCreated {
		t.Fatalf("create current session: expected 201, got %d", createRec2.Code)
	}

	// GET /sessions/ses-current/context should return the prior session's summary.
	ctxReq := httptest.NewRequest(http.MethodGet, "/sessions/ses-current/context", nil)
	ctxRec := httptest.NewRecorder()
	h.ServeHTTP(ctxRec, ctxReq)

	if ctxRec.Code != http.StatusOK {
		t.Fatalf("GET context: expected 200, got %d: %s", ctxRec.Code, ctxRec.Body.String())
	}

	var ctxResp map[string]any
	if err := json.NewDecoder(ctxRec.Body).Decode(&ctxResp); err != nil {
		t.Fatalf("parse context response: %v", err)
	}
	if ctxResp["context"] == nil {
		t.Fatal("expected context field in response")
	}
	if ctxResp["context"] != "previous work done" {
		t.Errorf("expected context='previous work done', got %v", ctxResp["context"])
	}
}

func TestGetSessionContext_NoPriorCompletedSession(t *testing.T) {
	st := newServerTestStore(t)
	srv := New(st, 0)
	h := srv.Handler()

	// Create a session but do NOT end it — no prior completed session.
	createReq := httptest.NewRequest(http.MethodPost, "/sessions",
		strings.NewReader(`{"id":"ses-alone","project_id":"ohara"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	h.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d", createRec.Code)
	}

	ctxReq := httptest.NewRequest(http.MethodGet, "/sessions/ses-alone/context", nil)
	ctxRec := httptest.NewRecorder()
	h.ServeHTTP(ctxRec, ctxReq)

	if ctxRec.Code != http.StatusOK {
		t.Fatalf("GET context with no prior: expected 200, got %d: %s", ctxRec.Code, ctxRec.Body.String())
	}

	var ctxResp map[string]any
	if err := json.NewDecoder(ctxRec.Body).Decode(&ctxResp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if ctxResp["context"] != "" {
		t.Errorf("expected empty context when no prior session, got %v", ctxResp["context"])
	}
}

func TestGetSessionContext_SessionNotFound(t *testing.T) {
	srv := New(newServerTestStore(t), 0)
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/sessions/does-not-exist/context", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing session, got %d", rec.Code)
	}
}

// ─── Pack Config tests ─────────────────────────────────────────────────────────

func TestHandlePack_UsesPackConfigDefaultBudget(t *testing.T) {
	st := newServerTestStore(t)
	srv := New(st, 0, WithPackConfig(PackConfig{
		DefaultBudgetTokens: 600,
		MaxBudgetTokens:     900,
	}))
	h := srv.Handler()

	// Ensure a session exists so pack can reference a project.
	_ = st.CreateSession("pack-session", "ohara", "")

	// Request with no budget_tokens → should use PackConfig default (600).
	body := `{"project_id":"ohara","session_id":"pack-session"}`
	req := httptest.NewRequest(http.MethodPost, "/pack", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp store.PackResult
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.BudgetTokens != 600 {
		t.Errorf("expected BudgetTokens=600 (PackConfig default), got %d", resp.BudgetTokens)
	}
}

func TestHandlePack_CapsBudgetAtPackConfigMaxBudget(t *testing.T) {
	st := newServerTestStore(t)
	srv := New(st, 0, WithPackConfig(PackConfig{
		DefaultBudgetTokens: 200,
		MaxBudgetTokens:     500,
	}))
	h := srv.Handler()

	_ = st.CreateSession("pack-session2", "ohara", "")

	// Request with budget_tokens=99999 → should be capped to PackConfig max (500).
	body := `{"project_id":"ohara","session_id":"pack-session2","budget_tokens":99999}`
	req := httptest.NewRequest(http.MethodPost, "/pack", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp store.PackResult
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.BudgetTokens != 500 {
		t.Errorf("expected BudgetTokens=500 (PackConfig max cap), got %d", resp.BudgetTokens)
	}
}

func TestHandlePack_ZeroPackConfigFallsBackToSpecDefaults(t *testing.T) {
	st := newServerTestStore(t)
	// No PackConfig → zero values → handlePack falls back to spec defaults (400/800).
	srv := New(st, 0)
	h := srv.Handler()

	_ = st.CreateSession("pack-session3", "ohara", "")

	// No budget_tokens → uses fallback default of 400.
	body := `{"project_id":"ohara","session_id":"pack-session3"}`
	req := httptest.NewRequest(http.MethodPost, "/pack", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp store.PackResult
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.BudgetTokens != 400 {
		t.Errorf("expected BudgetTokens=400 (spec fallback default), got %d", resp.BudgetTokens)
	}
}

// ─── DELETE /memories/:id tests ─────────────────────────────────────────────────

func TestHandleDeleteMemory_Returns405(t *testing.T) {
	st := newServerTestStore(t)
	srv := New(st, 0)
	h := srv.Handler()

	// DELETE on any memory ID must return 405 — memories are never hard-deleted.
	req := httptest.NewRequest(http.MethodDelete, "/memories/1", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE /memories/:id: expected 405, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error"] == "" {
		t.Error("expected error field in 405 response body")
	}
}

func TestHandleDeleteMemory_Returns405ForNonExistentID(t *testing.T) {
	st := newServerTestStore(t)
	srv := New(st, 0)
	h := srv.Handler()

	// Even for a non-existent ID, DELETE must return 405 (not 404).
	// This is per spec: the DELETE method itself is disallowed, not the resource.
	req := httptest.NewRequest(http.MethodDelete, "/memories/999999", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE /memories/999999: expected 405 (method not resource), got %d", rec.Code)
	}
}

func TestHandleDeleteMemory_PATCHStillWorks(t *testing.T) {
	// Verify that PATCH (archive via status) still works after DELETE is registered as 405.
	st := newServerTestStore(t)
	srv := New(st, 0)
	h := srv.Handler()

	// First create a memory.
	createBody := `{"project_id":"ohara","kind":"decision","title":"Temp decision","body":"Delete me via PATCH"}`
	createReq := httptest.NewRequest(http.MethodPost, "/memories", strings.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	h.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", createRec.Code, createRec.Body.String())
	}

	var createResp map[string]any
	if err := json.NewDecoder(createRec.Body).Decode(&createResp); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	memID := int(createResp["id"].(float64))

	// Archive it via PATCH — this is the correct way to "delete".
	patchBody := fmt.Sprintf(`{"status":"archived","reason":"testing archive flow"}`)
	patchReq := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/memories/%d", memID), strings.NewReader(patchBody))
	patchReq.Header.Set("Content-Type", "application/json")
	patchRec := httptest.NewRecorder()
	h.ServeHTTP(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("PATCH archive: expected 200, got %d: %s", patchRec.Code, patchRec.Body.String())
	}

	// DELETE must still return 405.
	delReq := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/memories/%d", memID), nil)
	delRec := httptest.NewRecorder()
	h.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE after archive: expected 405, got %d", delRec.Code)
	}
}
