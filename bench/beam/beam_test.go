package beam

import (
	"testing"
)

// TestLoadFixture_Valid verifies the deterministic fixture loads correctly.
func TestLoadFixture_Valid(t *testing.T) {
	fixture, err := LoadFixture("fixture.json")
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	if len(fixture.Conversations) != 2 {
		t.Errorf("expected 2 conversations, got %d", len(fixture.Conversations))
	}
	if len(fixture.Probes) != 10 {
		t.Errorf("expected 10 probes, got %d", len(fixture.Probes))
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
	if report.TotalProbes != 10 {
		t.Errorf("expected 10 probes, got %d", report.TotalProbes)
	}
	if report.PassedProbes < 3 {
		t.Errorf("expected at least 3 passed, got %d", report.PassedProbes)
	}
	if report.OverallMetrics.RecallAt3 < 0.20 {
		t.Errorf("recall@3 %.3f below threshold 0.20", report.OverallMetrics.RecallAt3)
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
	if report.TotalProbes != 10 {
		t.Errorf("expected 10 probes, got %d", report.TotalProbes)
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
		if r.Report.TotalProbes != 10 {
			t.Errorf("sweep mode %s: expected 10 probes, got %d", r.Name, r.Report.TotalProbes)
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
