// Package locomo implements a LoCoMo-style benchmark harness for Ohara.
//
// LoCoMo evaluates long-context memory retrieval: conversations with ~600 turns
// and ~26k tokens each, testing single-hop, multi-hop, temporal, and open-domain
// recall across long dialogs.
//
// The harness is self-contained with a deterministic fixture — no external
// dataset or model dependency is required for the baseline spine.
//
// For public LoCoMo dataset integration:
//   - ImportFromJSON imports a LoCoMo dataset JSON file into a Fixture.
//
// A built-in OverlapJudge provides baseline answer evaluation without LLM
// dependency, using the JudgeModel interface from bench/longmemeval.
package locomo

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ashwnn/ohara/bench/longmemeval"
	"github.com/ashwnn/ohara/internal/store"
)

// Fixture is the top-level benchmark fixture loaded from JSON.
type Fixture struct {
	Description   string                `json:"description"`
	Conversations []ConversationFixture `json:"conversations"`
	Questions     []QuestionFixture     `json:"questions"`
	Thresholds    ThresholdsFixture     `json:"thresholds"`
}

// ConversationFixture represents one long conversation in the benchmark.
type ConversationFixture struct {
	ID       string            `json:"id"`
	Messages []MessageFixture  `json:"messages"`
}

// MessageFixture is a single turn in a conversation.
type MessageFixture struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// QuestionFixture represents a single evaluation question.
type QuestionFixture struct {
	ID               string   `json:"id"`
	ConversationID   string   `json:"conversation_id"`
	Category         string   `json:"category"` // single-hop, multi-hop, temporal, open-domain
	Question         string   `json:"question"`
	ExpectedFactKeys []string `json:"expected_fact_keys"`
	ExpectedAnswers  []string `json:"expected_answers,omitempty"`
}

// ThresholdsFixture defines quality gates for the benchmark.
type ThresholdsFixture struct {
	OverallRecallAt3 float64 `json:"overall_recall_at_3"`
	MRROverall       float64 `json:"mrr_overall"`
	LatencyP95MsMax  float64 `json:"latency_p95_ms_max"`
	LatencyMaxMsMax  float64 `json:"latency_max_ms_max"`
}

// RunOptions configures a benchmark run.
type RunOptions struct {
	FixturePath    string
	K              int
	Enforce        bool
	SkipLatency    bool
	Judge          longmemeval.JudgeModel
	Mode           string // "fts5" (default) or "hybrid"
	QuestionsLimit int
	Sweep          bool
	Workers        int
}

// SweepMode defines a single mode config for LoCoMo sweeps.
type SweepMode struct {
	Name string
	Mode string
}

// DefaultSweepModes returns the standard sweep modes for LoCoMo.
func DefaultSweepModes() []SweepMode {
	return []SweepMode{
		{Name: "fts5", Mode: "fts5"},
		{Name: "hybrid-deterministic", Mode: "hybrid"},
	}
}

// SweepResult holds results for one mode in a sweep.
type SweepResult struct {
	Name   string `json:"name"`
	Mode   string `json:"mode"`
	Report Report `json:"report"`
	Error  string `json:"error,omitempty"`
}

// RunSweep runs the benchmark across all sweep modes.
func RunSweep(baseOpts RunOptions, modes []SweepMode) []SweepResult {
	if modes == nil {
		modes = DefaultSweepModes()
	}
	results := make([]SweepResult, 0, len(modes))
	for _, sm := range modes {
		opts := baseOpts
		opts.Mode = sm.Mode
		opts.Sweep = false

		report, err := RunBenchmark(opts)
		sr := SweepResult{Name: sm.Name, Mode: sm.Mode}
		if err != nil {
			sr.Error = err.Error()
		}
		sr.Report = report
		results = append(results, sr)
	}
	return results
}

// Report is the full benchmark output.
type Report struct {
	FixtureDescription string              `json:"fixture_description"`
	TotalQuestions     int                 `json:"total_questions"`
	PassedQuestions    int                 `json:"passed_questions"`
	FailedQuestions    int                 `json:"failed_questions"`
	OverallMetrics     Metrics             `json:"overall_metrics"`
	CategoryMetrics    map[string]Metrics  `json:"category_metrics"`
	Latency            LatencyMetrics      `json:"latency"`
	CaseResults        []CaseResult        `json:"case_results"`
	Runtime            time.Duration       `json:"-"`
	RuntimeMs          float64             `json:"runtime_ms"`
	Thresholds         ThresholdsFixture   `json:"thresholds"`
	Failures           []QuestionFailure   `json:"failures"`
	JudgeEnabled       bool                `json:"judge_enabled"`
	JudgeMeanScore     float64             `json:"judge_mean_score,omitempty"`
	RetrievalMode      string              `json:"retrieval_mode"`
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
	QuestionID     string   `json:"question_id"`
	Category       string   `json:"category"`
	Pass           bool     `json:"pass"`
	Reason         string   `json:"failure_reason,omitempty"`
	DurationMs     float64  `json:"duration_ms"`
	TopKeys        []string `json:"top_keys"`
	ExpectedKeys   []string `json:"expected_keys"`
	TopBodies      []string `json:"top_bodies,omitempty"`
	JudgeScore     float64  `json:"judge_score,omitempty"`
}

// QuestionFailure records a failed question with diagnostic info.
type QuestionFailure struct {
	QuestionID   string   `json:"question_id"`
	Category     string   `json:"category"`
	Question     string   `json:"question"`
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

// LoadFixture reads and parses a LoCoMo fixture JSON file.
func LoadFixture(path string) (Fixture, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Fixture{}, err
	}
	var fixture Fixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		return Fixture{}, err
	}
	if len(fixture.Conversations) == 0 {
		return Fixture{}, fmt.Errorf("fixture has no conversations")
	}
	if len(fixture.Questions) == 0 {
		return Fixture{}, fmt.Errorf("fixture has no questions")
	}
	return fixture, nil
}

// ImportFromJSON reads a LoCoMo dataset JSON object and converts it into a Fixture.
//
// Expected format: a JSON object with "conversations" and "questions" arrays
// matching the LoCoMo dataset structure. Each conversation has an "id" and
// "messages" array. Each question has "id", "conversation_id", "category",
// "question", "answer", and optionally "expected_fact_keys".
func ImportFromJSON(r *os.File) (Fixture, error) {
	// Try to decode as a raw map to detect format.
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return Fixture{}, fmt.Errorf("decode LoCoMo JSON: %w", err)
	}

	fixture := Fixture{
		Description: "Imported from LoCoMo dataset",
	}

	// Parse conversations.
	if convsRaw, ok := raw["conversations"]; ok {
		var convs []ConversationFixture
		if err := json.Unmarshal(convsRaw, &convs); err != nil {
			return Fixture{}, fmt.Errorf("parse conversations: %w", err)
		}
		fixture.Conversations = convs
	}

	// Parse questions.
	if questionsRaw, ok := raw["questions"]; ok {
		var questions []QuestionFixture
		if err := json.Unmarshal(questionsRaw, &questions); err != nil {
			return Fixture{}, fmt.Errorf("parse questions: %w", err)
		}
		fixture.Questions = questions
	}

	if len(fixture.Conversations) == 0 {
		return Fixture{}, fmt.Errorf("no conversations found in input")
	}
	if len(fixture.Questions) == 0 {
		return Fixture{}, fmt.Errorf("no questions found in input")
	}

	return fixture, nil
}

// RunBenchmark executes the full LoCoMo benchmark and returns a report.
func RunBenchmark(opts RunOptions) (Report, error) {
	start := time.Now()
	if opts.K <= 0 {
		opts.K = 5
	}
	if opts.FixturePath == "" {
		opts.FixturePath = filepath.Join("bench", "locomo", "fixture.json")
	}

	fixture, err := LoadFixture(opts.FixturePath)
	if err != nil {
		return Report{}, err
	}

	// Derive facts from conversation messages. Each fact is a turn (message)
	// inserted as a memory so retrieval can find the turn containing answer info.
	facts := conversationsToFacts(fixture.Conversations)
	if len(facts) == 0 {
		return Report{}, fmt.Errorf("no facts derived from conversations")
	}

	// Seed store with facts.
	retrievalMode := strings.TrimSpace(opts.Mode)
	if retrievalMode == "" {
		retrievalMode = "fts5"
	}

	seedStart := time.Now()
	s, keyToID, err := seedStore(facts, retrievalMode, opts.Workers)
	if err != nil {
		return Report{}, err
	}
	defer s.Close()
	seedDuration := time.Since(seedStart)
	fmt.Fprintf(os.Stderr, "[ohara-bench-locomo] seeding complete in %v\n", seedDuration.Round(time.Millisecond))
	idToKey := invertKeyMap(keyToID)

	factBodyByKey := map[string]string{}
	for _, f := range facts {
		factBodyByKey[f.Key] = f.Body
	}

	questions := fixture.Questions
	if opts.QuestionsLimit > 0 && opts.QuestionsLimit < len(questions) {
		questions = questions[:opts.QuestionsLimit]
		fmt.Fprintf(os.Stderr, "[ohara-bench-locomo] evaluating first %d of %d questions\n",
			opts.QuestionsLimit, len(fixture.Questions))
	}

	// Default to containment judge when expected answers are provided.
	if opts.Judge == nil && qsHaveExpectedAnswers(questions) {
		opts.Judge = longmemeval.ContainmentJudge{}
	}

	// Evaluate questions.
	globalAgg := &questionAgg{}
	categoryAggs := map[string]*questionAgg{}
	failures := make([]QuestionFailure, 0, 8)
	caseResults := make([]CaseResult, len(questions))

	totalQuestions := len(questions)
	queryStart := time.Now()
	fmt.Fprintf(os.Stderr, "[ohara-bench-locomo] evaluating %d questions...\n", totalQuestions)

	var completed atomic.Int64
	workCh := make(chan int)
	var wg sync.WaitGroup
	workers := resolveWorkers(opts.Workers)

	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for qi := range workCh {
				q := questions[qi]
				caseStart := time.Now()
				results, searchErr := s.SearchMemories(q.Question, "locomo", "", "", "",
					store.MemoryStatusActive, opts.K, "")
				durationMs := float64(time.Since(caseStart)) / float64(time.Millisecond)

				if searchErr != nil {
					caseResults[qi] = CaseResult{
						QuestionID:   q.ID,
						Category:     q.Category,
						Pass:         false,
						Reason:       searchErr.Error(),
						ExpectedKeys: q.ExpectedFactKeys,
						DurationMs:   durationMs,
					}
					completed.Add(1)
					continue
				}

				top := topIDs(results, opts.K)
				topKeys := idsToKeys(idToKey, top)
				expectedIDs := keysToIDs(keyToID, q.ExpectedFactKeys)
				passed := countHitsInTop(top, expectedIDs) > 0
				reason := ""

				var judgeScore float64
				var topBodies []string
				if opts.Judge != nil && len(q.ExpectedAnswers) > 0 {
					topBodies = make([]string, 0, len(top))
					for _, item := range results {
						if len(topBodies) >= opts.K {
							break
						}
						topBodies = append(topBodies, item.Body)
					}
					judgeScore = opts.Judge.Score(q.Question, topBodies, q.ExpectedAnswers)
					passed = judgeScore >= 0.8
					if !passed {
						reason = fmt.Sprintf("judge score %.3f < 0.8", judgeScore)
					}
				} else if !passed {
					reason = fmt.Sprintf("no expected facts in top-%d results", opts.K)
				}

				caseResults[qi] = CaseResult{
					QuestionID:   q.ID,
					Category:     q.Category,
					Pass:         passed,
					Reason:       reason,
					DurationMs:   durationMs,
					TopKeys:      topKeys,
					ExpectedKeys: q.ExpectedFactKeys,
					TopBodies:    topBodies,
					JudgeScore:   judgeScore,
				}
				completed.Add(1)
			}
		}()
	}

	for qi := range questions {
		workCh <- qi
	}
	close(workCh)
	wg.Wait()

	// Aggregate metrics.
	for _, cr := range caseResults {
		expectedIDs := keysToIDs(keyToID, cr.ExpectedKeys)
		if cr.Reason != "" {
			failures = append(failures, QuestionFailure{
				QuestionID:   cr.QuestionID,
				Category:     cr.Category,
				ExpectedKeys: cr.ExpectedKeys,
				ActualKeys:   cr.TopKeys,
				Reason:       cr.Reason,
			})
		}
		aggKey := cr.Category
		if aggKey == "" {
			aggKey = "general"
		}
		if _, ok := categoryAggs[aggKey]; !ok {
			categoryAggs[aggKey] = &questionAgg{}
		}
		topIDs := keysToIDs(keyToID, cr.TopKeys)
		updateAggFromTopIDs(globalAgg, cr.Pass, topIDs, expectedIDs, opts.K)
		updateAggFromTopIDs(categoryAggs[aggKey], cr.Pass, topIDs, expectedIDs, opts.K)
	}

	queryDuration := time.Since(queryStart)
	fmt.Fprintf(os.Stderr, "[ohara-bench-locomo] querying complete in %v | %d passed / %d failed\n",
		queryDuration.Round(time.Millisecond), globalAgg.passed, globalAgg.count-globalAgg.passed)

	report := Report{
		FixtureDescription: fixture.Description,
		TotalQuestions:     len(questions),
		PassedQuestions:    globalAgg.passed,
		FailedQuestions:    globalAgg.count - globalAgg.passed,
		OverallMetrics:     aggToMetrics(globalAgg),
		CategoryMetrics:    mapAggsToMetrics(categoryAggs),
		CaseResults:        caseResults,
		Runtime:            time.Since(start),
		RuntimeMs:          float64(time.Since(start)) / float64(time.Millisecond),
		Thresholds:         withDefaultThresholds(fixture.Thresholds),
		Failures:           failures,
		JudgeEnabled:       opts.Judge != nil,
		RetrievalMode:      retrievalMode,
	}

	if opts.Judge != nil {
		var sum float64
		count := 0
		for _, cr := range caseResults {
			if cr.JudgeScore > 0 || cr.Pass {
				sum += cr.JudgeScore
				count++
			}
		}
		if count > 0 {
			report.JudgeMeanScore = sum / float64(count)
		}
	}

	report.Latency = computeLatency(caseResults)

	if opts.Enforce {
		if err := enforceThresholds(report); err != nil {
			return report, err
		}
	}
	return report, nil
}

// conversationsToFacts derives fact fixtures from conversation messages.
// Each message becomes a fact keyed by conversation_id:turn_index.
func conversationsToFacts(convs []ConversationFixture) []FactFixture {
	var facts []FactFixture
	for _, conv := range convs {
		for i, msg := range conv.Messages {
			content := strings.TrimSpace(msg.Content)
			if content == "" {
				continue
			}
			key := fmt.Sprintf("%s:%d", conv.ID, i)
			title := firstLine(content, 80)
			facts = append(facts, FactFixture{
				Key:       key,
				Title:     title,
				Body:      content,
				Kind:      "discovery",
				Domain:    "locomo",
				SessionID: conv.ID,
				Turn:      i,
			})
		}
	}
	return facts
}

// FactFixture is a simplified fact for internal use.
type FactFixture struct {
	Key       string
	Title     string
	Body      string
	Kind      string
	Domain    string
	SessionID string
	Turn      int
}

func seedStore(facts []FactFixture, mode string, workers int) (*store.Store, map[string]int64, error) {
	tmp, err := os.MkdirTemp("", "ohara-bench-locomo-")
	if err != nil {
		return nil, nil, err
	}

	cfg := store.FallbackConfig(tmp)
	cfg.NoJobWorker = true
	if workers > 0 {
		cfg.MaxOpenConns = workers
	}
	cfg.SQLiteCacheSizeKB = 200 * 1024
	cfg.SQLiteMmapSizeBytes = 512 << 20
	cfg.SQLiteTempStoreMemory = true
	if strings.EqualFold(mode, "hybrid") {
		cfg.RetrievalMode = "hybrid"
		cfg.EmbeddingBackend = "deterministic-test"
	}
	s, err := store.New(cfg)
	if err != nil {
		os.RemoveAll(tmp)
		return nil, nil, err
	}

	totalFacts := len(facts)
	fmt.Fprintf(os.Stderr, "[ohara-bench-locomo] seeding %d facts...\n", totalFacts)

	bulkParams := make([]store.BulkSeedMemoryParams, totalFacts)
	keys := make([]string, totalFacts)
	for i, fact := range facts {
		keys[i] = fact.Key
		bulkParams[i] = store.BulkSeedMemoryParams{
			ProjectID: "locomo",
			Kind:      fact.Kind,
			Title:     fact.Title,
			Body:      fact.Body,
			Domain:    fact.Domain,
			SessionID: fact.SessionID,
		}
	}

	ids, err := s.BulkSeedMemories(bulkParams)
	if err != nil {
		s.Close()
		os.RemoveAll(tmp)
		return nil, nil, fmt.Errorf("bulk seed facts: %w", err)
	}

	keyToID := make(map[string]int64, totalFacts)
	for i, id := range ids {
		keyToID[keys[i]] = id
	}
	return s, keyToID, nil
}

// -- helpers (shared patterns with longmemeval) --

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

func keysToIDs(keyToID map[string]int64, keys []string) []int64 {
	out := make([]int64, 0, len(keys))
	for _, key := range keys {
		if id := keyToID[key]; id > 0 {
			out = append(out, id)
		}
	}
	return out
}

func idsToKeys(idToKey map[int64]string, ids []int64) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if key, ok := idToKey[id]; ok {
			out = append(out, key)
		}
	}
	return out
}

func invertKeyMap(keyToID map[string]int64) map[int64]string {
	idToKey := make(map[int64]string, len(keyToID))
	for key, id := range keyToID {
		idToKey[id] = key
	}
	return idToKey
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

func containsInt64(list []int64, value int64) bool {
	for _, v := range list {
		if v == value {
			return true
		}
	}
	return false
}

func updateAggFromTopIDs(agg *questionAgg, passed bool, topIDs []int64, expectedIDs []int64, k int) {
	if agg == nil {
		return
	}
	agg.count++
	if passed {
		agg.passed++
	}

	firstRank := firstRelevantRank(topIDs, expectedIDs)
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
	agg.ndcgSum += ndcgAtK(topIDs, expectedIDs, minInt(5, k))
}

func firstRelevantRank(topIDs []int64, expectedIDs []int64) int {
	if len(expectedIDs) == 0 {
		return 0
	}
	for i, id := range topIDs {
		if containsInt64(expectedIDs, id) {
			return i + 1
		}
	}
	return 0
}

func ndcgAtK(topIDs []int64, expectedIDs []int64, k int) float64 {
	if k <= 0 || len(expectedIDs) == 0 {
		return 0
	}
	if k > len(topIDs) {
		k = len(topIDs)
	}
	dcg := 0.0
	for i := 0; i < k; i++ {
		relevant := 0.0
		if containsInt64(expectedIDs, topIDs[i]) {
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

func withDefaultThresholds(in ThresholdsFixture) ThresholdsFixture {
	out := in
	if out.OverallRecallAt3 <= 0 {
		out.OverallRecallAt3 = 0.50
	}
	if out.MRROverall <= 0 {
		out.MRROverall = 0.45
	}
	if out.LatencyP95MsMax <= 0 {
		out.LatencyP95MsMax = 200
	}
	if out.LatencyMaxMsMax <= 0 {
		out.LatencyMaxMsMax = 1000
	}
	return out
}

func enforceThresholds(r Report) error {
	var failures []string
	if r.OverallMetrics.RecallAt3 < r.Thresholds.OverallRecallAt3 {
		failures = append(failures, fmt.Sprintf("overall recall@3 %.3f < %.3f",
			r.OverallMetrics.RecallAt3, r.Thresholds.OverallRecallAt3))
	}
	if r.OverallMetrics.MRR < r.Thresholds.MRROverall {
		failures = append(failures, fmt.Sprintf("overall MRR %.3f < %.3f",
			r.OverallMetrics.MRR, r.Thresholds.MRROverall))
	}
	if r.Latency.P95Ms > r.Thresholds.LatencyP95MsMax {
		failures = append(failures, fmt.Sprintf("latency p95 %.1fms > %.0fms",
			r.Latency.P95Ms, r.Thresholds.LatencyP95MsMax))
	}
	if r.Latency.MaxMs > r.Thresholds.LatencyMaxMsMax {
		failures = append(failures, fmt.Sprintf("latency max %.1fms > %.0fms",
			r.Latency.MaxMs, r.Thresholds.LatencyMaxMsMax))
	}
	if len(failures) > 0 {
		return fmt.Errorf("threshold enforcement failed: %s", strings.Join(failures, "; "))
	}
	return nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func resolveWorkers(explicit int) int {
	if explicit > 0 {
		return explicit
	}
	return 4
}

func qsHaveExpectedAnswers(questions []QuestionFixture) bool {
	for _, q := range questions {
		if len(q.ExpectedAnswers) > 0 {
			return true
		}
	}
	return false
}

func firstLine(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	idx := strings.IndexAny(s, "\n.")
	if idx > 0 {
		s = s[:idx]
	}
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) > maxLen {
		return string(runes[:maxLen]) + "..."
	}
	return s
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
