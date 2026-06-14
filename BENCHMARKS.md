# Benchmarks

Hardware: Intel Core i5-6500T @ 2.50GHz, Linux amd64, Go 1.26.2.

## Quality (bench/quality/)

| Metric | Result | Goal |
|--------|--------|------|
| MRR@k=5 | 0.80 | ≥ 0.7 |
| Recall@3 | 0.80 | ≥ 0.7 |
| Recall@10 | 0.80 | ≥ 0.9 |
| Conflict detection | PASS | overlap score 0.67 |
| Staleness isolation | PASS | archived hidden from active |
| Access count tracking | PASS | correct after 5 retrievals |
| Actor write isolation | PASS | agent/user tracked separately |
| Temporal decay | PASS | newer memories rank higher |
| Stale auto-archive | PASS | 3 oldest archived correctly |
| Access frequency boost | PASS | high-access ranks higher |
| Deduplication | PASS | no duplicate IDs |
| Classification consistency | PASS | kind filter working |

## Store Performance (bench/store/)

| Benchmark | Result | Conditions |
|-----------|--------|------------|
| Save throughput | ~1.28M ops/sec | 100 items, temp store |
| Search p50 latency | ~18ms | 100-item corpus |
| Search p95 latency | ~25ms | 100-item corpus |
| Pack build (200 token budget) | <3ms | — |
| DB size per memory item | ~2-3KB | — |

## Retrieval Fixture Harness (bench/retrieval/)

Deterministic fixture-driven recall, MRR, nDCG, stale/superseded filtering, file context, graph context, pack budgeting, abstention false-positive rate.

Latest run (FTS5 mode, 70 cases): Recall@3=0.966, MRR=0.900, nDCG@5=0.914, file-context accuracy=1.000, graph-context accuracy=1.000, pack budget compliance=1.000, abstention FP=0.000, p95 latency=35.5ms.

## Forgetting Harness (bench/forgetting/)

Verifies the forgetting system doesn't corrupt memory state: stale recall prevention, false forget prevention (foundational memories survive expiry), conflict survival through forget operations.

## Precision (bench/precision/)

Keyword-match precision@k on deterministic fixtures: precision@3 = 0.2222 (22.2%) — 3 queries. Low precision reflects FTS5 matching on short fixture titles.

## Token Counting (internal/token/)

| Benchmark | Op/s | ns/op |
|-----------|------|-------|
| Count | ~31K | 31,796 |
| CountStrict | ~33K | 30,581 |
| WithinBudget | ~34K | 29,007 |

## LongMemEval Harness (bench/longmemeval/)

LongMemEval-style session-distance recall benchmark. Seeds 20 facts across 5 sessions and evaluates retrieval quality at near (1 session), medium (2-3 sessions), and far (4-5 sessions) distances using FTS5 search.

Latest run (FTS5 mode, 20 questions): Recall@3=1.000, MRR=1.000, nDCG@5=1.000, near/medium/far Recall@3 all 1.000, p95 latency=9.6ms.

## Running Benchmarks

```bash
# Quality + correctness
go test ./bench/quality/...  -v -bench=. -benchtime=1s

# Store performance
go test ./bench/store/       -bench=. -benchmem -benchtime=1s

# Forgetting quality
go test ./bench/forgetting/ -v

# Precision@k
go run ./bench/precision/   -k 3

# Retrieval fixture harness
go test ./bench/retrieval/ -v
go run ./bench/run_retrieval.go -k 5

# LongMemEval session-distance recall
go test ./bench/longmemeval/ -v
go run ./bench/run_longmemeval.go -k 5

# Token counting
go test ./internal/token/   -bench=. -benchmem -benchtime=1s
```

Full details: [bench/README.md](bench/README.md), [bench/store/README.md](bench/store/README.md).
