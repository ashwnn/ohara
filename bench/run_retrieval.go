package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ashwnn/ohara/bench/retrieval"
)

func main() {
	fixturePath := flag.String("fixture", filepath.Join("bench", "fixtures", "retrieval_fixture.json"), "path to retrieval fixture JSON")
	k := flag.Int("k", 5, "top-k for reporting")
	enforce := flag.Bool("enforce", true, "enforce threshold gates and exit non-zero on regression")
	jsonOut := flag.Bool("json", false, "pretty-print full report as JSON")
	flag.Parse()

	report, err := retrieval.RunBenchmark(retrieval.RunOptions{
		FixturePath: *fixturePath,
		K:           *k,
		Enforce:     *enforce,
		Mode:        os.Getenv("OHARA_RETRIEVAL_MODE"),
		Embedding:   os.Getenv("OHARA_EMBEDDING_BACKEND"),
		OllamaURL:   os.Getenv("OHARA_OLLAMA_URL"),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "benchmark error: %v\n\n", err)
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if encodeErr := enc.Encode(report); encodeErr != nil {
			fmt.Fprintf(os.Stderr, "json encode error: %v\n", encodeErr)
			os.Exit(1)
		}
		if err != nil {
			os.Exit(1)
		}
		return
	}

	fmt.Println("Ohara retrieval benchmark")
	fmt.Println()
	fmt.Printf("Mode: %s\n", report.Mode)
	fmt.Printf("Embedding mode: %s\n", report.EmbeddingMode)
	fmt.Printf("Hybrid enabled: %t\n", report.HybridEnabled)
	fmt.Printf("Embeddings available: %t\n", report.EmbeddingsAvailable)
	fmt.Printf("Cases: %d\n", report.TotalCases)
	fmt.Printf("Passed: %d\n", report.PassedCases)
	fmt.Printf("Failed: %d\n", report.FailedCases)
	fmt.Printf("Runtime: %s\n", report.Runtime.Round(10e6))
	fmt.Println()
	fmt.Printf("Recall@1: %.3f\n", report.Metrics.RecallAt1)
	fmt.Printf("Recall@3: %.3f\n", report.Metrics.RecallAt3)
	fmt.Printf("Recall@5: %.3f\n", report.Metrics.RecallAt5)
	fmt.Printf("MRR: %.3f\n", report.Metrics.MRR)
	fmt.Printf("nDCG@5: %.3f\n", report.Metrics.NDCGAt5)
	fmt.Printf("Stale-hit rate: %.4f\n", report.Metrics.StaleHitRate)
	fmt.Printf("Wrong-project-hit rate: %.4f\n", report.Metrics.WrongProjectHitRate)
	fmt.Printf("Superseded-hit rate: %.4f\n", report.Metrics.SupersededHitRate)
	fmt.Printf("File-context accuracy: %.3f\n", report.Metrics.FileContextAccuracy)
	fmt.Printf("Graph-context accuracy: %.3f\n", report.Metrics.GraphContextAccuracy)
	fmt.Printf("Pack budget compliance: %.3f\n", report.Metrics.PackBudgetCompliance)
	fmt.Printf("Abstention false-positive rate: %.3f\n", report.Metrics.AbstentionFalsePos)
	fmt.Println()
	fmt.Println("Latency (per-case):")
	fmt.Printf("- p50: %.1fms\n", report.Latency.P50Ms)
	fmt.Printf("- p95: %.1fms\n", report.Latency.P95Ms)
	fmt.Printf("- max: %.1fms\n", report.Latency.MaxMs)
	fmt.Printf("- mean: %.1fms\n", report.Latency.MeanMs)
	fmt.Println()
	fmt.Println("Thresholds:")
	fmt.Printf("- latency p95 SLO: <= %.0fms\n", report.Thresholds.LatencyP95MsMax)
	fmt.Printf("- latency max SLO: <= %.0fms\n", report.Thresholds.LatencyMaxMsMax)
	fmt.Printf("- fixture weak-distractor rate SLO: <= %.3f\n", report.Thresholds.FixtureWeakDistractorRateMax)
	fmt.Printf("- fixture high-overlap rate SLO: <= %.3f\n", report.Thresholds.FixtureHighOverlapRateMax)

	fmt.Println("\nCase count by category:")
	for _, category := range sortedCategoryCaseKeys(report.CategoryCaseCounts) {
		fmt.Printf("- %s: %d\n", category, report.CategoryCaseCounts[category])
	}

	fmt.Println("\nPer-category:")
	for _, category := range sortedCategoryKeys(report.PerCategory) {
		m := report.PerCategory[category]
		fmt.Printf("- %s: recall@3=%.3f mrr=%.3f file_acc=%.3f graph_acc=%.3f pack_budget=%.3f abst_fp=%.3f\n",
			category, m.RecallAt3, m.MRR, m.FileContextAccuracy, m.GraphContextAccuracy, m.PackBudgetCompliance, m.AbstentionFalsePos)
	}

	fmt.Println("\nFixture audit:")
	fmt.Printf("- search cases analyzed: %d\n", report.FixtureAudit.SearchCaseCount)
	fmt.Printf("- avg expected-title overlap: %.3f\n", report.FixtureAudit.AverageTitleOverlap)
	fmt.Printf("- max expected-title overlap: %.3f\n", report.FixtureAudit.MaxTitleOverlap)
	fmt.Printf("- weak-distractor cases: %d (rate: %.3f)\n", report.FixtureAudit.WeakDistractorCount, report.FixtureAudit.WeakDistractorRate)
	fmt.Printf("- exact/happy-path cases: %d\n", report.FixtureAudit.HappyPathExactCount)
	fmt.Printf("- high-overlap cases: %d (rate: %.3f)\n", len(report.FixtureAudit.HighOverlapCaseIDs), report.FixtureAudit.HighOverlapRate)
	if len(report.FixtureAudit.CategoriesUnder5) == 0 {
		fmt.Println("- categories under 5 cases: none")
	} else {
		fmt.Printf("- categories under 5 cases: %s\n", strings.Join(report.FixtureAudit.CategoriesUnder5, ", "))
	}

	fmt.Println("\nWorst failures:")
	if len(report.Failures) == 0 {
		fmt.Println("- none")
	} else {
		limit := 10
		if len(report.Failures) < limit {
			limit = len(report.Failures)
		}
		for i := 0; i < limit; i++ {
			f := report.Failures[i]
			fmt.Printf("- case_id: %s\n", f.CaseID)
			fmt.Printf("  category: %s\n", f.Category)
			fmt.Printf("  query_or_path: %q\n", f.QueryOrPath)
			fmt.Printf("  expected: %v\n", f.ExpectedIDs)
			fmt.Printf("  actual: %v\n", f.ActualTopK)
			fmt.Printf("  issue: %s\n", f.Reason)
			fmt.Printf("  source: %s\n", f.Source)
			fmt.Printf("  flags: stale=%t wrong_project=%t superseded=%t\n", f.HasStale, f.HasWrongPrj, f.HasSupersed)
		}
	}

	if err != nil {
		os.Exit(1)
	}
}

func sortedCategoryKeys(input map[string]retrieval.Metrics) []string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}

func sortedCategoryCaseKeys(input map[string]int) []string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}
