package retrieval

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ashwnn/ohara/internal/store"
)

type Fixture struct {
	Memories   []MemoryFixture   `json:"memories"`
	Cases      []CaseFixture     `json:"cases"`
	Thresholds ThresholdsFixture `json:"thresholds"`
}

type MemoryFixture struct {
	Key            string   `json:"key"`
	Project        string   `json:"project"`
	Kind           string   `json:"kind"`
	Title          string   `json:"title"`
	Body           string   `json:"body"`
	Domain         string   `json:"domain"`
	Classification string   `json:"classification"`
	SessionID      string   `json:"session_id"`
	Tags           []string `json:"tags"`
	AppliesToFiles []string `json:"applies_to_files"`
	EvidenceFile   string   `json:"evidence_file"`
	RelatedFiles   []string `json:"related_files"`
	WrittenBy      string   `json:"written_by"`
	TrustLevel     string   `json:"trust_level"`
	Status         string   `json:"status"`
	SupersededBy   string   `json:"superseded_by"`
	ExpiresDaysAgo int      `json:"expires_days_ago"`
	UpdatedDaysAgo int      `json:"updated_days_ago"`
}

type CaseFixture struct {
	ID            string   `json:"id"`
	Category      string   `json:"category"`
	Type          string   `json:"type"` // search|search_abstain|file_history|file_context|pack|graph_context
	Mode          string   `json:"mode"` // default|hybrid_fallback
	Query         string   `json:"query"`
	Path          string   `json:"path"`
	Entity        string   `json:"entity"`
	Project       string   `json:"project"`
	SessionID     string   `json:"session_id"`
	Domain        string   `json:"domain"`
	Kind          string   `json:"kind"`
	BudgetTokens  int      `json:"budget_tokens"`
	TopK          int      `json:"top_k"`
	ExpectedKeys  []string `json:"expected_keys"`
	ForbiddenKeys []string `json:"forbidden_keys"`
	MinHits       int      `json:"min_hits"`
	MaxTopScore   float64  `json:"max_top_score"`
}

type ThresholdsFixture struct {
	OverallRecallAt3       float64 `json:"overall_recall_at_3"`
	RecallAt3Lexical       float64 `json:"recall_at_3_lexical"`
	RecallAt3FileAware     float64 `json:"recall_at_3_file_aware"`
	MRROverall             float64 `json:"mrr_overall"`
	StaleHitRateMax        float64 `json:"stale_hit_rate_max"`
	WrongProjectHitRateMax float64 `json:"wrong_project_hit_rate_max"`
	SupersededHitRateMax   float64 `json:"superseded_hit_rate_max"`
	PackBudgetCompliance   float64 `json:"pack_budget_compliance"`
	AbstentionFalsePosMax  float64 `json:"abstention_false_positive_rate_max"`
}

type RunOptions struct {
	FixturePath string
	K           int
	Enforce     bool
	Mode        string
	Embedding   string
	OllamaURL   string
}

type Metrics struct {
	RecallAt1            float64
	RecallAt3            float64
	RecallAt5            float64
	MRR                  float64
	NDCGAt5              float64
	StaleHitRate         float64
	WrongProjectHitRate  float64
	SupersededHitRate    float64
	FileContextAccuracy  float64
	GraphContextAccuracy float64
	PackBudgetCompliance float64
	AbstentionFalsePos   float64
}

type Failure struct {
	CaseID      string
	Category    string
	QueryOrPath string
	ExpectedIDs []int64
	ActualTopK  []int64
	Reason      string
	Severity    float64
	Source      string
	HasStale    bool
	HasWrongPrj bool
	HasSupersed bool
}

type CaseResult struct {
	CaseID        string  `json:"case_id"`
	Category      string  `json:"category"`
	Type          string  `json:"type"`
	Source        string  `json:"source"`
	Pass          bool    `json:"pass"`
	FailureReason string  `json:"failure_reason,omitempty"`
	TopIDs        []int64 `json:"top_ids"`
	ExpectedIDs   []int64 `json:"expected_ids"`
	ForbiddenIDs  []int64 `json:"forbidden_ids"`
	DurationMs    float64 `json:"duration_ms"`
}

type Report struct {
	Mode                string
	EmbeddingMode       string
	TotalCases          int
	PassedCases         int
	FailedCases         int
	Metrics             Metrics
	PerCategory         map[string]Metrics
	CategoryCaseCounts  map[string]int
	CaseResults         []CaseResult
	Failures            []Failure
	Runtime             time.Duration
	HybridEnabled       bool
	EmbeddingsAvailable bool
	Thresholds          ThresholdsFixture
	FixtureAudit        FixtureAudit
}

type caseAgg struct {
	count             int
	hit1              int
	hit3              int
	hit5              int
	rrSum             float64
	ndcgSum           float64
	staleHits         int
	wrongProjectHits  int
	supersededHits    int
	fileExpectedTotal int
	fileHitTotal      int
	graphExpectedTotal int
	graphHitTotal      int
	packCases         int
	packPass          int
	abstentionCases   int
	abstentionFP      int
}

type FixtureAudit struct {
	CategoryCounts      map[string]int
	CategoriesUnder5    []string
	HappyPathExactCount int
	WeakDistractorCount int
	HighOverlapCaseIDs  []string
	AverageTitleOverlap float64
	MaxTitleOverlap     float64
	SearchCaseCount     int
}

func LoadFixture(path string) (Fixture, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Fixture{}, err
	}
	var fixture Fixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		return Fixture{}, err
	}
	if len(fixture.Cases) == 0 {
		return Fixture{}, fmt.Errorf("fixture has no cases")
	}
	return fixture, nil
}

func RunBenchmark(opts RunOptions) (Report, error) {
	start := time.Now()
	if opts.K <= 0 {
		opts.K = 5
	}
	if opts.FixturePath == "" {
		opts.FixturePath = filepath.Join("bench", "fixtures", "retrieval_fixture.json")
	}

	fixture, err := LoadFixture(opts.FixturePath)
	if err != nil {
		return Report{}, err
	}
	thresholds := withDefaultThresholds(fixture.Thresholds)
	audit := auditFixture(fixture)

	mode := strings.TrimSpace(opts.Mode)
	if mode == "" {
		mode = strings.TrimSpace(os.Getenv("OHARA_RETRIEVAL_MODE"))
	}
	if mode == "" {
		mode = "fts5"
	}
	embeddingBackend := strings.TrimSpace(opts.Embedding)
	if embeddingBackend == "" {
		embeddingBackend = strings.TrimSpace(os.Getenv("OHARA_EMBEDDING_BACKEND"))
	}
	ollamaURL := strings.TrimSpace(opts.OllamaURL)
	if ollamaURL == "" {
		ollamaURL = strings.TrimSpace(os.Getenv("OHARA_OLLAMA_URL"))
	}
	if ollamaURL == "" {
		ollamaURL = "http://127.0.0.1:11434"
	}

	ftsStore, ftsIDs, err := seededStore(fixture, nil)
	if err != nil {
		return Report{}, err
	}
	defer ftsStore.Close()

	hybridStore, hybridIDs, err := seededStore(fixture, func(cfg store.Config) store.Config {
		cfg.RetrievalMode = "hybrid"
		cfg.EmbeddingBackend = "ollama"
		cfg.OllamaURL = "http://127.0.0.1:1"
		return cfg
	})
	if err != nil {
		return Report{}, err
	}
	defer hybridStore.Close()

	activeStore := ftsStore
	activeIDs := ftsIDs
	hybridEnabled := false
	embeddingsAvailable := false
	embeddingMode := "fts-fallback"
	if strings.EqualFold(mode, "hybrid") {
		if embeddingBackend == "" {
			embeddingBackend = "ollama"
		}
		activeStore, activeIDs, err = seededStore(fixture, func(cfg store.Config) store.Config {
			cfg.RetrievalMode = "hybrid"
			if embeddingBackend != "" {
				cfg.EmbeddingBackend = embeddingBackend
			} else {
				cfg.EmbeddingBackend = "ollama"
			}
			cfg.OllamaURL = ollamaURL
			return cfg
		})
		if err != nil {
			return Report{}, err
		}
		defer activeStore.Close()
		hybridEnabled = true
		embeddingsAvailable = detectEmbeddingAvailability(embeddingBackend, ollamaURL)
		switch {
		case strings.EqualFold(embeddingBackend, "deterministic-test"):
			embeddingMode = "deterministic-test"
		case strings.EqualFold(embeddingBackend, "ollama") && embeddingsAvailable:
			embeddingMode = "real-ollama"
		default:
			embeddingMode = "fts-fallback"
		}
	}

	report := Report{
		Mode:                mode,
		EmbeddingMode:       embeddingMode,
		PerCategory:         map[string]Metrics{},
		CategoryCaseCounts:  map[string]int{},
		HybridEnabled:       hybridEnabled,
		EmbeddingsAvailable: embeddingsAvailable,
		Thresholds:          thresholds,
		FixtureAudit:        audit,
	}

	global := &caseAgg{}
	byCategory := map[string]*caseAgg{}
	failures := make([]Failure, 0, 32)
	caseResults := make([]CaseResult, 0, len(fixture.Cases))

	for _, c := range fixture.Cases {
		report.TotalCases++
		category := strings.TrimSpace(c.Category)
		if category == "" {
			category = "uncategorized"
		}
		report.CategoryCaseCounts[category]++
		cAgg := byCategory[category]
		if cAgg == nil {
			cAgg = &caseAgg{}
			byCategory[category] = cAgg
		}

		targetStore := activeStore
		keyToID := activeIDs
		if c.Mode == "hybrid_fallback" {
			targetStore = hybridStore
			keyToID = hybridIDs
		}

		caseStart := time.Now()
		passed, failure, updates, source, topIDsForResult := runCase(targetStore, keyToID, c, opts.K)
		durationMs := float64(time.Since(caseStart)) / float64(time.Millisecond)

		expectedIDs := keysToIDs(keyToID, c.ExpectedKeys)
		forbiddenIDs := keysToIDs(keyToID, c.ForbiddenKeys)
		reason := ""
		if !passed {
			reason = failure.Reason
		}

		cr := CaseResult{
			CaseID:        c.ID,
			Category:      category,
			Type:          c.Type,
			Source:        source,
			Pass:          passed,
			FailureReason: reason,
			TopIDs:        topIDsForResult,
			ExpectedIDs:   expectedIDs,
			ForbiddenIDs:  forbiddenIDs,
			DurationMs:    durationMs,
		}
		caseResults = append(caseResults, cr)

		mergeAgg(global, updates)
		mergeAgg(cAgg, updates)
		if passed {
			report.PassedCases++
		} else {
			report.FailedCases++
			failures = append(failures, failure)
		}
	}

	report.CaseResults = caseResults

	report.Metrics = aggToMetrics(global)
	for category, agg := range byCategory {
		report.PerCategory[category] = aggToMetrics(agg)
	}
	sort.SliceStable(failures, func(i, j int) bool {
		if failures[i].Severity == failures[j].Severity {
			return failures[i].CaseID < failures[j].CaseID
		}
		return failures[i].Severity > failures[j].Severity
	})
	report.Failures = failures
	report.Runtime = time.Since(start)

	if opts.Enforce {
		if err := enforceThresholds(report); err != nil {
			return report, err
		}
	}
	return report, nil
}

func withDefaultThresholds(in ThresholdsFixture) ThresholdsFixture {
	out := in
	if out.OverallRecallAt3 <= 0 {
		out.OverallRecallAt3 = 0.80
	}
	if out.RecallAt3Lexical <= 0 {
		out.RecallAt3Lexical = 0.90
	}
	if out.RecallAt3FileAware <= 0 {
		out.RecallAt3FileAware = 0.85
	}
	if out.MRROverall <= 0 {
		out.MRROverall = 0.70
	}
	out.StaleHitRateMax = maxFloat(0, out.StaleHitRateMax)
	out.WrongProjectHitRateMax = maxFloat(0, out.WrongProjectHitRateMax)
	out.SupersededHitRateMax = maxFloat(0, out.SupersededHitRateMax)
	if out.PackBudgetCompliance <= 0 {
		out.PackBudgetCompliance = 1.0
	}
	if out.AbstentionFalsePosMax <= 0 {
		out.AbstentionFalsePosMax = 0.10
	}
	return out
}

func seededStore(fixture Fixture, mutator func(store.Config) store.Config) (*store.Store, map[string]int64, error) {
	cfg := store.FallbackConfig(filepath.Join(os.TempDir(), fmt.Sprintf("ohara-bench-%d", time.Now().UnixNano())))
	if mutator != nil {
		cfg = mutator(cfg)
	}
	s, err := store.New(cfg)
	if err != nil {
		return nil, nil, err
	}

	keyToID := map[string]int64{}
	for _, memory := range fixture.Memories {
		appliesToJSON := ""
		if len(memory.AppliesToFiles) > 0 {
			b, _ := json.Marshal(map[string]any{"files": memory.AppliesToFiles})
			appliesToJSON = string(b)
		}
		evidenceJSON := ""
		if strings.TrimSpace(memory.EvidenceFile) != "" {
			b, _ := json.Marshal(map[string]any{"file": memory.EvidenceFile})
			evidenceJSON = string(b)
		}
		relatedJSON := ""
		if len(memory.RelatedFiles) > 0 {
			b, _ := json.Marshal(map[string]any{"files": memory.RelatedFiles})
			relatedJSON = string(b)
		}

		id, err := s.AddMemory(store.AddMemoryParams{
			ProjectID:      memory.Project,
			Kind:           memory.Kind,
			Title:          memory.Title,
			Body:           memory.Body,
			Domain:         memory.Domain,
			Classification: memory.Classification,
			SessionID:      memory.SessionID,
			Tags:           memory.Tags,
			AppliesToJSON:  appliesToJSON,
			EvidenceJSON:   evidenceJSON,
			RelatedJSON:    relatedJSON,
			WrittenBy:      memory.WrittenBy,
			TrustLevel:     memory.TrustLevel,
		})
		if err != nil {
			_ = s.Close()
			return nil, nil, fmt.Errorf("seed %s: %w", memory.Key, err)
		}
		keyToID[memory.Key] = id

		entities := store.ExtractEntitiesHeuristic(memory.Title + "\n" + memory.Body)
		if _, err := s.AttachExtractedEntities(id, memory.Project, entities); err != nil {
			_ = s.Close()
			return nil, nil, fmt.Errorf("entity attach %s: %w", memory.Key, err)
		}
	}

	for _, memory := range fixture.Memories {
		id := keyToID[memory.Key]
		if id == 0 {
			continue
		}
		if memory.ExpiresDaysAgo > 0 {
			ts := time.Now().UTC().AddDate(0, 0, -memory.ExpiresDaysAgo).Format(time.RFC3339)
			if _, err := s.Exec(`UPDATE memory_items SET expires_at = ? WHERE id = ?`, ts, id); err != nil {
				_ = s.Close()
				return nil, nil, err
			}
		}
		if memory.UpdatedDaysAgo > 0 {
			ts := time.Now().UTC().AddDate(0, 0, -memory.UpdatedDaysAgo).Format(time.RFC3339)
			if _, err := s.Exec(`UPDATE memory_items SET updated_at = ?, last_accessed = ? WHERE id = ?`, ts, ts, id); err != nil {
				_ = s.Close()
				return nil, nil, err
			}
		}
		if strings.TrimSpace(memory.Status) != "" {
			supBy := int64(0)
			if memory.SupersededBy != "" {
				supBy = keyToID[memory.SupersededBy]
			}
			if _, err := s.Exec(`UPDATE memory_items SET status = ?, superseded_by = CASE WHEN ? = 0 THEN superseded_by ELSE ? END WHERE id = ?`, memory.Status, supBy, supBy, id); err != nil {
				_ = s.Close()
				return nil, nil, err
			}
		}
	}
	return s, keyToID, nil
}

func runCase(s *store.Store, keyToID map[string]int64, c CaseFixture, defaultK int) (bool, Failure, *caseAgg, string, []int64) {
	agg := &caseAgg{}
	k := c.TopK
	if k <= 0 {
		k = defaultK
	}
	if k <= 0 {
		k = 5
	}

	switch c.Type {
	case "search", "search_abstain":
		passed, failure, updates, source, topIDs := runSearchCase(s, keyToID, c, k, agg)
		return passed, failure, updates, source, topIDs
	case "file_history":
		passed, failure, updates, source, topIDs := runFileHistoryCase(s, keyToID, c, k, agg)
		return passed, failure, updates, source, topIDs
	case "file_context":
		passed, failure, updates, source, topIDs := runFileContextCase(s, keyToID, c, agg)
		return passed, failure, updates, source, topIDs
	case "graph_context":
		passed, failure, updates, source, topIDs := runGraphContextCase(s, keyToID, c, agg)
		return passed, failure, updates, source, topIDs
	case "pack":
		passed, failure, updates, source, topIDs := runPackCase(s, keyToID, c, agg)
		return passed, failure, updates, source, topIDs
	default:
		return false, Failure{CaseID: c.ID, Category: c.Category, Reason: "unknown case type", Severity: 1.0}, agg, "unknown", nil
	}
}

func runSearchCase(s *store.Store, keyToID map[string]int64, c CaseFixture, k int, agg *caseAgg) (bool, Failure, *caseAgg, string, []int64) {
	results, err := s.SearchMemories(c.Query, c.Project, "", c.Kind, c.Domain, store.MemoryStatusActive, k, "")
	if err != nil {
		return false, Failure{CaseID: c.ID, Category: c.Category, QueryOrPath: c.Query, Reason: err.Error(), Severity: 1.0}, agg, "error", nil
	}

	expectedIDs := keysToIDs(keyToID, c.ExpectedKeys)
	forbiddenIDs := keysToIDs(keyToID, c.ForbiddenKeys)
	top := topIDs(results, k)
	firstRank := firstRelevantRank(results, expectedIDs)
	relevantInTopK := countRelevant(results, expectedIDs, k)

	passed := true
	reason := ""
	source := detectSearchSource(s, c, k, len(results) > 0)
	minHits := c.MinHits
	if minHits <= 0 && len(expectedIDs) > 0 {
		minHits = 1
	}
	if c.Type == "search_abstain" {
		agg.abstentionCases++
		falsePositive := false
		passed = len(expectedIDs) == 0 && relevantInTopK == 0
		if passed && c.MaxTopScore > 0 && len(results) > 0 && results[0].RelevanceScore > c.MaxTopScore {
			passed = false
			falsePositive = true
			reason = fmt.Sprintf("abstention score too high: %.4f > %.4f", results[0].RelevanceScore, c.MaxTopScore)
		}
		if passed && len(forbiddenIDs) > 0 {
			for _, id := range top {
				if containsInt64(forbiddenIDs, id) {
					passed = false
					falsePositive = true
					reason = "abstention returned forbidden memory"
					break
				}
			}
		}
		if !falsePositive && len(results) > 0 {
			if c.MaxTopScore <= 0 || results[0].RelevanceScore > c.MaxTopScore {
				falsePositive = true
			}
		}
		if falsePositive {
			agg.abstentionFP++
		}
		if !passed && reason == "" {
			reason = "abstention case retrieved relevant/forbidden memory"
		}
	} else {
		agg.count++
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
		staleHits, supersededHits := staleAndSupersededHits(results, k)
		agg.staleHits += staleHits
		agg.supersededHits += supersededHits
		if c.Project != "" {
			agg.wrongProjectHits += wrongProjectHits(results, c.Project, k)
		}

		if relevantInTopK < minHits {
			passed = false
			reason = fmt.Sprintf("only %d/%d expected memories in top-%d", relevantInTopK, minHits, k)
		}
		if passed && len(forbiddenIDs) > 0 {
			for _, id := range top {
				if containsInt64(forbiddenIDs, id) {
					passed = false
					reason = "forbidden memory returned in top results"
					break
				}
			}
		}
	}

	if !passed {
		staleHits, supersededHits := staleAndSupersededHits(results, k)
		wrongProject := 0
		if c.Project != "" {
			wrongProject = wrongProjectHits(results, c.Project, k)
		}
		return false, Failure{
			CaseID:      c.ID,
			Category:    c.Category,
			QueryOrPath: c.Query,
			ExpectedIDs: expectedIDs,
			ActualTopK:  top,
			Reason:      reason,
			Severity:    failureSeverity(relevantInTopK, minHits, firstRank),
			Source:      source,
			HasStale:    staleHits > 0,
			HasSupersed: supersededHits > 0,
			HasWrongPrj: wrongProject > 0,
		}, agg, source, top
	}
	return true, Failure{}, agg, source, top
}

func runFileHistoryCase(s *store.Store, keyToID map[string]int64, c CaseFixture, k int, agg *caseAgg) (bool, Failure, *caseAgg, string, []int64) {
	agg.count++
	items, err := s.FileHistory(c.Path, c.Project, k)
	if err != nil {
		return false, Failure{CaseID: c.ID, Category: c.Category, QueryOrPath: c.Path, Reason: err.Error(), Severity: 1.0}, agg, "file_history_error", nil
	}
	expectedIDs := keysToIDs(keyToID, c.ExpectedKeys)
	top := topIDs(items, k)
	firstRank := firstRelevantRank(items, expectedIDs)
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
	agg.ndcgSum += ndcgAtK(items, expectedIDs, minInt(5, k))
	hits := countIDsInTop(top, expectedIDs)
	required := c.MinHits
	if required <= 0 {
		required = 1
	}

	agg.fileExpectedTotal += maxInt(1, required)
	agg.fileHitTotal += minInt(hits, required)

	if hits < required {
		return false, Failure{
			CaseID:      c.ID,
			Category:    c.Category,
			QueryOrPath: c.Path,
			ExpectedIDs: expectedIDs,
			ActualTopK:  top,
			Reason:      fmt.Sprintf("file history hits %d < required %d", hits, required),
			Severity:    float64(required-hits) + 0.5,
			Source:      "file_history_scoring",
		}, agg, "file_history_scoring", top
	}
	return true, Failure{}, agg, "file_history_scoring", top
}

func runFileContextCase(s *store.Store, keyToID map[string]int64, c CaseFixture, agg *caseAgg) (bool, Failure, *caseAgg, string, []int64) {
	agg.count++
	budget := c.BudgetTokens
	if budget <= 0 {
		budget = 260
	}
	ctx, err := s.FileContext(c.Path, c.Project, budget)
	if err != nil {
		return false, Failure{CaseID: c.ID, Category: c.Category, QueryOrPath: c.Path, Reason: err.Error(), Severity: 1.0}, agg, "file_context_error", nil
	}
	expectedIDs := keysToIDs(keyToID, c.ExpectedKeys)
	actualIDs := topIDs(ctx.MemoryItems, 10)
	firstRank := firstRelevantRank(ctx.MemoryItems, expectedIDs)
	if firstRank == 1 {
		agg.hit1++
	}
	if firstRank > 0 && firstRank <= 3 {
		agg.hit3++
	}
	if firstRank > 0 && firstRank <= 5 {
		agg.hit5++
	}
	if firstRank > 0 {
		agg.rrSum += 1.0 / float64(firstRank)
	}
	agg.ndcgSum += ndcgAtK(ctx.MemoryItems, expectedIDs, 5)
	hits := countIDsInTop(actualIDs, expectedIDs)
	required := c.MinHits
	if required <= 0 {
		required = 1
	}
	agg.fileExpectedTotal += maxInt(1, required)
	agg.fileHitTotal += minInt(hits, required)

	if ctx.TokenCount > budget {
		return false, Failure{
			CaseID:      c.ID,
			Category:    c.Category,
			QueryOrPath: c.Path,
			ExpectedIDs: expectedIDs,
			ActualTopK:  actualIDs,
			Reason:      fmt.Sprintf("file context exceeded budget: %d > %d", ctx.TokenCount, budget),
			Severity:    1.0,
			Source:      "file_context_scoring",
		}, agg, "file_context_scoring", actualIDs
	}
	if hits < required {
		return false, Failure{
			CaseID:      c.ID,
			Category:    c.Category,
			QueryOrPath: c.Path,
			ExpectedIDs: expectedIDs,
			ActualTopK:  actualIDs,
			Reason:      fmt.Sprintf("file context hits %d < required %d", hits, required),
			Severity:    float64(required-hits) + 0.5,
			Source:      "file_context_scoring",
		}, agg, "file_context_scoring", actualIDs
	}
	return true, Failure{}, agg, "file_context_scoring", actualIDs
}

func runGraphContextCase(s *store.Store, keyToID map[string]int64, c CaseFixture, agg *caseAgg) (bool, Failure, *caseAgg, string, []int64) {
	agg.count++
	k := c.TopK
	if k <= 0 {
		k = 5
	}
	items, err := s.GraphContext(c.Project, c.Entity, k)
	if err != nil {
		return false, Failure{CaseID: c.ID, Category: c.Category, QueryOrPath: c.Entity, Reason: err.Error(), Severity: 1.0}, agg, "graph_context_error", nil
	}
	expectedIDs := keysToIDs(keyToID, c.ExpectedKeys)
	forbiddenIDs := keysToIDs(keyToID, c.ForbiddenKeys)
	top := topIDs(items, k)
	firstRank := firstRelevantRank(items, expectedIDs)
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
	agg.ndcgSum += ndcgAtK(items, expectedIDs, minInt(5, k))
	hits := countIDsInTop(top, expectedIDs)
	required := c.MinHits
	if required <= 0 {
		required = 1
	}
	agg.graphExpectedTotal += maxInt(1, required)
	agg.graphHitTotal += minInt(hits, required)

	if hits < required {
		return false, Failure{
			CaseID:      c.ID,
			Category:    c.Category,
			QueryOrPath: c.Entity,
			ExpectedIDs: expectedIDs,
			ActualTopK:  top,
			Reason:      fmt.Sprintf("graph context hits %d < required %d", hits, required),
			Severity:    float64(required-hits) + 0.5,
			Source:      "graph_context",
		}, agg, "graph_context", top
	}
	for _, id := range top {
		if containsInt64(forbiddenIDs, id) {
			return false, Failure{
				CaseID:      c.ID,
				Category:    c.Category,
				QueryOrPath: c.Entity,
				ExpectedIDs: expectedIDs,
				ActualTopK:  top,
				Reason:      "graph context returned forbidden memory",
				Severity:    1.0,
				Source:      "graph_context",
			}, agg, "graph_context", top
		}
	}
	return true, Failure{}, agg, "graph_context", top
}

func runPackCase(s *store.Store, keyToID map[string]int64, c CaseFixture, agg *caseAgg) (bool, Failure, *caseAgg, string, []int64) {
	budget := c.BudgetTokens
	if budget <= 0 {
		budget = 280
	}
	pack, err := s.BuildPack(store.PackParams{
		ProjectID:    c.Project,
		SessionID:    c.SessionID,
		BudgetTokens: budget,
		Domain:       c.Domain,
		Explain:      true,
	})
	if err != nil {
		return false, Failure{CaseID: c.ID, Category: c.Category, QueryOrPath: c.Project, Reason: err.Error(), Severity: 1.0}, agg, "pack_error", nil
	}
	agg.packCases++
	if pack.TokenCount <= budget {
		agg.packPass++
	}

	expectedIDs := keysToIDs(keyToID, c.ExpectedKeys)
	forbiddenIDs := keysToIDs(keyToID, c.ForbiddenKeys)
	actual := topIDs(pack.MemoryItems, 12)
	hits := countIDsInTop(actual, expectedIDs)
	required := c.MinHits
	if required <= 0 {
		required = 1
	}

	if pack.TokenCount > budget {
		return false, Failure{
			CaseID:      c.ID,
			Category:    c.Category,
			QueryOrPath: c.Project,
			ExpectedIDs: expectedIDs,
			ActualTopK:  actual,
			Reason:      fmt.Sprintf("pack token budget exceeded: %d > %d", pack.TokenCount, budget),
			Severity:    1.0,
			Source:      "pack_scoring",
		}, agg, "pack_scoring", actual
	}
	if hits < required {
		return false, Failure{
			CaseID:      c.ID,
			Category:    c.Category,
			QueryOrPath: c.Project,
			ExpectedIDs: expectedIDs,
			ActualTopK:  actual,
			Reason:      fmt.Sprintf("pack included only %d/%d required memories", hits, required),
			Severity:    float64(required-hits) + 0.5,
			Source:      "pack_scoring",
		}, agg, "pack_scoring", actual
	}
	for _, id := range actual {
		if containsInt64(forbiddenIDs, id) {
			return false, Failure{
				CaseID:      c.ID,
				Category:    c.Category,
				QueryOrPath: c.Project,
				ExpectedIDs: expectedIDs,
				ActualTopK:  actual,
				Reason:      "pack included forbidden memory",
				Severity:    1.0,
				Source:      "pack_scoring",
			}, agg, "pack_scoring", actual
		}
	}
	return true, Failure{}, agg, "pack_scoring", actual
}

func aggToMetrics(agg *caseAgg) Metrics {
	m := Metrics{}
	if agg.count > 0 {
		total := float64(agg.count)
		m.RecallAt1 = float64(agg.hit1) / total
		m.RecallAt3 = float64(agg.hit3) / total
		m.RecallAt5 = float64(agg.hit5) / total
		m.MRR = agg.rrSum / total
		m.NDCGAt5 = agg.ndcgSum / total
	}
	if agg.count > 0 {
		div := float64(maxInt(1, agg.count) * 5)
		m.StaleHitRate = float64(agg.staleHits) / div
		m.WrongProjectHitRate = float64(agg.wrongProjectHits) / div
		m.SupersededHitRate = float64(agg.supersededHits) / div
	}
	if agg.fileExpectedTotal > 0 {
		m.FileContextAccuracy = float64(agg.fileHitTotal) / float64(agg.fileExpectedTotal)
	}
	if agg.graphExpectedTotal > 0 {
		m.GraphContextAccuracy = float64(agg.graphHitTotal) / float64(agg.graphExpectedTotal)
	}
	if agg.packCases > 0 {
		m.PackBudgetCompliance = float64(agg.packPass) / float64(agg.packCases)
	}
	if agg.abstentionCases > 0 {
		m.AbstentionFalsePos = float64(agg.abstentionFP) / float64(agg.abstentionCases)
	}
	return m
}

func mergeAgg(dst, src *caseAgg) {
	dst.count += src.count
	dst.hit1 += src.hit1
	dst.hit3 += src.hit3
	dst.hit5 += src.hit5
	dst.rrSum += src.rrSum
	dst.ndcgSum += src.ndcgSum
	dst.staleHits += src.staleHits
	dst.wrongProjectHits += src.wrongProjectHits
	dst.supersededHits += src.supersededHits
	dst.fileExpectedTotal += src.fileExpectedTotal
	dst.fileHitTotal += src.fileHitTotal
	dst.graphExpectedTotal += src.graphExpectedTotal
	dst.graphHitTotal += src.graphHitTotal
	dst.packCases += src.packCases
	dst.packPass += src.packPass
	dst.abstentionCases += src.abstentionCases
	dst.abstentionFP += src.abstentionFP
}

func enforceThresholds(r Report) error {
	lex := r.PerCategory["lexical"]
	fileAware := r.PerCategory["file_aware"]
	var failures []string

	if lex.RecallAt3 < r.Thresholds.RecallAt3Lexical {
		failures = append(failures, fmt.Sprintf("lexical recall@3 %.3f < %.3f", lex.RecallAt3, r.Thresholds.RecallAt3Lexical))
	}
	if fileAware.RecallAt3 < r.Thresholds.RecallAt3FileAware {
		failures = append(failures, fmt.Sprintf("file-aware recall@3 %.3f < %.3f", fileAware.RecallAt3, r.Thresholds.RecallAt3FileAware))
	}
	if r.Metrics.RecallAt3 < r.Thresholds.OverallRecallAt3 {
		failures = append(failures, fmt.Sprintf("overall recall@3 %.3f < %.3f", r.Metrics.RecallAt3, r.Thresholds.OverallRecallAt3))
	}
	if r.Metrics.MRR < r.Thresholds.MRROverall {
		failures = append(failures, fmt.Sprintf("overall MRR %.3f < %.3f", r.Metrics.MRR, r.Thresholds.MRROverall))
	}
	if r.Metrics.StaleHitRate > r.Thresholds.StaleHitRateMax {
		failures = append(failures, fmt.Sprintf("stale-hit rate %.4f > %.4f", r.Metrics.StaleHitRate, r.Thresholds.StaleHitRateMax))
	}
	if r.Metrics.WrongProjectHitRate > r.Thresholds.WrongProjectHitRateMax {
		failures = append(failures, fmt.Sprintf("wrong-project-hit rate %.4f > %.4f", r.Metrics.WrongProjectHitRate, r.Thresholds.WrongProjectHitRateMax))
	}
	if r.Metrics.SupersededHitRate > r.Thresholds.SupersededHitRateMax {
		failures = append(failures, fmt.Sprintf("superseded-hit rate %.4f > %.4f", r.Metrics.SupersededHitRate, r.Thresholds.SupersededHitRateMax))
	}
	if r.Metrics.PackBudgetCompliance < r.Thresholds.PackBudgetCompliance {
		failures = append(failures, fmt.Sprintf("pack budget compliance %.3f < %.3f", r.Metrics.PackBudgetCompliance, r.Thresholds.PackBudgetCompliance))
	}
	if r.Metrics.AbstentionFalsePos > r.Thresholds.AbstentionFalsePosMax {
		failures = append(failures, fmt.Sprintf("abstention false-positive rate %.3f > %.3f", r.Metrics.AbstentionFalsePos, r.Thresholds.AbstentionFalsePosMax))
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
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

func countRelevant(results []store.MemoryItem, expectedIDs []int64, k int) int {
	if len(expectedIDs) == 0 {
		return 0
	}
	if k > len(results) {
		k = len(results)
	}
	count := 0
	for i := 0; i < k; i++ {
		if containsInt64(expectedIDs, results[i].ID) {
			count++
		}
	}
	return count
}

func staleAndSupersededHits(results []store.MemoryItem, k int) (staleHits, supersededHits int) {
	now := time.Now().UTC()
	if k > len(results) {
		k = len(results)
	}
	for i := 0; i < k; i++ {
		item := results[i]
		if item.Status == store.MemoryStatusArchived || item.Status == store.MemoryStatusSuperseded || (item.SupersededBy != nil && *item.SupersededBy > 0) {
			supersededHits++
		}
		if item.ExpiresAt != nil && *item.ExpiresAt != "" {
			if ts, err := time.Parse(time.RFC3339Nano, *item.ExpiresAt); err == nil && !ts.After(now) {
				staleHits++
			}
		}
	}
	return staleHits, supersededHits
}

func wrongProjectHits(results []store.MemoryItem, project string, k int) int {
	if project == "" {
		return 0
	}
	if k > len(results) {
		k = len(results)
	}
	hits := 0
	for i := 0; i < k; i++ {
		if results[i].ProjectID != project {
			hits++
		}
	}
	return hits
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

func countIDsInTop(top []int64, expected []int64) int {
	count := 0
	for _, id := range top {
		if containsInt64(expected, id) {
			count++
		}
	}
	return count
}

func detectSearchSource(s *store.Store, c CaseFixture, k int, hasResults bool) string {
	if c.Mode == "hybrid_fallback" {
		return "hybrid_fallback"
	}
	if !hasResults {
		return "none"
	}
	if strictFTSHasHits(s, c, k) {
		return "strict_fts"
	}
	if fallbackFTSHasHits(s, c, k) {
		return "or_fallback"
	}
	return "unknown"
}

func strictFTSHasHits(s *store.Store, c CaseFixture, k int) bool {
	match := quoteAnd(strings.Fields(c.Query))
	return hasFTSHits(s, c, match, k)
}

func fallbackFTSHasHits(s *store.Store, c CaseFixture, k int) bool {
	terms := buildFallbackTermsLocal(c.Query)
	if len(terms) == 0 {
		return false
	}
	match := quoteOR(terms)
	return hasFTSHits(s, c, match, maxInt(30, k*6))
}

var fallbackStopWords = map[string]struct{}{
	"the": {}, "and": {}, "or": {}, "for": {}, "with": {}, "from": {}, "into": {},
	"this": {}, "that": {}, "what": {}, "when": {}, "where": {}, "which": {}, "who": {},
	"is": {}, "are": {}, "was": {}, "were": {}, "be": {}, "been": {}, "being": {},
	"should": {}, "must": {}, "can": {}, "could": {}, "would": {}, "will": {},
	"how": {}, "why": {}, "before": {}, "after": {}, "then": {}, "current": {},
}

func buildFallbackTermsLocal(raw string) []string {
	parts := strings.Fields(strings.ToLower(raw))
	terms := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		token := strings.Trim(part, `"'.,;:!?()[]{}<>`)
		if len(token) < 3 {
			continue
		}
		if _, skip := fallbackStopWords[token]; skip {
			continue
		}
		candidates := []string{token}
		if strings.HasSuffix(token, "ies") && len(token) > 4 {
			candidates = append(candidates, token[:len(token)-3]+"y")
		} else if strings.HasSuffix(token, "es") && len(token) > 4 {
			candidates = append(candidates, token[:len(token)-2])
		} else if strings.HasSuffix(token, "s") && len(token) > 3 {
			candidates = append(candidates, token[:len(token)-1])
		}
		for _, candidate := range candidates {
			if len(candidate) < 3 {
				continue
			}
			if _, ok := seen[candidate]; ok {
				continue
			}
			seen[candidate] = struct{}{}
			terms = append(terms, candidate)
		}
	}
	return terms
}

func hasFTSHits(s *store.Store, c CaseFixture, match string, k int) bool {
	if strings.TrimSpace(match) == "" {
		return false
	}
	query := `
		SELECT COUNT(*)
		FROM memory_items_fts fts
		JOIN memory_items mi ON mi.id = fts.rowid
		WHERE memory_items_fts MATCH ?
		  AND mi.status = ?
		  AND (mi.expires_at IS NULL OR mi.expires_at = '' OR mi.expires_at > datetime('now'))`
	args := []any{match, store.MemoryStatusActive}
	if c.Project != "" {
		query += " AND mi.project_id = ?"
		args = append(args, c.Project)
	}
	if c.Kind != "" {
		query += " AND mi.kind = ?"
		args = append(args, c.Kind)
	}
	if c.Domain != "" {
		query += " AND mi.domain = ?"
		args = append(args, c.Domain)
	}
	if k <= 0 {
		k = 5
	}
	query += " LIMIT ?"
	args = append(args, k)

	row := s.QueryRow(query, args...)
	var count int
	if err := row.Scan(&count); err != nil {
		return false
	}
	return count > 0
}

func quoteAnd(words []string) string {
	quoted := make([]string, 0, len(words))
	for _, word := range words {
		token := strings.Trim(word, `"`)
		if strings.TrimSpace(token) == "" {
			continue
		}
		quoted = append(quoted, `"`+token+`"`)
	}
	return strings.Join(quoted, " ")
}

func quoteOR(words []string) string {
	quoted := make([]string, 0, len(words))
	for _, word := range words {
		token := strings.Trim(word, `"`)
		if strings.TrimSpace(token) == "" {
			continue
		}
		quoted = append(quoted, `"`+token+`"`)
	}
	return strings.Join(quoted, " OR ")
}

func detectEmbeddingAvailability(backend, ollamaURL string) bool {
	if strings.EqualFold(strings.TrimSpace(backend), "deterministic-test") {
		return true
	}
	if !strings.EqualFold(strings.TrimSpace(backend), "ollama") {
		return false
	}
	base := strings.TrimSpace(ollamaURL)
	if base == "" {
		base = "http://127.0.0.1:11434"
	}
	url := strings.TrimRight(base, "/") + "/api/embeddings"
	reqBody := strings.NewReader(`{"model":"nomic-embed-text","prompt":"healthcheck"}`)
	req, err := http.NewRequest(http.MethodPost, url, reqBody)
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 1200 * time.Millisecond}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func auditFixture(fixture Fixture) FixtureAudit {
	out := FixtureAudit{
		CategoryCounts: map[string]int{},
	}
	keyByMemory := map[string]MemoryFixture{}
	for _, mem := range fixture.Memories {
		keyByMemory[mem.Key] = mem
	}

	overlapSum := 0.0
	for _, c := range fixture.Cases {
		category := strings.TrimSpace(c.Category)
		if category == "" {
			category = "uncategorized"
		}
		out.CategoryCounts[category]++
		if c.Type != "search" && c.Type != "search_abstain" {
			continue
		}
		if len(c.ExpectedKeys) == 0 {
			continue
		}

		out.SearchCaseCount++
		queryTerms := normalizedTerms(c.Query)
		bestOverlap := 0.0
		for _, key := range c.ExpectedKeys {
			mem, ok := keyByMemory[key]
			if !ok {
				continue
			}
			titleTerms := normalizedTerms(mem.Title)
			overlap := termOverlapRatio(queryTerms, titleTerms)
			if overlap > bestOverlap {
				bestOverlap = overlap
			}
		}
		overlapSum += bestOverlap
		if bestOverlap >= 0.70 {
			out.HighOverlapCaseIDs = append(out.HighOverlapCaseIDs, c.ID)
		}
		if bestOverlap > out.MaxTitleOverlap {
			out.MaxTitleOverlap = bestOverlap
		}
		if len(c.ForbiddenKeys) == 0 && len(c.ExpectedKeys) == 1 {
			out.WeakDistractorCount++
			if bestOverlap >= 0.75 {
				out.HappyPathExactCount++
			}
		}
	}
	if out.SearchCaseCount > 0 {
		out.AverageTitleOverlap = overlapSum / float64(out.SearchCaseCount)
	}
	for category, count := range out.CategoryCounts {
		if count < 5 {
			out.CategoriesUnder5 = append(out.CategoriesUnder5, fmt.Sprintf("%s:%d", category, count))
		}
	}
	sort.Strings(out.CategoriesUnder5)
	sort.Strings(out.HighOverlapCaseIDs)
	return out
}

func normalizedTerms(text string) map[string]struct{} {
	parts := strings.Fields(strings.ToLower(text))
	out := map[string]struct{}{}
	for _, part := range parts {
		token := strings.Trim(part, `"'.,;:!?()[]{}<>`)
		if len(token) < 3 {
			continue
		}
		if _, skip := fallbackStopWords[token]; skip {
			continue
		}
		out[token] = struct{}{}
	}
	return out
}

func termOverlapRatio(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	union := map[string]struct{}{}
	for k := range a {
		union[k] = struct{}{}
	}
	for k := range b {
		if _, ok := a[k]; ok {
			inter++
		}
		union[k] = struct{}{}
	}
	if len(union) == 0 {
		return 0
	}
	return float64(inter) / float64(len(union))
}

func failureSeverity(hits, required, firstRank int) float64 {
	if required <= 0 {
		required = 1
	}
	miss := float64(maxInt(0, required-hits))
	rankPenalty := 0.0
	if firstRank == 0 {
		rankPenalty = 1.0
	} else {
		rankPenalty = float64(firstRank) / 10.0
	}
	return miss + rankPenalty
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

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
