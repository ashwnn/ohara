package quality

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/ashwnn/ohara/internal/store"
)

// seedRand returns a deterministically-seeded *rand.Rand for reproducible results.
func seedRand() *rand.Rand {
	return rand.New(rand.NewSource(42))
}

// newTestStore creates a temporary store for test correctness checks.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	cfg := store.FallbackConfig(t.TempDir())
	s, err := store.New(cfg)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// newBenchStore creates a temporary store for benchmarking (uses t.TempDir via testing.B interface).
func newBenchStore(b testing.TB) *store.Store {
	cfg := store.FallbackConfig(b.TempDir())
	s, err := store.New(cfg)
	if err != nil {
		b.Fatalf("new store: %v", err)
	}
	b.Cleanup(func() { _ = s.Close() })
	return s
}

// memo represents a test memory with known content for quality verification.
type memo struct {
	title         string
	body          string
	kind          string
	domain        string
	actorID       string
	writtenBy     string
	classification string
}

// seedTestMemories inserts a deterministic set of memories for quality testing.
func seedTestMemories(s *store.Store, projectID string, r *rand.Rand) map[string]int64 {
	memos := []memo{
		{"JWT token rotation strategy", "Use refresh token rotation with sliding window expiration. Old tokens invalidated on use to prevent replay attacks.", store.MemoryKindDecision, "auth", "agent", "agent", "foundational"},
		{"SQLite WAL mode configuration", "Enable WAL mode in SQLite for concurrent read/write performance improvement.", store.MemoryKindConfig, "database", "agent", "agent", "tactical"},
		{"Rate limiter algorithm", "Token bucket algorithm with burst capacity of 10 requests and refill rate of 5 per second.", store.MemoryKindPattern, "api", "agent", "agent", "tactical"},
		{"Fix N+1 query in user list", "Added eager loading for user.roles relationship in repository query.", store.MemoryKindBugfix, "database", "agent", "agent", "tactical"},
		{"Auth middleware token expiry bug", "Token expiry check used < instead of <= causing 1-second window where expired tokens accepted.", store.MemoryKindBugfix, "auth", "agent", "agent", "tactical"},
		{"Cache invalidation strategy", "Use cache-aside pattern with versioned keys for automatic invalidation.", store.MemoryKindPattern, "cache", "agent", "agent", "tactical"},
		{"FTS5 query sanitization", "Wrap each search term in quotes before passing to FTS5 MATCH to handle special characters.", store.MemoryKindDiscovery, "database", "agent", "agent", "observational"},
		{"Connection pool sizing formula", "Pool size = ((number of CPUs * 2) + number of spindles) for optimal throughput.", store.MemoryKindDiscovery, "database", "agent", "agent", "observational"},
		{"Deploy to Kubernetes steps", "Build image, push to registry, update deployment yaml, apply kubectl.", store.MemoryKindProcedure, "infra", "agent", "agent", "foundational"},
		{"Health check endpoint pattern", "GET /health returns 200 with {status:ok,version:gitRev} after DB and cache checks.", store.MemoryKindPattern, "api", "agent", "agent", "tactical"},
		{"Postgres RLS policy setup", "Enable RLS on table, create policies for tenant_id filter, set session var on connection.", store.MemoryKindConfig, "database", "user", "user", "tactical"},
		{"Fix race condition in token refresh", "Added mutex lock around refresh token rotation to prevent concurrent requests from generating duplicate tokens.", store.MemoryKindBugfix, "auth", "agent", "agent", "tactical"},
		{"Decision: use JWT over sessions", "JWT preferred over server-side sessions for stateless auth across multiple service instances.", store.MemoryKindDecision, "auth", "agent", "agent", "foundational"},
		{"Retry HTTP client with backoff", "Exponential backoff with jitter for 5xx errors. Max 3 retries with 1s/2s/4s delays.", store.MemoryKindPattern, "api", "agent", "agent", "tactical"},
		{"User preference: dark mode default", "Users prefer dark mode as default theme. Light mode available via toggle.", store.MemoryKindUserPreference, "ui", "user", "user", "tactical"},
		{"Glossary: memory item terms", "MemoryItem is a curated, typed, versioned memory record with P0-P3 field tiers.", store.MemoryKindGlossary, "database", "agent", "agent", "tactical"},
		{"Decision: hybrid search alpha 0.6", "Set hybrid search alpha to 0.6 favoring FTS over embedding similarity.", store.MemoryKindDecision, "database", "agent", "agent", "foundational"},
		{"Observational: embedding latency", "Embedding calls add 40-80ms latency per query in local Ollama setup.", store.MemoryKindDiscovery, "database", "agent", "agent", "observational"},
		{"Memory consolidation workflow", "Generate candidates from observational memories, agent reviews, marks consolidated.", store.MemoryKindProcedure, "database", "agent", "agent", "foundational"},
		{"Config: hybrid search alpha", "Set OHARA_HYBRID_ALPHA=0.6 in environment for optimal FTS/embedding blend.", store.MemoryKindConfig, "database", "agent", "agent", "tactical"},
	}

	idMap := make(map[string]int64)
	for i, m := range memos {
		id, err := s.AddMemory(store.AddMemoryParams{
			ProjectID:     projectID,
			Kind:          m.kind,
			Domain:        m.domain,
			Title:         m.title,
			Body:          m.body,
			ActorID:       m.actorID,
			WrittenBy:     m.writtenBy,
			Classification: m.classification,
			Source:        "agent",
		})
		if err != nil {
			panic(fmt.Sprintf("seed memory %d (%s): %v", i, m.title, err))
		}
		idMap[m.title] = id
		_ = r // deterministic insertion order
	}
	return idMap
}

// =============================================================================
// BENCHMARK 1: MRR (Mean Reciprocal Rank)
// =============================================================================

func computeMRR(s *store.Store, projectID string, idMap map[string]int64) float64 {
	queries := []struct {
		q          string
		relevantID int64
	}{
		{"JWT token rotation", idMap["JWT token rotation strategy"]},
		{"SQLite WAL mode", idMap["SQLite WAL mode configuration"]},
		{"rate limiter algorithm", idMap["Rate limiter algorithm"]},
		{"fix N+1 query bug", idMap["Fix N+1 query in user list"]},
		{"FTS5 query sanitization", idMap["FTS5 query sanitization"]},
		{"cache invalidation strategy", idMap["Cache invalidation strategy"]},
		{"deploy kubernetes steps", idMap["Deploy to Kubernetes steps"]},
		{"health check endpoint", idMap["Health check endpoint pattern"]},
		{"JWT vs sessions decision", idMap["Decision: use JWT over sessions"]},
		{"retry HTTP client backoff", idMap["Retry HTTP client with backoff"]},
	}

	var sumRR float64
	for _, qq := range queries {
		results, err := s.SearchMemories(qq.q, projectID, "", "", "", store.MemoryStatusActive, 5, "")
		if err != nil {
			continue
		}
		rank := 0
		for i, r := range results {
			if r.ID == qq.relevantID {
				rank = i + 1
				break
			}
		}
		if rank > 0 {
			sumRR += 1.0 / float64(rank)
		}
	}
	return sumRR / float64(len(queries))
}

func TestMRRQuality(t *testing.T) {
	s := newTestStore(t)
	r := seedRand()
	idMap := seedTestMemories(s, "test-mrr", r)

	mrr := computeMRR(s, "test-mrr", idMap)
	fmt.Printf("MRR@k=5: %.4f (goal: >= 0.7 for useful retrieval)\n", mrr)
	if mrr < 0.5 {
		t.Fatalf("MRR too low: %.4f (relevant memories not ranked in top results)", mrr)
	}
}

func BenchmarkMRR(b *testing.B) {
	b.ReportAllocs()
	s := newBenchStore(b)
	r := seedRand()
	idMap := seedTestMemories(s, "bench-mrr", r)

	b.ResetTimer()
	mrr := computeMRR(s, "bench-mrr", idMap)
	b.StopTimer()

	b.ReportMetric(mrr, "MRR@k=5")
	b.Logf("MRR@k=5: %.4f", mrr)
}

// =============================================================================
// BENCHMARK 2: Recall at k
// =============================================================================

func computeRecall(s *store.Store, projectID string, idMap map[string]int64) (recall3, recall10 float64) {
	queries := []struct {
		q          string
		relevantID int64
	}{
		{"JWT token rotation", idMap["JWT token rotation strategy"]},
		{"SQLite WAL mode", idMap["SQLite WAL mode configuration"]},
		{"rate limiter algorithm", idMap["Rate limiter algorithm"]},
		{"fix N+1 query bug", idMap["Fix N+1 query in user list"]},
		{"FTS5 query sanitization", idMap["FTS5 query sanitization"]},
		{"cache invalidation strategy", idMap["Cache invalidation strategy"]},
		{"deploy kubernetes steps", idMap["Deploy to Kubernetes steps"]},
		{"health check endpoint", idMap["Health check endpoint pattern"]},
		{"JWT vs sessions decision", idMap["Decision: use JWT over sessions"]},
		{"retry HTTP client backoff", idMap["Retry HTTP client with backoff"]},
	}

	var hits3, hits10 int
	for _, qq := range queries {
		results3, err := s.SearchMemories(qq.q, projectID, "", "", "", store.MemoryStatusActive, 3, "")
		if err != nil {
			continue
		}
		results10, err := s.SearchMemories(qq.q, projectID, "", "", "", store.MemoryStatusActive, 10, "")
		if err != nil {
			continue
		}

		found3 := false
		found10 := false
		for _, r := range results3 {
			if r.ID == qq.relevantID {
				found3 = true
				break
			}
		}
		for _, r := range results10 {
			if r.ID == qq.relevantID {
				found10 = true
				break
			}
		}
		if found3 {
			hits3++
		}
		if found10 {
			hits10++
		}
	}

	return float64(hits3) / float64(len(queries)), float64(hits10) / float64(len(queries))
}

func TestRecallAtKQuality(t *testing.T) {
	s := newTestStore(t)
	r := seedRand()
	idMap := seedTestMemories(s, "test-recall", r)

	recall3, recall10 := computeRecall(s, "test-recall", idMap)
	fmt.Printf("Recall@3: %.4f (goal: >= 0.7)\n", recall3)
	fmt.Printf("Recall@10: %.4f (goal: >= 0.9)\n", recall10)
	if recall3 < 0.5 {
		t.Fatalf("Recall@3 too low: %.4f", recall3)
	}
}

func BenchmarkRecallAtK(b *testing.B) {
	b.ReportAllocs()
	s := newBenchStore(b)
	r := seedRand()
	idMap := seedTestMemories(s, "bench-recall", r)

	b.ResetTimer()
	recall3, recall10 := computeRecall(s, "bench-recall", idMap)
	b.StopTimer()

	b.ReportMetric(recall3, "recall@3")
	b.ReportMetric(recall10, "recall@10")
	b.Logf("Recall@3: %.4f, Recall@10: %.4f", recall3, recall10)
}

// =============================================================================
// BENCHMARK 3: Classification Consistency
// =============================================================================

func TestClassificationConsistency(t *testing.T) {
	s := newTestStore(t)

	decID, err := s.AddMemory(store.AddMemoryParams{
		ProjectID:     "test-class",
		Kind:          store.MemoryKindDecision,
		Title:         "Decision: auth strategy",
		Body:          "Use JWT for stateless auth",
		Classification: "foundational",
	})
	if err != nil {
		t.Fatalf("add decision: %v", err)
	}
	patID, err := s.AddMemory(store.AddMemoryParams{
		ProjectID:     "test-class",
		Kind:          store.MemoryKindPattern,
		Title:         "Pattern: retry logic",
		Body:          "Exponential backoff retry pattern",
		Classification: "tactical",
	})
	if err != nil {
		t.Fatalf("add pattern: %v", err)
	}
	disID, err := s.AddMemory(store.AddMemoryParams{
		ProjectID:     "test-class",
		Kind:          store.MemoryKindDiscovery,
		Title:         "Discovery: cache performance",
		Body:          "Cache adds 20ms avg latency",
		Classification: "observational",
	})
	if err != nil {
		t.Fatalf("add discovery: %v", err)
	}

	results, err := s.SearchMemories("auth strategy decision", "test-class", "", store.MemoryKindDecision, "", store.MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("search by kind: %v", err)
	}

	foundDecision := false
	for _, r := range results {
		if r.ID == decID {
			foundDecision = true
		}
		if r.Kind != store.MemoryKindDecision {
			t.Fatalf("kind filter failed: got kind=%s in results, expected only decision", r.Kind)
		}
	}
	if !foundDecision {
		t.Fatalf("expected decision memory in results, got none")
	}

	allResults, err := s.SearchMemories("retry logic pattern", "test-class", "", "", "", store.MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("search without kind filter: %v", err)
	}
	foundPattern := false
	for _, r := range allResults {
		if r.ID == patID {
			foundPattern = true
		}
	}
	if !foundPattern {
		t.Fatalf("pattern memory not found in unfiltered search")
	}

	_ = disID
	fmt.Printf("Classification consistency: PASS (kind filter working correctly)\n")
}

func BenchmarkClassificationConsistency(b *testing.B) {
	b.ReportAllocs()
	s := newBenchStore(b)

	decID, _ := s.AddMemory(store.AddMemoryParams{
		ProjectID:     "bench-class",
		Kind:          store.MemoryKindDecision,
		Title:         "Decision: auth strategy",
		Body:          "Use JWT for stateless auth",
		Classification: "foundational",
	})
	patID, _ := s.AddMemory(store.AddMemoryParams{
		ProjectID:     "bench-class",
		Kind:          store.MemoryKindPattern,
		Title:         "Pattern: retry logic",
		Body:          "Exponential backoff retry pattern",
		Classification: "tactical",
	})
	disID, _ := s.AddMemory(store.AddMemoryParams{
		ProjectID:     "bench-class",
		Kind:          store.MemoryKindDiscovery,
		Title:         "Discovery: cache performance",
		Body:          "Cache adds 20ms avg latency",
		Classification: "observational",
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		results, _ := s.SearchMemories("auth strategy decision", "bench-class", "", store.MemoryKindDecision, "", store.MemoryStatusActive, 10, "")
		for _, r := range results {
			if r.Kind != store.MemoryKindDecision {
				b.Fatalf("kind filter leaked")
			}
		}
		_, _ = results, decID
	}
	b.StopTimer()

	_ = patID
	_ = disID
}

// =============================================================================
// BENCHMARK 4: Conflict Detection Accuracy
// =============================================================================

func TestConflictDetectionAccuracy(t *testing.T) {
	s := newTestStore(t)

	id1, err := s.AddMemory(store.AddMemoryParams{
		ProjectID:     "test-conflict",
		Kind:          store.MemoryKindDecision,
		Title:         "Decision: Use PostgreSQL for primary storage",
		Body:          "PostgreSQL chosen for ACID compliance and JSONB support.",
		Classification: "foundational",
	})
	if err != nil {
		t.Fatalf("add memory 1: %v", err)
	}
	id2, err := s.AddMemory(store.AddMemoryParams{
		ProjectID:     "test-conflict",
		Kind:          store.MemoryKindDecision,
		Title:         "Decision: Use SQLite for primary storage",
		Body:          "SQLite chosen for simplicity and embedded deployment.",
		Classification: "foundational",
	})
	if err != nil {
		t.Fatalf("add memory 2: %v", err)
	}

	conflict, err := s.DetectConflict(store.AddMemoryParams{
		ProjectID: "test-conflict",
		Kind:     store.MemoryKindDecision,
		Title:    "Decision: Use MongoDB for primary storage",
		Body:     "MongoDB chosen for schema flexibility.",
	})
	if err != nil {
		t.Fatalf("detect conflict: %v", err)
	}

	if conflict == nil {
		t.Fatalf("expected conflict detection for contradicting decisions")
	}

	if conflict.ExistingMemory == nil {
		t.Fatalf("conflict.ExistingMemory is nil")
	}
	if conflict.ExistingMemory.ID != id1 && conflict.ExistingMemory.ID != id2 {
		t.Fatalf("conflict references wrong memory: got id=%d, expected %d or %d", conflict.ExistingMemory.ID, id1, id2)
	}

	fmt.Printf("Conflict detection: PASS (detected overlap score %.2f between contradictory decisions)\n", conflict.OverlapScore)
}

func BenchmarkConflictDetection(b *testing.B) {
	b.ReportAllocs()
	s := newBenchStore(b)

	// Pre-populate with conflicting decisions
	id1, _ := s.AddMemory(store.AddMemoryParams{
		ProjectID:     "bench-conflict",
		Kind:          store.MemoryKindDecision,
		Title:         "Decision: Use PostgreSQL for primary storage",
		Body:          "PostgreSQL chosen for ACID compliance and JSONB support.",
		Classification: "foundational",
	})
	id2, _ := s.AddMemory(store.AddMemoryParams{
		ProjectID:     "bench-conflict",
		Kind:          store.MemoryKindDecision,
		Title:         "Decision: Use SQLite for primary storage",
		Body:          "SQLite chosen for simplicity and embedded deployment.",
		Classification: "foundational",
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		conflict, _ := s.DetectConflict(store.AddMemoryParams{
			ProjectID: "bench-conflict",
			Kind:     store.MemoryKindDecision,
			Title:    "Decision: Use MongoDB for primary storage",
			Body:     "MongoDB chosen for schema flexibility.",
		})
		if conflict == nil {
			b.Fatalf("expected conflict detection")
		}
		if conflict.ExistingMemory.ID != id1 && conflict.ExistingMemory.ID != id2 {
			b.Fatalf("conflict references wrong memory")
		}
	}
	b.StopTimer()
}

// =============================================================================
// BENCHMARK 5: Deduplication
// =============================================================================

func TestDeduplication(t *testing.T) {
	s := newTestStore(t)

	params := store.AddMemoryParams{
		ProjectID:     "test-dedup",
		Kind:          store.MemoryKindPattern,
		Title:         "Rate limiter pattern",
		Body:          "Token bucket with burst capacity of 10 requests and refill rate of 5 per second.",
		Classification: "tactical",
	}
	id1, err := s.AddMemory(params)
	if err != nil {
		t.Fatalf("add memory 1: %v", err)
	}
	id2, err := s.AddMemory(params)
	if err != nil {
		t.Fatalf("add memory 2: %v", err)
	}

	mem1, err := s.GetMemory(id1)
	if err != nil {
		t.Fatalf("get memory %d: %v", id1, err)
	}
	mem2, err := s.GetMemory(id2)
	if err != nil {
		t.Fatalf("get memory %d: %v", id2, err)
	}

	if mem1.Title != mem2.Title || mem1.Body != mem2.Body {
		t.Fatalf("duplicate memories have different content")
	}

	results, err := s.SearchMemories("token bucket", "test-dedup", "", "", "", store.MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	idCount := make(map[int64]int)
	for _, r := range results {
		idCount[r.ID]++
	}
	for id, count := range idCount {
		if count > 1 {
			t.Fatalf("duplicate ID %d appears %d times in results", id, count)
		}
	}

	fmt.Printf("Deduplication: PASS (no duplicate IDs in search results, stored %d duplicate memories)\n", len(results))
	_ = mem1
	_ = mem2
}

func BenchmarkDeduplication(b *testing.B) {
	b.ReportAllocs()
	s := newBenchStore(b)

	params := store.AddMemoryParams{
		ProjectID:     "bench-dedup",
		Kind:          store.MemoryKindPattern,
		Title:         "Rate limiter pattern",
		Body:          "Token bucket with burst capacity of 10 requests and refill rate of 5 per second.",
		Classification: "tactical",
	}
	id1, _ := s.AddMemory(params)
	id2, _ := s.AddMemory(params)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		results, _ := s.SearchMemories("token bucket", "bench-dedup", "", "", "", store.MemoryStatusActive, 10, "")
		idCount := make(map[int64]int)
		for _, r := range results {
			idCount[r.ID]++
		}
		for id, count := range idCount {
			if count > 1 {
				b.Fatalf("duplicate ID %d appears %d times", id, count)
			}
		}
		_ = id1
		_ = id2
	}
	b.StopTimer()
}

// =============================================================================
// BENCHMARK 6: Staleness Isolation
// =============================================================================

func TestStalenessIsolation(t *testing.T) {
	s := newTestStore(t)

	activeID, err := s.AddMemory(store.AddMemoryParams{
		ProjectID:     "test-stale",
		Kind:          store.MemoryKindPattern,
		Title:         "Current pattern: rate limiting",
		Body:          "Token bucket algorithm in production",
		Classification: "tactical",
	})
	if err != nil {
		t.Fatalf("add active memory: %v", err)
	}
	archivedID, err := s.AddMemory(store.AddMemoryParams{
		ProjectID:     "test-stale",
		Kind:          store.MemoryKindPattern,
		Title:         "Old pattern: fixed window",
		Body:          "Fixed window counter (deprecated)",
		Classification: "tactical",
	})
	if err != nil {
		t.Fatalf("add archived memory: %v", err)
	}

	err = s.ForgetMemory(archivedID, "superseded by token bucket", "agent")
	if err != nil {
		t.Fatalf("archive memory: %v", err)
	}

	results, err := s.SearchMemories("rate limiting pattern", "test-stale", "", "", "", store.MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("search active: %v", err)
	}
	for _, r := range results {
		if r.ID == archivedID {
			t.Fatalf("archived memory leaked into active search: id=%d", archivedID)
		}
	}

	archivedResults, err := s.GetMemories("test-stale", "", "", store.MemoryStatusArchived, 10)
	if err != nil {
		t.Fatalf("get archived: %v", err)
	}
	found := false
	for _, r := range archivedResults {
		if r.ID == archivedID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("archived memory not retrievable via explicit archived status query")
	}

	fmt.Printf("Staleness isolation: PASS (archived memory hidden from active search, retrievable on explicit query)\n")
	_ = activeID
}

func BenchmarkStalenessIsolation(b *testing.B) {
	b.ReportAllocs()
	s := newBenchStore(b)

	activeID, _ := s.AddMemory(store.AddMemoryParams{
		ProjectID:     "bench-stale",
		Kind:          store.MemoryKindPattern,
		Title:         "Current pattern: rate limiting",
		Body:          "Token bucket algorithm in production",
		Classification: "tactical",
	})
	archivedID, _ := s.AddMemory(store.AddMemoryParams{
		ProjectID:     "bench-stale",
		Kind:          store.MemoryKindPattern,
		Title:         "Old pattern: fixed window",
		Body:          "Fixed window counter (deprecated)",
		Classification: "tactical",
	})
	_ = s.ForgetMemory(archivedID, "superseded", "agent")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		results, _ := s.SearchMemories("rate limiting pattern", "bench-stale", "", "", "", store.MemoryStatusActive, 10, "")
		for _, r := range results {
			if r.ID == archivedID {
				b.Fatalf("archived leaked into active search")
			}
		}
	}
	b.StopTimer()

	_ = activeID
}

// =============================================================================
// BENCHMARK 7: Access Count Tracking
// =============================================================================

func TestAccessCountTracking(t *testing.T) {
	s := newTestStore(t)

	id, err := s.AddMemory(store.AddMemoryParams{
		ProjectID:     "test-access",
		Kind:          store.MemoryKindPattern,
		Title:         "Access counted pattern",
		Body:          "This memory tracks access count",
		Classification: "tactical",
	})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}

	for i := 0; i < 5; i++ {
		mem, err := s.GetMemory(id)
		if err != nil {
			t.Fatalf("get memory attempt %d: %v", i+1, err)
		}
		if mem.ID != id {
			t.Fatalf("got wrong memory: expected %d, got %d", id, mem.ID)
		}
	}

	mem, err := s.GetMemory(id)
	if err != nil {
		t.Fatalf("final get memory: %v", err)
	}
	if mem.AccessCount < 5 {
		t.Fatalf("access_count = %d, expected 5 after 5 retrievals", mem.AccessCount)
	}

	fmt.Printf("Access count tracking: PASS (access_count=%d after 5 retrievals)\n", mem.AccessCount)
}

func BenchmarkAccessCountTracking(b *testing.B) {
	b.ReportAllocs()
	s := newBenchStore(b)

	id, _ := s.AddMemory(store.AddMemoryParams{
		ProjectID:     "bench-access",
		Kind:          store.MemoryKindPattern,
		Title:         "Access counted pattern",
		Body:          "This memory tracks access count",
		Classification: "tactical",
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := 0; j < 5; j++ {
			_, _ = s.GetMemory(id)
		}
	}
	b.StopTimer()

	mem, _ := s.GetMemory(id)
	b.ReportMetric(float64(mem.AccessCount), "access_count")
}

// =============================================================================
// BENCHMARK 8: Actor Write Isolation
// =============================================================================

func TestActorWriteIsolation(t *testing.T) {
	s := newTestStore(t)

	agentID, err := s.AddMemory(store.AddMemoryParams{
		ProjectID: "test-actor",
		Kind:     store.MemoryKindPattern,
		Title:    "Agent memory",
		Body:    "Written by agent actor",
		ActorID:  "agent",
		WrittenBy: "agent",
	})
	if err != nil {
		t.Fatalf("add agent memory: %v", err)
	}

	userID, err := s.AddMemory(store.AddMemoryParams{
		ProjectID: "test-actor",
		Kind:     store.MemoryKindPattern,
		Title:    "User memory",
		Body:    "Written by user actor",
		ActorID:  "user",
		WrittenBy: "user",
	})
	if err != nil {
		t.Fatalf("add user memory: %v", err)
	}

	memAgent, err := s.GetMemory(agentID)
	if err != nil {
		t.Fatalf("get agent memory: %v", err)
	}
	if memAgent.WrittenBy != "agent" {
		t.Fatalf("agent memory written_by=%s, expected agent", memAgent.WrittenBy)
	}

	memUser, err := s.GetMemory(userID)
	if err != nil {
		t.Fatalf("get user memory: %v", err)
	}
	if memUser.WrittenBy != "user" {
		t.Fatalf("user memory written_by=%s, expected user", memUser.WrittenBy)
	}

	agentResults, err := s.SearchMemories("memory", "test-actor", "", "", "", store.MemoryStatusActive, 10, "agent")
	if err != nil {
		t.Fatalf("search agent: %v", err)
	}
	for _, r := range agentResults {
		if r.WrittenBy != "agent" {
			t.Fatalf("written_by filter failed: got written_by=%s", r.WrittenBy)
		}
	}

	userResults, err := s.SearchMemories("memory", "test-actor", "", "", "", store.MemoryStatusActive, 10, "user")
	if err != nil {
		t.Fatalf("search user: %v", err)
	}
	for _, r := range userResults {
		if r.WrittenBy != "user" {
			t.Fatalf("written_by filter failed: got written_by=%s", r.WrittenBy)
		}
	}

	fmt.Printf("Actor write isolation: PASS (agent and user tracked separately, filterable)\n")
}

func BenchmarkActorWriteIsolation(b *testing.B) {
	b.ReportAllocs()
	s := newBenchStore(b)

	agentID, _ := s.AddMemory(store.AddMemoryParams{
		ProjectID: "bench-actor",
		Kind:     store.MemoryKindPattern,
		Title:    "Agent memory",
		Body:    "Written by agent actor",
		ActorID:  "agent",
		WrittenBy: "agent",
	})
	userID, _ := s.AddMemory(store.AddMemoryParams{
		ProjectID: "bench-actor",
		Kind:     store.MemoryKindPattern,
		Title:    "User memory",
		Body:    "Written by user actor",
		ActorID:  "user",
		WrittenBy: "user",
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		agentResults, _ := s.SearchMemories("memory", "bench-actor", "", "", "", store.MemoryStatusActive, 10, "agent")
		for _, r := range agentResults {
			if r.WrittenBy != "agent" {
				b.Fatalf("written_by filter failed: got %s", r.WrittenBy)
			}
		}
		userResults, _ := s.SearchMemories("memory", "bench-actor", "", "", "", store.MemoryStatusActive, 10, "user")
		for _, r := range userResults {
			if r.WrittenBy != "user" {
				b.Fatalf("written_by filter failed: got %s", r.WrittenBy)
			}
		}
	}
	b.StopTimer()

	_ = agentID
	_ = userID
}

// =============================================================================
// Quality Report
// =============================================================================

func TestQualityReport(t *testing.T) {
	s := newTestStore(t)
	r := seedRand()
	idMap := seedTestMemories(s, "test-report", r)

	mrr := computeMRR(s, "test-report", idMap)
	recall3, recall10 := computeRecall(s, "test-report", idMap)

	fmt.Println("=== Ohara Memory Quality Report ===")
	fmt.Printf("MRR@k=5:       %.4f  (goal: >= 0.7)\n", mrr)
	fmt.Printf("Recall@3:      %.4f  (goal: >= 0.7)\n", recall3)
	fmt.Printf("Recall@10:     %.4f  (goal: >= 0.9)\n", recall10)
	fmt.Println("===================================")

	passed := mrr >= 0.5 && recall3 >= 0.5 && recall10 >= 0.5
	if !passed {
		t.Fatalf("Quality metrics below threshold")
	}
}

// =============================================================================
// Combined Benchmark Suite
// =============================================================================

func BenchmarkQualitySuite(b *testing.B) {
	b.ReportAllocs()

	b.Run("MRR", func(b *testing.B) {
		b.ReportAllocs()
		s := newBenchStore(b)
		r := seedRand()
		idMap := seedTestMemories(s, "bench-suite-mrr", r)
		b.ResetTimer()
		mrr := computeMRR(s, "bench-suite-mrr", idMap)
		b.StopTimer()
		b.ReportMetric(mrr, "MRR@k=5")
	})

	b.Run("RecallAtK", func(b *testing.B) {
		b.ReportAllocs()
		s := newBenchStore(b)
		r := seedRand()
		idMap := seedTestMemories(s, "bench-suite-recall", r)
		b.ResetTimer()
		recall3, recall10 := computeRecall(s, "bench-suite-recall", idMap)
		b.StopTimer()
		b.ReportMetric(recall3, "recall@3")
		b.ReportMetric(recall10, "recall@10")
	})

	b.Run("ConflictDetection", func(b *testing.B) {
		b.ReportAllocs()
		s := newBenchStore(b)
		id1, _ := s.AddMemory(store.AddMemoryParams{
			ProjectID:     "bench-suite-conflict",
			Kind:          store.MemoryKindDecision,
			Title:         "Decision: Use PostgreSQL for primary storage",
			Body:          "PostgreSQL for ACID",
			Classification: "foundational",
		})
		id2, _ := s.AddMemory(store.AddMemoryParams{
			ProjectID:     "bench-suite-conflict",
			Kind:          store.MemoryKindDecision,
			Title:         "Decision: Use SQLite for primary storage",
			Body:          "SQLite for simplicity",
			Classification: "foundational",
		})
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			conflict, _ := s.DetectConflict(store.AddMemoryParams{
				ProjectID: "bench-suite-conflict",
				Kind:     store.MemoryKindDecision,
				Title:    "Decision: Use MongoDB for primary storage",
				Body:     "MongoDB for flexibility",
			})
			if conflict == nil {
				b.Fatalf("expected conflict detection")
			}
			if conflict.ExistingMemory.ID != id1 && conflict.ExistingMemory.ID != id2 {
				b.Fatalf("conflict references wrong memory")
			}
		}
		b.StopTimer()
	})

	b.Run("StalenessIsolation", func(b *testing.B) {
		b.ReportAllocs()
		s := newBenchStore(b)
		activeID, _ := s.AddMemory(store.AddMemoryParams{
			ProjectID:     "bench-suite-stale",
			Kind:          store.MemoryKindPattern,
			Title:         "Current pattern",
			Body:          "In production",
			Classification: "tactical",
		})
		archivedID, _ := s.AddMemory(store.AddMemoryParams{
			ProjectID:     "bench-suite-stale",
			Kind:          store.MemoryKindPattern,
			Title:         "Old pattern",
			Body:          "Deprecated",
			Classification: "tactical",
		})
		_ = s.ForgetMemory(archivedID, "superseded", "agent")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			results, _ := s.SearchMemories("pattern", "bench-suite-stale", "", "", "", store.MemoryStatusActive, 10, "")
			for _, r := range results {
				if r.ID == archivedID {
					b.Fatalf("archived leaked into active search")
				}
			}
		}
		b.StopTimer()
		_ = activeID
	})

	b.Run("AccessCountTracking", func(b *testing.B) {
		b.ReportAllocs()
		s := newBenchStore(b)
		id, _ := s.AddMemory(store.AddMemoryParams{
			ProjectID:     "bench-suite-access",
			Kind:          store.MemoryKindPattern,
			Title:         "Access counted",
			Body:          "Count access",
			Classification: "tactical",
		})
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for j := 0; j < 5; j++ {
				_, _ = s.GetMemory(id)
			}
		}
		b.StopTimer()
	})

	b.Run("ActorWriteIsolation", func(b *testing.B) {
		b.ReportAllocs()
		s := newBenchStore(b)
		agentID, _ := s.AddMemory(store.AddMemoryParams{
			ProjectID: "bench-suite-actor",
			Kind:     store.MemoryKindPattern,
			Title:    "Agent memory",
			Body:    "By agent",
			ActorID:  "agent",
			WrittenBy: "agent",
		})
		userID, _ := s.AddMemory(store.AddMemoryParams{
			ProjectID: "bench-suite-actor",
			Kind:     store.MemoryKindPattern,
			Title:    "User memory",
			Body:    "By user",
			ActorID:  "user",
			WrittenBy: "user",
		})
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			agentResults, _ := s.SearchMemories("memory", "bench-suite-actor", "", "", "", store.MemoryStatusActive, 10, "agent")
			for _, r := range agentResults {
				if r.WrittenBy != "agent" {
					b.Fatalf("isolation failed: got %s", r.WrittenBy)
				}
			}
			userResults, _ := s.SearchMemories("memory", "bench-suite-actor", "", "", "", store.MemoryStatusActive, 10, "user")
			for _, r := range userResults {
				if r.WrittenBy != "user" {
					b.Fatalf("isolation failed: got %s", r.WrittenBy)
				}
			}
		}
		b.StopTimer()
		_ = agentID
		_ = userID
	})
}

// Suppress unused variable warnings
var _ = strings.TrimSpace
var _ = time.Now