// Package longmemeval implements a LongMemEval-style benchmark harness for Ohara.
//
// LongMemEval evaluates an agent memory system's ability to retain and recall facts
// across multiple sessions (time steps). Facts are inserted in earlier sessions,
// and questions are asked in later sessions, measuring recall degradation over
// increasing session distances.
//
// The harness is self-contained with a deterministic fixture — no external dataset
// or model dependency is required for the baseline spine.
package longmemeval

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ashwnn/ohara/internal/store"
)

// Fixture is the top-level benchmark fixture loaded from JSON.
type Fixture struct {
	Description string             `json:"description"`
	Sessions    []SessionFixture   `json:"sessions"`
	Facts       []FactFixture      `json:"facts"`
	Questions   []QuestionFixture  `json:"questions"`
	Thresholds  ThresholdsFixture  `json:"thresholds"`
}

// SessionFixture represents a logical session (time step) in the benchmark.
type SessionFixture struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Order int    `json:"order"`
}

// FactFixture represents a single fact (memory) to be inserted.
type FactFixture struct {
	Key       string `json:"key"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Kind      string `json:"kind"`
	Domain    string `json:"domain"`
	SessionID string `json:"session_id"`
	Turn      int    `json:"turn"`
}

// QuestionFixture represents a single evaluation question.
type QuestionFixture struct {
	ID               string   `json:"id"`
	Category         string   `json:"category"`
	Distance         string   `json:"distance"`
	DistanceSessions int      `json:"distance_sessions"`
	Query            string   `json:"query"`
	ExpectedFactKeys []string `json:"expected_fact_keys"`
	SessionID        string   `json:"session_id"`
}

// ThresholdsFixture defines quality gates for the benchmark.
type ThresholdsFixture struct {
	OverallRecallAt3 float64 `json:"overall_recall_at_3"`
	NearRecallAt3    float64 `json:"near_recall_at_3"`
	MediumRecallAt3  float64 `json:"medium_recall_at_3"`
	FarRecallAt3     float64 `json:"far_recall_at_3"`
	MRROverall       float64 `json:"mrr_overall"`
	LatencyP95MsMax  float64 `json:"latency_p95_ms_max"`
	LatencyMaxMsMax  float64 `json:"latency_max_ms_max"`
}

// RunOptions configures a benchmark run.
type RunOptions struct {
	FixturePath     string
	K               int
	Enforce         bool
	SkipLatencyGate bool
}

// Metrics holds computed retrieval quality metrics.
type Metrics struct {
	RecallAt1  float64 `json:"recall_at_1"`
	RecallAt3  float64 `json:"recall_at_3"`
	RecallAt5  float64 `json:"recall_at_5"`
	MRR        float64 `json:"mrr"`
	NDCGAt5    float64 `json:"ndcg_at_5"`
	TotalCases int     `json:"total_cases"`
	Passed     int     `json:"passed"`
	Failed     int     `json:"failed"`
}

// LatencyMetrics holds per-case timing statistics.
type LatencyMetrics struct {
	P50Ms  float64 `json:"p50_ms"`
	P95Ms  float64 `json:"p95_ms"`
	MaxMs  float64 `json:"max_ms"`
	MeanMs float64 `json:"mean_ms"`
}

// CaseResult captures the outcome of a single question evaluation.
type CaseResult struct {
	QuestionID   string   `json:"question_id"`
	Category     string   `json:"category"`
	Distance     string   `json:"distance"`
	DistanceSess int      `json:"distance_sessions"`
	Pass         bool     `json:"pass"`
	Reason       string   `json:"failure_reason,omitempty"`
	TopIDs       []int64  `json:"top_ids"`
	ExpectedIDs  []int64  `json:"expected_ids"`
	TopKeys      []string `json:"top_keys"`
	ExpectedKeys []string `json:"expected_keys"`
	DurationMs   float64  `json:"duration_ms"`
	Query        string   `json:"query"`
}

// Report is the full benchmark output.
type Report struct {
	FixtureDescription string             `json:"fixture_description"`
	TotalQuestions     int                `json:"total_questions"`
	PassedQuestions    int                `json:"passed_questions"`
	FailedQuestions    int                `json:"failed_questions"`
	OverallMetrics     Metrics            `json:"overall_metrics"`
	DistanceMetrics    map[string]Metrics `json:"distance_metrics"`
	CategoryMetrics    map[string]Metrics `json:"category_metrics"`
	Latency            LatencyMetrics     `json:"latency"`
	CaseResults        []CaseResult       `json:"case_results"`
	Runtime            time.Duration      `json:"-"`
	RuntimeMs          float64            `json:"runtime_ms"`
	Thresholds         ThresholdsFixture  `json:"thresholds"`
	Failures           []QuestionFailure  `json:"failures"`
}

// QuestionFailure records a failed question with diagnostic info.
type QuestionFailure struct {
	QuestionID   string   `json:"question_id"`
	Category     string   `json:"category"`
	Distance     string   `json:"distance"`
	Query        string   `json:"query"`
	ExpectedKeys []string `json:"expected_keys"`
	ActualKeys   []string `json:"actual_keys"`
	Reason       string   `json:"reason"`
}

// questionAgg accumulates per-case stats for metric computation.
type questionAgg struct {
	count  int
	passed int
	hit1   int
	hit3   int
	hit5   int
	rrSum  float64
	ndcgSum float64
}

// LoadFixture reads and parses a LongMemEval fixture JSON file.
func LoadFixture(path string) (Fixture, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Fixture{}, err
	}
	var fixture Fixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		return Fixture{}, err
	}
	if len(fixture.Facts) == 0 {
		return Fixture{}, fmt.Errorf("fixture has no facts")
	}
	if len(fixture.Questions) == 0 {
		return Fixture{}, fmt.Errorf("fixture has no questions")
	}
	return fixture, nil
}

// RunBenchmark executes the full LongMemEval benchmark and returns a report.
func RunBenchmark(opts RunOptions) (Report, error) {
	start := time.Now()
	if opts.K <= 0 {
		opts.K = 5
	}
	if opts.FixturePath == "" {
		opts.FixturePath = filepath.Join("bench", "longmemeval", "fixture.json")
	}

	fixture, err := LoadFixture(opts.FixturePath)
	if err != nil {
		return Report{}, err
	}
	thresholds := withDefaultThresholds(fixture.Thresholds)

	// Seed store with facts.
	s, keyToID, err := seedStore(fixture)
	if err != nil {
		return Report{}, err
	}
	defer s.Close()

	globalAgg := &questionAgg{}
	distanceAggs := map[string]*questionAgg{}
	categoryAggs := map[string]*questionAgg{}
	failures := make([]QuestionFailure, 0, 8)
	caseResults := make([]CaseResult, 0, len(fixture.Questions))

	for _, q := range fixture.Questions {
		caseStart := time.Now()

		// Run search.
		results, searchErr := s.SearchMemories(q.Query, "longmemeval", "", "", "", store.MemoryStatusActive, opts.K, "")
		durationMs := float64(time.Since(caseStart)) / float64(time.Millisecond)

		if searchErr != nil {
			cr := CaseResult{
				QuestionID:   q.ID,
				Category:     q.Category,
				Distance:     q.Distance,
				DistanceSess: q.DistanceSessions,
				Pass:         false,
				Reason:       searchErr.Error(),
				ExpectedKeys: q.ExpectedFactKeys,
				DurationMs:   durationMs,
				Query:        q.Query,
			}
			caseResults = append(caseResults, cr)
			failures = append(failures, QuestionFailure{
				QuestionID:   q.ID,
				Category:     q.Category,
				Distance:     q.Distance,
				Query:        q.Query,
				ExpectedKeys: q.ExpectedFactKeys,
				Reason:       searchErr.Error(),
			})
			continue
		}

		expectedIDs := keysToIDs(keyToID, q.ExpectedFactKeys)
		top := topIDs(results, opts.K)
		topKeys := idsToKeys(keyToID, top)

		hits := countHitsInTop(top, expectedIDs)
		passed := hits > 0
		reason := ""
		if !passed {
			reason = fmt.Sprintf("no expected facts in top-%d results", opts.K)
		}

		cr := CaseResult{
			QuestionID:   q.ID,
			Category:     q.Category,
			Distance:     q.Distance,
			DistanceSess: q.DistanceSessions,
			Pass:         passed,
			Reason:       reason,
			TopIDs:       top,
			ExpectedIDs:  expectedIDs,
			TopKeys:      topKeys,
			ExpectedKeys: q.ExpectedFactKeys,
			DurationMs:   durationMs,
			Query:        q.Query,
		}
		caseResults = append(caseResults, cr)

		// Update aggregators.
		updateAggFromResults(globalAgg, passed, results, expectedIDs, opts.K)
		if agg := getOrCreateAgg(distanceAggs, q.Distance); agg != nil {
			updateAggFromResults(agg, passed, results, expectedIDs, opts.K)
		}
		if agg := getOrCreateAgg(categoryAggs, q.Category); agg != nil {
			updateAggFromResults(agg, passed, results, expectedIDs, opts.K)
		}

		if !passed {
			failures = append(failures, QuestionFailure{
				QuestionID:   q.ID,
				Category:     q.Category,
				Distance:     q.Distance,
				Query:        q.Query,
				ExpectedKeys: q.ExpectedFactKeys,
				ActualKeys:   topKeys,
				Reason:       reason,
			})
		}
	}

	report := Report{
		FixtureDescription: fixture.Description,
		TotalQuestions:     len(fixture.Questions),
		PassedQuestions:    globalAgg.passed,
		FailedQuestions:    globalAgg.count - globalAgg.passed,
		OverallMetrics:     aggToMetrics(globalAgg),
		DistanceMetrics:    mapAggsToMetrics(distanceAggs),
		CategoryMetrics:    mapAggsToMetrics(categoryAggs),
		CaseResults:        caseResults,
		Runtime:            time.Since(start),
		RuntimeMs:          float64(time.Since(start)) / float64(time.Millisecond),
		Thresholds:         thresholds,
		Failures:           sortFailures(failures),
	}
	report.Latency = computeLatency(caseResults)

	if opts.SkipLatencyGate {
		report.Thresholds.LatencyP95MsMax = math.MaxFloat64
		report.Thresholds.LatencyMaxMsMax = math.MaxFloat64
	}
	if opts.Enforce {
		if err := enforceThresholds(report); err != nil {
			return report, err
		}
	}
	return report, nil
}

// seedStore creates a temporary store, inserts all facts, and returns a key→ID map.
func seedStore(fixture Fixture) (*store.Store, map[string]int64, error) {
	tmp, err := os.MkdirTemp("", "ohara-bench-longmemeval-")
	if err != nil {
		return nil, nil, err
	}

	cfg := store.FallbackConfig(tmp)
	s, err := store.New(cfg)
	if err != nil {
		os.RemoveAll(tmp)
		return nil, nil, err
	}

	keyToID := map[string]int64{}
	for _, fact := range fixture.Facts {
		id, err := s.AddMemory(store.AddMemoryParams{
			ProjectID: "longmemeval",
			Kind:      fact.Kind,
			Title:     fact.Title,
			Body:      fact.Body,
			Domain:    fact.Domain,
			SessionID: fact.SessionID,
		})
		if err != nil {
			s.Close()
			os.RemoveAll(tmp)
			return nil, nil, fmt.Errorf("seed fact %s: %w", fact.Key, err)
		}
		keyToID[fact.Key] = id
	}

	return s, keyToID, nil
}

func updateAggFromResults(agg *questionAgg, passed bool, results []store.MemoryItem, expectedIDs []int64, k int) {
	if agg == nil {
		return
	}
	agg.count++
	if passed {
		agg.passed++
	}

	firstRank := firstRelevantRank(results, expectedIDs)
	if firstRank == 1 {
		agg.hit1++
	}
	if firstRank > 0 && firstRank <= minInt(3, k) {
		agg.hit3++
	}
	if firstRank > 0 && firstRank <= minInt(5, k) {
		agg.hit5++
	}
	if firstRank > 0 {
		agg.rrSum += 1.0 / float64(firstRank)
	}
	agg.ndcgSum += ndcgAtK(results, expectedIDs, minInt(5, k))
}

func aggToMetrics(agg *questionAgg) Metrics {
	m := Metrics{
		TotalCases: agg.count,
		Passed:     agg.passed,
		Failed:     agg.count - agg.passed,
	}
	if agg.count > 0 {
		total := float64(agg.count)
		m.RecallAt1 = float64(agg.hit1) / total
		m.RecallAt3 = float64(agg.hit3) / total
		m.RecallAt5 = float64(agg.hit5) / total
		m.MRR = agg.rrSum / total
		m.NDCGAt5 = agg.ndcgSum / total
	}
	return m
}

func mapAggsToMetrics(aggs map[string]*questionAgg) map[string]Metrics {
	out := map[string]Metrics{}
	for key, agg := range aggs {
		out[key] = aggToMetrics(agg)
	}
	return out
}

func computeLatency(results []CaseResult) LatencyMetrics {
	if len(results) == 0 {
		return LatencyMetrics{}
	}
	durations := make([]float64, len(results))
	var sum float64
	for i, cr := range results {
		durations[i] = cr.DurationMs
		sum += cr.DurationMs
	}
	sort.Float64s(durations)

	n := len(durations)
	return LatencyMetrics{
		P50Ms:  percentileSorted(durations, 0.50),
		P95Ms:  percentileSorted(durations, 0.95),
		MaxMs:  durations[n-1],
		MeanMs: sum / float64(n),
	}
}

func withDefaultThresholds(in ThresholdsFixture) ThresholdsFixture {
	out := in
	if out.OverallRecallAt3 <= 0 {
		out.OverallRecallAt3 = 0.75
	}
	if out.NearRecallAt3 <= 0 {
		out.NearRecallAt3 = 0.80
	}
	if out.MediumRecallAt3 <= 0 {
		out.MediumRecallAt3 = 0.70
	}
	if out.FarRecallAt3 <= 0 {
		out.FarRecallAt3 = 0.60
	}
	if out.MRROverall <= 0 {
		out.MRROverall = 0.65
	}
	if out.LatencyP95MsMax <= 0 {
		out.LatencyP95MsMax = 100
	}
	if out.LatencyMaxMsMax <= 0 {
		out.LatencyMaxMsMax = 500
	}
	return out
}

func enforceThresholds(r Report) error {
	var failures []string

	if r.OverallMetrics.RecallAt3 < r.Thresholds.OverallRecallAt3 {
		failures = append(failures, fmt.Sprintf("overall recall@3 %.3f < %.3f", r.OverallMetrics.RecallAt3, r.Thresholds.OverallRecallAt3))
	}
	if r.OverallMetrics.MRR < r.Thresholds.MRROverall {
		failures = append(failures, fmt.Sprintf("overall MRR %.3f < %.3f", r.OverallMetrics.MRR, r.Thresholds.MRROverall))
	}

	if m, ok := r.DistanceMetrics["near"]; ok && m.RecallAt3 < r.Thresholds.NearRecallAt3 {
		failures = append(failures, fmt.Sprintf("near recall@3 %.3f < %.3f", m.RecallAt3, r.Thresholds.NearRecallAt3))
	}
	if m, ok := r.DistanceMetrics["medium"]; ok && m.RecallAt3 < r.Thresholds.MediumRecallAt3 {
		failures = append(failures, fmt.Sprintf("medium recall@3 %.3f < %.3f", m.RecallAt3, r.Thresholds.MediumRecallAt3))
	}
	if m, ok := r.DistanceMetrics["far"]; ok && m.RecallAt3 < r.Thresholds.FarRecallAt3 {
		failures = append(failures, fmt.Sprintf("far recall@3 %.3f < %.3f", m.RecallAt3, r.Thresholds.FarRecallAt3))
	}

	if r.Latency.P95Ms > r.Thresholds.LatencyP95MsMax {
		failures = append(failures, fmt.Sprintf("latency p95 %.1fms > %.0fms", r.Latency.P95Ms, r.Thresholds.LatencyP95MsMax))
	}
	if r.Latency.MaxMs > r.Thresholds.LatencyMaxMsMax {
		failures = append(failures, fmt.Sprintf("latency max %.1fms > %.0fms", r.Latency.MaxMs, r.Thresholds.LatencyMaxMsMax))
	}

	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

// -- helpers --

func keysToIDs(keyToID map[string]int64, keys []string) []int64 {
	out := make([]int64, 0, len(keys))
	for _, key := range keys {
		if id := keyToID[key]; id > 0 {
			out = append(out, id)
		}
	}
	return out
}

func idsToKeys(keyToID map[string]int64, ids []int64) []string {
	idToKey := map[int64]string{}
	for k, v := range keyToID {
		idToKey[v] = k
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if key, ok := idToKey[id]; ok {
			out = append(out, key)
		}
	}
	return out
}

func topIDs(items []store.MemoryItem, k int) []int64 {
	if k <= 0 || k > len(items) {
		k = len(items)
	}
	out := make([]int64, 0, k)
	for i := 0; i < k; i++ {
		out = append(out, items[i].ID)
	}
	return out
}

func countHitsInTop(top []int64, expected []int64) int {
	count := 0
	for _, id := range top {
		if containsInt64(expected, id) {
			count++
		}
	}
	return count
}

func firstRelevantRank(results []store.MemoryItem, expectedIDs []int64) int {
	if len(expectedIDs) == 0 {
		return 0
	}
	for i, item := range results {
		if containsInt64(expectedIDs, item.ID) {
			return i + 1
		}
	}
	return 0
}

func ndcgAtK(results []store.MemoryItem, expectedIDs []int64, k int) float64 {
	if k <= 0 || len(expectedIDs) == 0 {
		return 0
	}
	if k > len(results) {
		k = len(results)
	}
	dcg := 0.0
	for i := 0; i < k; i++ {
		relevant := 0.0
		if containsInt64(expectedIDs, results[i].ID) {
			relevant = 1.0
		}
		if relevant > 0 {
			dcg += (math.Pow(2, relevant) - 1) / math.Log2(float64(i+2))
		}
	}
	idcg := 0.0
	for i := 0; i < minInt(len(expectedIDs), k); i++ {
		idcg += (math.Pow(2, 1) - 1) / math.Log2(float64(i+2))
	}
	if idcg == 0 {
		return 0
	}
	return dcg / idcg
}

func containsInt64(list []int64, value int64) bool {
	for _, v := range list {
		if v == value {
			return true
		}
	}
	return false
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func percentileSorted(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[len(sorted)-1]
	}
	idx := p * float64(len(sorted)-1)
	lo := int(math.Floor(idx))
	hi := int(math.Ceil(idx))
	if lo == hi {
		return sorted[lo]
	}
	frac := idx - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

func getOrCreateAgg(aggs map[string]*questionAgg, key string) *questionAgg {
	if key == "" {
		return nil
	}
	if agg, ok := aggs[key]; ok {
		return agg
	}
	agg := &questionAgg{}
	aggs[key] = agg
	return agg
}

func sortFailures(failures []QuestionFailure) []QuestionFailure {
	order := map[string]int{"far": 0, "medium": 1, "near": 2}
	sort.SliceStable(failures, func(i, j int) bool {
		if failures[i].Distance == failures[j].Distance {
			return failures[i].QuestionID < failures[j].QuestionID
		}
		return order[failures[i].Distance] < order[failures[j].Distance]
	})
	return failures
}

// -- string helpers for report rendering --

// SortedDistanceKeys returns distance keys in decreasing order (far→medium→near).
func SortedDistanceKeys(input map[string]Metrics) []string {
	keys := make([]string, 0, len(input))
	for k := range input {
		keys = append(keys, k)
	}
	order := map[string]int{"near": 2, "medium": 1, "far": 0}
	sort.SliceStable(keys, func(i, j int) bool {
		return order[keys[i]] < order[keys[j]]
	})
	return keys
}

// SortedCategoryKeys returns category keys in alphabetical order.
func SortedCategoryKeys(input map[string]Metrics) []string {
	keys := make([]string, 0, len(input))
	for k := range input {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// SortedCaseKeys returns string keys in alphabetical order (for map[string]int).
func SortedCaseKeys(input map[string]int) []string {
	keys := make([]string, 0, len(input))
	for k := range input {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
