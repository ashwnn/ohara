// Run command for LongMemEval benchmark harness.
//
// Usage:
//
//	go run ./bench/cmd/run-longmemeval/ -k 5
//	go run ./bench/cmd/run-longmemeval/ -k 5 -json
//	go run ./bench/cmd/run-longmemeval/ -k 5 -judge -mode hybrid
//	go run ./bench/cmd/run-longmemeval/ -k 5 -enforce -skip-latency
//	go run ./bench/cmd/run-longmemeval/ -k 5 -dataset bench/longmemeval/data/longmemeval_s_cleaned.json
//	go run ./bench/cmd/run-longmemeval/ -questions-limit 50      (debug: first 50 questions only)
//	go run ./bench/cmd/run-longmemeval/ -k 5 -json 2>&1          (stderr has live progress)
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ashwnn/ohara/bench/longmemeval"
	longmemevaljudge "github.com/ashwnn/ohara/evals/longmemeval"
)

func main() {
	fixturePath := flag.String("fixture", filepath.Join("bench", "longmemeval", "fixture.json"), "path to LongMemEval fixture JSON")
	datasetPath := flag.String("dataset", "", "path to LongMemEval dataset (default: bench/longmemeval/data/longmemeval_s_cleaned.json when present)")
	k := flag.Int("k", 5, "top-k for retrieval")
	enforce := flag.Bool("enforce", true, "enforce threshold gates and exit non-zero on regression")
	skipLatency := flag.Bool("skip-latency", false, "skip latency SLO gates")
	jsonOut := flag.Bool("json", false, "pretty-print full report as JSON")
	useJudge := flag.Bool("judge", false, "enable overlap-based judge scoring")
	useOllamaJudge := flag.Bool("ollama-judge", false, "enable Ollama LLM-based judge scoring (offline bench only)")
	ollamaModel := flag.String("ollama-model", longmemevaljudge.DefaultJudgeModel, "Ollama model name for judge")
	ollamaURL := flag.String("ollama-url", longmemevaljudge.DefaultOllamaURL, "Ollama API URL")
	mode := flag.String("mode", "", "retrieval mode: fts5 (default) or hybrid")
	workers := flag.Int("workers", 0, "number of concurrent query workers (default: GOMAXPROCS)")
	questionsLimit := flag.Int("questions-limit", 0, "evaluate only first N questions (0 = all, for debug/diagnostic runs)")
	sweep := flag.Bool("sweep", false, "run across all supported modes and compare")
	flag.Parse()

	if *sweep {
		baseOpts := longmemeval.RunOptions{
			FixturePath:     *fixturePath,
			DatasetPath:     *datasetPath,
			K:               *k,
			Enforce:         false,
			SkipLatencyGate: true,
			QuestionsLimit:  *questionsLimit,
			Workers:         *workers,
		}
		results := longmemeval.RunLmeSweep(baseOpts, nil)
		fmt.Println("Ohara LongMemEval benchmark — sweep results")
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

	opts := longmemeval.RunOptions{
		FixturePath:     *fixturePath,
		DatasetPath:     *datasetPath,
		K:               *k,
		Enforce:         *enforce,
		SkipLatencyGate: *skipLatency,
		Mode:            strings.TrimSpace(*mode),
		QuestionsLimit:  *questionsLimit,
		Workers:         *workers,
	}
	if *useJudge {
		opts.Judge = longmemeval.OverlapJudge{}
	}
	if *useOllamaJudge {
		opts.Judge = longmemevaljudge.NewOllamaJudge(longmemevaljudge.OllamaJudgeConfig{
			URL:   *ollamaURL,
			Model: *ollamaModel,
		})
		fmt.Fprintf(os.Stderr, "Ollama judge: model=%s url=%s\n", *ollamaModel, *ollamaURL)
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
		label := judgeLabel(report, *useOllamaJudge, *ollamaModel)
		fmt.Printf("Judge: %s (mean score: %.3f)\n", label, report.JudgeMeanScore)
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

func judgeLabel(report longmemeval.Report, useOllama bool, ollamaModel string) string {
	if useOllama {
		return fmt.Sprintf("ollama/%s", ollamaModel)
	}
	if strings.Contains(strings.ToLower(report.FixtureDescription), "json array dataset") {
		return "containment"
	}
	return "overlap"
}
