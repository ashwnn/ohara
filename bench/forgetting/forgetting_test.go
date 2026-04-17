package forgetting

import (
	"testing"
	"time"

	"github.com/ashwnn/ohara/internal/maintain"
	"github.com/ashwnn/ohara/internal/store"
)

func newBenchStore(t *testing.T) *store.Store {
	t.Helper()
	cfg := store.FallbackConfig(t.TempDir())
	s, err := store.New(cfg)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestStaleRecallHarness(t *testing.T) {
	s := newBenchStore(t)

	staleID, _ := s.AddMemory(store.AddMemoryParams{ProjectID: "bench", Kind: store.MemoryKindPattern, Title: "Old archived", Body: "legacy setting", Classification: "tactical"})
	activeID, _ := s.AddMemory(store.AddMemoryParams{ProjectID: "bench", Kind: store.MemoryKindPattern, Title: "Current setting", Body: "new setting", Classification: "tactical"})

	// Force one memory to archived and verify default retrieval avoids stale recall.
	err := s.ForgetMemory(staleID, "stale", "bench")
	if err != nil {
		t.Fatalf("forget stale memory: %v", err)
	}

	results, err := s.SearchMemories("setting", "bench", "", "", "", store.MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("search active: %v", err)
	}
	for _, r := range results {
		if r.ID == staleID {
			t.Fatalf("stale archived memory leaked into active recall")
		}
	}
	_ = activeID
}

func TestFalseForgetHarness(t *testing.T) {
	s := newBenchStore(t)

	foundationalID, err := s.AddMemory(store.AddMemoryParams{
		ProjectID:      "bench",
		Kind:           store.MemoryKindDecision,
		Title:          "Foundational decision",
		Body:           "never forget",
		Classification: "foundational",
		ExpiresAt:      time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("add foundational memory: %v", err)
	}

	if _, err := maintain.ArchiveExpired(s, false); err != nil {
		t.Fatalf("archive expired: %v", err)
	}
	m, err := s.GetMemory(foundationalID)
	if err != nil {
		t.Fatalf("get foundational memory: %v", err)
	}
	if m.Status != store.MemoryStatusActive {
		t.Fatalf("false forget detected: foundational memory archived unexpectedly (status=%s)", m.Status)
	}
}

func TestConflictSurvivalHarness(t *testing.T) {
	s := newBenchStore(t)

	aID, _ := s.AddMemory(store.AddMemoryParams{ProjectID: "bench", Kind: store.MemoryKindDecision, Title: "Enable WAL", Body: "always enable wal", Classification: "foundational"})
	bID, _ := s.AddMemory(store.AddMemoryParams{ProjectID: "bench", Kind: store.MemoryKindDecision, Title: "Disable WAL", Body: "never enable wal", Classification: "tactical"})
	if err := s.AddRelation(aID, bID, store.RelationContradicts); err != nil {
		t.Fatalf("add contradiction relation: %v", err)
	}

	if err := s.ForgetMemory(bID, "superseded", "bench"); err != nil {
		t.Fatalf("forget conflicting memory: %v", err)
	}

	related, err := s.GetRelated(aID, store.RelationContradicts)
	if err != nil {
		t.Fatalf("get related contradictions: %v", err)
	}
	found := false
	for _, r := range related {
		if r.ID == bID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("conflict survival failed: contradicts relation disappeared after forget")
	}
}
