// Package beam implements a BEAM-style benchmark harness for Ohara.
//
// BEAM (Benchmark for Episodic Agent Memory) evaluates agent memory systems
// at scale with up to 100 conversations, up to 10M tokens, and 2,000 probes
// across facts/entities, updates, contradictions, temporal order,
// instructions-vs-preferences, multi-hop, and summarization.
//
// This harness targets BEAM-1M (~1M tokens) for CI, with BEAM-10M as a
// separate scaling effort.
//
// The harness is self-contained with a deterministic fixture and reuses the
// JudgeModel interface from bench/longmemeval for answer evaluation.
package beam

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
	Probes        []ProbeFixture        `json:"probes"`
	Thresholds    ThresholdsFixture     `json:"thresholds"`
}

// ConversationFixture represents one conversation with system-wide facts.
type ConversationFixture struct {
	ID       string           `json:"id"`
	Messages []MessageFixture `json:"messages"`
	Facts    []FactFixture    `json:"facts,omitempty"` // explicit facts within this conversation
}

// MessageFixture is a single turn in a conversation.
type MessageFixture struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// FactFixture represents a discrete fact to be remembered.
type FactFixture struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Body  string `json:"body"`
	Kind  string `json:"kind"`
}

// ProbeFixture represents a single evaluation probe.
type ProbeFixture struct {
	ID               string   `json:"id"`
	Category         string   `json:"category"` // conversation scope
	ProbeType        string   `json:"probe_type"`
	// "fact_retrieval" | "entity_linking" | "contradiction" |
	// "temporal_order" | "instruction_preference" | "multi_hop" | "summarization"
	ConversationID   string   `json:"conversation_id"`
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
	Mode           string
	QuestionsLimit int
	Sweep          bool
	Workers        int
}

// SweepMode defines a single mode config for BEAM sweeps.
type SweepMode struct {
	Name string
	Mode string
}

// DefaultSweepModes returns the standard sweep modes for BEAM.
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
	FixtureDescription string             `json:"fixture_description"`
	TotalProbes        int               `json:"total_probes"`
	PassedProbes       int               `json:"passed_probes"`
	FailedProbes       int               `json:"failed_probes"`
	OverallMetrics     Metrics           `json:"overall_metrics"`
	ProbeTypeMetrics   map[string]Metrics `json:"probe_type_metrics"`
	Latency            LatencyMetrics    `json:"latency"`
	CaseResults        []CaseResult      `json:"case_results"`
	Runtime            time.Duration     `json:"-"`
	RuntimeMs          float64           `json:"runtime_ms"`
	Thresholds         ThresholdsFixture `json:"thresholds"`
	Failures           []ProbeFailure    `json:"failures"`
	JudgeEnabled       bool              `json:"judge_enabled"`
	JudgeMeanScore     float64           `json:"judge_mean_score,omitempty"`
	RetrievalMode      string            `json:"retrieval_mode"`
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

// CaseResult captures the outcome of a single probe evaluation.
type CaseResult struct {
	ProbeID      string   `json:"probe_id"`
	ProbeType    string   `json:"probe_type"`
	Category     string   `json:"category"`
	Pass         bool     `json:"pass"`
	Reason       string   `json:"failure_reason,omitempty"`
	DurationMs   float64  `json:"duration_ms"`
	TopKeys      []string `json:"top_keys"`
	ExpectedKeys []string `json:"expected_keys"`
	TopBodies    []string `json:"top_bodies,omitempty"`
	JudgeScore   float64  `json:"judge_score,omitempty"`
}

// ProbeFailure records a failed probe with diagnostic info.
type ProbeFailure struct {
	ProbeID      string   `json:"probe_id"`
	ProbeType    string   `json:"probe_type"`
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

// LoadFixture reads and parses a BEAM fixture JSON file.
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
	if len(fixture.Probes) == 0 {
		return Fixture{}, fmt.Errorf("fixture has no probes")
	}
	return fixture, nil
}

// ImportFromJSON reads a BEAM dataset JSON file and converts it into a Fixture.
func ImportFromJSON(r *os.File) (Fixture, error) {
	var fixture Fixture
	if err := json.NewDecoder(r).Decode(&fixture); err != nil {
		return Fixture{}, fmt.Errorf("decode BEAM JSON: %w", err)
	}
	if len(fixture.Conversations) == 0 {
		return Fixture{}, fmt.Errorf("no conversations found in input")
	}
	if len(fixture.Probes) == 0 {
		return Fixture{}, fmt.Errorf("no probes found in input")
	}
	return fixture, nil
}

// RunBenchmark executes the full BEAM benchmark and returns a report.
func RunBenchmark(opts RunOptions) (Report, error) {
	start := time.Now()
	if opts.K <= 0 {
		opts.K = 5
	}
	if opts.FixturePath == "" {
		opts.FixturePath = filepath.Join("bench", "beam", "fixture.json")
	}

	fixture, err := LoadFixture(opts.FixturePath)
	if err != nil {
		return Report{}, err
	}

	// Derive facts from conversations: each message becomes a fact, plus explicit facts.
	facts := fixtureToFacts(fixture)
	if len(facts) == 0 {
		return Report{}, fmt.Errorf("no facts derived from fixture")
	}

	retrievalMode := strings.TrimSpace(opts.Mode)
	if retrievalMode == "" {
		retrievalMode = "fts5"
	}

	seedStart := time.Now()
	s, keyToID, err := seedBeamStore(facts, retrievalMode, opts.Workers)
	if err != nil {
		return Report{}, err
	}
	defer s.Close()
	seedDuration := time.Since(seedStart)
	fmt.Fprintf(os.Stderr, "[ohara-bench-beam] seeding complete in %v\n", seedDuration.Round(time.Millisecond))
	idToKey := invertKeyMap(keyToID)

	probes := fixture.Probes
	if opts.QuestionsLimit > 0 && opts.QuestionsLimit < len(probes) {
		probes = probes[:opts.QuestionsLimit]
		fmt.Fprintf(os.Stderr, "[ohara-bench-beam] evaluating first %d of %d probes\n",
			opts.QuestionsLimit, len(fixture.Probes))
	}

	if opts.Judge == nil && probesHaveExpectedAnswers(probes) {
		opts.Judge = longmemeval.ContainmentJudge{}
	}

	globalAgg := &questionAgg{}
	probeTypeAggs := map[string]*questionAgg{}
	failures := make([]ProbeFailure, 0, 8)
	caseResults := make([]CaseResult, len(probes))

	totalProbes := len(probes)
	queryStart := time.Now()
	fmt.Fprintf(os.Stderr, "[ohara-bench-beam] evaluating %d probes...\n", totalProbes)

	var completed atomic.Int64
	workCh := make(chan int)
	var wg sync.WaitGroup
	workers := resolveBeamWorkers(opts.Workers)

	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for pi := range workCh {
				p := probes[pi]
				caseStart := time.Now()
				results, searchErr := s.SearchMemories(p.Question, "beam", "", "", "",
					store.MemoryStatusActive, opts.K, "")
				durationMs := float64(time.Since(caseStart)) / float64(time.Millisecond)

				if searchErr != nil {
					caseResults[pi] = CaseResult{
						ProbeID:      p.ID,
						ProbeType:    p.ProbeType,
						Category:     p.Category,
						Pass:         false,
						Reason:       searchErr.Error(),
						ExpectedKeys: p.ExpectedFactKeys,
						DurationMs:   durationMs,
					}
					completed.Add(1)
					continue
				}

				top := topIDs(results, opts.K)
				topKeys := idsToKeys(idToKey, top)
				expectedIDs := keysToIDs(keyToID, p.ExpectedFactKeys)
				passed := countHitsInTop(top, expectedIDs) > 0
				reason := ""

				var judgeScore float64
				var topBodies []string
				if opts.Judge != nil && len(p.ExpectedAnswers) > 0 {
					topBodies = make([]string, 0, len(top))
					for _, item := range results {
						if len(topBodies) >= opts.K {
							break
						}
						topBodies = append(topBodies, item.Body)
					}
					judgeScore = opts.Judge.Score(p.Question, topBodies, p.ExpectedAnswers)
					passed = judgeScore >= 0.8
					if !passed {
						reason = fmt.Sprintf("judge score %.3f < 0.8", judgeScore)
					}
				} else if !passed {
					reason = fmt.Sprintf("no expected facts in top-%d results", opts.K)
				}

				caseResults[pi] = CaseResult{
					ProbeID:      p.ID,
					ProbeType:    p.ProbeType,
					Category:     p.Category,
					Pass:         passed,
					Reason:       reason,
					DurationMs:   durationMs,
					TopKeys:      topKeys,
					ExpectedKeys: p.ExpectedFactKeys,
					TopBodies:    topBodies,
					JudgeScore:   judgeScore,
				}
				completed.Add(1)
			}
		}()
	}

	for pi := range probes {
		workCh <- pi
	}
	close(workCh)
	wg.Wait()

	// Aggregate.
	for _, cr := range caseResults {
		expectedIDs := keysToIDs(keyToID, cr.ExpectedKeys)
		if cr.Reason != "" {
			failures = append(failures, ProbeFailure{
				ProbeID:      cr.ProbeID,
				ProbeType:    cr.ProbeType,
				Category:     cr.Category,
				ExpectedKeys: cr.ExpectedKeys,
				ActualKeys:   cr.TopKeys,
				Reason:       cr.Reason,
			})
		}
		aggKey := cr.ProbeType
		if aggKey == "" {
			aggKey = "general"
		}
		if _, ok := probeTypeAggs[aggKey]; !ok {
			probeTypeAggs[aggKey] = &questionAgg{}
		}
		topIDs := keysToIDs(keyToID, cr.TopKeys)
		updateBeamAgg(globalAgg, cr.Pass, topIDs, expectedIDs, opts.K)
		updateBeamAgg(probeTypeAggs[aggKey], cr.Pass, topIDs, expectedIDs, opts.K)
	}

	queryDuration := time.Since(queryStart)
	fmt.Fprintf(os.Stderr, "[ohara-bench-beam] querying complete in %v | %d passed / %d failed\n",
		queryDuration.Round(time.Millisecond), globalAgg.passed, globalAgg.count-globalAgg.passed)

	report := Report{
		FixtureDescription: fixture.Description,
		TotalProbes:        len(probes),
		PassedProbes:       globalAgg.passed,
		FailedProbes:       globalAgg.count - globalAgg.passed,
		OverallMetrics:     aggToMetrics(globalAgg),
		ProbeTypeMetrics:   mapAggsToMetrics(probeTypeAggs),
		CaseResults:        caseResults,
		Runtime:            time.Since(start),
		RuntimeMs:          float64(time.Since(start)) / float64(time.Millisecond),
		Thresholds:         withBeamDefaults(fixture.Thresholds),
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
		if err := enforceBeamThresholds(report); err != nil {
			return report, err
		}
	}
	return report, nil
}

// DeriveFact holds a fact ready for seeding.
type DeriveFact struct {
	Key       string
	Title     string
	Body      string
	Kind      string
	Domain    string
	SessionID string
	Turn      int
}

func fixtureToFacts(fix Fixture) []DeriveFact {
	var facts []DeriveFact
	for _, conv := range fix.Conversations {
		// Explicit facts take priority.
		for _, f := range conv.Facts {
			facts = append(facts, DeriveFact{
				Key:       f.Key,
				Title:     f.Title,
				Body:      f.Body,
				Kind:      orDefault(f.Kind, "discovery"),
				Domain:    "beam",
				SessionID: conv.ID,
				Turn:      0,
			})
		}
		// Messages become facts.
		for i, msg := range conv.Messages {
			content := strings.TrimSpace(msg.Content)
			if content == "" {
				continue
			}
			key := fmt.Sprintf("%s:msg:%d", conv.ID, i)
			title := beamFirstLine(content, 80)
			facts = append(facts, DeriveFact{
				Key:       key,
				Title:     title,
				Body:      content,
				Kind:      "discovery",
				Domain:    "beam",
				SessionID: conv.ID,
				Turn:      i,
			})
		}
	}
	return facts
}

func seedBeamStore(facts []DeriveFact, mode string, workers int) (*store.Store, map[string]int64, error) {
	tmp, err := os.MkdirTemp("", "ohara-bench-beam-")
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
	fmt.Fprintf(os.Stderr, "[ohara-bench-beam] seeding %d facts...\n", totalFacts)

	bulkParams := make([]store.BulkSeedMemoryParams, totalFacts)
	keys := make([]string, totalFacts)
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

// -- shared helpers --

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
		if beamContains(expected, id) {
			count++
		}
	}
	return count
}

func beamContains(list []int64, val int64) bool {
	for _, v := range list {
		if v == val {
			return true
		}
	}
	return false
}

func updateBeamAgg(agg *questionAgg, passed bool, topIDs []int64, expectedIDs []int64, k int) {
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
	if firstRank > 0 && firstRank <= beamMin(3, k) {
		agg.hit3++
	}
	if firstRank > 0 && firstRank <= beamMin(5, k) {
		agg.hit5++
	}
	if firstRank > 0 {
		agg.rrSum += 1.0 / float64(firstRank)
	}
	agg.ndcgSum += ndcgAtK(topIDs, expectedIDs, beamMin(5, k))
}

func firstRelevantRank(topIDs []int64, expectedIDs []int64) int {
	if len(expectedIDs) == 0 {
		return 0
	}
	for i, id := range topIDs {
		if beamContains(expectedIDs, id) {
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
		if beamContains(expectedIDs, topIDs[i]) {
			relevant = 1.0
		}
		if relevant > 0 {
			dcg += (math.Pow(2, relevant) - 1) / math.Log2(float64(i+2))
		}
	}
	idcg := 0.0
	for i := 0; i < beamMin(len(expectedIDs), k); i++ {
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

func beamMin(a, b int) int {
	if a < b {
		return a
	}
	return b
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
		P50Ms:  beamPercentile(durations, 0.50),
		P95Ms:  beamPercentile(durations, 0.95),
		MaxMs:  durations[n-1],
		MeanMs: sum / float64(n),
	}
}

func beamPercentile(sorted []float64, p float64) float64 {
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

func withBeamDefaults(in ThresholdsFixture) ThresholdsFixture {
	out := in
	if out.OverallRecallAt3 <= 0 {
		out.OverallRecallAt3 = 0.40
	}
	if out.MRROverall <= 0 {
		out.MRROverall = 0.35
	}
	if out.LatencyP95MsMax <= 0 {
		out.LatencyP95MsMax = 500
	}
	if out.LatencyMaxMsMax <= 0 {
		out.LatencyMaxMsMax = 2000
	}
	return out
}

func enforceBeamThresholds(r Report) error {
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

func resolveBeamWorkers(explicit int) int {
	if explicit > 0 {
		return explicit
	}
	return 4
}

func probesHaveExpectedAnswers(probes []ProbeFixture) bool {
	for _, p := range probes {
		if len(p.ExpectedAnswers) > 0 {
			return true
		}
	}
	return false
}

func beamFirstLine(s string, maxLen int) string {
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

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// SortedProbeTypeKeys returns probe-type keys in alphabetical order.
func SortedProbeTypeKeys(input map[string]Metrics) []string {
	keys := make([]string, 0, len(input))
	for k := range input {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
