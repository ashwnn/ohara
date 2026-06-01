# Modernization Validation Report (2026-05-20)

## Scope
This pass validated retrieval behavior, durable async jobs, passive capture ingestion, file-aware memory retrieval, pack explain diagnostics, and benchmark reliability for the recent modernization changes.

## What Was Checked

### 1. Search / retrieval
- Verified FTS path in `SearchMemories` and OR fallback path (`buildFallbackTerms` + `sanitizeFTSOR`).
- Verified hybrid path uses **rank-based RRF** in `fuseHybridRRF` (not alpha blending).
- Verified vector lane reads embeddings in a single join query (`vectorSearchMemories`), avoiding N+1 row lookups.
- Verified active retrieval excludes archived/expired items; added explicit exclusion for active rows carrying `superseded_by`.
- Verified wrong-project filtering via project predicates in FTS and vector lanes.
- Verified deterministic fallback when embeddings fail (hybrid silently returns FTS-ranked results).

### 2. Durable jobs
- Verified `AddMemory` and `enqueueDerivedJobsTx` run in one transaction (`withTx`) and rollback on enqueue failure.
- Verified jobs persist across restart and can be drained with `RunJobsOnce`.
- Verified failure handling updates `attempts`, `last_error`, `status`, and backoff (`markJobFailure`).
- Verified derived-indexing failures do not corrupt committed canonical memories; failures remain isolated to job state.
- Verified migration DDL for job tables/indexes is `IF NOT EXISTS` and idempotent.

### 3. Passive capture
- Verified passive observation lane (`/observe` -> `Observe`) sanitizes private tags and truncates title/body/payload.
- Verified accepted capture levels: `off|prompts|metadata|tools|full`, with unknown level normalized to `metadata`.
- Verified OpenCode-side behavior remains non-fatal when Ohara is unavailable (`oharaFetch` returns null without throwing).
- Verified sub-agent session suppression path exists in plugin (`subAgentSessions` guard for session registration/capture).

### 4. File-aware memory
- Verified structured file hint extraction order includes `applies_to_json`, `evidence_json`, `related_json`, tags, then title/body fallback.
- Verified directory-level matching path exists (`pathMatchScore` directory logic).
- Added active exclusion for `superseded_by` in file-history query path.
- Verified token budget enforcement in `FileContext`.

### 5. Pack explain
- Verified explain output is generated from the same scored candidates used for selection (no parallel approximation).
- Verified explain rows include score components, token estimates, inclusion flag, and reason.

## Bugs / Gaps Found
1. Hybrid validation was previously coupled to Ollama availability, making benchmark-mode hybrid validation non-deterministic in CI/local environments without Ollama.
2. Retrieval fallback had avoidable pluralization misses (e.g. `ranks` vs `rank`) that masked relevant hits.
3. Active retrieval and file-history paths could still include rows with `superseded_by` populated if status remained `active`.
4. Benchmark output lacked an explicit embedding provenance mode label (`real-ollama` vs deterministic vs fallback).
5. Missing explicit CLI coverage for `ohara jobs run --once` drain path.

## Fixes Made

### Deterministic hybrid validation
- Added explicit embedding backend `deterministic-test` in store embedding path (`internal/store/hybrid.go`).
- Kept production defaults unchanged (`fts5` default; `ollama` still normal hybrid backend when configured).
- Hybrid now enables for backends: `ollama`, `deterministic-test`; unknown backends are ignored for hybrid lane.

### Retrieval hardening
- Added active-result exclusion for `superseded_by` in:
  - FTS retrieval (`SearchMemories`)
  - Vector lane (`vectorSearchMemories`)
  - File history (`FileHistory`)
- Improved OR fallback token normalization with lightweight plural handling (`ies/es/s` stems).
- Rebalanced FTS kind/classification weighting to reduce observational-note dominance in general retrieval.

### Benchmark reliability and reporting
- Added `EmbeddingMode` to benchmark report with explicit values:
  - `real-ollama`
  - `deterministic-test`
  - `fts-fallback`
- Benchmark hybrid mode now follows store defaults (`ollama`), while deterministic hybrid validation requires explicit `OHARA_EMBEDDING_BACKEND=deterministic-test`.
- CLI benchmark output now prints `Embedding mode` and labels failure section as `Worst failures`.

### Test coverage added/updated
- `internal/store/hybrid_rrf_test.go`
  - deterministic-backend hybrid RRF coverage
  - active filtering of superseded-linked + wrong-project results
- `internal/store/file_context_test.go`
  - directory-level file-history matching
  - exclusion of superseded-linked file memories
- `internal/store/observations_test.go`
  - capture-level acceptance coverage for all levels
- `cmd/ohara/main_extra_test.go`
  - `realCmdJobs` run-once drain path with retry-state assertion
- `bench/retrieval/retrieval_test.go`
  - deterministic hybrid benchmark mode label assertions
  - metric aggregation correctness checks
  - threshold enforcement regression check

## Validation Results (Updated 2026-05-31)

### Command: `go test ./internal/store/ -count=1`
- Result: **PASS** (3.3s)

### Command: `go run ./bench/run_retrieval.go -k 5 -json`
- Mode: `fts5`
- Embedding mode: `fts-fallback`
- Cases: 70
- Passed: 69
- Failed: 1
- Overall Recall@3: **0.966**
- Overall MRR: **0.900**
- nDCG@5: **0.914**
- File-context accuracy: **1.000**
- Graph-context accuracy: **1.000**
- Pack budget compliance: **1.000**
- Abstention FP: **0.000**
- stale/wrong-project/superseded hit rates: **0.0000 / 0.0000 / 0.0000**
- p95 latency: 35.5ms (within 50ms SLO)

### Command: `OHARA_RETRIEVAL_MODE=hybrid OHARA_EMBEDDING_BACKEND=deterministic-test go run ./bench/run_retrieval.go -k 5 -json`
- Mode: `hybrid`
- Embedding mode: `deterministic-test`
- Cases: 70
- Passed: 69
- Failed: 1
- Hybrid path executes deterministically; only tmp_04 fails (same as keyword mode).

## Remaining Risk
1 benchmark case still fails:
- `tmp_04` — query `"middleware notes"` expected to find `a_auth_nil_bugfix` (ID 3) but FTS5 strict AND matches `a_session_noise_1` (ID 15) which contains both terms `"scratch notes auth middleware"`. Memory 3 lacks the word "notes" in its title/body, so it gets zero FTS AND hits. The OR fallback is not triggered because strict FTS returned ≥1 result. This is an inherent precision-recall tradeoff for short ambiguous queries — relaxing the OR fallback trigger condition risks precision regressions across other cases.

## Resolved in this cycle
- `ms_03` — fixed by relaxing OR fallback `minTermHits` threshold from `≥3→2` to `≥6→2`. The rare discriminating term "fts5" was being filtered out because it only matched 1 of 4 fallback terms.
- `ms_05`, `pack_01`, `pack_05` — now pass naturally (batch size increase from 64→70 cases, session-scoped pack boost in prior commit 9a3a9de).
