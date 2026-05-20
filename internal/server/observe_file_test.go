package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ashwnn/ohara/internal/store"
)

func TestHandleObserveCapturesSessionObservation(t *testing.T) {
	st := newServerTestStore(t)
	srv := New(st, 0)
	h := srv.Handler()

	body := map[string]any{
		"session_id":    "sess-observe",
		"project_id":    "ohara",
		"event_type":    "tool.execute.after",
		"capture_level": "tools",
		"source":        "opencode",
		"title":         "tool finished",
		"body":          "write file completed",
		"payload_json":  `{"tool":"write"}`,
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/observe", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var count int
	if err := st.QueryRow("SELECT COUNT(*) FROM session_observations WHERE session_id = 'sess-observe'").Scan(&count); err != nil {
		t.Fatalf("count observations: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 observation row, got %d", count)
	}
}

func TestFileHistoryAndContextEndpoints(t *testing.T) {
	st := newServerTestStore(t)
	_, _ = st.AddMemory(store.AddMemoryParams{
		ProjectID:     "ohara",
		Kind:          store.MemoryKindBugfix,
		Title:         "Fixed auth middleware panic",
		Body:          "Guard nil principal in services/auth/middleware.go",
		AppliesToJSON: `{"files":["services/auth/middleware.go"]}`,
	})

	srv := New(st, 0)
	h := srv.Handler()

	historyReq := httptest.NewRequest(http.MethodGet, "/files/history?path=services/auth/middleware.go&project=ohara", nil)
	historyRec := httptest.NewRecorder()
	h.ServeHTTP(historyRec, historyReq)
	if historyRec.Code != http.StatusOK {
		t.Fatalf("history expected 200, got %d: %s", historyRec.Code, historyRec.Body.String())
	}

	contextBody := map[string]any{
		"path":          "services/auth/middleware.go",
		"project":       "ohara",
		"budget_tokens": 180,
	}
	raw, _ := json.Marshal(contextBody)
	contextReq := httptest.NewRequest(http.MethodPost, "/files/context", bytes.NewReader(raw))
	contextRec := httptest.NewRecorder()
	h.ServeHTTP(contextRec, contextReq)
	if contextRec.Code != http.StatusOK {
		t.Fatalf("context expected 200, got %d: %s", contextRec.Code, contextRec.Body.String())
	}

	var payload struct {
		Context string `json:"context"`
	}
	if err := json.Unmarshal(contextRec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode context response: %v", err)
	}
	if payload.Context == "" {
		t.Fatal("expected non-empty file context")
	}
}
