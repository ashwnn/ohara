# Ohara Store Benchmark Suite

Real-world performance benchmarks for the Ohara memory store. These measure actual SQLite/FTS5 operations with realistic data, not synthetic microbenchmarks.

## Running the Benchmarks

```bash
go test ./bench/store/ -bench=. -benchmem -benchtime=3s
```

Or use the wrapper script:

```bash
./bench/store/run.sh
```

## Benchmark Definitions

### 1. Save Throughput (`BenchmarkSaveThroughput*`)

**What it measures**: Raw insert performance — how fast the store can persist new memory items.

**How it works**: Inserts N memories (100, 1K, 10K) with deterministic but realistic titles/bodies and measures operations/second.

**Realistic data**:
- Title: 30-80 chars, uses real programming terms (auth, database, API, cache, etc.)
- Body: 100-500 chars, actual bugfix/decision/procedure content
- Mix of all 8 memory kinds
- Varied tag sets (2-3 tags per memory)

**Interpretation**:
- Higher `ops/sec` = better
- `ns/op` should decrease as batch size grows (SQLite WAL advantages kick in)
- If `ns/op` increases dramatically at 10K, indicates write amplification or WAL checkpoint overhead

### 2. Search Latency (`BenchmarkSearchLatency*`)

**What it measures**: FTS5 search latency at different DB population levels.

**How it works**:
1. Seed DB with N memories (100, 1K, 10K)
2. Run 10 diverse queries × 10 iterations each (100 total measurements per size)
3. Compute p50, p95, p99 across all measurements

**Realistic queries** (multi-word, varied specificity):
- `token refresh race`
- `sqlite wal mode`
- `retry backoff exponential`
- `connection pool memory leak`
- `index query performance`
- `RLS policy multi tenant`
- `cache invalidation strategy`
- `error handling context`
- `health check endpoint`
- `FTS5 search optimization`

**Interpretation**:
- p50 < 10ms: excellent, interactive feel
- p50 10-50ms: acceptable for background tasks
- p95 < 100ms: good for non-interactive use
- p99 > 200ms: indicates FTS table or query issues
- Latency growth from 1K→10K should be sub-linear (FTS5 B-trees scale well)

### 3. Context Pack Assembly (`BenchmarkBuildPack*`)

**What it measures**: Time to assemble a prime/context pack at different token budgets.

**How it works**: Builds packs at 200, 400, 800 token budgets (common LLM context window sizes) with 500 memories in DB.

**What the pack includes**:
- Global items (identity, preferences, glossary) — always fetched
- Project items filtered by domain/asof
- Token counting and truncation logic
- Relevance scoring and ranking

**Interpretation**:
- < 5ms per pack: excellent, can build on every request
- 5-20ms: acceptable, cache packs when possible
- > 50ms: concerning — check GetMemories performance and token counting overhead
- Larger budgets should scale linearly (+/- truncation variance)

### 4. DB Size Growth (`BenchmarkDBSizeGrowth`)

**What it measures**: Bytes per memory item as the DB grows.

**How it works**: Creates fresh stores at 100, 1K, 10K population, closes DB (flushes to disk), measures file size.

**Interpretation**:
- ~2-5 KB per memory item is typical (row overhead + FTS index entries)
- If bytes/memory grows with scale, indicates fragmentation or index imbalance
- WAL file not included (temporary — depends on write pattern)
- Total DB size = `(bytes_per_mem × count) + fixed overhead`

## Expected Results (approximate)

| Benchmark | 100 | 1K | 10K |
|-----------|-----|----|----|
| Save throughput | ~5K ops/s | ~7K ops/s | ~8K ops/s |
| Search p50 | <1ms | <2ms | <5ms |
| Search p99 | <5ms | <10ms | <30ms |
| Pack build | <3ms | <5ms | <8ms |
| Bytes/memory | ~3KB | ~2.5KB | ~2KB |

These vary by hardware. Run with `-benchtime=3s` for reliable numbers.

## Output Format

```
BenchmarkSaveThroughput100        100      118432 ns/op    0.52 MB/s    3248 B/op    42 allocs/op
BenchmarkSaveThroughput1K        1000      142847 ns/op    0.71 MB/s    2891 B/op    38 allocs/op
BenchmarkSaveThroughput10K      10000      156234 ns/op    0.64 MB/s    2804 B/op    35 allocs/op
BenchmarkSearchLatency100        100       48231 ns/op    0.00 MB/s     128 B/op     2 allocs/op
...
```

Key columns:
- `ns/op`: nanoseconds per operation (lower = faster)
- `MB/s`: throughput (higher = better for bulk ops)
- `B/op`: bytes allocated per op (memory efficiency)
- `allocs/op`: heap allocations per op (GC pressure)

## Interpreting Benchmark Changes

When you modify the store:

1. **Save throughput drops**: Could be schema migration overhead, extra triggers, or WAL checkpoint thrashing
2. **Search latency spikes at 10K**: FTS table bloat, missing index, or query plan regression
3. **Pack build slows**: Changes to GetMemories, token counting, or filtering logic
4. **Bytes/memory increases**: New columns without backfill, extra indexes, or fragmentation

Always compare against a baseline before accepting performance changes.