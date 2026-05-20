package store

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newHybridTestStore(t *testing.T, ollamaURL string) *Store {
	t.Helper()
	return newHybridTestStoreWithBackend(t, "ollama", ollamaURL)
}

func newHybridTestStoreWithBackend(t *testing.T, backend, ollamaURL string) *Store {
	t.Helper()
	cfg := mustDefaultConfig(t)
	cfg.DataDir = t.TempDir()
	cfg.DedupeWindow = time.Hour
	cfg.RetrievalMode = "hybrid"
	cfg.EmbeddingBackend = backend
	cfg.OllamaURL = ollamaURL

	s, err := New(cfg)
	if err != nil {
		t.Fatalf("new hybrid store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func newMockEmbeddingServer(t *testing.T, embedding []float32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embeddings" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embedding": embedding})
	}))
}

func TestSearchMemoriesFTSOnlyWorks(t *testing.T) {
	s := newTestStore(t)
	_, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindBugfix,
		Title:     "Fixed JWT refresh race",
		Body:      "Added lock around refresh rotation in auth middleware.",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	results, err := s.SearchMemories("JWT refresh", "ohara", "", "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected FTS results")
	}
}

func TestSearchMemoriesHybridFallsBackWhenEmbeddingUnavailable(t *testing.T) {
	s := newHybridTestStore(t, "http://127.0.0.1:1")
	firstID, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "JWT refresh policy",
		Body:      "Use short-lived access tokens with rotated refresh tokens.",
	})
	if err != nil {
		t.Fatalf("AddMemory first: %v", err)
	}
	_, _ = s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindPattern,
		Title:     "Cache warmup checklist",
		Body:      "Run warmup after deploy.",
	})

	results, err := s.SearchMemories("JWT refresh", "ohara", "", "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories fallback: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results when embedding backend is unavailable")
	}
	if results[0].ID != firstID {
		t.Fatalf("expected FTS-first result %d, got %d", firstID, results[0].ID)
	}
}

func TestSearchMemoriesRRFPrefersDualLaneResult(t *testing.T) {
	embServer := newMockEmbeddingServer(t, []float32{1, 0})
	defer embServer.Close()

	s := newHybridTestStore(t, embServer.URL)
	ftsOnlyID, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindPattern,
		Title:     "JWT rotation policy and refresh strategy",
		Body:      "Primary policy for JWT rotation.",
	})
	if err != nil {
		t.Fatalf("AddMemory fts-only: %v", err)
	}
	dualLaneID, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "JWT rotation strategy",
		Body:      "Secondary policy entry.",
	})
	if err != nil {
		t.Fatalf("AddMemory dual-lane: %v", err)
	}

	// Make the dual-lane item strongly similar in vector space.
	if _, err := s.Exec(
		`INSERT INTO obs_embeddings (obs_id, embedding, model, created_at) VALUES (?, ?, ?, datetime('now'))
		 ON CONFLICT(obs_id) DO UPDATE SET embedding=excluded.embedding, model=excluded.model, created_at=excluded.created_at`,
		dualLaneID, floatsToBytes([]float32{1, 0}), "test", time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("insert embedding: %v", err)
	}

	results, err := s.SearchMemories("JWT rotation policy", "ohara", "", "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories hybrid: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}
	if results[0].ID != dualLaneID {
		t.Fatalf("expected dual-lane memory %d to rank first, got %d (fts-only=%d)", dualLaneID, results[0].ID, ftsOnlyID)
	}
}

func TestSearchMemoriesHybridCanReturnVectorOnlyCandidates(t *testing.T) {
	embServer := newMockEmbeddingServer(t, []float32{1, 0, 0})
	defer embServer.Close()

	s := newHybridTestStore(t, embServer.URL)
	lexicalID, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindPattern,
		Title:     "API pagination strategy",
		Body:      "Use cursor pagination for list endpoints.",
	})
	if err != nil {
		t.Fatalf("AddMemory lexical: %v", err)
	}
	vectorOnlyID, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Client identity assertion",
		Body:      "Trusted token replay prevention policy.",
	})
	if err != nil {
		t.Fatalf("AddMemory vector-only: %v", err)
	}

	// Ensure only the vector-only memory has an embedding.
	if _, err := s.Exec(`DELETE FROM obs_embeddings WHERE obs_id = ?`, lexicalID); err != nil {
		t.Fatalf("delete lexical embedding: %v", err)
	}
	if _, err := s.Exec(
		`INSERT INTO obs_embeddings (obs_id, embedding, model, created_at) VALUES (?, ?, ?, datetime('now'))
		 ON CONFLICT(obs_id) DO UPDATE SET embedding=excluded.embedding, model=excluded.model, created_at=excluded.created_at`,
		vectorOnlyID, floatsToBytes([]float32{1, 0, 0}), "test", time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("insert vector embedding: %v", err)
	}

	results, err := s.SearchMemories("non-lexical semantic prompt", "ohara", "", "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	foundVectorOnly := false
	for _, item := range results {
		if item.ID == vectorOnlyID {
			foundVectorOnly = true
			break
		}
	}
	if !foundVectorOnly {
		t.Fatal("expected vector-only candidate to appear in hybrid results")
	}
}

func TestSearchMemoriesHybridKeepsStrongLexicalMatchFirst(t *testing.T) {
	embServer := newMockEmbeddingServer(t, []float32{1, 0})
	defer embServer.Close()

	s := newHybridTestStore(t, embServer.URL)
	lexicalID, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindBugfix,
		Title:     "Fix auth middleware nil principal panic",
		Body:      "Guard nil principal before role checks.",
	})
	if err != nil {
		t.Fatalf("AddMemory lexical: %v", err)
	}
	noiseID, err := s.AddMemory(AddMemoryParams{
		ProjectID:      "ohara",
		Kind:           MemoryKindDecision,
		Title:          "Unrelated planning decision",
		Body:           "Mostly unrelated content.",
		Classification: "foundational",
	})
	if err != nil {
		t.Fatalf("AddMemory noise: %v", err)
	}
	if _, err := s.Exec(
		`INSERT INTO obs_embeddings (obs_id, embedding, model, created_at) VALUES (?, ?, ?, datetime('now'))
		 ON CONFLICT(obs_id) DO UPDATE SET embedding=excluded.embedding, model=excluded.model, created_at=excluded.created_at`,
		noiseID, floatsToBytes([]float32{1, 0}), "test", time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("insert noise embedding: %v", err)
	}

	results, err := s.SearchMemories("auth middleware nil principal panic", "ohara", "", "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected search results")
	}
	if results[0].ID != lexicalID {
		t.Fatalf("expected strong lexical match first, got id=%d", results[0].ID)
	}
}

func TestSearchMemoriesHybridLoadsEmbeddingsInSingleQuery(t *testing.T) {
	embServer := newMockEmbeddingServer(t, []float32{1, 0})
	defer embServer.Close()

	s := newHybridTestStore(t, embServer.URL)
	for i := 0; i < 3; i++ {
		id, err := s.AddMemory(AddMemoryParams{
			ProjectID: "ohara",
			Kind:      MemoryKindBugfix,
			Title:     "Auth middleware token issue",
			Body:      "Token bug details.",
		})
		if err != nil {
			t.Fatalf("AddMemory %d: %v", i, err)
		}
		if _, err := s.Exec(
			`INSERT INTO obs_embeddings (obs_id, embedding, model, created_at) VALUES (?, ?, ?, datetime('now'))
			 ON CONFLICT(obs_id) DO UPDATE SET embedding=excluded.embedding, model=excluded.model, created_at=excluded.created_at`,
			id, floatsToBytes([]float32{1, 0}), "test", time.Now().UTC().Format(time.RFC3339Nano),
		); err != nil {
			t.Fatalf("insert embedding %d: %v", i, err)
		}
	}

	originalQueryIt := s.hooks.queryIt
	defer func() { s.hooks.queryIt = originalQueryIt }()
	embeddingQueries := 0
	s.hooks.queryIt = func(db queryer, query string, args ...any) (rowScanner, error) {
		if strings.Contains(query, "FROM obs_embeddings") {
			embeddingQueries++
		}
		return originalQueryIt(db, query, args...)
	}

	if _, err := s.SearchMemories("Auth middleware token issue", "ohara", "", "", "", MemoryStatusActive, 10, ""); err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	if embeddingQueries != 1 {
		t.Fatalf("expected 1 embedding query, got %d", embeddingQueries)
	}
}

func TestSearchMemoriesExcludesArchivedAndExpired(t *testing.T) {
	s := newTestStore(t)

	_, _ = s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindPattern,
		Title:     "Auth middleware baseline",
		Body:      "Current policy",
	})

	archivedID, _ := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindPattern,
		Title:     "Auth middleware baseline",
		Body:      "Old archived policy",
	})
	_, _ = s.UpdateMemory(archivedID, UpdateMemoryParams{
		Status:  &[]string{MemoryStatusArchived}[0],
		ActorID: "test",
	})

	_, _ = s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindPattern,
		Title:     "Auth middleware baseline",
		Body:      "Expired policy",
		ExpiresAt: time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339),
	})

	results, err := s.SearchMemories("Auth middleware baseline", "ohara", "", "", "", MemoryStatusActive, 20, "")
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	for _, item := range results {
		if item.Status == MemoryStatusArchived {
			t.Fatalf("archived memory %d should not be returned in active search", item.ID)
		}
	}
}

func TestDeterministicEmbeddingBackendSupportsHybridRRF(t *testing.T) {
	s := newHybridTestStoreWithBackend(t, "deterministic-test", "http://127.0.0.1:11434")
	ftsOnlyID, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindPattern,
		Title:     "Middleware ranking notes",
		Body:      "Search ranking notes with loose guidance.",
	})
	if err != nil {
		t.Fatalf("AddMemory fts-only: %v", err)
	}
	dualLaneID, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Reciprocal rank fusion policy",
		Body:      "Combine lexical and vector lanes with reciprocal rank fusion.",
	})
	if err != nil {
		t.Fatalf("AddMemory dual-lane: %v", err)
	}
	if _, err := s.Exec(`DELETE FROM obs_embeddings WHERE obs_id = ?`, ftsOnlyID); err != nil {
		t.Fatalf("delete fts-only embedding: %v", err)
	}

	results, err := s.SearchMemories("combine search ranks with reciprocal fusion", "ohara", "", "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}
	if results[0].ID != dualLaneID {
		t.Fatalf("expected dual-lane memory %d first, got %d (fts-only=%d)", dualLaneID, results[0].ID, ftsOnlyID)
	}
}

func TestSearchMemoriesActiveFiltersSupersededAndWrongProject(t *testing.T) {
	s := newHybridTestStoreWithBackend(t, "deterministic-test", "http://127.0.0.1:11434")
	currentID, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Current auth policy",
		Body:      "Use rotating refresh tokens with replay protection.",
	})
	if err != nil {
		t.Fatalf("AddMemory current: %v", err)
	}
	oldID, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Current auth policy",
		Body:      "Legacy static refresh tokens.",
	})
	if err != nil {
		t.Fatalf("AddMemory old: %v", err)
	}
	if _, err := s.Exec(`UPDATE memory_items SET superseded_by = ? WHERE id = ?`, currentID, oldID); err != nil {
		t.Fatalf("mark superseded_by: %v", err)
	}
	_, err = s.AddMemory(AddMemoryParams{
		ProjectID: "other-project",
		Kind:      MemoryKindDecision,
		Title:     "Current auth policy",
		Body:      "Wrong project policy should not leak.",
	})
	if err != nil {
		t.Fatalf("AddMemory wrong-project: %v", err)
	}

	results, err := s.SearchMemories("current auth policy", "ohara", "", "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	for _, item := range results {
		if item.ProjectID != "ohara" {
			t.Fatalf("wrong project memory leaked: %s", item.ProjectID)
		}
		if item.ID == oldID {
			t.Fatalf("superseded-linked memory %d leaked into active results", oldID)
		}
	}
}
