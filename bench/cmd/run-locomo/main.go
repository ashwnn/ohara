// Run command for LoCoMo benchmark harness.
//
// Usage:
//
//	go run ./bench/cmd/run-locomo/ -k 5
//	go run ./bench/cmd/run-locomo/ -k 5 -json
//	go run ./bench/cmd/run-locomo/ -k 5 -mode hybrid
//	go run ./bench/cmd/run-locomo/ -k 5 -enforce -skip-latency
//	go run ./bench/cmd/run-locomo/ -k 5 -sweep
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ashwnn/ohara/bench/locomo"
)

func main() {
	fixturePath := flag.String("fixture", filepath.Join("bench", "locomo", "fixture.json"), "path to LoCoMo fixture JSON")
	k := flag.Int("k", 5, "top-k for retrieval")
	enforce := flag.Bool("enforce", false, "enforce threshold gates and exit non-zero on regression")
	skipLatency := flag.Bool("skip-latency", false, "skip latency SLO gates")
	jsonOut := flag.Bool("json", false, "pretty-print full report as JSON")
	mode := flag.String("mode", "", "retrieval mode: fts5 (default) or hybrid")
	workers := flag.Int("workers", 0, "number of concurrent query workers (default: 4)")
	questionsLimit := flag.Int("questions-limit", 0, "evaluate only first N questions (0 = all)")
	sweep := flag.Bool("sweep", false, "run across all supported modes and compare")
	flag.Parse()

	if *sweep {
		baseOpts := locomo.RunOptions{
			FixturePath:    *fixturePath,
			K:              *k,
			Enforce:        false,
			SkipLatency:    true,
			QuestionsLimit: *questionsLimit,
			Workers:        *workers,
		}
		results := locomo.RunSweep(baseOpts, nil)
		fmt.Println("Ohara LoCoMo benchmark — sweep results")
		fmt.Println()
		fmt.Printf("%-28s %-10s %-10s %-10s %-10s %-10s %-10s\n",
			"Mode", "Recall@1", "Recall@3", "MRR", "nDCG@5", "P95(ms)", "Passed/Total")
		fmt.Println(strings.Repeat("-", 100))
		for _, r := range results {
			errTag := ""
			if r.Error != "" {
				errTag = " ERROR"
			}
			fmt.Printf("%-28s %-10.3f %-10.3f %-10.3f %-10.3f %-10.1f %d/%-10d%s\n",
				r.Name,
				r.Report.OverallMetrics.RecallAt1,
				r.Report.OverallMetrics.RecallAt3,
				r.Report.OverallMetrics.MRR,
				r.Report.OverallMetrics.NDCGAt5,
				r.Report.Latency.P95Ms,
				r.Report.PassedQuestions, r.Report.TotalQuestions,
				errTag,
			)
		}
		if *jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(results)
		}
		return
	}

	opts := locomo.RunOptions{
		FixturePath:    *fixturePath,
		K:              *k,
		Enforce:        *enforce,
		SkipLatency:    *skipLatency,
		Mode:           strings.TrimSpace(*mode),
		QuestionsLimit: *questionsLimit,
		Workers:        *workers,
	}

	report, err := locomo.RunBenchmark(opts)
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

	fmt.Println("Ohara LoCoMo benchmark")
	fmt.Println()
	fmt.Printf("Fixture: %s\n", report.FixtureDescription)
	fmt.Printf("Mode: %s\n", report.RetrievalMode)
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

	fmt.Println("\nPer-category:")
	for _, cat := range locomo.SortedCategoryKeys(report.CategoryMetrics) {
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
	fmt.Printf("  MRR SLO:              >= %.2f\n", report.Thresholds.MRROverall)
	fmt.Printf("  latency p95 SLO:      <= %.0fms\n", report.Thresholds.LatencyP95MsMax)
	fmt.Printf("  latency max SLO:      <= %.0fms\n", report.Thresholds.LatencyMaxMsMax)

	fmt.Println("\nFailures:")
	if len(report.Failures) == 0 {
		fmt.Println("  none")
	} else {
		for _, f := range report.Failures {
			fmt.Printf("  - %s (%s)\n", f.QuestionID, f.Category)
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
