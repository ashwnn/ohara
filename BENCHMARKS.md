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

LongMemEval-style session-distance recall benchmark. Seeds 45 facts across 6 sessions and evaluates retrieval quality at near (1 session), medium (2-3 sessions), and far (4-5 sessions) distances using FTS5 search.

Latest run (FTS5 mode, 30-question curated fixture): Recall@1=1.000, Recall@3=1.000, Recall@5=1.000, MRR=1.000, nDCG@5=1.000, near/medium/far Recall@3=1.000.

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

Latest FTS5 run (100-question slice, 4 workers): Recall@1=0.100, Recall@3=0.180,
Recall@5=0.260, MRR=0.150. Absolute recall reflects the difficulty of pure-lexical
FTS5 retrieval over large transcripts — LongMemEval is designed to reward semantic
memory. These are honest baseline numbers for the FTS5 spine, not a tuned result.

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
go test ./bench/store/       -bench=. -benchmem -benchtime=1s

# Forgetting quality
go test ./bench/forgetting/ -v

# Precision@k
go run ./bench/precision/   -k 3

# Retrieval fixture harness
go test ./bench/retrieval/ -v
go run ./bench/cmd/run-retrieval/ -k 5

# LongMemEval session-distance recall
go test ./bench/longmemeval/ -v
go run ./bench/cmd/run-longmemeval/ -k 5
./bench/run-benchmark-build.sh

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
