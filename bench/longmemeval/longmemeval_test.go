package longmemeval

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func fixturePath() string {
	return filepath.Join("..", "longmemeval", "fixture.json")
}

func TestRunBenchmarkAllQuestionsPass(t *testing.T) {
	report, err := RunBenchmark(RunOptions{
		FixturePath:     fixturePath(),
		K:               5,
		Enforce:         false,
		SkipLatencyGate: true,
	})
	if err != nil {
		t.Fatalf("benchmark run failed: %v", err)
	}
	if report.TotalQuestions == 0 {
		t.Fatal("expected questions")
	}
	// With k=5, FTS5 should find at least some hits per question.
	if report.FailedQuestions > report.TotalQuestions/3 {
		t.Errorf("too many failed questions: %d/%d", report.FailedQuestions, report.TotalQuestions)
	}
}

func TestRunBenchmarkHasDistanceCoverage(t *testing.T) {
	report, err := RunBenchmark(RunOptions{
		FixturePath:     fixturePath(),
		K:               5,
		Enforce:         false,
		SkipLatencyGate: true,
	})
	if err != nil {
		t.Fatalf("benchmark run failed: %v", err)
	}
	required := []string{"near", "medium", "far"}
	for _, dist := range required {
		if _, ok := report.DistanceMetrics[dist]; !ok {
			t.Errorf("missing distance coverage: %s", dist)
		}
	}
}

func TestRunBenchmarkHasCategoryCoverage(t *testing.T) {
	report, err := RunBenchmark(RunOptions{
		FixturePath:     fixturePath(),
		K:               5,
		Enforce:         false,
		SkipLatencyGate: true,
	})
	if err != nil {
		t.Fatalf("benchmark run failed: %v", err)
	}
	required := []string{"session_distance_1", "session_distance_2", "session_distance_3", "session_distance_4", "session_distance_5"}
	for _, cat := range required {
		if _, ok := report.CategoryMetrics[cat]; !ok {
			t.Errorf("missing category coverage: %s", cat)
		}
	}
}

func TestCaseResultsCountMatches(t *testing.T) {
	report, err := RunBenchmark(RunOptions{
		FixturePath:     fixturePath(),
		K:               5,
		Enforce:         false,
		SkipLatencyGate: true,
	})
	if err != nil {
		t.Fatalf("benchmark run failed: %v", err)
	}
	if len(report.CaseResults) != report.TotalQuestions {
		t.Errorf("case results count %d != total questions %d", len(report.CaseResults), report.TotalQuestions)
	}
}

func TestAllCaseResultsHaveDuration(t *testing.T) {
	report, err := RunBenchmark(RunOptions{
		FixturePath:     fixturePath(),
		K:               5,
		Enforce:         false,
		SkipLatencyGate: true,
	})
	if err != nil {
		t.Fatalf("benchmark run failed: %v", err)
	}
	for _, cr := range report.CaseResults {
		if cr.DurationMs < 0 {
			t.Errorf("case %s has negative duration: %.3f", cr.QuestionID, cr.DurationMs)
		}
	}
}

func TestReportHasLatencyMetrics(t *testing.T) {
	report, err := RunBenchmark(RunOptions{
		FixturePath:     fixturePath(),
		K:               5,
		Enforce:         false,
		SkipLatencyGate: true,
	})
	if err != nil {
		t.Fatalf("benchmark run failed: %v", err)
	}
	if report.Latency.MaxMs <= 0 {
		t.Fatal("expected non-zero latency max")
	}
	if report.Latency.MeanMs <= 0 {
		t.Fatal("expected non-zero latency mean")
	}
}

func TestReportJSONRoundTrip(t *testing.T) {
	report, err := RunBenchmark(RunOptions{
		FixturePath:     fixturePath(),
		K:               5,
		Enforce:         false,
		SkipLatencyGate: true,
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
	if decoded.TotalQuestions != report.TotalQuestions {
		t.Errorf("round-trip total questions mismatch: %d != %d", decoded.TotalQuestions, report.TotalQuestions)
	}
	if len(decoded.CaseResults) != len(report.CaseResults) {
		t.Errorf("round-trip case results count mismatch: %d != %d", len(decoded.CaseResults), len(report.CaseResults))
	}
}

func TestEnforceThresholdsRejectsLowRecall(t *testing.T) {
	r := Report{
		OverallMetrics: Metrics{
			RecallAt3: 0.50,
			MRR:       0.50,
		},
		DistanceMetrics: map[string]Metrics{
			"near":   {RecallAt3: 0.60},
			"medium": {RecallAt3: 0.50},
			"far":    {RecallAt3: 0.40},
		},
		Latency: LatencyMetrics{
			P95Ms: 10,
			MaxMs: 50,
		},
		Thresholds: ThresholdsFixture{
			OverallRecallAt3: 0.75,
			NearRecallAt3:    0.80,
			MediumRecallAt3:  0.70,
			FarRecallAt3:     0.60,
			MRROverall:       0.65,
		},
	}
	err := enforceThresholds(r)
	if err == nil {
		t.Fatal("expected enforceThresholds to fail")
	}
}

func TestEnforceThresholdsRejectsLatencyP95(t *testing.T) {
	r := Report{
		OverallMetrics: Metrics{
			RecallAt3: 1.0,
			MRR:       1.0,
		},
		DistanceMetrics: map[string]Metrics{
			"near":   {RecallAt3: 1.0},
			"medium": {RecallAt3: 1.0},
			"far":    {RecallAt3: 1.0},
		},
		Latency: LatencyMetrics{
			P95Ms: 120,
			MaxMs: 200,
		},
		Thresholds: ThresholdsFixture{
			OverallRecallAt3: 0.75,
			NearRecallAt3:    0.80,
			MediumRecallAt3:  0.70,
			FarRecallAt3:     0.60,
			MRROverall:       0.65,
			LatencyP95MsMax:  100,
			LatencyMaxMsMax:  500,
		},
	}
	err := enforceThresholds(r)
	if err == nil {
		t.Fatal("expected latency enforcement to fail")
	}
}

func TestWithDefaultThresholds(t *testing.T) {
	in := ThresholdsFixture{}
	out := withDefaultThresholds(in)
	if out.OverallRecallAt3 != 0.75 {
		t.Errorf("default overall recall@3 = %f, want 0.75", out.OverallRecallAt3)
	}
	if out.NearRecallAt3 != 0.80 {
		t.Errorf("default near recall@3 = %f, want 0.80", out.NearRecallAt3)
	}
	if out.LatencyP95MsMax != 100 {
		t.Errorf("default latency p95 max = %f, want 100", out.LatencyP95MsMax)
	}
}

func TestAggToMetricsCalculations(t *testing.T) {
	agg := &questionAgg{
		count:  4,
		passed: 3,
		hit1:   2,
		hit3:   3,
		hit5:   4,
		rrSum:  2.75,
		ndcgSum: 2.5,
	}
	m := aggToMetrics(agg)
	if m.RecallAt1 != 0.5 {
		t.Errorf("recall@1 = %f, want 0.5", m.RecallAt1)
	}
	if m.RecallAt3 != 0.75 {
		t.Errorf("recall@3 = %f, want 0.75", m.RecallAt3)
	}
	if m.RecallAt5 != 1.0 {
		t.Errorf("recall@5 = %f, want 1.0", m.RecallAt5)
	}
	if m.MRR != 0.6875 {
		t.Errorf("MRR = %f, want 0.6875", m.MRR)
	}
}

func TestComputeLatencyMetrics(t *testing.T) {
	results := []CaseResult{
		{QuestionID: "a", DurationMs: 10},
		{QuestionID: "b", DurationMs: 20},
		{QuestionID: "c", DurationMs: 30},
		{QuestionID: "d", DurationMs: 40},
		{QuestionID: "e", DurationMs: 50},
	}
	m := computeLatency(results)
	if m.P50Ms != 30 {
		t.Errorf("p50 = %f, want 30", m.P50Ms)
	}
	if m.MaxMs != 50 {
		t.Errorf("max = %f, want 50", m.MaxMs)
	}
	if m.MeanMs != 30 {
		t.Errorf("mean = %f, want 30", m.MeanMs)
	}
}

func TestComputeLatencyMetricsEmpty(t *testing.T) {
	m := computeLatency(nil)
	if m.P50Ms != 0 || m.P95Ms != 0 || m.MaxMs != 0 || m.MeanMs != 0 {
		t.Fatal("expected zero latency metrics for empty input")
	}
}

func TestLoadFixtureRejectsEmpty(t *testing.T) {
	_, err := LoadFixture("/nonexistent/path")
	if err == nil {
		t.Fatal("expected error for nonexistent fixture")
	}
}

func BenchmarkLongMemEvalHarness(b *testing.B) {
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

// -- JSONL import tests --

func TestImportFromJSONLBasic(t *testing.T) {
	input := `{"fact_key":"f1","session_id":"s1","title":"Test fact","body":"This is a test fact about deployments.","kind":"decision","domain":"infra","turn":1,"questions":[{"id":"q1","category":"s1","distance":"near","distance_sessions":1,"query":"deployment fact","expected_fact_keys":["f1"],"ask_session_id":"s2"}]}
{"fact_key":"f2","session_id":"s2","title":"Another fact","body":"This is a fact about auth tokens.","kind":"pattern","domain":"auth","turn":2,"questions":[{"id":"q2","category":"s2","distance":"medium","distance_sessions":2,"query":"auth token fact","expected_fact_keys":["f2"],"ask_session_id":"s3"}]}
`
	fixture, result, err := ImportFromJSONL(strings.NewReader(input))
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}
	if result.RecordsRead != 2 {
		t.Errorf("records read = %d, want 2", result.RecordsRead)
	}
	if result.FactsCreated != 2 {
		t.Errorf("facts created = %d, want 2", result.FactsCreated)
	}
	if result.QuestionsCreated != 2 {
		t.Errorf("questions created = %d, want 2", result.QuestionsCreated)
	}
	if len(fixture.Facts) != 2 {
		t.Errorf("fixture facts = %d, want 2", len(fixture.Facts))
	}
	if len(fixture.Questions) != 2 {
		t.Errorf("fixture questions = %d, want 2", len(fixture.Questions))
	}
	if fixture.Facts[0].Key != "f1" {
		t.Errorf("fact[0].key = %q, want f1", fixture.Facts[0].Key)
	}
	if fixture.Questions[1].Query != "auth token fact" {
		t.Errorf("question[1].query = %q, want 'auth token fact'", fixture.Questions[1].Query)
	}
}

func TestImportFromJSONLEmpty(t *testing.T) {
	_, _, err := ImportFromJSONL(strings.NewReader(""))
	if err == nil {
		t.Fatal("expected error for empty input")
	}
}

func TestImportFromJSONLInvalidLines(t *testing.T) {
	input := `{"fact_key":"f1","session_id":"s1","title":"ok","body":"ok"}
not valid json
{"fact_key":"f2","session_id":"s2","title":"also ok","body":"also ok"}
`
	fixture, result, err := ImportFromJSONL(strings.NewReader(input))
	if err != nil {
		t.Fatalf("import should succeed with partial errors: %v", err)
	}
	if result.RecordsRead != 3 {
		t.Errorf("records read = %d, want 3", result.RecordsRead)
	}
	if result.FactsCreated != 2 {
		t.Errorf("facts created = %d, want 2", result.FactsCreated)
	}
	if len(result.Errors) != 1 {
		t.Errorf("errors = %d, want 1", len(result.Errors))
	}
	if len(fixture.Facts) != 2 {
		t.Errorf("fixture facts = %d, want 2", len(fixture.Facts))
	}
}

func TestImportFromJSONLDefaultKind(t *testing.T) {
	input := `{"fact_key":"f1","session_id":"s1","title":"No kind field","body":"Should default to discovery kind."}
`
	fixture, _, err := ImportFromJSONL(strings.NewReader(input))
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}
	if fixture.Facts[0].Kind != "discovery" {
		t.Errorf("default kind = %q, want discovery", fixture.Facts[0].Kind)
	}
}

// -- Judge model tests --

func TestOverlapJudgeExactMatch(t *testing.T) {
	j := OverlapJudge{}
	score := j.Score("test",
		[]string{"JWT tokens are signed using RS256 with a 2048-bit RSA key."},
		[]string{"JWT tokens are signed using RS256 with a 2048-bit RSA key."},
	)
	if score <= 0.5 {
		t.Errorf("expected high score for exact match, got %.3f", score)
	}
}

func TestOverlapJudgeNoMatch(t *testing.T) {
	j := OverlapJudge{}
	score := j.Score("test",
		[]string{"The database uses WAL journal mode."},
		[]string{"JWT tokens are signed using RS256 with a 2048-bit RSA key."},
	)
	if score > 0.1 {
		t.Errorf("expected low score for unrelated texts, got %.3f", score)
	}
}

func TestOverlapJudgePartialMatch(t *testing.T) {
	j := OverlapJudge{}
	score := j.Score("test",
		[]string{"The login endpoint rate limits to 5 per minute per IP."},
		[]string{"Login rate limit is 5 attempts per minute per IP address."},
	)
	if score < 0.3 {
		t.Errorf("expected moderate overlap score, got %.3f", score)
	}
}

func TestOverlapJudgeEmptyInput(t *testing.T) {
	j := OverlapJudge{}
	score := j.Score("test", []string{}, []string{"some content"})
	if score != 0 {
		t.Errorf("expected 0 for empty retrieved, got %.3f", score)
	}
	score = j.Score("test", []string{"some content"}, []string{})
	if score != 0 {
		t.Errorf("expected 0 for empty expected, got %.3f", score)
	}
}

// -- Judge scoring in benchmark --

func TestRunBenchmarkWithJudge(t *testing.T) {
	report, err := RunBenchmark(RunOptions{
		FixturePath:     fixturePath(),
		K:               5,
		Enforce:         false,
		SkipLatencyGate: true,
		Judge:           OverlapJudge{},
	})
	if err != nil {
		t.Fatalf("benchmark run failed: %v", err)
	}
	if !report.JudgeEnabled {
		t.Fatal("expected judge to be enabled")
	}
	if report.JudgeMeanScore <= 0 {
		t.Error("expected positive mean judge score")
	}
	// Verify judge scores are populated in case results.
	hasScore := false
	for _, cr := range report.CaseResults {
		if cr.JudgeScore > 0 {
			hasScore = true
			break
		}
	}
	if !hasScore {
		t.Error("expected at least one case result with judge score > 0")
	}
}

// -- Hybrid mode test --

func TestRunBenchmarkHybridMode(t *testing.T) {
	report, err := RunBenchmark(RunOptions{
		FixturePath:     fixturePath(),
		K:               5,
		Enforce:         false,
		SkipLatencyGate: true,
		Mode:            "hybrid",
	})
	if err != nil {
		t.Fatalf("hybrid benchmark run failed: %v", err)
	}
	if report.RetrievalMode != "hybrid" {
		t.Errorf("retrieval mode = %q, want hybrid", report.RetrievalMode)
	}
	// Hybrid mode should still pass most questions.
	if report.FailedQuestions > report.TotalQuestions/2 {
		t.Errorf("too many hybrid failures: %d/%d", report.FailedQuestions, report.TotalQuestions)
	}
}

func TestRunBenchmarkReportIncludesRetrievalMode(t *testing.T) {
	report, err := RunBenchmark(RunOptions{
		FixturePath:     fixturePath(),
		K:               5,
		Enforce:         false,
		SkipLatencyGate: true,
	})
	if err != nil {
		t.Fatalf("benchmark run failed: %v", err)
	}
	if report.RetrievalMode != "fts5" {
		t.Errorf("default retrieval mode = %q, want fts5", report.RetrievalMode)
	}
}
