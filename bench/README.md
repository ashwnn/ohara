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
go run ./bench/cmd/run-retrieval/ -k 5
OHARA_RETRIEVAL_MODE=fts5 go run ./bench/cmd/run-retrieval/ -k 5
OHARA_RETRIEVAL_MODE=hybrid go run ./bench/cmd/run-retrieval/ -k 5
OHARA_RETRIEVAL_MODE=hybrid OHARA_EMBEDDING_BACKEND=deterministic-test go run ./bench/cmd/run-retrieval/ -k 5
OHARA_RETRIEVAL_MODE=hybrid OHARA_EMBEDDING_BACKEND=ollama go run ./bench/cmd/run-retrieval/ -k 5
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
go run ./bench/cmd/run-retrieval/ -k 5 -json
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

## LongMemEval (Session-Distance Recall)

LongMemEval-style benchmark evaluating recall quality across increasing session distances.
Seeds facts across 5 simulated sessions and measures whether FTS5 retrieval can find facts
from near (1 session), medium (2-3 sessions), and far (4-5 sessions) distances.

Run deterministic tests:

```bash
go test ./bench/longmemeval -v
```

Run standalone harness:

```bash
go run ./bench/cmd/run-longmemeval/ -k 5
go run ./bench/cmd/run-longmemeval/ -k 5 -json
go run ./bench/cmd/run-longmemeval/ -k 5 -enforce -skip-latency
# Ollama LLM-based judge (requires local Ollama + model):
go run ./bench/cmd/run-longmemeval/ -k 5 -ollama-judge -ollama-model qwen3:0.6b
# Use external JSONL dataset from evals/:
go run ./bench/cmd/run-longmemeval/ -k 5 -dataset evals/longmemeval/data.jsonl
```

The harness report includes:

- overall metrics (`Recall@1/3/5`, `MRR`, `nDCG@5`)
- distance-stratified metrics (near, medium, far)
- per-category metrics (session_distance_1 through session_distance_5)
- per-case latency metrics (p50, p95, max, mean)
- threshold SLO gates (overall recall, near/medium/far recall, MRR, latency)
- per-question failures with expected/actual fact keys

### JSON Replay Output

Pass `-json` to output the full report as pretty-printed JSON:

```bash
go run ./bench/cmd/run-longmemeval/ -k 5 -json
```

The JSON includes a `CaseResults` array with per-question fields:

| Field | Type | Description |
|-------|------|-------------|
| `question_id` | string | Question identifier |
| `category` | string | Session distance category |
| `distance` | string | Distance bucket (near/medium/far) |
| `distance_sessions` | int | Number of sessions between fact insert and question |
| `pass` | bool | Whether the question passed |
| `failure_reason` | string | Failure description (empty on pass) |
| `top_ids` | []int64 | Top returned memory IDs |
| `expected_ids` | []int64 | Expected memory IDs |
| `top_keys` | []string | Top returned fact keys |
| `expected_keys` | []string | Expected fact keys |
| `duration_ms` | float64 | Per-question wall-clock time in milliseconds |
| `query` | string | The search query used |

### Fixture Design

The baseline fixture (`bench/longmemeval/fixture.json`) contains 20 facts and 20 questions
spanning domains: auth, database, api, infra. This is a self-contained deterministic spine
with no external dataset dependency. T2+ can extend with the full LongMemEval public dataset
and judge-model integration.
