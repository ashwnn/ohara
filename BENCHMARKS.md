# Benchmarks

> Last updated: 2026-06-22  
> Hardware: Apple M-series (arm64, macOS), Go 1.24.2.  
> Prior run hardware: Intel Core i5-6500T @ 2.50GHz, Linux amd64, Go 1.26.2.

## Quality (bench/quality/)

| Metric | Result | Goal |
|--------|--------|------|
| MRR@k=5 | 1.000 | ≥ 0.7 |
| Recall@3 | 1.000 | ≥ 0.7 |
| Recall@10 | 1.000 | ≥ 0.9 |
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
| Search p50 latency | ~12ms | 100-item corpus |
| Search p95 latency | ~16ms | 100-item corpus |
| DB size per memory item | ~2-3KB | — |

Note: Save-throughput benchmarks (`BenchmarkSaveThroughput*`) are excluded from this
table — they fail under `-benchtime=1s` due to the temp-store lifecycle; run
`go test ./bench/store/ -bench=BenchmarkSaveThroughput -benchtime=30s` for throughput numbers.

## Retrieval Fixture Harness (bench/retrieval/)

Deterministic fixture-driven recall, MRR, nDCG, stale/superseded filtering, file context, graph context, pack budgeting, abstention false-positive rate.

Latest run (FTS5 mode, 70 cases): Recall@1=0.831, Recall@3=0.966, Recall@5=0.983, MRR=0.900, nDCG@5=0.916, file-context accuracy=1.000, graph-context accuracy=1.000, pack budget compliance=1.000, abstention FP=0.000, p50=2.9ms, p95=7.6ms.

## Forgetting Harness (bench/forgetting/)

All three tests pass: stale recall prevention, false forget prevention (foundational memories survive expiry), conflict survival through forget operations.

## Precision (bench/precision/)

Keyword-match precision@k on deterministic fixtures: precision@3 = 0.3333 (33.3%) — 3 queries. Low precision is expected given FTS5 matching over short fixture content; ranking quality is better represented by the retrieval harness MRR.

## Token Counting (internal/token/)

| Benchmark | Op/s | ns/op |
|-----------|------|-------|
| Count | ~69K | 14,482 |
| CountStrict | ~74K | 13,552 |
| WithinBudget | ~88K | 11,377 |

## LongMemEval Harness (bench/longmemeval/)

LongMemEval-style session-distance recall benchmark. Seeds 45 facts across 6 sessions and evaluates retrieval quality at near (1 session), medium (2-3 sessions), and far (4-5 sessions) distances using FTS5 search.

Latest run (FTS5 mode, 30-question curated fixture): Recall@1=1.000, Recall@3=1.000, Recall@5=1.000, MRR=1.000, nDCG@5=1.000, near/medium/far Recall@3=1.000, p50=39ms, p95=68ms.

Includes:
- **Expanded distractor fixture**: 45 facts with overlapping titles and paraphrased bodies across auth, database, api, infra domains
- **JSONL dataset importer**: `ImportFromJSONL()` converts LongMemEval-style JSONL records to fixture format
- **Judge model**: `OverlapJudge` provides baseline token-overlap answer evaluation (no LLM dependency); mean judge score: 0.970
- **Hybrid mode**: `-mode hybrid` tests deterministic embedding retrieval alongside FTS5
- **25 deterministic tests** covering harness, import, judge, and hybrid modes

### Official LongMemEval-S dataset (500 questions, 18,460 sessions)

The cleaned LongMemEval-S dataset (`bench/longmemeval/data/longmemeval_s_cleaned.json`)
imports as 18,460 session "facts" — each a full multi-turn conversation transcript —
and 500 questions. This is the accreditation-scale run via
[bench/run-benchmark-build.sh](bench/run-benchmark-build.sh).

Full 500Q run completed 2026-06-23. Judge: containment (mean score: 0.761). Both
FTS5 and hybrid-deterministic modes produce identical scores — the deterministic
embedder used in CI does not call Ollama, so both lanes behave identically.

| Mode | Recall@1 | Recall@3 | Recall@5 | MRR | nDCG@5 | Pass/500 | Runtime |
|------|----------|----------|----------|-----|--------|----------|---------|
| fts5 | 0.096 | 0.142 | 0.172 | 0.121 | 0.115 | 169 | ~2h04m |
| hybrid-deterministic | 0.096 | 0.142 | 0.172 | 0.121 | 0.115 | 169 | ~1h59m |

Per-category (fts5):

| Category | Recall@3 | MRR | Cases |
|----------|----------|-----|-------|
| knowledge-update | 0.397 | 0.351 | 78 |
| single-session-user | 0.243 | 0.197 | 70 |
| multi-session | 0.090 | 0.079 | 133 |
| single-session-preference | 0.033 | 0.011 | 30 |
| temporal-reasoning | 0.068 | 0.058 | 133 |
| single-session-assistant | 0.018 | 0.018 | 56 |

Distance breakdown: near Recall@3=0.217, medium=0.189, far=0.129.

These are honest FTS5 baseline numbers. LongMemEval is designed to reward semantic
memory — questions like "How many doctor's appointments did I go to in March?" require
understanding full conversation transcripts, which pure lexical FTS5 does not do well.
The knowledge-update category (0.397) is strongest because those questions often contain
exact terms present in the stored transcript. To compete with Mem0 93.4 / Hindsight 91.4,
Ohara needs real vector embeddings (Phase 1: `vec0` + Ollama `nomic-embed-text`).

### Performance optimizations

The exhaustive run was reduced from **~3h+ to ~75 min** (full 500Q × 2 modes) by
three changes, each verified to preserve correctness (curated fixture stays 30/30,
full store + bench suites pass):

| Stage | Before | After | Change |
|-------|--------|-------|--------|
| Seed 18,460 facts | hours (serial `AddMemory`) | ~12s | `Store.BulkSeedMemories` — single transaction, prepared statement, skips audit/revisions/jobs/truncation |
| Seed 45-fact fixture | 2.6s | 25ms | (same) |
| Per-query (40Q, serial) | 18.2s | 6.2s | `sanitizeFTSAnd` drops stopwords from the FTS5 AND query so it no longer intersects corpus-wide posting lists for `"i"`, `"did"`, `"with"`, etc. |
| Recall@5 (40Q, same change) | 0.250 | 0.350 | stopword removal also stops valid sessions being excluded — strict quality gain |
| Auto worker count | GOMAXPROCS (8) | capped at 4 | pure-Go `modernc/sqlite` FTS5 reads contend on shared cache; throughput peaks at ~4 readers (8 measured slower than 4) |

The remaining per-query cost is CPU-bound inside the pure-Go SQLite FTS5 iterator
(`modernc.org/sqlite`) scanning large transcript payloads; further gains would
require CGO SQLite or reusing one seeded DB across modes.

## Running Benchmarks

```bash
# Quality + correctness
go test ./bench/quality/...  -v -bench=. -benchtime=1s

# Store performance
go test ./bench/store/       -bench=BenchmarkSearchLatency100 -benchmem -benchtime=1s

# Forgetting quality
go test ./bench/forgetting/ -v

# Precision@k
go run ./bench/precision/   -k 3

# Retrieval fixture harness
go test ./bench/retrieval/ -v
go run ./bench/cmd/run-retrieval/ -k 5

# LongMemEval session-distance recall
go test ./bench/longmemeval/ -v
go run ./bench/cmd/run-longmemeval/ -k 5 -fixture bench/longmemeval/fixture.json
./bench/run-benchmark-build.sh   # requires bench/longmemeval/data/ (download separately)

# Token counting
go test ./internal/token/   -bench=. -benchmem -benchtime=1s
```

### Sweep Mode

Both `run-retrieval` and `run-longmemeval` support `-sweep` flag to run across all
supported retrieval modes (FTS5, hybrid-deterministic, hybrid-ollama-fallback) and
print a comparison table. Pass `-json` for structured output:

```bash
go run ./bench/cmd/run-retrieval/ -k 5 -sweep
go run ./bench/cmd/run-retrieval/ -k 5 -sweep -json
go run ./bench/cmd/run-longmemeval/ -k 5 -sweep
```

Full details: [bench/README.md](bench/README.md), [bench/store/README.md](bench/store/README.md).

For the official cleaned 500-question LongMemEval dataset, use the local
benchmark-build path in [bench/run-benchmark-build.sh](bench/run-benchmark-build.sh).
It defaults to `bench/longmemeval/data/longmemeval_s_cleaned.json` when present
and keeps the exhaustive run out of GitHub Actions.
