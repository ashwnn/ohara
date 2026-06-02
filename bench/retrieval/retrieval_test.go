package retrieval

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func fixturePath() string {
	return filepath.Join("..", "fixtures", "retrieval_fixture.json")
}

func TestRetrievalBenchmarkMeetsThresholds(t *testing.T) {
	report, err := RunBenchmark(RunOptions{
		FixturePath:     fixturePath(),
		K:               5,
		Enforce:         true,
		SkipLatencyGate: true,
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
		"graph_context",
	}
	for _, category := range requiredCategories {
		if _, ok := report.PerCategory[category]; !ok {
			t.Fatalf("missing category coverage: %s", category)
		}
	}
}

func TestCaseResultsCountMatchesTotalCases(t *testing.T) {
	report, err := RunBenchmark(RunOptions{
		FixturePath: fixturePath(),
		K:           5,
		Enforce:     false,
	})
	if err != nil {
		t.Fatalf("benchmark run failed: %v", err)
	}
	if len(report.CaseResults) != report.TotalCases {
		t.Fatalf("case results count %d != total cases %d", len(report.CaseResults), report.TotalCases)
	}
}

func TestCaseResultsDurationsNonNegative(t *testing.T) {
	report, err := RunBenchmark(RunOptions{
		FixturePath: fixturePath(),
		K:           5,
		Enforce:     false,
	})
	if err != nil {
		t.Fatalf("benchmark run failed: %v", err)
	}
	for _, cr := range report.CaseResults {
		if cr.DurationMs < 0 {
			t.Fatalf("case %s has negative duration_ms: %.3f", cr.CaseID, cr.DurationMs)
		}
	}
}

func TestCaseResultsGraphContextTraceExists(t *testing.T) {
	report, err := RunBenchmark(RunOptions{
		FixturePath: fixturePath(),
		K:           5,
		Enforce:     false,
	})
	if err != nil {
		t.Fatalf("benchmark run failed: %v", err)
	}
	found := false
	for _, cr := range report.CaseResults {
		if cr.Type == "graph_context" {
			found = true
			if cr.Source != "graph_context" && cr.Source != "graph_context_error" {
				t.Fatalf("graph_context case %s has unexpected source: %s", cr.CaseID, cr.Source)
			}
		}
	}
	if !found {
		t.Fatal("no graph_context cases found in results")
	}
}

func TestReportJSONRoundTrip(t *testing.T) {
	report, err := RunBenchmark(RunOptions{
		FixturePath: fixturePath(),
		K:           5,
		Enforce:     false,
	})
	if err != nil {
		t.Fatalf("benchmark run failed: %v", err)
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("json marshal failed: %v", err)
	}
	var decoded Report
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json unmarshal failed: %v", err)
	}
	if decoded.TotalCases != report.TotalCases {
		t.Fatalf("round-trip total cases mismatch: %d != %d", decoded.TotalCases, report.TotalCases)
	}
	if len(decoded.CaseResults) != len(report.CaseResults) {
		t.Fatalf("round-trip case results count mismatch: %d != %d", len(decoded.CaseResults), len(report.CaseResults))
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
		count:              4,
		hit1:               2,
		hit3:               3,
		hit5:               4,
		rrSum:              2.75,
		ndcgSum:            2.5,
		staleHits:          1,
		wrongProjectHits:   2,
		supersededHits:     1,
		fileExpectedTotal:  5,
		fileHitTotal:       4,
		graphExpectedTotal: 6,
		graphHitTotal:      4,
		packCases:          3,
		packPass:           2,
		abstentionCases:    4,
		abstentionFP:       1,
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
	if m.GraphContextAccuracy != (4.0 / 6.0) {
		t.Fatalf("graph accuracy=%f want=%f", m.GraphContextAccuracy, 4.0/6.0)
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
		Latency: LatencyMetrics{
			P50Ms:  10,
			P95Ms:  30,
			MaxMs:  100,
			MeanMs: 15,
		},
		PerCategory: map[string]Metrics{
			"lexical":    {RecallAt3: 0.80},
			"file_aware": {RecallAt3: 0.70},
		},
		Thresholds: ThresholdsFixture{
			OverallRecallAt3:            0.80,
			RecallAt3Lexical:            0.90,
			RecallAt3FileAware:          0.85,
			MRROverall:                  0.70,
			StaleHitRateMax:             0.0,
			WrongProjectHitRateMax:      0.0,
			SupersededHitRateMax:        0.0,
			PackBudgetCompliance:        1.0,
			AbstentionFalsePosMax:       0.10,
			LatencyP95MsMax:             50,
			LatencyMaxMsMax:             150,
			FixtureWeakDistractorRateMax: 0.55,
			FixtureHighOverlapRateMax:    0.35,
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

func TestComputeLatencyMetrics(t *testing.T) {
	results := []CaseResult{
		{CaseID: "a", DurationMs: 10},
		{CaseID: "b", DurationMs: 20},
		{CaseID: "c", DurationMs: 30},
		{CaseID: "d", DurationMs: 40},
		{CaseID: "e", DurationMs: 50},
	}
	m := computeLatencyMetrics(results)
	if m.P50Ms != 30 {
		t.Fatalf("p50=%f want=30", m.P50Ms)
	}
	if m.P95Ms != 48 {
		t.Fatalf("p95=%f want=48", m.P95Ms)
	}
	if m.MaxMs != 50 {
		t.Fatalf("max=%f want=50", m.MaxMs)
	}
	if m.MeanMs != 30 {
		t.Fatalf("mean=%f want=30", m.MeanMs)
	}
}

func TestComputeLatencyMetricsEmpty(t *testing.T) {
	m := computeLatencyMetrics(nil)
	if m.P50Ms != 0 || m.P95Ms != 0 || m.MaxMs != 0 || m.MeanMs != 0 {
		t.Fatal("expected zero latency metrics for empty input")
	}
}

func TestWithDefaultThresholdsLatencyAndFixture(t *testing.T) {
	in := ThresholdsFixture{}
	out := withDefaultThresholds(in)
	if out.LatencyP95MsMax != 50 {
		t.Fatalf("default latency p95 max=%f want=50", out.LatencyP95MsMax)
	}
	if out.LatencyMaxMsMax != 150 {
		t.Fatalf("default latency max max=%f want=150", out.LatencyMaxMsMax)
	}
	if out.FixtureWeakDistractorRateMax != 0.55 {
		t.Fatalf("default weak-distractor rate max=%f want=0.55", out.FixtureWeakDistractorRateMax)
	}
	if out.FixtureHighOverlapRateMax != 0.35 {
		t.Fatalf("default high-overlap rate max=%f want=0.35", out.FixtureHighOverlapRateMax)
	}
}

func TestEnforceThresholdsRejectsLatencyP95(t *testing.T) {
	r := Report{
		Metrics: Metrics{
			RecallAt3:           1.0,
			MRR:                 1.0,
			StaleHitRate:        0.0,
			WrongProjectHitRate: 0.0,
			SupersededHitRate:   0.0,
			PackBudgetCompliance: 1.0,
			AbstentionFalsePos:  0.0,
		},
		Latency: LatencyMetrics{
			P95Ms: 60,
			MaxMs: 100,
		},
		PerCategory: map[string]Metrics{
			"lexical":    {RecallAt3: 1.0},
			"file_aware": {RecallAt3: 1.0},
		},
		Thresholds: ThresholdsFixture{
			OverallRecallAt3:            0.80,
			RecallAt3Lexical:            0.90,
			RecallAt3FileAware:          0.85,
			MRROverall:                  0.70,
			StaleHitRateMax:             0.0,
			WrongProjectHitRateMax:      0.0,
			SupersededHitRateMax:        0.0,
			PackBudgetCompliance:        1.0,
			AbstentionFalsePosMax:       0.10,
			LatencyP95MsMax:             50,
			LatencyMaxMsMax:             150,
			FixtureWeakDistractorRateMax: 0.55,
			FixtureHighOverlapRateMax:    0.35,
		},
	}
	err := enforceThresholds(r)
	if err == nil {
		t.Fatal("expected latency p95 enforcement to fail")
	}
	if !strings.Contains(err.Error(), "latency p95") {
		t.Fatalf("expected latency p95 message in error, got: %v", err)
	}
}

func TestEnforceThresholdsRejectsLatencyMax(t *testing.T) {
	r := Report{
		Metrics: Metrics{
			RecallAt3:           1.0,
			MRR:                 1.0,
			StaleHitRate:        0.0,
			WrongProjectHitRate: 0.0,
			SupersededHitRate:   0.0,
			PackBudgetCompliance: 1.0,
			AbstentionFalsePos:  0.0,
		},
		Latency: LatencyMetrics{
			P95Ms: 30,
			MaxMs: 200,
		},
		PerCategory: map[string]Metrics{
			"lexical":    {RecallAt3: 1.0},
			"file_aware": {RecallAt3: 1.0},
		},
		Thresholds: ThresholdsFixture{
			OverallRecallAt3:            0.80,
			RecallAt3Lexical:            0.90,
			RecallAt3FileAware:          0.85,
			MRROverall:                  0.70,
			StaleHitRateMax:             0.0,
			WrongProjectHitRateMax:      0.0,
			SupersededHitRateMax:        0.0,
			PackBudgetCompliance:        1.0,
			AbstentionFalsePosMax:       0.10,
			LatencyP95MsMax:             50,
			LatencyMaxMsMax:             150,
			FixtureWeakDistractorRateMax: 0.55,
			FixtureHighOverlapRateMax:    0.35,
		},
	}
	err := enforceThresholds(r)
	if err == nil {
		t.Fatal("expected latency max enforcement to fail")
	}
	if !strings.Contains(err.Error(), "latency max") {
		t.Fatalf("expected latency max message in error, got: %v", err)
	}
}

func TestEnforceThresholdsRejectsFixtureWeakDistractorRate(t *testing.T) {
	r := Report{
		Metrics: Metrics{
			RecallAt3:           1.0,
			MRR:                 1.0,
			StaleHitRate:        0.0,
			WrongProjectHitRate: 0.0,
			SupersededHitRate:   0.0,
			PackBudgetCompliance: 1.0,
			AbstentionFalsePos:  0.0,
		},
		Latency: LatencyMetrics{
			P95Ms: 10,
			MaxMs: 50,
		},
		PerCategory: map[string]Metrics{
			"lexical":    {RecallAt3: 1.0},
			"file_aware": {RecallAt3: 1.0},
		},
		FixtureAudit: FixtureAudit{
			SearchCaseCount:     20,
			WeakDistractorCount: 18,
			WeakDistractorRate:  0.90,
		},
		Thresholds: ThresholdsFixture{
			OverallRecallAt3:            0.80,
			RecallAt3Lexical:            0.90,
			RecallAt3FileAware:          0.85,
			MRROverall:                  0.70,
			StaleHitRateMax:             0.0,
			WrongProjectHitRateMax:      0.0,
			SupersededHitRateMax:        0.0,
			PackBudgetCompliance:        1.0,
			AbstentionFalsePosMax:       0.10,
			LatencyP95MsMax:             50,
			LatencyMaxMsMax:             150,
			FixtureWeakDistractorRateMax: 0.55,
			FixtureHighOverlapRateMax:    0.35,
		},
	}
	err := enforceThresholds(r)
	if err == nil {
		t.Fatal("expected fixture weak-distractor enforcement to fail")
	}
	if !strings.Contains(err.Error(), "fixture weak-distractor rate") {
		t.Fatalf("expected weak-distractor message in error, got: %v", err)
	}
}

func TestEnforceThresholdsRejectsFixtureHighOverlapRate(t *testing.T) {
	r := Report{
		Metrics: Metrics{
			RecallAt3:           1.0,
			MRR:                 1.0,
			StaleHitRate:        0.0,
			WrongProjectHitRate: 0.0,
			SupersededHitRate:   0.0,
			PackBudgetCompliance: 1.0,
			AbstentionFalsePos:  0.0,
		},
		Latency: LatencyMetrics{
			P95Ms: 10,
			MaxMs: 50,
		},
		PerCategory: map[string]Metrics{
			"lexical":    {RecallAt3: 1.0},
			"file_aware": {RecallAt3: 1.0},
		},
		FixtureAudit: FixtureAudit{
			SearchCaseCount:    20,
			HighOverlapCaseIDs: []string{"a", "b", "c", "d", "e", "f", "g", "h"},
			HighOverlapRate:    0.40,
		},
		Thresholds: ThresholdsFixture{
			OverallRecallAt3:            0.80,
			RecallAt3Lexical:            0.90,
			RecallAt3FileAware:          0.85,
			MRROverall:                  0.70,
			StaleHitRateMax:             0.0,
			WrongProjectHitRateMax:      0.0,
			SupersededHitRateMax:        0.0,
			PackBudgetCompliance:        1.0,
			AbstentionFalsePosMax:       0.10,
			LatencyP95MsMax:             50,
			LatencyMaxMsMax:             150,
			FixtureWeakDistractorRateMax: 0.55,
			FixtureHighOverlapRateMax:    0.35,
		},
	}
	err := enforceThresholds(r)
	if err == nil {
		t.Fatal("expected fixture high-overlap enforcement to fail")
	}
	if !strings.Contains(err.Error(), "fixture high-overlap rate") {
		t.Fatalf("expected high-overlap message in error, got: %v", err)
	}
}

func TestReportIncludesLatencyMetrics(t *testing.T) {
	report, err := RunBenchmark(RunOptions{
		FixturePath: fixturePath(),
		K:           5,
		Enforce:     false,
	})
	if err != nil {
		t.Fatalf("benchmark run failed: %v", err)
	}
	if report.Latency.MaxMs <= 0 {
		t.Fatal("expected non-zero latency max after benchmark run")
	}
	if report.Latency.P95Ms <= 0 {
		t.Fatal("expected non-zero latency p95 after benchmark run")
	}
	if report.Latency.MeanMs <= 0 {
		t.Fatal("expected non-zero latency mean after benchmark run")
	}
	if report.Latency.P50Ms <= 0 {
		t.Fatal("expected non-zero latency p50 after benchmark run")
	}
}

func TestFixtureAuditRates(t *testing.T) {
	report, err := RunBenchmark(RunOptions{
		FixturePath: fixturePath(),
		K:           5,
		Enforce:     false,
	})
	if err != nil {
		t.Fatalf("benchmark run failed: %v", err)
	}
	if report.FixtureAudit.SearchCaseCount > 0 {
		expectedRate := float64(report.FixtureAudit.WeakDistractorCount) / float64(report.FixtureAudit.SearchCaseCount)
		if report.FixtureAudit.WeakDistractorRate != expectedRate {
			t.Fatalf("WeakDistractorRate=%f want=%f", report.FixtureAudit.WeakDistractorRate, expectedRate)
		}
		expectedHORate := float64(len(report.FixtureAudit.HighOverlapCaseIDs)) / float64(report.FixtureAudit.SearchCaseCount)
		if report.FixtureAudit.HighOverlapRate != expectedHORate {
			t.Fatalf("HighOverlapRate=%f want=%f", report.FixtureAudit.HighOverlapRate, expectedHORate)
		}
	}
}
