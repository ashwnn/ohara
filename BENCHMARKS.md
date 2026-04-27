# Benchmarks

Run the full suite:

```bash
# Quality and correctness
go test ./bench/quality/...  -v -bench=. -benchtime=1s

# Store performance
go test ./bench/store/       -bench=. -benchmem -benchtime=1s

# Forgetting quality harness
go test ./bench/forgetting/ -v

# Precision@k
go run ./bench/precision/   -k 3

# Token counting
go test ./internal/token/   -bench=. -benchmem -benchtime=1s
```

Or use the runner script:

```bash
chmod +x bench/store/run.sh
./bench/store/run.sh
```

## Quality Benchmarks (`bench/quality/`)

Measures whether the memory system produces correct and useful results.

| Metric | Result | Goal |
|--------|--------|------|
| MRR@k=5 | **0.80** | ≥ 0.7 |
| Recall@3 | **0.80** | ≥ 0.7 |
| Recall@10 | **0.80** | ≥ 0.9 |
| Conflict detection | **PASS** | overlap score 0.67 |
| Staleness isolation | **PASS** | archived hidden from active |
| Access count tracking | **PASS** | correct after 5 retrievals |
| Actor write isolation | **PASS** | agent/user tracked separately |
| Temporal decay | **PASS** | newer memories rank higher |
| Stale auto-archive | **PASS** | 3 oldest archived correctly |
| Access frequency boost | **PASS** | high-access ranks higher |
| Deduplication | **PASS** | no duplicate IDs |
| Classification consistency | **PASS** | kind filter working |

## Performance Benchmarks (`bench/store/`)

Measures latency and throughput of core operations.

| Benchmark | Result | Conditions |
|-----------|--------|------------|
| Save throughput | ~1.28M ops/sec | 100 items, temp store |
| Search p50 latency | ~18ms | 100-item corpus |
| Search p95 latency | ~25ms | 100-item corpus |
| Context pack (200 token budget) | measurable | ns/op reported |
| DB size growth | tracked | bytes per memory |

## Forgetting Harness (`bench/forgetting/`)

Verifies that the forgetting system doesn't corrupt memory state.

- **Stale recall prevention**: archived memories never leak into active search
- **False forget prevention**: foundational memories survive expiry
- **Conflict survival**: relations survive through forget operations

## Precision Benchmark (`bench/precision/`)

Measures keyword-match precision@k on deterministic fixtures.

```
precision@3 = 0.2222 (22.2%) — 3 queries
```

Low precision here reflects FTS5 matching on short fixture titles (minimal text for the matcher to work with). Run with `-k 5` or `-k 10` for a softer metric.

## Token Benchmarks (`internal/token/`)

Measures token counting performance.

| Benchmark | Op/s | ns/op |
|-----------|------|-------|
| Count | ~31K | 31,796 |
| CountStrict | ~33K | 30,581 |
| WithinBudget | ~34K | 29,007 |

## Hardware

Results measured on: Intel Core i5-6500T @ 2.50GHz, Linux amd64, Go 1.26.2

For full test evidence and known limitations, see [Production Notes](docs/PRODUCTION_NOTES.md).
