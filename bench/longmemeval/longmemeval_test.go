package longmemeval

import (
	"encoding/json"
	"os"
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

// -- JSON array (cleaned dataset) import tests --

func TestImportFromJSONArrayBasic(t *testing.T) {
	input := `[
  {
    "question_id": "e47becba",
    "question_type": "single-session-user",
    "question": "What degree did I graduate with?",
    "question_date": "2023/05/30 (Tue) 23:40",
    "answer": "Business Administration",
    "answer_session_ids": ["sharegpt_yywfIrx_0"],
    "haystack_dates": ["2023/05/20 (Sat) 02:21","2023/05/20 (Sat) 02:57"],
    "haystack_session_ids": ["sharegpt_yywfIrx_0","85a1be56_1"],
    "haystack_sessions": [
      [
        {"role":"user","content":"The farmer needs to transport a fox."},
        {"role":"assistant","content":"To solve this puzzle, take the chicken first."}
      ],
      [
        {"role":"user","content":"Whats your favorite color?"},
        {"role":"assistant","content":"I like blue."}
      ]
    ]
  },
  {
    "question_id": "118b2229",
    "question_type": "single-session-user",
    "question": "How long is my daily commute to work?",
    "question_date": "2023/05/30 (Tue) 20:36",
    "answer": "45 minutes each way",
    "answer_session_ids": ["db73b7e4_4"],
    "haystack_dates": ["2023/05/20 (Sat) 03:29","2023/05/20 (Sat) 07:48"],
    "haystack_session_ids": ["db73b7e4_4","sharegpt_5V5H2HN_0"],
    "haystack_sessions": [
      [
        {"role":"user","content":"Looking for advice on leather boots."},
        {"role":"assistant","content":"Condition every 2-3 months."}
      ],
      [
        {"role":"user","content":"What is the best time to visit Bali?"},
        {"role":"assistant","content":"April to October."}
      ]
    ]
  }
]`

	fixture, result, err := ImportFromJSONArray(strings.NewReader(input))
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}
	if result.RecordsRead != 2 {
		t.Errorf("records read = %d, want 2", result.RecordsRead)
	}
	// 2 records * 2 haystack sessions each = 4 facts
	if result.FactsCreated != 4 {
		t.Errorf("facts created = %d, want 4", result.FactsCreated)
	}
	if result.QuestionsCreated != 2 {
		t.Errorf("questions created = %d, want 2", result.QuestionsCreated)
	}
	if len(fixture.Facts) != 4 {
		t.Errorf("fixture facts = %d, want 4", len(fixture.Facts))
	}
	if len(fixture.Questions) != 2 {
		t.Errorf("fixture questions = %d, want 2", len(fixture.Questions))
	}
	// Verify first question fields.
	q0 := fixture.Questions[0]
	if q0.ID != "e47becba" {
		t.Errorf("q[0].id = %q, want e47becba", q0.ID)
	}
	if q0.Category != "single-session-user" {
		t.Errorf("q[0].category = %q, want single-session-user", q0.Category)
	}
	if q0.Query != "What degree did I graduate with?" {
		t.Errorf("q[0].query = %q", q0.Query)
	}
	if len(q0.ExpectedFactKeys) != 1 {
		t.Errorf("q[0].expected_fact_keys = %v, want [e47becba_hs_0]", q0.ExpectedFactKeys)
	}
	if len(q0.ExpectedAnswers) != 1 || q0.ExpectedAnswers[0] != "Business Administration" {
		t.Errorf("q[0].expected_answers = %v, want [Business Administration]", q0.ExpectedAnswers)
	}
	// Answer session sharegpt_yywfIrx_0 maps to fact key e47becba_hs_0.
	if q0.ExpectedFactKeys[0] != "e47becba_hs_0" {
		t.Errorf("q[0].expected_fact_keys[0] = %q, want e47becba_hs_0", q0.ExpectedFactKeys[0])
	}
	// Verify fact bodies include conversation text.
	factBody := fixture.Facts[0].Body
	if !strings.Contains(factBody, "fox") {
		t.Errorf("fact[0] body should contain 'fox', got %q", factBody)
	}
	if strings.Contains(factBody, "User:") {
		t.Errorf("fact[0] body should not contain role prefixes, got %q", factBody)
	}
	// Verify sessions were registered.
	if len(fixture.Sessions) < 2 {
		t.Errorf("expected at least 2 sessions, got %d", len(fixture.Sessions))
	}
	if q0.Distance != "near" {
		t.Errorf("q[0].distance = %q, want near", q0.Distance)
	}
}

func TestImportFromJSONArrayDeduplicatesRepeatedHaystackSessions(t *testing.T) {
	input := `[
  {
    "question_id": "q1",
    "question_type": "test",
    "question": "where did I study?",
    "answer": "University",
    "answer_session_ids": ["shared_1"],
    "haystack_dates": ["2023/05/20"],
    "haystack_session_ids": ["shared_1"],
    "haystack_sessions": [
      [
        {"role":"user","content":"I studied business administration."},
        {"role":"assistant","content":"You graduated with a business degree."}
      ]
    ]
  },
  {
    "question_id": "q2",
    "question_type": "test",
    "question": "what degree did I finish?",
    "answer": "Business",
    "answer_session_ids": ["shared_1"],
    "haystack_dates": ["2023/05/21"],
    "haystack_session_ids": ["shared_1"],
    "haystack_sessions": [
      [
        {"role":"user","content":"I studied business administration."},
        {"role":"assistant","content":"You graduated with a business degree."}
      ]
    ]
  }
]`

	fixture, result, err := ImportFromJSONArray(strings.NewReader(input))
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}
	if result.FactsCreated != 1 {
		t.Fatalf("facts created = %d, want 1", result.FactsCreated)
	}
	if len(fixture.Facts) != 1 {
		t.Fatalf("fixture facts = %d, want 1", len(fixture.Facts))
	}
	if got, want := fixture.Questions[0].ExpectedFactKeys[0], fixture.Questions[1].ExpectedFactKeys[0]; got != want {
		t.Fatalf("expected reused fact key, got %q and %q", got, want)
	}
}

func TestBuildConversationBodyNormalizesWhitespaceAndRoles(t *testing.T) {
	body := buildConversationBody([]HaystackMessage{
		{Role: "user", Content: "  What   degree did I graduate with?  "},
		{Role: "assistant", Content: "\nBusiness   Administration \n"},
	})

	if strings.Contains(body, "User:") || strings.Contains(body, "Assistant:") {
		t.Fatalf("body should not include role labels: %q", body)
	}
	if strings.Contains(body, "  ") {
		t.Fatalf("body should normalize repeated whitespace: %q", body)
	}
	if !strings.Contains(body, "Business Administration") {
		t.Fatalf("body should preserve semantic content: %q", body)
	}
}

func TestContainmentJudgeFindsAnswerInsideLongTranscript(t *testing.T) {
	score := ContainmentJudge{}.Score(
		"What degree did I graduate with?",
		[]string{"I studied for years and eventually graduated with a Business Administration degree after moving cities."},
		[]string{"Business Administration"},
	)
	if score < 1.0 {
		t.Fatalf("containment score = %.3f, want 1.0", score)
	}
}

func TestFirstLineSkipsLeadingPunctuation(t *testing.T) {
	title := firstLine(". What happens if a candidate placed by OODA Team doesn't work out?\n\nMore text follows.", 80)
	if title == "" {
		t.Fatal("title should not be empty")
	}
	if strings.HasPrefix(title, ".") {
		t.Fatalf("title should trim leading punctuation: %q", title)
	}
	if !strings.Contains(title, "What happens if a candidate") {
		t.Fatalf("unexpected title: %q", title)
	}
}

func TestImportFromJSONArrayEmptyArray(t *testing.T) {
	_, _, err := ImportFromJSONArray(strings.NewReader("[]"))
	if err == nil {
		t.Fatal("expected error for empty array")
	}
	if !strings.Contains(err.Error(), "no records") {
		t.Errorf("error = %q, want 'no records'", err.Error())
	}
}

func TestImportFromJSONArrayInvalidJSON(t *testing.T) {
	_, _, err := ImportFromJSONArray(strings.NewReader("not json at all"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestImportFromJSONArrayMismatchedArrays(t *testing.T) {
	input := `[
  {
    "question_id": "test1",
    "question_type": "single-session-user",
    "question": "test q",
    "answer": "test a",
    "answer_session_ids": ["s1"],
    "haystack_dates": ["2023/05/20"],
    "haystack_session_ids": ["s1","s2"],
    "haystack_sessions": [
      [{"role":"user","content":"msg1"}]
    ]
  }
]`
	fixture, result, err := ImportFromJSONArray(strings.NewReader(input))
	if err != nil {
		t.Fatalf("import should succeed with partial errors: %v", err)
	}
	if result.RecordsRead != 1 {
		t.Errorf("records read = %d, want 1", result.RecordsRead)
	}
	if result.FactsCreated != 0 {
		t.Errorf("expected 0 facts for mismatched record, got %d", result.FactsCreated)
	}
	if len(result.Errors) != 1 {
		t.Errorf("expected 1 error, got %d: %v", len(result.Errors), result.Errors)
	}
	if len(fixture.Facts) != 0 {
		t.Errorf("expected 0 fixture facts, got %d", len(fixture.Facts))
	}
}

// TestImportFromJSONArrayRunBenchmark runs the benchmark on JSON-array-imported data
// to verify end-to-end integration (format detection + execution).
func TestImportFromJSONArrayRunBenchmark(t *testing.T) {
	// Write a tiny JSON array to a temp file and run benchmark with DatasetPath.
	smallInput := `[
  {
    "question_id": "q01",
    "question_type": "test",
    "question": "fox chicken grain puzzle",
    "question_date": "2023/05/30 23:40",
    "answer": "take the chicken first",
    "answer_session_ids": ["s1"],
    "haystack_dates": ["2023/05/20"],
    "haystack_session_ids": ["s1"],
    "haystack_sessions": [
      [
        {"role":"user","content":"Can you help with a fox chicken grain puzzle?"},
        {"role":"assistant","content":"Take the chicken first, then the fox, then the grain."}
      ]
    ]
  },
  {
    "question_id": "q02",
    "question_type": "test",
    "question": "boot care advice",
    "question_date": "2023/05/30 20:36",
    "answer": "condition every 2-3 months",
    "answer_session_ids": ["s2"],
    "haystack_dates": ["2023/05/21"],
    "haystack_session_ids": ["s2"],
    "haystack_sessions": [
      [
        {"role":"user","content":"How to care for leather boots?"},
        {"role":"assistant","content":"Condition every 2-3 months with leather conditioner."}
      ]
    ]
  }
]`
	tmpDir := t.TempDir()
	tmpFile := tmpDir + "/test_array.json"
	if err := os.WriteFile(tmpFile, []byte(smallInput), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	report, err := RunBenchmark(RunOptions{
		DatasetPath:     tmpFile,
		K:               5,
		Enforce:         false,
		SkipLatencyGate: true,
	})
	if err != nil {
		t.Fatalf("benchmark run failed: %v", err)
	}
	if report.TotalQuestions != 2 {
		t.Errorf("total questions = %d, want 2", report.TotalQuestions)
	}
	if report.FailedQuestions > report.TotalQuestions {
		t.Errorf("failed questions %d > total %d", report.FailedQuestions, report.TotalQuestions)
	}
	if len(report.CaseResults) != 2 {
		t.Errorf("case results = %d, want 2", len(report.CaseResults))
	}
}

func TestRunBenchmarkParallelMatchesSerial(t *testing.T) {
	opts := RunOptions{
		FixturePath:     fixturePath(),
		K:               5,
		Enforce:         false,
		SkipLatencyGate: true,
	}

	serial, err := RunBenchmark(opts)
	if err != nil {
		t.Fatalf("serial benchmark failed: %v", err)
	}

	parallel, err := RunBenchmark(RunOptions{
		FixturePath:     fixturePath(),
		K:               5,
		Enforce:         false,
		SkipLatencyGate: true,
		Workers:         4,
	})
	if err != nil {
		t.Fatalf("parallel benchmark failed: %v", err)
	}

	if serial.TotalQuestions != parallel.TotalQuestions {
		t.Fatalf("total questions mismatch: %d != %d", serial.TotalQuestions, parallel.TotalQuestions)
	}
	if serial.PassedQuestions != parallel.PassedQuestions {
		t.Fatalf("passed questions mismatch: %d != %d", serial.PassedQuestions, parallel.PassedQuestions)
	}
	if len(serial.CaseResults) != len(parallel.CaseResults) {
		t.Fatalf("case results length mismatch: %d != %d", len(serial.CaseResults), len(parallel.CaseResults))
	}
	for i := range serial.CaseResults {
		if serial.CaseResults[i].QuestionID != parallel.CaseResults[i].QuestionID {
			t.Fatalf("question order mismatch at %d: %q != %q", i, serial.CaseResults[i].QuestionID, parallel.CaseResults[i].QuestionID)
		}
		if serial.CaseResults[i].Pass != parallel.CaseResults[i].Pass {
			t.Fatalf("pass mismatch for %s: %v != %v", serial.CaseResults[i].QuestionID, serial.CaseResults[i].Pass, parallel.CaseResults[i].Pass)
		}
		if strings.Join(serial.CaseResults[i].ExpectedKeys, ",") != strings.Join(parallel.CaseResults[i].ExpectedKeys, ",") {
			t.Fatalf("expected keys mismatch for %s", serial.CaseResults[i].QuestionID)
		}
		if strings.Join(serial.CaseResults[i].TopKeys, ",") != strings.Join(parallel.CaseResults[i].TopKeys, ",") {
			t.Fatalf("top keys mismatch for %s", serial.CaseResults[i].QuestionID)
		}
	}
}
