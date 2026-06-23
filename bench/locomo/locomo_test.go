package locomo

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
	if len(fixture.Questions) != 10 {
		t.Errorf("expected 10 questions, got %d", len(fixture.Questions))
	}
}

// TestRunBenchmark_FTS5 verifies the fts5 benchmark completes without errors
// and meets SLO thresholds on the built-in fixture.
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
	if report.TotalQuestions != 10 {
		t.Errorf("expected 10 questions, got %d", report.TotalQuestions)
	}
	if report.PassedQuestions < 5 {
		t.Errorf("expected at least 5 passed, got %d", report.PassedQuestions)
	}
	if report.OverallMetrics.RecallAt3 < 0.5 {
		t.Errorf("recall@3 %.3f below threshold 0.50", report.OverallMetrics.RecallAt3)
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
	if report.TotalQuestions != 10 {
		t.Errorf("expected 10 questions, got %d", report.TotalQuestions)
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
		if r.Report.TotalQuestions != 10 {
			t.Errorf("sweep mode %s: expected 10 questions, got %d", r.Name, r.Report.TotalQuestions)
		}
	}
}

// TestConversationsToFacts verifies fact derivation from conversations.
func TestConversationsToFacts(t *testing.T) {
	convs := []ConversationFixture{
		{
			ID: "test-conv",
			Messages: []MessageFixture{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", Content: "Hi there"},
			},
		},
	}
	facts := conversationsToFacts(convs)
	if len(facts) != 2 {
		t.Errorf("expected 2 facts, got %d", len(facts))
	}
	if facts[0].Key != "test-conv:0" {
		t.Errorf("expected key test-conv:0, got %s", facts[0].Key)
	}
	if facts[1].Key != "test-conv:1" {
		t.Errorf("expected key test-conv:1, got %s", facts[1].Key)
	}
}

// TestConversationsToFacts_SkipsEmpty skips empty messages.
func TestConversationsToFacts_SkipsEmpty(t *testing.T) {
	convs := []ConversationFixture{
		{
			ID: "test-conv",
			Messages: []MessageFixture{
				{Role: "user", Content: ""},
				{Role: "assistant", Content: "  "},
				{Role: "user", Content: "actual content"},
			},
		},
	}
	facts := conversationsToFacts(convs)
	if len(facts) != 1 {
		t.Errorf("expected 1 fact (skipped empties), got %d", len(facts))
	}
	if facts[0].Key != "test-conv:2" {
		t.Errorf("expected key test-conv:2, got %s", facts[0].Key)
	}
}
