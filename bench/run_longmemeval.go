// Run command for LongMemEval benchmark harness.
//
// Usage:
//
//	go run ./bench/run_longmemeval.go -k 5
//	go run ./bench/run_longmemeval.go -k 5 -json
//	go run ./bench/run_longmemeval.go -k 5 -judge -mode hybrid
//	go run ./bench/run_longmemeval.go -k 5 -enforce -skip-latency
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ashwnn/ohara/bench/longmemeval"
)

func main() {
	fixturePath := flag.String("fixture", filepath.Join("bench", "longmemeval", "fixture.json"), "path to LongMemEval fixture JSON")
	k := flag.Int("k", 5, "top-k for retrieval")
	enforce := flag.Bool("enforce", true, "enforce threshold gates and exit non-zero on regression")
	skipLatency := flag.Bool("skip-latency", false, "skip latency SLO gates")
	jsonOut := flag.Bool("json", false, "pretty-print full report as JSON")
	useJudge := flag.Bool("judge", false, "enable overlap-based judge scoring")
	mode := flag.String("mode", "", "retrieval mode: fts5 (default) or hybrid")
	flag.Parse()

	opts := longmemeval.RunOptions{
		FixturePath:     *fixturePath,
		K:               *k,
		Enforce:         *enforce,
		SkipLatencyGate: *skipLatency,
		Mode:            strings.TrimSpace(*mode),
	}
	if *useJudge {
		opts.Judge = longmemeval.OverlapJudge{}
	}

	report, err := longmemeval.RunBenchmark(opts)
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

	fmt.Println("Ohara LongMemEval benchmark")
	fmt.Println()
	fmt.Printf("Fixture: %s\n", report.FixtureDescription)
	fmt.Printf("Mode: %s\n", report.RetrievalMode)
	if report.JudgeEnabled {
		fmt.Printf("Judge: overlap (mean score: %.3f)\n", report.JudgeMeanScore)
	}
	fmt.Printf("Questions: %d\n", report.TotalQuestions)
	fmt.Printf("Passed: %d\n", report.PassedQuestions)
	fmt.Printf("Failed: %d\n", report.FailedQuestions)
	fmt.Printf("Runtime: %.0fms\n", report.RuntimeMs)

	fmt.Println("\nOverall metrics:")
	fmt.Printf("  Recall@1: %.3f\n", report.OverallMetrics.RecallAt1)
	fmt.Printf("  Recall@3: %.3f\n", report.OverallMetrics.RecallAt3)
	fmt.Printf("  Recall@5: %.3f\n", report.OverallMetrics.RecallAt5)
	fmt.Printf("  MRR: %.3f\n", report.OverallMetrics.MRR)
	fmt.Printf("  nDCG@5: %.3f\n", report.OverallMetrics.NDCGAt5)

	fmt.Println("\nDistance metrics (near→far):")
	for _, dist := range longmemeval.SortedDistanceKeys(report.DistanceMetrics) {
		m := report.DistanceMetrics[dist]
		fmt.Printf("  %-8s: recall@3=%-7.3f  recall@5=%-7.3f  mrr=%-7.3f  cases=%-2d passed=%-2d\n",
			dist, m.RecallAt3, m.RecallAt5, m.MRR, m.TotalCases, m.Passed)
	}

	fmt.Println("\nPer-category:")
	for _, cat := range longmemeval.SortedCategoryKeys(report.CategoryMetrics) {
		m := report.CategoryMetrics[cat]
		fmt.Printf("  %-22s: recall@3=%-7.3f  mrr=%-7.3f  cases=%-2d passed=%-2d\n",
			cat, m.RecallAt3, m.MRR, m.TotalCases, m.Passed)
	}

	fmt.Println("\nLatency (per-question):")
	fmt.Printf("  p50: %.1fms\n", report.Latency.P50Ms)
	fmt.Printf("  p95: %.1fms\n", report.Latency.P95Ms)
	fmt.Printf("  max: %.1fms\n", report.Latency.MaxMs)
	fmt.Printf("  mean: %.1fms\n", report.Latency.MeanMs)

	fmt.Println("\nThresholds:")
	fmt.Printf("  overall recall@3 SLO: >= %.2f\n", report.Thresholds.OverallRecallAt3)
	fmt.Printf("  near recall@3 SLO:    >= %.2f\n", report.Thresholds.NearRecallAt3)
	fmt.Printf("  medium recall@3 SLO:  >= %.2f\n", report.Thresholds.MediumRecallAt3)
	fmt.Printf("  far recall@3 SLO:     >= %.2f\n", report.Thresholds.FarRecallAt3)
	fmt.Printf("  MRR SLO:              >= %.2f\n", report.Thresholds.MRROverall)
	fmt.Printf("  latency p95 SLO:      <= %.0fms\n", report.Thresholds.LatencyP95MsMax)
	fmt.Printf("  latency max SLO:      <= %.0fms\n", report.Thresholds.LatencyMaxMsMax)

	fmt.Println("\nFailures:")
	if len(report.Failures) == 0 {
		fmt.Println("  none")
	} else {
		for _, f := range report.Failures {
			fmt.Printf("  - %s (%s | %s)\n", f.QuestionID, f.Distance, f.Category)
			fmt.Printf("    query:   %q\n", f.Query)
			fmt.Printf("    expect:  %v\n", f.ExpectedKeys)
			fmt.Printf("    actual:  %v\n", f.ActualKeys)
			fmt.Printf("    reason:  %s\n", f.Reason)
		}
	}

	fmt.Println()

	if err != nil {
		os.Exit(1)
	}
}
