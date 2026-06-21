// Package longmemeval implements a LongMemEval-style benchmark harness for Ohara.
//
// LongMemEval evaluates an agent memory system's ability to retain and recall facts
// across multiple sessions (time steps). Facts are inserted in earlier sessions,
// and questions are asked in later sessions, measuring recall degradation over
// increasing session distances.
//
// The harness is self-contained with a deterministic fixture — no external dataset
// or model dependency is required for the baseline spine.
//
// For public LongMemEval dataset integration:
//   - ImportFromJSONL converts JSONL (newline-delimited JSON) records into a Fixture.
//   - ImportFromJSONArray converts the LongMemEval cleaned JSON array format
//     (bench/longmemeval/data/longmemeval_s_cleaned.json) into a Fixture.
//
// RunBenchmark auto-detects the input format by checking the first non-whitespace
// byte: '[' routes to ImportFromJSONArray, anything else routes to ImportFromJSONL.
//
// A built-in OverlapJudge provides baseline answer evaluation without LLM dependency.
package longmemeval

import (
	"bufio"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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
	ExpectedAnswers  []string `json:"expected_answers,omitempty"`
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

// DefaultEvalsDatasetPath is the default path to the checked-in LongMemEval
// cleaned dataset used for local exhaustive benchmark runs. When this file
// exists, the runner imports it in preference to the built-in fixture.
//
// The file may be either a JSON array (LongMemEval cleaned format) or
// JSONL (newline-delimited JSON). The format is auto-detected.
const DefaultEvalsDatasetPath = "bench/longmemeval/data/longmemeval_s_cleaned.json"

// RunOptions configures a benchmark run.
type RunOptions struct {
	FixturePath     string
	K               int
	Enforce         bool
	SkipLatencyGate bool
	Judge           JudgeModel // optional answer-quality judge (nil = skip)
	Mode            string     // retrieval mode: "fts5" (default) or "hybrid"
	DatasetPath     string     // optional path to JSONL dataset (see ImportFromJSONL)
	QuestionsLimit  int        // if > 0, evaluate only first N questions (debug/diagnostic)
	Sweep           bool       // when true, runs across all supported modes
	Workers         int        // number of concurrent query workers (0 = runtime default)
	JudgePassScore  float64    // minimum score needed for judge-based pass (0 = default)
}

// SweepMode defines a single mode config for LongMemEval sweeps.
type LmeSweepMode struct {
	Name string
	Mode string
}

// DefaultLmeSweepModes returns the standard sweep modes for LongMemEval.
func DefaultLmeSweepModes() []LmeSweepMode {
	return []LmeSweepMode{
		{Name: "fts5", Mode: "fts5"},
		{Name: "hybrid-deterministic", Mode: "hybrid"},
	}
}

// LmeSweepResult holds results for one mode in a sweep.
type LmeSweepResult struct {
	Name   string `json:"name"`
	Mode   string `json:"mode"`
	Report Report `json:"report"`
	Error  string `json:"error,omitempty"`
}

// RunLmeSweep runs the benchmark across all sweep modes.
func RunLmeSweep(baseOpts RunOptions, modes []LmeSweepMode) []LmeSweepResult {
	if modes == nil {
		modes = DefaultLmeSweepModes()
	}
	results := make([]LmeSweepResult, 0, len(modes))
	for _, sm := range modes {
		opts := baseOpts
		opts.Mode = sm.Mode
		opts.Sweep = false

		report, err := RunBenchmark(opts)
		sr := LmeSweepResult{Name: sm.Name, Mode: sm.Mode}
		if err != nil {
			sr.Error = err.Error()
		}
		sr.Report = report
		results = append(results, sr)
	}
	return results
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
	JudgeScore   float64  `json:"judge_score,omitempty"`
	TopBodies    []string `json:"top_bodies,omitempty"`
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
	JudgeEnabled       bool               `json:"judge_enabled"`
	JudgeMeanScore     float64            `json:"judge_mean_score,omitempty"`
	RetrievalMode      string             `json:"retrieval_mode"`
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

// JudgeModel evaluates whether a retrieved answer matches the expected answer.
// Implementations range from simple lexical overlap to LLM-based evaluation.
type JudgeModel interface {
	// Score returns a score in [0.0, 1.0] indicating how well the retrieved
	// content matches the expected content.
	Score(query string, retrievedBodies []string, expectedBodies []string) float64
}

// OverlapJudge is a baseline judge that computes Jaccard similarity between
// token sets of retrieved and expected bodies. No LLM dependency.
type OverlapJudge struct{}

// Score computes token-level Jaccard similarity. Returns 1.0 if any expected
// body shares ≥50% tokens with any retrieved body, scaled proportionally.
func (j OverlapJudge) Score(query string, retrievedBodies []string, expectedBodies []string) float64 {
	if len(expectedBodies) == 0 || len(retrievedBodies) == 0 {
		return 0
	}
	best := 0.0
	for _, exp := range expectedBodies {
		expTokens := tokenize(exp)
		if len(expTokens) == 0 {
			continue
		}
		for _, ret := range retrievedBodies {
			retTokens := tokenize(ret)
			if len(retTokens) == 0 {
				continue
			}
			inter := intersectCount(expTokens, retTokens)
			union := len(expTokens) + len(retTokens) - inter
			if union > 0 {
				sim := float64(inter) / float64(union)
				if sim > best {
					best = sim
				}
			}
		}
	}
	return best
}

// ContainmentJudge measures how completely an expected answer is present in any
// retrieved body without penalizing long transcript bodies.
type ContainmentJudge struct{}

func (j ContainmentJudge) Score(query string, retrievedBodies []string, expectedBodies []string) float64 {
	if len(expectedBodies) == 0 || len(retrievedBodies) == 0 {
		return 0
	}
	best := 0.0
	for _, exp := range expectedBodies {
		expTokens := tokenize(exp)
		if len(expTokens) == 0 {
			continue
		}
		for _, ret := range retrievedBodies {
			retText := strings.ToLower(ret)
			matched := 0
			for _, token := range expTokens {
				if strings.Contains(retText, token) {
					matched++
				}
			}
			score := float64(matched) / float64(len(expTokens))
			if score > best {
				best = score
			}
		}
	}
	return best
}

func tokenize(s string) []string {
	parts := strings.Fields(strings.ToLower(s))
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, p := range parts {
		token := strings.Trim(p, `"'.,;:!?()[]{}<>`)
		if len(token) < 2 {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		out = append(out, token)
	}
	return out
}

func intersectCount(a, b []string) int {
	set := map[string]struct{}{}
	for _, s := range a {
		set[s] = struct{}{}
	}
	count := 0
	for _, s := range b {
		if _, ok := set[s]; ok {
			count++
		}
	}
	return count
}

// DatasetRecord is a single record from a LongMemEval-style JSONL dataset file.
// Each record contains a fact to insert and optional associated questions.
type DatasetRecord struct {
	FactKey   string `json:"fact_key"`
	SessionID string `json:"session_id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Kind      string `json:"kind"`
	Domain    string `json:"domain"`
	Turn      int    `json:"turn"`
	// Optional: questions associated with this fact.
	Questions []DatasetQuestion `json:"questions,omitempty"`
}

// DatasetQuestion is a question tied to a DatasetRecord.
type DatasetQuestion struct {
	ID               string   `json:"id"`
	Category         string   `json:"category"`
	Distance         string   `json:"distance"`
	DistanceSessions int      `json:"distance_sessions"`
	Query            string   `json:"query"`
	ExpectedFactKeys []string `json:"expected_fact_keys"`
	AskSessionID     string   `json:"ask_session_id"`
}

// HaystackMessage is a single message in a haystack session conversation.
type HaystackMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// LongMemEvalArrayRecord is a single record from the LongMemEval cleaned JSON
// array dataset format (bench/longmemeval/data/longmemeval_s_cleaned.json).
type LongMemEvalArrayRecord struct {
	QuestionID        string             `json:"question_id"`
	QuestionType      string             `json:"question_type"`
	Question          string             `json:"question"`
	QuestionDate      string             `json:"question_date"`
	Answer            interface{}        `json:"answer"`
	AnswerSessionIDs  []string           `json:"answer_session_ids"`
	HaystackDates     []string           `json:"haystack_dates"`
	HaystackSessionIDs []string          `json:"haystack_session_ids"`
	HaystackSessions  [][]HaystackMessage `json:"haystack_sessions"`
}

// ImportResult holds the result of importing a dataset.
type ImportResult struct {
	RecordsRead  int
	FactsCreated int
	QuestionsCreated int
	Errors       []string
}

// ImportFromJSONL reads a JSONL (newline-delimited JSON) stream and converts it
// into a Fixture. This enables integration with the public LongMemEval dataset
// hosted on HuggingFace (hf://datasets/...).
//
// Each line must be a valid DatasetRecord. Questions are aggregated across all
// records into the fixture's Questions array.
func ImportFromJSONL(r io.Reader) (Fixture, ImportResult, error) {
	result := ImportResult{}
	fixture := Fixture{
		Description: "Imported from LongMemEval JSONL dataset",
		Sessions:    []SessionFixture{},
		Facts:       []FactFixture{},
		Questions:   []QuestionFixture{},
	}
	seenSessions := map[string]bool{}
	sessionOrder := 0
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<20) // 1MB line buffer

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		result.RecordsRead++

		var record DatasetRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("line %d: %v", result.RecordsRead, err))
			continue
		}

		// Default kind if unspecified.
		kind := record.Kind
		if kind == "" {
			kind = "discovery"
		}
		domain := record.Domain
		if domain == "" {
			domain = "general"
		}

		fixture.Facts = append(fixture.Facts, FactFixture{
			Key:       record.FactKey,
			Title:     record.Title,
			Body:      record.Body,
			Kind:      kind,
			Domain:    domain,
			SessionID: record.SessionID,
			Turn:      record.Turn,
		})
		result.FactsCreated++

		// Register session if new.
		if !seenSessions[record.SessionID] {
			seenSessions[record.SessionID] = true
			sessionOrder++
			fixture.Sessions = append(fixture.Sessions, SessionFixture{
				ID:    record.SessionID,
				Label: record.SessionID,
				Order: sessionOrder,
			})
		}

		// Add questions from this record.
		for _, q := range record.Questions {
			askSess := q.AskSessionID
			if askSess == "" {
				askSess = record.SessionID
			}
			if !seenSessions[askSess] {
				seenSessions[askSess] = true
				sessionOrder++
				fixture.Sessions = append(fixture.Sessions, SessionFixture{
					ID:    askSess,
					Label: askSess,
					Order: sessionOrder,
				})
			}
			fixture.Questions = append(fixture.Questions, QuestionFixture{
				ID:               q.ID,
				Category:         q.Category,
				Distance:         q.Distance,
				DistanceSessions: q.DistanceSessions,
				Query:            q.Query,
				ExpectedFactKeys: q.ExpectedFactKeys,
				SessionID:        askSess,
			})
			result.QuestionsCreated++
		}
	}

	if err := scanner.Err(); err != nil {
		return fixture, result, fmt.Errorf("scan error: %w", err)
	}
	if result.FactsCreated == 0 {
		return fixture, result, fmt.Errorf("no valid records found in input")
	}
	return fixture, result, nil
}

// ImportFromJSONArray reads a JSON array of LongMemEval cleaned dataset records
// (bench/longmemeval/data/longmemeval_s_cleaned.json) and converts them into a Fixture.
//
// Each element of the JSON array is a LongMemEvalArrayRecord with fields like
// question_id, question_type, question, answer, haystack_session_ids, and
// haystack_sessions. The function creates a Fact for each haystack session's
// conversation text and maps answer_session_ids to expected fact keys.
func ImportFromJSONArray(r io.Reader) (Fixture, ImportResult, error) {
	result := ImportResult{}
	fixture := Fixture{
		Description: "Imported from LongMemEval JSON array dataset",
		Sessions:    []SessionFixture{},
		Facts:       []FactFixture{},
		Questions:   []QuestionFixture{},
	}

	var records []LongMemEvalArrayRecord
	if err := json.NewDecoder(r).Decode(&records); err != nil {
		return fixture, result, fmt.Errorf("decode JSON array: %w", err)
	}
	if len(records) == 0 {
		return fixture, result, fmt.Errorf("no records in JSON array")
	}

	seenSessions := map[string]int{} // session_id → order
	factKeyBySessionID := map[string]string{}
	factKeyByBodyHash := map[string]string{}
	sessionOrder := 0

	getOrCreateSession := func(sid string) int {
		if order, ok := seenSessions[sid]; ok {
			return order
		}
		sessionOrder++
		seenSessions[sid] = sessionOrder
		fixture.Sessions = append(fixture.Sessions, SessionFixture{
			ID:    sid,
			Label: sid,
			Order: sessionOrder,
		})
		return sessionOrder
	}

	for _, rec := range records {
		result.RecordsRead++

		// Validate parallel arrays.
		if len(rec.HaystackSessionIDs) != len(rec.HaystackSessions) {
			result.Errors = append(result.Errors,
				fmt.Sprintf("record %s: haystack_session_ids length %d != haystack_sessions length %d",
					rec.QuestionID, len(rec.HaystackSessionIDs), len(rec.HaystackSessions)))
			continue
		}

		// Register all haystack sessions.
		for _, sid := range rec.HaystackSessionIDs {
			getOrCreateSession(sid)
		}

		// Build a map from answer session ID → fact key(s).
		ansSessionKeys := map[string][]string{}
		for i, sid := range rec.HaystackSessionIDs {
			if i >= len(rec.HaystackSessions) {
				continue
			}
			body := buildConversationBody(rec.HaystackSessions[i])
			if strings.TrimSpace(body) == "" {
				continue
			}
			bodyHash := hashConversationBody(body)
			key := ""
			switch {
			case sid != "" && factKeyBySessionID[sid] != "":
				key = factKeyBySessionID[sid]
			case factKeyByBodyHash[bodyHash] != "":
				key = factKeyByBodyHash[bodyHash]
			default:
				key = fmt.Sprintf("%s_hs_%d", rec.QuestionID, i)
				fixture.Facts = append(fixture.Facts, FactFixture{
					Key:       key,
					Title:     firstLine(body, 80),
					Body:      body,
					Kind:      "discovery",
					Domain:    rec.QuestionType,
					SessionID: sid,
					Turn:      i + 1,
				})
				result.FactsCreated++
				if sid != "" {
					factKeyBySessionID[sid] = key
				}
				factKeyByBodyHash[bodyHash] = key
			}

			// If this session is an answer session, record the key.
			for _, ansSid := range rec.AnswerSessionIDs {
				if sid == ansSid {
					ansSessionKeys[ansSid] = append(ansSessionKeys[ansSid], key)
				}
			}
		}

		// Collect expected fact keys from answer sessions.
		expectedKeys := make([]string, 0, len(rec.AnswerSessionIDs))
		for _, ansSid := range rec.AnswerSessionIDs {
			if keys, ok := ansSessionKeys[ansSid]; ok {
				expectedKeys = append(expectedKeys, keys...)
			}
		}
		expectedAnswers := stringifyAnswers(rec.Answer)
		// If no answer session fact was created, create a standalone answer fact.
		if len(expectedKeys) == 0 {
			key := fmt.Sprintf("%s_answer", rec.QuestionID)
			ansStr := fmt.Sprintf("%v", rec.Answer)
			fixture.Facts = append(fixture.Facts, FactFixture{
				Key:       key,
				Title:     truncateText(ansStr, 60),
				Body:      ansStr,
				Kind:      "discovery",
				Domain:    rec.QuestionType,
				SessionID: rec.AnswerSessionIDs[0],
				Turn:      0,
			})
			result.FactsCreated++
			expectedKeys = append(expectedKeys, key)
		}

		// Create question session (synthetic, ordered after all haystack sessions).
		qSid := rec.QuestionID + "_q"
		getOrCreateSession(qSid)

		// Approximate distance from the latest answer-bearing session to the
		// query point within this record's local haystack timeline.
		distSess := 1
		if gap, ok := answerGapWithinRecord(rec); ok {
			distSess = gap
		}
		distance := "near"
		if distSess > 3 {
			distance = "far"
		} else if distSess > 1 {
			distance = "medium"
		}

		fixture.Questions = append(fixture.Questions, QuestionFixture{
			ID:               rec.QuestionID,
			Category:         rec.QuestionType,
			Distance:         distance,
			DistanceSessions: distSess,
			Query:            rec.Question,
			ExpectedFactKeys: expectedKeys,
			ExpectedAnswers:  expectedAnswers,
			SessionID:        qSid,
		})
		result.QuestionsCreated++
	}

	if result.FactsCreated == 0 && len(result.Errors) == 0 {
		return fixture, result, fmt.Errorf("no valid records found in input")
	}
	return fixture, result, nil
}

// buildConversationBody concatenates haystack session messages into a single
// searchable body string.
func buildConversationBody(messages []HaystackMessage) string {
	parts := make([]string, 0, len(messages))
	for _, msg := range messages {
		content := normalizeConversationText(msg.Content)
		if content == "" {
			continue
		}
		parts = append(parts, content)
	}
	return strings.Join(parts, "\n")
}

func normalizeConversationText(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

func hashConversationBody(body string) string {
	return fmt.Sprintf("%x", sha1.Sum([]byte(strings.ToLower(body))))
}

func stringifyAnswers(answer interface{}) []string {
	switch v := answer.(type) {
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s := strings.TrimSpace(fmt.Sprintf("%v", item))
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		s := strings.TrimSpace(fmt.Sprintf("%v", answer))
		if s == "" {
			return nil
		}
		return []string{s}
	}
}

func answerGapWithinRecord(rec LongMemEvalArrayRecord) (int, bool) {
	if len(rec.HaystackSessionIDs) == 0 || len(rec.AnswerSessionIDs) == 0 {
		return 0, false
	}
	answerIndex := -1
	for _, ansSid := range rec.AnswerSessionIDs {
		for i, sid := range rec.HaystackSessionIDs {
			if sid == ansSid && i > answerIndex {
				answerIndex = i
			}
		}
	}
	if answerIndex < 0 {
		return 0, false
	}
	gap := len(rec.HaystackSessionIDs) - 1 - answerIndex
	if gap < 1 {
		gap = 1
	}
	return gap, true
}

// firstLine returns the first line of s, truncated to maxLen runes.
func firstLine(s string, maxLen int) string {
	original := s
	s = strings.TrimSpace(s)
	s = strings.TrimLeft(s, " .,:;!?-")
	idx := strings.IndexAny(s, "\n.")
	if idx > 0 {
		s = s[:idx]
	}
	s = strings.TrimSpace(s)
	if s == "" {
		s = strings.TrimSpace(strings.TrimLeft(normalizeConversationText(original), " .,:;!?-"))
	}
	return truncateText(s, maxLen)
}

// truncateText truncates s to at most maxLen runes.
func truncateText(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
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
	fixturePathExplicit := strings.TrimSpace(opts.FixturePath) != ""
	if opts.FixturePath == "" {
		opts.FixturePath = filepath.Join("bench", "longmemeval", "fixture.json")
	}

	// If DatasetPath is explicitly set, import from it instead. When the caller
	// leaves DatasetPath empty, only auto-use the checked-in official dataset when
	// the default fixture path is also in use. This keeps explicit fixture runs
	// deterministic and prevents the local gate from silently switching datasets.
	datasetPath := strings.TrimSpace(opts.DatasetPath)
	if datasetPath == "" && !fixturePathExplicit {
		datasetPath = DefaultEvalsDatasetPath
	}
	var importedFromDataset bool
	var fixture Fixture
	fixtureFile := opts.FixturePath
	if _, statErr := os.Stat(datasetPath); statErr == nil {
		f, openErr := os.Open(datasetPath)
		if openErr == nil {
			// Peek at first non-whitespace byte to detect format.
			isArray := isJSONArrayFile(f)
			f.Close()
			f, openErr = os.Open(datasetPath)
			if openErr == nil {
				var impErr error
				var imported Fixture
				if isArray {
					imported, _, impErr = ImportFromJSONArray(f)
				} else {
					imported, _, impErr = ImportFromJSONL(f)
				}
				f.Close()
				if impErr == nil && len(imported.Facts) > 0 {
					fixture = imported
					importedFromDataset = true
				} else if impErr != nil {
					return Report{}, fmt.Errorf("dataset import from %s: %w", datasetPath, impErr)
				}
			}
		}
	}
	if !importedFromDataset {
		var err error
		fixture, err = LoadFixture(fixtureFile)
		if err != nil {
			return Report{}, err
		}
	}
	if opts.Judge == nil && fixtureHasExpectedAnswers(fixture) {
		opts.Judge = ContainmentJudge{}
	}
	thresholds := withDefaultThresholds(fixture.Thresholds)

	// Determine retrieval mode.
	retrievalMode := strings.TrimSpace(opts.Mode)
	if retrievalMode == "" {
		retrievalMode = strings.TrimSpace(os.Getenv("OHARA_RETRIEVAL_MODE"))
	}
	if retrievalMode == "" {
		retrievalMode = "fts5"
	}

	// Seed store with facts.
	seedStart := time.Now()
	workers := resolveWorkerCount(opts.Workers)
	s, keyToID, err := seedStore(fixture, retrievalMode, workers)
	if err != nil {
		return Report{}, err
	}
	defer s.Close()
	seedDuration := time.Since(seedStart)
	fmt.Fprintf(os.Stderr, "[ohara-bench] seeding complete in %v\n", seedDuration.Round(time.Millisecond))
	idToKey := invertKeyMap(keyToID)

	questions := fixture.Questions
	if opts.QuestionsLimit > 0 && opts.QuestionsLimit < len(questions) {
		questions = questions[:opts.QuestionsLimit]
		fmt.Fprintf(os.Stderr, "[ohara-bench] evaluating first %d of %d questions (questions-limit set)\n", opts.QuestionsLimit, len(fixture.Questions))
	}

	globalAgg := &questionAgg{}
	distanceAggs := map[string]*questionAgg{}
	categoryAggs := map[string]*questionAgg{}
	failures := make([]QuestionFailure, 0, 8)
	caseResults := make([]CaseResult, len(questions))

	totalQuestions := len(questions)
	queryStart := time.Now()
	progressEvery := maxInt(1, totalQuestions/20) // log ~20 progress lines regardless of dataset size
	if progressEvery > 50 {
		progressEvery = 50 // but cap at every 50 for very large datasets
	}
	fmt.Fprintf(os.Stderr, "[ohara-bench] evaluating %d questions...\n", totalQuestions)
	factBodyByKey := mapFactBodiesByKey(fixture.Facts)
	var completed atomic.Int64
	workCh := make(chan int)
	var wg sync.WaitGroup

	evaluateQuestion := func(q QuestionFixture) CaseResult {
		caseStart := time.Now()
		results, searchErr := s.SearchMemories(q.Query, "longmemeval", "", "", "", store.MemoryStatusActive, opts.K, "")
		durationMs := float64(time.Since(caseStart)) / float64(time.Millisecond)
		expectedIDs := keysToIDs(keyToID, q.ExpectedFactKeys)

		if searchErr != nil {
			return CaseResult{
				QuestionID:   q.ID,
				Category:     q.Category,
				Distance:     q.Distance,
				DistanceSess: q.DistanceSessions,
				Pass:         false,
				Reason:       searchErr.Error(),
				ExpectedIDs:  expectedIDs,
				ExpectedKeys: q.ExpectedFactKeys,
				DurationMs:   durationMs,
				Query:        q.Query,
			}
		}

		top := topIDs(results, opts.K)
		topKeys := idsToKeys(idToKey, top)
		passed := countHitsInTop(top, expectedIDs) > 0
		reason := ""

		var judgeScore float64
		var topBodies []string
		if opts.Judge != nil {
			topBodies = make([]string, 0, len(top))
			for _, item := range results {
				if len(topBodies) >= opts.K {
					break
				}
				topBodies = append(topBodies, item.Body)
			}
			expectedBodies := expectedBodiesForQuestion(q, factBodyByKey)
			judgeScore = opts.Judge.Score(q.Query, topBodies, expectedBodies)
		}
		if len(q.ExpectedAnswers) > 0 && opts.Judge != nil {
			passed = judgeScore >= judgePassScore(opts)
			if !passed {
				reason = fmt.Sprintf("judge score %.3f < %.3f", judgeScore, judgePassScore(opts))
			}
		} else if !passed {
			reason = fmt.Sprintf("no expected facts in top-%d results", opts.K)
		}

		return CaseResult{
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
			JudgeScore:   judgeScore,
			TopBodies:    topBodies,
		}
	}

	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for qi := range workCh {
				caseResults[qi] = evaluateQuestion(questions[qi])
				done := completed.Add(1)
				if done%int64(progressEvery) == 0 || done == int64(totalQuestions) {
					elapsed := time.Since(queryStart)
					avgPerQ := elapsed / time.Duration(done)
					remaining := avgPerQ * time.Duration(totalQuestions-int(done))
					fmt.Fprintf(os.Stderr, "[ohara-bench] querying: %d/%d (%.0f%%) | elapsed %v | ETA %v\n",
						done, totalQuestions,
						float64(done)/float64(totalQuestions)*100,
						elapsed.Round(time.Millisecond),
						remaining.Round(time.Second))
				}
			}
		}()
	}
	for qi := range questions {
		workCh <- qi
	}
	close(workCh)
	wg.Wait()

	for _, cr := range caseResults {
		updateAggFromTopIDs(globalAgg, cr.Pass, cr.TopIDs, cr.ExpectedIDs, opts.K)
		if agg := getOrCreateAgg(distanceAggs, cr.Distance); agg != nil {
			updateAggFromTopIDs(agg, cr.Pass, cr.TopIDs, cr.ExpectedIDs, opts.K)
		}
		if agg := getOrCreateAgg(categoryAggs, cr.Category); agg != nil {
			updateAggFromTopIDs(agg, cr.Pass, cr.TopIDs, cr.ExpectedIDs, opts.K)
		}
		if cr.Reason != "" {
			failures = append(failures, QuestionFailure{
				QuestionID:   cr.QuestionID,
				Category:     cr.Category,
				Distance:     cr.Distance,
				Query:        cr.Query,
				ExpectedKeys: cr.ExpectedKeys,
				ActualKeys:   cr.TopKeys,
				Reason:       cr.Reason,
			})
		}
	}

	queryDuration := time.Since(queryStart)
	fmt.Fprintf(os.Stderr, "[ohara-bench] querying complete in %v | results: %d passed / %d failed\n",
		queryDuration.Round(time.Millisecond), globalAgg.passed, globalAgg.count-globalAgg.passed)

	report := Report{
		FixtureDescription: fixture.Description,
		TotalQuestions:     len(questions),
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
		JudgeEnabled:       opts.Judge != nil,
		RetrievalMode:      retrievalMode,
	}
	// Compute mean judge score if enabled.
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
// When mode is "hybrid", configures the store for hybrid retrieval with deterministic
// test embeddings (no Ollama dependency).
func seedStore(fixture Fixture, mode string, workers int) (*store.Store, map[string]int64, error) {
	tmp, err := os.MkdirTemp("", "ohara-bench-longmemeval-")
	if err != nil {
		return nil, nil, err
	}

	cfg := store.FallbackConfig(tmp)
	cfg.NoJobWorker = true
	cfg.MaxOpenConns = workers
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

	logProgress := func(step, total int, label string) {
		if step%1000 == 0 || step == total {
			pct := float64(step) / float64(total) * 100
			fmt.Fprintf(os.Stderr, "[ohara-bench] seeding %s: %d/%d (%.0f%%)\n", label, step, total, pct)
		}
	}

	keyToID := map[string]int64{}
	totalFacts := len(fixture.Facts)
	fmt.Fprintf(os.Stderr, "[ohara-bench] seeding %d facts...\n", totalFacts)
	for i, fact := range fixture.Facts {
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
		logProgress(i+1, totalFacts, "facts")
	}

	return s, keyToID, nil
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

// isJSONArrayFile checks whether the first non-whitespace byte in f is '['.
// It resets f's read position to 0 on return.
func isJSONArrayFile(f *os.File) bool {
	var buf [16]byte
	n, err := f.Read(buf[:])
	if err != nil || n == 0 {
		f.Seek(0, 0)
		return false
	}
	for _, b := range buf[:n] {
		if b == '[' {
			f.Seek(0, 0)
			return true
		}
		if b != ' ' && b != '\t' && b != '\n' && b != '\r' {
			f.Seek(0, 0)
			return false
		}
	}
	f.Seek(0, 0)
	return false
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

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func invertKeyMap(keyToID map[string]int64) map[int64]string {
	idToKey := make(map[int64]string, len(keyToID))
	for key, id := range keyToID {
		idToKey[id] = key
	}
	return idToKey
}

func mapFactBodiesByKey(facts []FactFixture) map[string]string {
	out := make(map[string]string, len(facts))
	for _, fact := range facts {
		out[fact.Key] = fact.Body
	}
	return out
}

func expectedBodiesForKeys(factBodyByKey map[string]string, keys []string) []string {
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		if body := factBodyByKey[key]; body != "" {
			out = append(out, body)
		}
	}
	return out
}

func expectedBodiesForQuestion(q QuestionFixture, factBodyByKey map[string]string) []string {
	if len(q.ExpectedAnswers) > 0 {
		return append([]string(nil), q.ExpectedAnswers...)
	}
	return expectedBodiesForKeys(factBodyByKey, q.ExpectedFactKeys)
}

func resolveWorkerCount(explicit int) int {
	if explicit > 0 {
		return explicit
	}
	if n := runtime.GOMAXPROCS(0); n > 0 {
		return n
	}
	return 1
}

func judgePassScore(opts RunOptions) float64 {
	if opts.JudgePassScore > 0 {
		return opts.JudgePassScore
	}
	return 0.8
}

func fixtureHasExpectedAnswers(f Fixture) bool {
	for _, q := range f.Questions {
		if len(q.ExpectedAnswers) > 0 {
			return true
		}
	}
	return false
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
