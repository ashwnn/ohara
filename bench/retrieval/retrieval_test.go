package retrieval

import (
	"path/filepath"
	"strings"
	"testing"
)

func fixturePath() string {
	return filepath.Join("..", "fixtures", "retrieval_fixture.json")
}

func TestRetrievalBenchmarkMeetsThresholds(t *testing.T) {
	report, err := RunBenchmark(RunOptions{
		FixturePath: fixturePath(),
		K:           5,
		Enforce:     true,
	})
	if err != nil {
		t.Fatalf("benchmark thresholds failed: %v", err)
	}
	if report.TotalCases == 0 {
		t.Fatal("expected benchmark cases")
	}
}

func TestRetrievalBenchmarkHasCategoryCoverage(t *testing.T) {
	report, err := RunBenchmark(RunOptions{
		FixturePath: fixturePath(),
		K:           5,
		Enforce:     false,
	})
	if err != nil {
		t.Fatalf("benchmark run failed: %v", err)
	}
	requiredCategories := []string{
		"lexical",
		"semantic",
		"multi_session",
		"knowledge_update",
		"temporal",
		"file_aware",
		"noise_resistance",
		"context_pack",
		"abstention",
		"hybrid_fallback",
	}
	for _, category := range requiredCategories {
		if _, ok := report.PerCategory[category]; !ok {
			t.Fatalf("missing category coverage: %s", category)
		}
	}
}

func BenchmarkRetrievalHarness(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, err := RunBenchmark(RunOptions{
			FixturePath: fixturePath(),
			K:           5,
			Enforce:     false,
		})
		if err != nil {
			b.Fatalf("benchmark run failed: %v", err)
		}
	}
}

func TestRunBenchmarkHybridDeterministicModeLabel(t *testing.T) {
	report, err := RunBenchmark(RunOptions{
		FixturePath: fixturePath(),
		K:           5,
		Enforce:     false,
		Mode:        "hybrid",
		Embedding:   "deterministic-test",
	})
	if err != nil {
		t.Fatalf("benchmark run failed: %v", err)
	}
	if !report.HybridEnabled {
		t.Fatal("expected hybrid benchmark mode to be enabled")
	}
	if !report.EmbeddingsAvailable {
		t.Fatal("expected deterministic embeddings to be available")
	}
	if report.EmbeddingMode != "deterministic-test" {
		t.Fatalf("expected embedding mode deterministic-test, got %q", report.EmbeddingMode)
	}
}

func TestRunBenchmarkHybridDefaultsToOllamaFallbackLabel(t *testing.T) {
	report, err := RunBenchmark(RunOptions{
		FixturePath: fixturePath(),
		K:           5,
		Enforce:     false,
		Mode:        "hybrid",
		OllamaURL:   "http://127.0.0.1:1",
	})
	if err != nil {
		t.Fatalf("benchmark run failed: %v", err)
	}
	if !report.HybridEnabled {
		t.Fatal("expected hybrid benchmark mode to be enabled")
	}
	if report.EmbeddingsAvailable {
		t.Fatal("expected unavailable ollama embeddings for fallback test")
	}
	if report.EmbeddingMode != "fts-fallback" {
		t.Fatalf("expected embedding mode fts-fallback, got %q", report.EmbeddingMode)
	}
}

func TestAggToMetricsCalculations(t *testing.T) {
	agg := &caseAgg{
		count:             4,
		hit1:              2,
		hit3:              3,
		hit5:              4,
		rrSum:             2.75,
		ndcgSum:           2.5,
		staleHits:         1,
		wrongProjectHits:  2,
		supersededHits:    1,
		fileExpectedTotal: 5,
		fileHitTotal:      4,
		packCases:         3,
		packPass:          2,
		abstentionCases:   4,
		abstentionFP:      1,
	}
	m := aggToMetrics(agg)
	if m.RecallAt1 != 0.5 {
		t.Fatalf("recall@1=%f want=0.5", m.RecallAt1)
	}
	if m.RecallAt3 != 0.75 {
		t.Fatalf("recall@3=%f want=0.75", m.RecallAt3)
	}
	if m.RecallAt5 != 1.0 {
		t.Fatalf("recall@5=%f want=1.0", m.RecallAt5)
	}
	if m.MRR != 0.6875 {
		t.Fatalf("mrr=%f want=0.6875", m.MRR)
	}
	if m.FileContextAccuracy != 0.8 {
		t.Fatalf("file accuracy=%f want=0.8", m.FileContextAccuracy)
	}
	if m.PackBudgetCompliance != (2.0 / 3.0) {
		t.Fatalf("pack compliance=%f want=%f", m.PackBudgetCompliance, 2.0/3.0)
	}
	if m.AbstentionFalsePos != 0.25 {
		t.Fatalf("abstention fp=%f want=0.25", m.AbstentionFalsePos)
	}
}

func TestEnforceThresholdsRejectsRegression(t *testing.T) {
	r := Report{
		Metrics: Metrics{
			RecallAt3:           0.70,
			MRR:                 0.65,
			StaleHitRate:        0.05,
			WrongProjectHitRate: 0.01,
			SupersededHitRate:   0.01,
		},
		PerCategory: map[string]Metrics{
			"lexical":    {RecallAt3: 0.80},
			"file_aware": {RecallAt3: 0.70},
		},
		Thresholds: ThresholdsFixture{
			OverallRecallAt3:       0.80,
			RecallAt3Lexical:       0.90,
			RecallAt3FileAware:     0.85,
			MRROverall:             0.70,
			StaleHitRateMax:        0.0,
			WrongProjectHitRateMax: 0.0,
			SupersededHitRateMax:   0.0,
			PackBudgetCompliance:   1.0,
			AbstentionFalsePosMax:  0.10,
		},
	}
	err := enforceThresholds(r)
	if err == nil {
		t.Fatal("expected enforceThresholds to fail")
	}
	if !strings.Contains(err.Error(), "overall recall@3") {
		t.Fatalf("expected overall recall failure in error, got: %v", err)
	}
}
