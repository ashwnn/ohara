//go:build e2e

package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ashwnn/ohara/internal/store"
)

func newE2EServer(t *testing.T) (*store.Store, *httptest.Server) {
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

	httpServer := httptest.NewServer(New(s, 0).Handler())
	t.Cleanup(func() {
		httpServer.Close()
		_ = s.Close()
	})

	return s, httpServer
}

func postJSON(t *testing.T, client *http.Client, url string, body any) *http.Response {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	resp, err := client.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("post %s: %v", url, err)
	}
	return resp
}

func decodeJSON[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	defer resp.Body.Close()
	var out T
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	return out
}

func TestValidationAndImportExportErrorsE2E(t *testing.T) {
	_, ts := newE2EServer(t)
	client := ts.Client()

	invalidSessionResp, err := client.Post(ts.URL+"/sessions", "application/json", strings.NewReader("{"))
	if err != nil {
		t.Fatalf("post invalid session json: %v", err)
	}
	if invalidSessionResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 invalid session json, got %d", invalidSessionResp.StatusCode)
	}
	invalidSessionResp.Body.Close()

	missingFieldsResp := postJSON(t, client, ts.URL+"/sessions", map[string]any{"id": "only-id"})
	if missingFieldsResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 missing required fields, got %d", missingFieldsResp.StatusCode)
	}
	missingFieldsResp.Body.Close()

	create := postJSON(t, client, ts.URL+"/sessions", map[string]any{
		"id":      "s-validate",
		"project": "ohara",
	})
	if create.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 creating session, got %d", create.StatusCode)
	}
	create.Body.Close()

	promptsMissingQResp, err := client.Get(ts.URL + "/prompts/search")
	if err != nil {
		t.Fatalf("search prompts without q: %v", err)
	}
	if promptsMissingQResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 prompts search missing q, got %d", promptsMissingQResp.StatusCode)
	}
	promptsMissingQResp.Body.Close()

	invalidImportResp, err := client.Post(ts.URL+"/import", "application/json", strings.NewReader("{"))
	if err != nil {
		t.Fatalf("import invalid json: %v", err)
	}
	if invalidImportResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 import invalid json, got %d", invalidImportResp.StatusCode)
	}
	invalidImportResp.Body.Close()

	exportResp, err := client.Get(ts.URL + "/export")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if exportResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 export, got %d", exportResp.StatusCode)
	}
	exportedBody, err := io.ReadAll(exportResp.Body)
	if err != nil {
		t.Fatalf("read export body: %v", err)
	}
	exportResp.Body.Close()

	reimportResp, err := client.Post(ts.URL+"/import", "application/json", bytes.NewReader(exportedBody))
	if err != nil {
		t.Fatalf("reimport: %v", err)
	}
	if reimportResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 import after export, got %d", reimportResp.StatusCode)
	}
	reimportResp.Body.Close()

	recentPromptsResp, err := client.Get(ts.URL + "/prompts/recent?project=ohara&limit=bad")
	if err != nil {
		t.Fatalf("recent prompts: %v", err)
	}
	if recentPromptsResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 recent prompts, got %d", recentPromptsResp.StatusCode)
	}
	recentPromptsResp.Body.Close()
}

func TestServerHandlersReturn500WhenStoreClosed(t *testing.T) {
	s, ts := newE2EServer(t)
	client := ts.Client()

	create := postJSON(t, client, ts.URL+"/sessions", map[string]any{
		"id":      "s-closed",
		"project": "ohara",
	})
	if create.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 creating session, got %d", create.StatusCode)
	}
	create.Body.Close()

	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	addPrompt := postJSON(t, client, ts.URL+"/prompts", map[string]any{
		"session_id": "s-closed",
		"content":    "prompt",
		"project":    "ohara",
	})
	if addPrompt.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 add prompt with closed store, got %d", addPrompt.StatusCode)
	}
	addPrompt.Body.Close()

	recentPromptsResp, err := client.Get(ts.URL + "/prompts/recent")
	if err != nil {
		t.Fatalf("recent prompts closed store: %v", err)
	}
	if recentPromptsResp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 recent prompts with closed store, got %d", recentPromptsResp.StatusCode)
	}
	recentPromptsResp.Body.Close()

	searchPromptsResp, err := client.Get(ts.URL + "/prompts/search?q=test")
	if err != nil {
		t.Fatalf("search prompts closed store: %v", err)
	}
	if searchPromptsResp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 search prompts with closed store, got %d", searchPromptsResp.StatusCode)
	}
	searchPromptsResp.Body.Close()

	contextResp, err := client.Get(ts.URL + "/context")
	if err != nil {
		t.Fatalf("context closed store: %v", err)
	}
	if contextResp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 context with closed store, got %d", contextResp.StatusCode)
	}
	contextResp.Body.Close()

	statsResp, err := client.Get(ts.URL + "/stats")
	if err != nil {
		t.Fatalf("stats closed store: %v", err)
	}
	if statsResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 stats with closed store fallback, got %d", statsResp.StatusCode)
	}
	statsResp.Body.Close()
}
