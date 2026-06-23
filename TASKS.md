# Ohara Implementation Tasks

Derived from `docs/analysis-20260619.md`, cross-checked against the live codebase and verified against external sources (June 2026). Where the analysis and the code disagree, this file follows the code. See `HANDOFF.md` for orientation and the full list of doc-vs-code corrections.

## Guardrails (apply to every task)

These constraints are non-negotiable and are how Ohara differs from every competitor. A task that breaks one is wrong even if it improves a metric.

- Single static Go binary. No second daemon, no external service required in default mode.
- No CGO. Pure-Go only (`modernc.org/sqlite`). Reject anything pulling `mattn/go-sqlite3`, libsql, DuckDB, Neo4j, Postgres.
- Zero LLM calls on the retrieval hot path. LLM use is opt-in and must run off the hot path (via the `memory_jobs` queue), never inside `mem_search`/`mem_pack`.
- SLO gates that already pass and must keep passing: Recall@3 >= 0.80, MRR >= 0.70, retrieval p95 <= 50 ms, max <= 150 ms, abstention false-positive <= 0.10.
- Binary-size tolerance: <= ~5 MB stripped amd64 increase per change; new direct dependencies require explicit justification.
- Additive migrations only. Do not break `topic_key` upserts, `normalized_hash` dedupe, or `deleted_at` soft-delete. Migrations are idempotent and version-gated (`currentSchemaVersion`, currently 28, in `internal/store/store.go`).
- Load the matching `skills/*/SKILL.md` before writing code (see `AGENTS.md`): `architecture-guardrails`, `business-rules`, `testing-coverage`, `branch-pr`, `commit-hygiene`.

## Phase ordering rationale

Phase 0 first because Ohara is currently under-credited: it has strong internal fixtures but zero public benchmark numbers, so nobody can place it next to Mem0/Zep. Cheap, high-trust wins. Phase 1 (native vector index) is the highest-ROI engineering change but only matters above ~50k items, so it follows the benchmarks that justify it. Phase 2 (bi-temporal layer) targets the genuinely empty market quadrant but is the largest surface area, so it comes last. Phase 3 is explicitly backlog.

---

## Phase 0 — Make capability legible (Weeks 1-3)

### T0.1 — Official LongMemEval 500-question harness ✅ (commit `541942c` + CI gate in ci.yml)

**What.** Add a loader and CI job that runs the *official* 500-question LongMemEval dataset through the existing benchmark pipeline and publishes a single end-to-end number to the README, next to Mem0 and Hindsight.

**Why.** Ohara reports Recall@3 `0.967`, but that is an internal 30-question fixture, not the public 500-Q benchmark. Until there is an official number, every external comparison is apples-to-oranges and Ohara reads as unproven. This is the cheapest credibility win available.

**Research / verification.**
- Mem0 reports LongMemEval `93.4` and LoCoMo `91.6` (verified against mem0.ai, June 2026). Hindsight reports LongMemEval `91.4`. These are the anchor numbers to publish against.
- The harness scaffolding already exists. `bench/longmemeval/harness.go` has `ImportFromJSONL()` (~line 365), `ImportFromJSONArray()` (~line 460), a `JudgeModel` interface (~line 234), and an `OverlapJudge` baseline. An Ollama judge backend was added recently (commit `6b62a5a`).
- Caveat to document: LongMemEval scoring normally uses an LLM judge. `OverlapJudge` is token-overlap only, so a number produced with it is not directly comparable to Mem0's LLM-judged number. Publish *which* judge produced the number, and prefer the Ollama judge for the headline figure.

**Implementation pointers.**
- New: `bench/longmemeval/official_500/` with a JSONL fixture loader reusing `ImportFromJSONL()`.
- New cmd runner under `bench/cmd/run-longmemeval/` flag (e.g. `-dataset official_500`) or a sibling cmd dir (mirror how standalone runners were split in commit `9af8c04`).
- CI: add a job to `.github/workflows/ci.yml` that runs the harness and fails if Recall@3 drops below the gate; emit the headline number as a build artifact / README badge update.

**Acceptance.** One reproducible command produces a published LongMemEval number for both `fts5` and `hybrid` modes, with the judge backend named. SLO gates still pass. README shows the number beside Mem0 `93.4` / Hindsight `91.4`.

**Effort.** ~1 day. **Risk.** Low. **Depends on.** Nothing.

---

### T0.2 — LoCoMo and BEAM-1M harnesses

**What.** Add LoCoMo and BEAM-1M benchmark harnesses on the existing fixture pipeline and `JudgeModel` interface.

**Why.** LoCoMo is the most widely reported memory metric in 2026; BEAM is the 2026 stress test no system has saturated. Without these two, Ohara cannot appear in the standard comparison tables at all.

**Research / verification.**
- LoCoMo: ~10 conversations, ~26k tokens each, ~600 turns, ~200 QAs per conversation; tests single-hop, multi-hop, temporal, open-domain. Mem0 `91.6`, full-context baseline ~`73`, best RAG baseline ~`61`.
- BEAM: up to 100 conversations, up to 10M tokens, 2,000 probes across facts/entities, updates, contradictions, temporal order, instructions-vs-preferences, multi-hop, summarization. Mem0 BEAM-1M `64.1`, BEAM-10M `48.6`; Hindsight BEAM-10M `64.1`. Start with BEAM-1M; 10M is a separate scaling effort and likely blocked on T1.x (native vector index) for acceptable latency.
- No LoCoMo or BEAM references exist in `bench/` today (confirmed). This is greenfield, but should reuse `JudgeModel`/import plumbing rather than a new framework.

**Implementation pointers.**
- New: `bench/locomo/` and `bench/beam/` mirroring `bench/longmemeval/` structure (harness + `_test.go` + cmd runner).
- Reuse the `-sweep`/`-json` comparison-table flags already present on `run-retrieval` / `run-longmemeval`.

**Acceptance.** Both harnesses run deterministically in CI, produce LoCoMo and BEAM-1M numbers for `fts5` and `hybrid`, and feed the comparison doc in T0.4.

**Effort.** ~3 days. **Risk.** Medium (dataset acquisition/licensing for official sets; multi-hop judging is noisier). **Depends on.** T0.1 (shared harness conventions).

---

### T0.3 — CI size and dependency guard ✅ (commit `32846d6`)

**What.** A CI check that fails a PR if the stripped `linux/amd64` binary grows by more than ~5 MB, or if a new direct dependency appears in `go.mod` without an approving label.

**Why.** The single-binary, three-dependency footprint *is* the product. Phase 1 and Phase 2 add code and a dependency bump; without an automated guard, the envelope erodes silently. This makes the constraint measurable on every PR.

**Research / verification.**
- Current footprint: 3 direct deps (`mark3labs/mcp-go`, `pkoukk/tiktoken-go`, `modernc.org/sqlite`), stripped binary ~14-18 MB. Build flags: `go build -trimpath -ldflags "-s -w -X main.version=..."`.
- `.github/workflows/ci.yml` already has a `build` job; this is an added step, not a new workflow. `pr-check.yml` already enforces a single `type:*` label, so the label-gating pattern exists to copy.

**Implementation pointers.**
- Add a step to the `build` job: build with release flags, record byte size, compare against a checked-in baseline (e.g. `.github/binary-size-baseline`), fail on > 5 MB delta.
- Parse `go.mod` `require` block; fail if the direct-dependency count increases unless the PR carries a `dep-approved` label.

**Acceptance.** A PR that bloats the binary or adds an unlabeled dependency fails CI with a clear message. Baseline file is updatable in-PR when growth is intentional.

**Effort.** ~2 hours. **Risk.** Low. **Depends on.** Nothing. **Do this early** — it protects Phase 1/2.

---

### T0.4 — Publish BENCHMARKS_RESULTS.md and refresh COMPARISON.md

**What.** A stable results doc comparing Ohara `fts5` vs `hybrid` against Mem0/Zep/Hindsight on identical fixtures where possible, and a rewrite of `docs/COMPARISON.md` from a claude-mem-only table to a multi-system table with verified numbers.

**Why.** The numbers from T0.1/T0.2 need a single canonical home that external readers can cite. The current comparison doc is narrow.

**Research / verification (numbers verified June 2026, cite primary sources, not the analysis).**
- Mem0: LongMemEval `93.4`, LoCoMo `91.6`, BEAM-1M `64.1`, BEAM-10M `48.6` (mem0.ai). Note a third-party report (agentry.press) cites Mem0 LongMemEval at `94.8`; prefer mem0.ai's own `93.4` and footnote the discrepancy.
- Zep: DMR `94.8%` vs MemGPT `93.4%` (arXiv 2501.13956, Jan 2025 — not 2026).
- Hindsight: LongMemEval `91.4`, BEAM-10M `64.1`.
- Always label Ohara's internal-fixture numbers as internal and name the judge, to avoid the apples-to-oranges trap the analysis itself flags.

**Acceptance.** `docs/BENCHMARKS_RESULTS.md` exists with a dated, sourced table. `docs/COMPARISON.md` covers Mem0/Zep/Letta/Hindsight, not just claude-mem. Every external number carries a primary-source link.

**Effort.** ~0.5 day. **Risk.** Low. **Depends on.** T0.1, T0.2.

---

## Phase 1 — Native vector search via `vec0` (Weeks 4-10)

> **Correction to the analysis.** The analysis presents `modernc.org/sqlite/vec` as a "drop-in blank import next to the existing `modernc.org/sqlite`." Verified June 2026: the pure-Go sqlite-vec port landed in **`modernc.org/sqlite v1.47.0` (released 2026-03-17)**. The repo pins **v1.45.0**. So Phase 1 begins with a dependency *upgrade*, which trips the T0.3 size guard and must be justified there. It remains CGO-free and single-binary, so it stays inside the envelope — but it is not free.

### T1.0 — Bump `modernc.org/sqlite` to >= v1.47.0 and audit

**What.** Upgrade the SQLite driver to the first version shipping the pure-Go `vec` extension, run the full suite, and measure binary-size impact.

**Why.** Prerequisite for all of Phase 1. Doing it as its own PR isolates any regression from the driver bump (FTS5 behavior, query planner, migrations) from the vec feature work.

**Research / verification.**
- `modernc.org/sqlite/vec` is a real, published package; blank-importing it auto-registers the `vec0` virtual table module. Supports float, int8, and binary vectors. No C toolchain required.
- Confirm FTS5 and all 28 migrations still pass on the new driver before adding any vec code.

**Implementation pointers.** `go get modernc.org/sqlite@v1.47.0` (or latest), `go mod tidy`, run `go test ./...`, `go test -race ./...`, and the bench suite. Update T0.3 baseline + `dep-approved` label.

**Acceptance.** All tests/benches green on the new driver; binary-size delta recorded and accepted in CI.

**Effort.** ~0.5 day. **Risk.** Medium (driver bump can shift FTS5/query-planner behavior). **Depends on.** T0.3.

---

### T1.1 — Add `vec0` virtual table with write-through dual-write

**What.** Create a `vec0` virtual table beside the existing `obs_embeddings` BLOB table and dual-write embeddings to both, keeping `obs_embeddings` authoritative until `vec0` correctness is proven.

**Why.** Today vectors are stored as BLOBs and similarity is a brute-force in-Go loop (`cosineSimilarity`, `internal/store/hybrid.go` ~line 171) — linear in N, fine at ~1k items, dominating cost past ~50k. `vec0` gives sub-linear KNN inside SQLite with no new runtime model.

**Research / verification.**
- Suggested schema (adapt column types to the package's accepted DDL):
  ```sql
  CREATE VIRTUAL TABLE IF NOT EXISTS observation_embeddings_vec USING vec0(
    obs_id INTEGER PRIMARY KEY,
    embedding FLOAT[768],
    model_name TEXT,
    content_hash TEXT
  );
  ```
- Default embedding model is `nomic-embed-text`, dim `768` (see `embedTextOllama`, `internal/store/hybrid.go` ~line 251). Keep the dimension a constant tied to the embedder config; the existing dimension-mismatch warning path must still fire.
- Write site: wherever `obs_embeddings` is populated (the `indexMemoryEmbedding` path). Add as migration 029 in the `applyMigration` switch in `internal/store/store.go` (current `obs_embeddings` DDL is ~lines 1525-1533; relations table ~1497-1510 for reference).

**Implementation pointers.** Migration 029 creates the virtual table; the embedding write path inserts into both tables in the same transaction. No read-path change yet.

**Acceptance.** New embeddings land in both tables; a backfill (T1.3) is not yet required for correctness; existing retrieval unchanged; tests green.

**Effort.** ~1 day. **Risk.** Low (write-only, behind no behavior change). **Depends on.** T1.0.

---

### T1.2 — Replace in-Go cosine with `vec0` KNN in the hybrid RRF lane

**What.** Switch the vector lane of `fuseHybridRRF` (`internal/store/hybrid.go` ~line 346) from the brute-force cosine loop to a `vec0` KNN query, keeping RRF math (`k=60`, lexical bonus) unchanged. Retain brute-force fallback for N <= 1000 and for the deterministic/static test embedders.

**Why.** This is the actual performance payoff. The RRF fusion, lexical bonus, and hybrid score modifiers stay identical, so retrieval quality should be unchanged while large-N latency drops.

**Research / verification.**
- Query shape: `SELECT obs_id, distance FROM observation_embeddings_vec WHERE embedding MATCH ? ORDER BY distance LIMIT ?`. Convert distance to a rank for RRF exactly as the current cosine lane does.
- Keep `cosineSimilarity` and `cosineTFIDF` (TF-IDF reranker lane, ~line 740) — TF-IDF is independent of this change.
- Determinism: CI uses `deterministicEmbedder`/`staticEmbedder`. Ensure the KNN path is gated so these tests stay reproducible (either route them through brute-force, or assert KNN returns identical ordering on the fixture).

**Implementation pointers.** Branch on corpus size and embedder type inside the vector lane; everything downstream of the two ranked lists is untouched.

**Acceptance.** `bench/retrieval` and `bench/longmemeval` produce identical (or within-noise) Recall@3/MRR/nDCG in `hybrid` mode; p95 unchanged at small N and materially lower at synthetic 50k+. SLO gates pass.

**Effort.** ~1 day. **Risk.** Medium (ranking parity must be proven, not assumed). **Depends on.** T1.1.

---

### T1.3 — Backfill embeddings via `memory_jobs`; optional int8 quantization

**What.** Backfill `vec0` rows for pre-existing embeddings through the durable `memory_jobs` queue, then (optionally) add int8 quantization for N > ~50k.

**Why.** Existing databases have BLOB embeddings with no `vec0` row; backfill makes `vec0` authoritative so `obs_embeddings` can later be deprecated. int8 gives ~4x storage reduction and ~2x scale speedup for a small single-digit recall hit — only worth enabling at scale.

**Research / verification.**
- `memory_jobs` is the existing durable post-write queue (`internal/store/jobs.go`); backfill must run there so it never blocks `mem_save`.
- modernc `vec` supports int8 and binary vectors (verified). Gate quantization behind a config flag and measure the recall delta on the fixtures before defaulting it on.

**Acceptance.** A one-shot backfill job populates `vec0` for all existing embeddings; with `vec0` authoritative, a follow-up can drop `obs_embeddings` writes. int8 path is flag-gated with a measured recall delta documented.

**Effort.** ~1 day (backfill) + ~1 day (quantization, optional). **Risk.** Low-Medium. **Depends on.** T1.2.

---

## Phase 2 — Bi-temporal layer on the existing relation graph (Weeks 8-14)

> Target the empty market quadrant — local single binary + bi-temporal knowledge graph — without a graph DB, without LLM on the hot path, additive to `memory_relations`. This copies the *useful* part of Zep (every fact carries world-time and transaction-time), not its Neo4j/Cypher stack.

### T2.1 — Add `valid_from` / `valid_to` to `memory_relations`

**What.** Migration adding two nullable temporal columns to `memory_relations`, plus backfill defaults. No behavior change yet.

**Why.** Converts the static typed relation graph (`caused`, `resolves`, `supersedes`, `related_to`, `implements`, `contradicts`) toward a bi-temporal model while staying in SQLite. Foundation for T2.3/T2.4.

**Research / verification.**
- `memory_relations` DDL is at `internal/store/store.go` ~lines 1497-1510 (migration 018); usage in `internal/store/memories.go` (~lines 1401-1728) and relation-weight scoring in `internal/store/pack_scoring.go` (~line 407).
- Observations already carry bi-temporal-ish fields (`created_at`, `updated_at`, `last_seen_at`, `expires_at`, `deleted_at`; summaries have `valid_from`/`valid_to`) — reuse those conventions for column semantics and naming.

**Implementation pointers.** Migration 030 (after Phase 1's 029); `ALTER TABLE memory_relations ADD COLUMN valid_from ...` / `valid_to ...`. Idempotent, additive.

**Acceptance.** Columns exist, default-backfilled; all existing relation reads/writes unaffected; tests green.

**Effort.** ~0.5 day. **Risk.** Low. **Depends on.** T1.1 (migration ordering only).

---

### T2.2 — `entities` table + zero-LLM deterministic extractor

**What.** An `entities` table (`kind` in `person|project|file|tool|topic|service`, `canonical_name`, `first_seen_at`, `last_seen_at`) populated by a deterministic extractor (regexes, capitalized terms, ISO dates, tool names, URL hosts) running as a `memory_jobs` background job.

**Why.** Entity-centric structure is the substrate for temporal-state queries. Keeping extraction deterministic and off-hot-path preserves the zero-LLM-at-retrieval guarantee. (Note: the analysis lists an `entities` table and `mem_extract_entities` tool as already existing in places; the live code has a `mem_extract_entities` MCP tool and `graph_feedback.go` — verify what is already there before building, and extend rather than duplicate.)

**Research / verification.**
- A `mem_extract_entities` tool is already registered (`internal/mcp/mcp.go`) and `internal/store/graph_feedback.go` handles entity-graph feedback. Audit these first; this task may be "finish/formalize the entities table + background extractor," not "build from zero."
- Extractor must be pure-Go heuristics only. An optional LLM relation/entity classifier may run *later* behind a flag via `memory_jobs` (see T2 optional), never on the hot path.

**Acceptance.** `entities` rows are produced from observation content by a background job that never blocks `mem_save`; extraction is deterministic and tested on fixtures.

**Effort.** ~2 days (less if extending existing code). **Risk.** Medium (dedup/canonicalization of entity names). **Depends on.** T2.1.

---

### T2.3 — MCP tools: `mem_temporal_state`, `mem_lineage`, `mem_supersedes`

**What.** Three additive, pure-SQL MCP tools: `mem_temporal_state(entity, at=ISO_TIME)`, `mem_lineage(topic_key)`, `mem_supersedes(obs_id)`.

**Why.** Exposes the bi-temporal layer to agents: "what did we believe about X at time T," "how did this topic evolve," "what replaced this memory." This is the user-visible differentiator.

**Research / verification.**
- Tools register via `srv.AddTool(mcp.NewTool(...))` in `internal/mcp/mcp.go`. **The live server already exposes 35 tools, not the 33 the analysis states** — so this raises the count to 38, and the tools must be added to the role/profile maps (`ProfileAgent`/`ProfileAdmin`, permission lists ~lines 152-274), not just registered.
- All three are read-only and pure SQL over `memory_relations` (+ `valid_from`/`valid_to`) and `entities`. No hot-path LLM.

**Acceptance.** Three tools registered, permission-mapped, documented; each returns correct results on a temporal fixture; tool-count and role tests updated.

**Effort.** ~1.5 days. **Risk.** Low-Medium. **Depends on.** T2.1, T2.2.

---

### T2.4 — Allen-interval temporal overlap boost in pack scoring

**What.** Add a small `temporalOverlapBonus` term to pack scoring: increase structural weight when a memory's validity window overlaps the query timeframe (Allen-interval overlap).

**Why.** The analysis identifies this as the single most predictive feature from Zep-style temporal analysis. It is a localized, low-risk scoring addition.

**Research / verification.**
- Pack scoring lives in `internal/store/pack_scoring.go` (`packScore` ~lines 150-230, with `packRecencyBoost` ~284, `packStalePenalty` ~299, relation weights ~407). Add the new term alongside the existing linear components and surface it in `mem_pack_explain`.
- Must be query-timeframe-aware; when the query has no timeframe, the term is zero (no behavior change for non-temporal queries).

**Acceptance.** Temporal queries show measurable Recall/MRR improvement on a temporal fixture; non-temporal queries unchanged; `mem_pack_explain` reports the new term; SLO gates pass.

**Effort.** ~1 day. **Risk.** Medium (must not regress the existing 0.967 fixture). **Depends on.** T2.1.

---

## Phase 3 — Backlog (needs written justification before starting)

These relax constraints or lack evidence of need. Do not start without a short design note arguing the constraint trade-off.

- **Pluggable HNSW index** (`coder/hnsw`, pure-Go). Only useful above ~50k items; `vec0` (Phase 1) likely makes it unnecessary. Re-evaluate only if `vec0` KNN proves insufficient at target scale.
- **Sleep-time consolidation** (Letta-style offline merge/dedupe via `memory_jobs`). Requires local Ollama; keep optional and local. Promising but large.
- **Personalized PageRank reranker** (HippoRAG 2). Pure-Go feasible, likely passes constraints; improves multi-hop recall. Candidate once BEAM multi-hop numbers (T0.2) show a gap.
- **BEAM-10M scaling.** Gated on Phase 1 latency at extreme N; treat as a research spike, not a deliverable.

### Explicit non-goals (do not implement)
- No `sqlite-vec` via `mattn/go-sqlite3` (CGO breaks portability).
- No graph database migration (second daemon kills single-binary design).
- No LLM on the retrieval hot path.
- Do not delete `topic_key` upserts, `normalized_hash` dedupe, or `deleted_at` soft-delete.
- Do not bundle an embedding model in the binary (`nomic-embed-text` ~274 MB on disk; ~15x size regression). Ollama stays optional and external.

---

## Suggested first PR sequence

1. T0.3 (size guard) — protects everything after it. ~2h.
2. T0.1 (LongMemEval 500) — first public number. ~1d.
3. T0.2 (LoCoMo + BEAM-1M) — standard comparison numbers. ~3d.
4. T0.4 (results + comparison docs). ~0.5d.
5. T1.0 (driver bump to v1.47.0). ~0.5d.
6. T1.1 (vec0 dual-write) → T1.2 (vec0 KNN in RRF) → T1.3 (backfill). ~3d.
7. T2.1 (temporal columns) → T2.2 (entities/extractor) → T2.3 (3 MCP tools) → T2.4 (Allen boost). ~6d.

## Sources

- vec0 in pure-Go modernc: [modernc.org/sqlite/vec](https://pkg.go.dev/modernc.org/sqlite/vec), [cznic/sqlite CHANGELOG](https://gitlab.com/cznic/sqlite/-/blob/master/CHANGELOG.md), [Gorse: modernc.org/sqlite now supports sqlite-vec](https://gorse.io/posts/sqlite-vec)
- Embedded vector DBs for Go: [Shaharia Azam comparison](https://shaharia.com/blog/choosing-embeddable-vector-database-go-application/)
- Mem0 2026 benchmarks: [mem0.ai/blog/ai-memory-benchmarks-in-2026](https://mem0.ai/blog/ai-memory-benchmarks-in-2026)
- Zep / Graphiti: [arXiv 2501.13956](https://arxiv.org/abs/2501.13956)
- sqlite-vec project: [github.com/asg017/sqlite-vec](https://github.com/asg017/sqlite-vec)
