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
- file-context accuracy and pack budget compliance
- abstention false-positive rate
- category case counts and per-category metrics
- fixture audit summary (lexical overlap + weak-distractor detection)
- worst failures with expected/actual IDs and scoring-source hints
- explicit embedding mode (`real-ollama`, `deterministic-test`, `fts-fallback`)
