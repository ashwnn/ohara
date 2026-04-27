package store

import (
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/ashwnn/ohara/internal/store"
)

// seedRand returns a deterministically-seeded *rand.Rand for reproducible test data.
func seedRand() *rand.Rand {
	return rand.New(rand.NewSource(42))
}

// realisticTitle generates a title with realistic length variation (30-80 chars).
func realisticTitle(r *rand.Rand, idx int) string {
	prefixes := []string{"Fix", "Add", "Update", "Refactor", "Remove", "Implement", "Document", "Optimize", "Handle", "Configure"}
	domains := []string{"auth", "database", "api", "cache", "config", "logging", "middleware", "storage", "network", "security"}
	nouns := []string{"JWT token", "connection pool", "retry logic", "error handling", "cache invalidation", "query builder", "session management", "rate limiter", "config parser", "health check"}
	mid := []string{"in", "for", "with", "using", "during", "for", "at"}

	p := prefixes[r.Intn(len(prefixes))]
	d := domains[r.Intn(len(domains))]
	n := nouns[r.Intn(len(nouns))]
	m := mid[r.Intn(len(mid))]
	return p + " " + n + " " + m + " " + d
}

// realisticBody generates a body with realistic length variation (100-500 chars).
func realisticBody(r *rand.Rand, idx int) string {
	sentences := []string{
		"Added mutex around refresh token rotation to prevent race conditions in concurrent requests.",
		"Enabled WAL mode for SQLite connections to improve write throughput under heavy load.",
		"Implemented exponential backoff with jitter for transient 5xx errors in the HTTP client.",
		"Fixed memory leak in connection pool by properly releasing resources on timeout.",
		"Added index on frequently queried columns to reduce query time from O(n) to O(log n).",
		"Configured RLS policies for multi-tenant tables to enforce row-level security.",
		"Updated caching strategy to use write-through for critical data paths.",
		"Refactored error handling to propagate context through the call stack properly.",
		"Added health check endpoint that validates database connectivity and external service reachability.",
		"Optimized FTS5 search by adding tokenize='porter unicode61' for better stemming.",
	}
	// Pick 2-4 sentences based on index to vary body length
	numSentences := 2 + (idx % 3)
	result := sentences[idx%len(sentences)]
	for i := 1; i < numSentences; i++ {
		result += " " + sentences[(idx+i)%len(sentences)]
	}
	return result
}

// kinds is a shuffled slice of memory kinds for varied insertion.
var kinds = []string{
	store.MemoryKindBugfix,
	store.MemoryKindDecision,
	store.MemoryKindPattern,
	store.MemoryKindDiscovery,
	store.MemoryKindProcedure,
	store.MemoryKindConfig,
	store.MemoryKindPostmortem,
}

// tagSets provides realistic tag combinations.
var tagSets = [][]string{
	{"auth", "security"},
	{"database", "sqlite"},
	{"api", "rest"},
	{"performance", "optimization"},
	{"bug", "urgent"},
	{"refactor", "maintainability"},
	{"config", "deployment"},
	{"security", "auth"},
	{"database", "performance"},
	{"api", "documentation"},
}

// newBenchStore creates a temporary store for benchmarking.
func newBenchStore(t *testing.T) *store.Store {
	t.Helper()
	cfg := store.FallbackConfig(t.TempDir())
	s, err := store.New(cfg)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// newBenchStoreWithDir creates a store with a specific temp directory for size benchmarks
// where we need to control the path.
func newBenchStoreWithDir(t *testing.T, dir string) *store.Store {
	t.Helper()
	cfg := store.FallbackConfig(dir)
	s, err := store.New(cfg)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// ---------------------------------------------------------------------------
// Benchmark: Save Throughput
// Varies N: 100, 1000, 10000 memories inserted and measures ops/sec.
// ---------------------------------------------------------------------------

func BenchmarkSaveThroughput100(b *testing.B) {
	cfg := store.FallbackConfig(b.TempDir())
	s, err := store.New(cfg)
	if err != nil {
		b.Fatalf("new store: %v", err)
	}
	defer s.Close()
	r := seedRand()
	b.ResetTimer()
	b.ReportAllocs()
	b.StopTimer()
	for i := 0; i < 100; i++ {
		kind := kinds[i%len(kinds)]
		title := realisticTitle(r, i)
		body := realisticBody(r, i)
		tags := tagSets[i%len(tagSets)]
		_, err := s.AddMemory(store.AddMemoryParams{
			ProjectID:    "bench",
			Kind:        kind,
			Title:       title,
			Body:        body,
			Tags:        tags,
			Source:      "benchmark",
			ActorID:     "bench",
			Classification: "tactical",
		})
		if err != nil {
			b.Fatalf("add memory: %v", err)
		}
	}
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		idx := i % 100
		kind := kinds[idx%len(kinds)]
		title := realisticTitle(r, idx)
		body := realisticBody(r, idx)
		tags := tagSets[idx%len(tagSets)]
		_, err := s.AddMemory(store.AddMemoryParams{
			ProjectID:    "bench",
			Kind:        kind,
			Title:       title,
			Body:        body,
			Tags:        tags,
			Source:      "benchmark",
			ActorID:     "bench",
			Classification: "tactical",
		})
		if err != nil {
			b.Fatalf("add memory: %v", err)
		}
	}
}

func BenchmarkSaveThroughput1K(b *testing.B) {
	cfg := store.FallbackConfig(b.TempDir())
	s, err := store.New(cfg)
	if err != nil {
		b.Fatalf("new store: %v", err)
	}
	defer s.Close()

	r := seedRand()
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < 1000; i++ {
		kind := kinds[i%len(kinds)]
		title := realisticTitle(r, i)
		body := realisticBody(r, i)
		tags := tagSets[i%len(tagSets)]
		_, err := s.AddMemory(store.AddMemoryParams{
			ProjectID:    "bench",
			Kind:        kind,
			Title:       title,
			Body:        body,
			Tags:        tags,
			Source:      "benchmark",
			ActorID:     "bench",
			Classification: "tactical",
		})
		if err != nil {
			b.Fatalf("add memory: %v", err)
		}
	}
}

func BenchmarkSaveThroughput10K(b *testing.B) {
	cfg := store.FallbackConfig(b.TempDir())
	s, err := store.New(cfg)
	if err != nil {
		b.Fatalf("new store: %v", err)
	}
	defer s.Close()

	r := seedRand()
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < 10000; i++ {
		kind := kinds[i%len(kinds)]
		title := realisticTitle(r, i)
		body := realisticBody(r, i)
		tags := tagSets[i%len(tagSets)]
		_, err := s.AddMemory(store.AddMemoryParams{
			ProjectID:    "bench",
			Kind:        kind,
			Title:       title,
			Body:        body,
			Tags:        tags,
			Source:      "benchmark",
			ActorID:     "bench",
			Classification: "tactical",
		})
		if err != nil {
			b.Fatalf("add memory: %v", err)
		}
	}
}

// ---------------------------------------------------------------------------
// Benchmark: Search Latency with varying DB sizes
// Seeds DB with 100, 1K, 10K memories then measures p50/p95/p99 latency.
// ---------------------------------------------------------------------------

// seedMemories seeds the store with n memories using deterministic data.
func seedMemories(s *store.Store, n int) {
	r := seedRand()
	for i := 0; i < n; i++ {
		kind := kinds[i%len(kinds)]
		title := realisticTitle(r, i)
		body := realisticBody(r, i)
		tags := tagSets[i%len(tagSets)]
		_, _ = s.AddMemory(store.AddMemoryParams{
			ProjectID:    "bench",
			Kind:        kind,
			Title:       title,
			Body:        body,
			Tags:        tags,
			Source:      "benchmark",
			ActorID:     "bench",
			Classification: "tactical",
		})
	}
}

// percentile computes the p-th percentile of a slice of durations.
func percentile(durations []time.Duration, p float64) time.Duration {
	sorted := make([]time.Duration, len(durations))
	copy(sorted, durations)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(float64(len(sorted)-1) * p / 100.0)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	if idx < 0 {
		idx = 0
	}
	return sorted[idx]
}

func measureSearchLatencies(b *testing.B, s *store.Store, queries []string, iters int) (p50, p95, p99 time.Duration) {
	// Use a fixed set of queries that are representative of real searches
	var allDurations []time.Duration
	for _, q := range queries {
		for i := 0; i < iters; i++ {
			start := time.Now()
			_, err := s.SearchMemories(q, "bench", "", "", "", store.MemoryStatusActive, 10, "")
			elapsed := time.Since(start)
			if err != nil {
				b.Fatalf("search: %v", err)
			}
			allDurations = append(allDurations, elapsed)
		}
	}
	// Compute percentiles across all measurements
	p50 = percentile(allDurations, 50)
	p95 = percentile(allDurations, 95)
	p99 = percentile(allDurations, 99)
	return
}

// searchQueries are diverse, realistic search queries.
var searchQueries = []string{
	"token refresh race",
	"sqlite wal mode",
	"retry backoff exponential",
	"connection pool memory leak",
	"index query performance",
	"RLS policy multi tenant",
	"cache invalidation strategy",
	"error handling context",
	"health check endpoint",
	"FTS5 search optimization",
}

func BenchmarkSearchLatency100(b *testing.B) {
	cfg := store.FallbackConfig(b.TempDir())
	s, err := store.New(cfg)
	if err != nil {
		b.Fatalf("new store: %v", err)
	}
	defer s.Close()

	seedMemories(s, 100)

	b.ResetTimer()
	b.ReportAllocs()

	p50, p95, p99 := measureSearchLatencies(b, s, searchQueries, 10)
	b.Logf("p50=%v p95=%v p99=%v", p50, p95, p99)
}

func BenchmarkSearchLatency1K(b *testing.B) {
	cfg := store.FallbackConfig(b.TempDir())
	s, err := store.New(cfg)
	if err != nil {
		b.Fatalf("new store: %v", err)
	}
	defer s.Close()

	seedMemories(s, 1000)

	b.ResetTimer()
	b.ReportAllocs()

	p50, p95, p99 := measureSearchLatencies(b, s, searchQueries, 10)
	b.Logf("p50=%v p95=%v p99=%v", p50, p95, p99)
}

func BenchmarkSearchLatency10K(b *testing.B) {
	cfg := store.FallbackConfig(b.TempDir())
	s, err := store.New(cfg)
	if err != nil {
		b.Fatalf("new store: %v", err)
	}
	defer s.Close()

	seedMemories(s, 10000)

	b.ResetTimer()
	b.ReportAllocs()

	p50, p95, p99 := measureSearchLatencies(b, s, searchQueries, 10)
	b.Logf("p50=%v p95=%v p99=%v", p50, p95, p99)
}

// ---------------------------------------------------------------------------
// Benchmark: Context Pack Assembly
// Measures time to build a prime/context pack with varying token budgets.
// ---------------------------------------------------------------------------

func BenchmarkBuildPack200(b *testing.B) {
	cfg := store.FallbackConfig(b.TempDir())
	s, err := store.New(cfg)
	if err != nil {
		b.Fatalf("new store: %v", err)
	}
	defer s.Close()

	// Seed with realistic data
	seedMemories(s, 500)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := s.BuildPack(store.PackParams{
			ProjectID:    "bench",
			BudgetTokens: 200,
		})
		if err != nil {
			b.Fatalf("build pack: %v", err)
		}
	}
}

func BenchmarkBuildPack400(b *testing.B) {
	cfg := store.FallbackConfig(b.TempDir())
	s, err := store.New(cfg)
	if err != nil {
		b.Fatalf("new store: %v", err)
	}
	defer s.Close()

	seedMemories(s, 500)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := s.BuildPack(store.PackParams{
			ProjectID:    "bench",
			BudgetTokens: 400,
		})
		if err != nil {
			b.Fatalf("build pack: %v", err)
		}
	}
}

func BenchmarkBuildPack800(b *testing.B) {
	cfg := store.FallbackConfig(b.TempDir())
	s, err := store.New(cfg)
	if err != nil {
		b.Fatalf("new store: %v", err)
	}
	defer s.Close()

	seedMemories(s, 500)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := s.BuildPack(store.PackParams{
			ProjectID:    "bench",
			BudgetTokens: 800,
		})
		if err != nil {
			b.Fatalf("build pack: %v", err)
		}
	}
}

// ---------------------------------------------------------------------------
// Benchmark: DB Size (bytes per memory as count grows)
// Measures DB file size vs memory count to track storage efficiency.
// ---------------------------------------------------------------------------

func BenchmarkDBSizeGrowth(b *testing.B) {
	// Test at 3 population points: 100, 1000, 10000
	sizes := []int{100, 1000, 10000}
	memCounts := make([]int, 0, len(sizes))
	dbSizes := make([]int64, 0, len(sizes))

	for _, n := range sizes {
		tmp, err := os.MkdirTemp("", "ohara-size-bench-*")
		if err != nil {
			b.Fatalf("mk temp dir: %v", err)
		}
		defer os.RemoveAll(tmp)

		cfg := store.FallbackConfig(tmp)
		s, err := store.New(cfg)
		if err != nil {
			b.Fatalf("new store: %v", err)
		}

		seedMemories(s, n)

		// Close store so DB file is flushed to disk
		_ = s.Close()

		// Get DB file size
		dbPath := filepath.Join(tmp, "ohara.db")
		info, err := os.Stat(dbPath)
		if err != nil {
			b.Fatalf("stat db file: %v", err)
		}

		memCounts = append(memCounts, n)
		dbSizes = append(dbSizes, info.Size())

		b.Logf("n=%d db_size=%d bytes_per_mem=%.2f", n, info.Size(), float64(info.Size())/float64(n))
	}

	// Report ratios for comparison
	for i := range sizes {
		b.Logf("size_point n=%d bytes_per_mem=%.2f", memCounts[i], float64(dbSizes[i])/float64(memCounts[i]))
	}
}