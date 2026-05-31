# Bench Harness

This directory contains lightweight evaluation harnesses for retrieval quality.

## Precision@k

Run:

```bash
go run ./bench/precision -k 3
```

The command seeds a temporary Ohara store with fixed fixtures and reports
`precision@k` over a small deterministic query set.

## Forgetting Quality

Run:

```bash
go test ./bench/forgetting -v
```

Coverage includes:

- stale recall prevention
- false forget prevention for foundational memories
- conflict-survival relation integrity after forget operations

## Retrieval Fixtures (Quality + Anti-Overfit)

Run deterministic retrieval checks backed by `bench/fixtures/retrieval_fixture.json`:

```bash
go test ./bench/retrieval -v
```

Run the standalone fixture harness:

```bash
go run ./bench/run_retrieval.go -k 5
OHARA_RETRIEVAL_MODE=fts5 go run ./bench/run_retrieval.go -k 5
OHARA_RETRIEVAL_MODE=hybrid go run ./bench/run_retrieval.go -k 5
OHARA_RETRIEVAL_MODE=hybrid OHARA_EMBEDDING_BACKEND=deterministic-test go run ./bench/run_retrieval.go -k 5
OHARA_RETRIEVAL_MODE=hybrid OHARA_EMBEDDING_BACKEND=ollama go run ./bench/run_retrieval.go -k 5
```

Notes:

- `OHARA_RETRIEVAL_MODE=hybrid` uses the configured embedding backend (default `ollama`) and cleanly reports `fts-fallback` when embeddings are unavailable.
- Set `OHARA_EMBEDDING_BACKEND=deterministic-test` to run deterministic hybrid validation without Ollama.
- Set `OHARA_EMBEDDING_BACKEND=ollama` (and optional `OHARA_OLLAMA_URL`) to validate against real Ollama embeddings.

The retrieval harness report includes:

- overall metrics (`Recall@1/3/5`, `MRR`, `nDCG@5`)
- stale/wrong-project/superseded hit rates
- file-context accuracy, graph-context accuracy, and pack budget compliance
- abstention false-positive rate
- per-case latency metrics (p50, p95, max, mean)
- threshold SLO gates (latency p95/max, fixture weak-distractor rate, fixture high-overlap rate)
- category case counts and per-category metrics
- fixture audit summary (lexical overlap, weak-distractor detection, high-overlap rate)
- worst failures with expected/actual IDs and scoring-source hints
- explicit embedding mode (`real-ollama`, `deterministic-test`, `fts-fallback`)

### JSON Replay Output

Pass `-json` to output the full report as pretty-printed JSON:

```bash
go run ./bench/run_retrieval.go -k 5 -json
```

The JSON includes a `CaseResults` array with one entry per fixture case:

| Field | Type | Description |
|-------|------|-------------|
| `case_id` | string | Fixture case identifier |
| `category` | string | Fixture category (e.g. `lexical`, `graph_context`) |
| `type` | string | Case type (`search`, `file_history`, `file_context`, `graph_context`, `pack`) |
| `source` | string | Retrieval source (`strict_fts`, `or_fallback`, `graph_context`, `file_context_scoring`, etc.) |
| `pass` | bool | Whether the case passed |
| `failure_reason` | string | Failure description (empty on pass) |
| `top_ids` | []int64 | Top returned memory IDs |
| `expected_ids` | []int64 | Expected memory IDs |
| `forbidden_ids` | []int64 | Forbidden memory IDs |
| `duration_ms` | float64 | Per-case wall-clock time in milliseconds |

No ranking behavior is changed; the JSON trace is a pure overlay on the existing harness.
