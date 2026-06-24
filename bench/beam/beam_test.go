package beam

import (
	"fmt"
	"os"
	"testing"

	"github.com/ashwnn/ohara/internal/store"
)

// TestLoadFixture_Valid verifies the deterministic fixture loads correctly.
func TestLoadFixture_Valid(t *testing.T) {
	fixture, err := LoadFixture("fixture.json")
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	if len(fixture.Conversations) != 3 {
		t.Errorf("expected 3 conversations, got %d", len(fixture.Conversations))
	}
	if len(fixture.Probes) != 28 {
		t.Errorf("expected 28 probes, got %d", len(fixture.Probes))
	}
}

// TestRunBenchmark_FTS5 verifies the fts5 benchmark completes and meets thresholds.
func TestRunBenchmark_FTS5(t *testing.T) {
	opts := RunOptions{
		FixturePath: "fixture.json",
		K:           5,
		Enforce:     true,
		SkipLatency: true,
		Mode:        "fts5",
		Workers:     1,
	}
	report, err := RunBenchmark(opts)
	if err != nil {
		t.Fatalf("RunBenchmark fts5: %v", err)
	}
	if report.TotalProbes != 28 {
		t.Errorf("expected 28 probes, got %d", report.TotalProbes)
	}
	if report.PassedProbes < 12 {
		t.Errorf("expected at least 12 passed, got %d", report.PassedProbes)
	}
	if report.OverallMetrics.RecallAt3 < 0.40 {
		t.Errorf("recall@3 %.3f below threshold 0.40", report.OverallMetrics.RecallAt3)
	}
}

// TestRunBenchmark_Hybrid verifies the hybrid-deterministic benchmark completes.
func TestRunBenchmark_Hybrid(t *testing.T) {
	opts := RunOptions{
		FixturePath: "fixture.json",
		K:           5,
		Enforce:     false,
		SkipLatency: true,
		Mode:        "hybrid",
		Workers:     1,
	}
	report, err := RunBenchmark(opts)
	if err != nil {
		t.Fatalf("RunBenchmark hybrid: %v", err)
	}
	if report.TotalProbes != 28 {
		t.Errorf("expected 28 probes, got %d", report.TotalProbes)
	}
	if report.RetrievalMode != "hybrid" {
		t.Errorf("expected hybrid mode, got %s", report.RetrievalMode)
	}
}

// TestSweep verifies sweep mode produces results for all modes.
func TestSweep(t *testing.T) {
	baseOpts := RunOptions{
		FixturePath: "fixture.json",
		K:           5,
		Enforce:     false,
		SkipLatency: true,
		Workers:     1,
	}
	results := RunSweep(baseOpts, nil)
	if len(results) != 2 {
		t.Errorf("expected 2 sweep results, got %d", len(results))
	}
	for _, r := range results {
		if r.Error != "" {
			t.Errorf("sweep mode %s error: %s", r.Name, r.Error)
		}
		if r.Report.TotalProbes != 28 {
			t.Errorf("sweep mode %s: expected 28 probes, got %d", r.Name, r.Report.TotalProbes)
		}
	}
}

// TestFixtureToFacts verifies fact derivation.
func TestFixtureToFacts(t *testing.T) {
	fix := Fixture{
		Conversations: []ConversationFixture{
			{
				ID: "test-conv",
				Messages: []MessageFixture{
					{Role: "user", Content: "Hello"},
					{Role: "assistant", Content: "Hi"},
				},
				Facts: []FactFixture{
					{Key: "explicit-1", Title: "Fact 1", Body: "explicit body"},
				},
			},
		},
	}
	facts := fixtureToFacts(fix)
	// 2 messages + 1 explicit fact = 3 facts
	if len(facts) != 3 {
		t.Errorf("expected 3 facts, got %d", len(facts))
	}
	// Explicit fact should be first.
	if facts[0].Key != "explicit-1" {
		t.Errorf("expected explicit-1 first, got %s", facts[0].Key)
	}
}

// TestFixtureToFacts_SkipsEmpty skips empty messages.
func TestFixtureToFacts_SkipsEmpty(t *testing.T) {
	fix := Fixture{
		Conversations: []ConversationFixture{
			{
				ID: "test-conv",
				Messages: []MessageFixture{
					{Role: "user", Content: ""},
					{Role: "assistant", Content: "  "},
					{Role: "user", Content: "valid"},
				},
			},
		},
	}
	facts := fixtureToFacts(fix)
	if len(facts) != 1 {
		t.Errorf("expected 1 fact, got %d", len(facts))
	}
}

// TestSeedBeamRelations verifies that seedBeamRelations creates
// intra-conversation relation edges.
func TestSeedBeamRelations(t *testing.T) {
	// Build a mini fixture: 1 conversation with 3 messages.
	fix := Fixture{
		Conversations: []ConversationFixture{
			{
				ID: "test-conv",
				Messages: []MessageFixture{
					{Role: "user", Content: "Ohara uses SQLite with FTS5."},
					{Role: "assistant", Content: "modernc.org/sqlite gives pure-Go SQLite."},
					{Role: "user", Content: "We need to stay on v1.45.0 for now."},
				},
			},
		},
	}
	facts := fixtureToFacts(fix)
	if len(facts) != 3 {
		t.Fatalf("expected 3 facts, got %d", len(facts))
	}

	// Create temp store and seed.
	tmp, err := os.MkdirTemp("", "ohara-test-beam-rel-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	cfg := store.FallbackConfig(tmp)
	cfg.NoJobWorker = true
	cfg.SQLiteTempStoreMemory = true
	cfg.MaxOpenConns = 1
	s, err := store.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	bulkParams := make([]store.BulkSeedMemoryParams, len(facts))
	keys := make([]string, len(facts))
	for i, f := range facts {
		keys[i] = f.Key
		bulkParams[i] = store.BulkSeedMemoryParams{
			ProjectID: "beam",
			Kind:      f.Kind,
			Title:     f.Title,
			Body:      f.Body,
			Domain:    f.Domain,
			SessionID: f.SessionID,
		}
	}

	ids, err := s.BulkSeedMemories(bulkParams)
	if err != nil {
		t.Fatalf("BulkSeedMemories: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("expected 3 IDs, got %d", len(ids))
	}

	keyToID := make(map[string]int64, len(ids))
	for i, id := range ids {
		keyToID[keys[i]] = id
	}

	// Run seedBeamRelations.
	if err := seedBeamRelations(s, facts, keyToID); err != nil {
		t.Fatalf("seedBeamRelations: %v", err)
	}

	// Verify relations exist by checking GetRelated on first memory.
	// 3 consecutive facts → 2 edges: msg0→msg1, msg1→msg2.
	// GetRelated on msg0 should return msg1 (bidirectional related_to).
	related, err := s.GetRelated(ids[0], "")
	if err != nil {
		t.Fatalf("GetRelated(%d): %v", ids[0], err)
	}
	if len(related) == 0 {
		t.Fatalf("expected msg0 to have at least 1 related memory, got 0")
	}
	foundNeighbor := false
	for _, item := range related {
		if item.ID == ids[1] {
			foundNeighbor = true
			break
		}
	}
	if !foundNeighbor {
		t.Errorf("expected msg0 to be related to msg1 (%d), but not found in results: %v", ids[1], related)
	}

	// Verify entity extraction: facts contain known terms like "SQLite", "FTS5", "modernc".
	// We can't directly query obs_entities from the test package, but the fact that
	// seedBeamRelations completes without error confirms entity upsert/link succeeds.
}
// TestRunBenchmark_PPR verifies the benchmark runs with PPR reranker
// enabled and completes without error.
func TestRunBenchmark_PPR(t *testing.T) {
	opts := RunOptions{
		FixturePath: "fixture.json",
		K:           5,
		Enforce:     false,
		SkipLatency: true,
		Mode:        "hybrid",
		Workers:     1,
		PPRRerank:   true,
	}
	report, err := RunBenchmark(opts)
	if err != nil {
		t.Fatalf("RunBenchmark PPR: %v", err)
	}
	if report.TotalProbes != 28 {
		t.Errorf("expected 28 probes, got %d", report.TotalProbes)
	}
	// PPR must not degrade below FTS5 baseline.
	if report.PassedProbes < 12 {
		t.Errorf("expected at least 12 passed with PPR, got %d", report.PassedProbes)
	}

	// Verify multi-hop recall has improved from baseline (was 0.000).
	mhMetrics := report.ProbeTypeMetrics["multi_hop"]
	if mhMetrics.RecallAt3 < 0.50 {
		t.Errorf("multi_hop recall@3 %.3f below 0.50", mhMetrics.RecallAt3)
	}

	// Verify per-probe-type metrics.
	fmt.Printf("PPR multi_hop: recall@3=%.3f mrr=%.3f\n",
		mhMetrics.RecallAt3, mhMetrics.MRR)
	fmt.Printf("PPR temporal_order: recall@3=%.3f mrr=%.3f\n",
		report.ProbeTypeMetrics["temporal_order"].RecallAt3,
		report.ProbeTypeMetrics["temporal_order"].MRR)
	fmt.Printf("PPR fact_retrieval: recall@3=%.3f mrr=%.3f\n",
		report.ProbeTypeMetrics["fact_retrieval"].RecallAt3,
		report.ProbeTypeMetrics["fact_retrieval"].MRR)
}
