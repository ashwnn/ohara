# Ohara Handoff

Orientation for whoever picks up implementation. Pairs with `TASKS.md` (the implementation plan). Written June 19, 2026. Updated June 23, 2026.

## Implementation status (as of 2026-06-23)

| Phase | Tasks | Status |
|-------|-------|--------|
| Phase 0 | T0.1–T0.4 | ✅ Complete |
| Phase 1 | T1.0–T1.3 | ✅ Complete (commit `34b0be3`) |
| Phase 2 | T2.1–T2.4 | ✅ Complete (commit `a0709c0`) |
| Phase 3 | Backlog | 🔲 Gated — requires written design note |

### Recent commits

- `32846d6` — CI size/dependency guard (T0.3)
- `239ecd8` — LongMemEval CI gate (T0.1)
- `896feba` — LoCoMo + BEAM-1M harnesses (T0.2)
- `66978e0` — Publish BENCHMARKS_RESULTS.md and refresh COMPARISON.md (T0.4)

## What Ohara is

A local-first agent-memory engine shipped as a single static Go binary. It stores curated memories in SQLite with FTS5 lexical search, optional Ollama vector embeddings, a typed relation graph, and a five-state lifecycle. It exposes a 35-tool MCP surface to coding agents (Claude Code, OpenCode, Gemini CLI). The differentiators are strict: single binary, no CGO, and zero LLM calls on the retrieval hot path.

- Module: `github.com/ashwnn/ohara`, Go 1.25.0.
- 3 direct deps: `mark3labs/mcp-go v0.44.0`, `pkoukk/tiktoken-go v0.1.8`, `modernc.org/sqlite v1.53.0`.
- Runtime: one binary + one SQLite WAL DB (`engram.db`) under `~/.local/share/ohara/`.
- Build: `go build -trimpath -ldflags "-s -w -X main.version=..."`; release matrix linux/darwin x amd64/arm64; stripped binary ~13.1 MB.

## Repo map (verified against code)

```
cmd/ohara/main.go              CLI entrypoint + all commands
internal/
  store/                       Core. SQLite + FTS5 + retrieval + lifecycle
    store.go                   Schema, migrations (currentSchemaVersion=32), sessions, stats
    memories.go                Memory CRUD, conflict detection, relations usage
    hybrid.go                  Embedder interface, Ollama embed, cosine loop, fuseHybridRRF
    pack.go / pack_scoring.go  Context-pack assembly and scoring
    graph_feedback.go          Relation graph, entities, utility feedback
    jobs.go                    Durable memory_jobs post-write queue
  mcp/mcp.go                   MCP stdio server, 35 tools, role/profile maps
  server/server.go             HTTP REST API (port 7331)
  config, redact, maintain, setup, sync, token, project, util, version
bench/
  retrieval/                   Fixture recall/MRR/nDCG harness (70 cases)
  longmemeval/                 LongMemEval-style harness; ImportFromJSONL, JudgeModel, OverlapJudge
  quality/ store/ forgetting/ precision/   Other harnesses
  cmd/run-retrieval, cmd/run-longmemeval    Runners (-k, -sweep, -json)
.github/workflows/             ci.yml (tests/race/e2e/build/vet/vuln), pr-check.yml (label gate), release.yml (4-platform)
docs/                          ARCHITECTURE, COMPARISON, OPERATIONS, PLUGINS, analysis-20260619
skills/                        ohara-* contributor skills (read before coding; see AGENTS.md)
```

Key code anchors (file ~line):
- Migrations switch: `internal/store/store.go` ~1300; `obs_embeddings` DDL ~1525; `memory_relations` DDL ~1497.
- Embedder interface `internal/store/hybrid.go` ~192; `embedTextOllama` ~251; `cosineSimilarity` ~171; `fuseHybridRRF` ~346.
- Pack scoring `internal/store/pack_scoring.go`: `packScore` ~150, `packRecencyBoost` ~284, relation weight ~407.
- MCP tool registration `internal/mcp/mcp.go`: `srv.AddTool(...)`; role/permission maps ~152-274.

## Ground-truth corrections (analysis vs. live code)

The analysis (`docs/analysis-20260619.md`) is a strong strategic document, but several concrete facts have drifted. **Trust the code; the plan in `TASKS.md` already reflects these.**

1. **MCP tool count is 35, not 33.** The analysis repeatedly says 33. New tools in Phase 2 raise it to 38 and must be added to the role/profile maps, not just registered.
2. **`modernc.org/sqlite/vec` requires a version bump.** The analysis frames it as a free "drop-in blank import" next to the existing import. Verified: the pure-Go sqlite-vec port shipped in **`modernc.org/sqlite v1.47.0` (2026-03-17)**; the repo pins **v1.45.0**. Phase 1 therefore starts with a dependency upgrade (task T1.0), which trips the proposed size guard and needs justification. It is still CGO-free and single-binary.
3. **Schema is at version 32** (was 28 at handoff creation), migration-switch driven and additive. New migrations should be 033+ in order.
4. **Pack scoring is split** across `pack.go` and `pack_scoring.go` (analysis implies a single `pack.go`).
5. **Entity work may be partly done.** A `mem_extract_entities` MCP tool exists and `graph_feedback.go` handles entity-graph feedback. Audit before building the `entities` table from scratch (task T2.2) — likely extend, not create.
6. **Module path is `github.com/ashwnn/ohara`** (not stated in analysis).
7. **Relation graph has 6 types** (`caused`, `resolves`, `supersedes`, `related_to`, `implements`, `contradicts`) — the analysis flags one visual that wrongly showed 5; the code has 6.
8. **License:** repo `LICENSE` is MIT (recent commit `b513685` switched to MIT); the analysis capability table still lists Ohara as CC BY-NC 4.0. Treat MIT as current.

## External claims I verified (June 2026)

- vec0 / pure-Go sqlite-vec in modernc: real, auto-registers on blank import, supports float/int8/binary vectors. (Correction above re: version.)
- Mem0: LongMemEval `93.4`, LoCoMo `91.6`, BEAM-1M `64.1`, BEAM-10M `48.6` (mem0.ai). One third-party source reports Mem0 LongMemEval at `94.8`; prefer mem0.ai's own `93.4` and footnote the discrepancy.
- Zep: DMR `94.8%` vs MemGPT `93.4%` (arXiv 2501.13956, published **Jan 2025**, not 2026).

Numbers I did *not* independently re-verify and that should be treated as analysis-supplied until checked: Hindsight `91.4` LME / `64.1` BEAM-10M, MemPalace `96.6` recall_any@5, and the various LoCoMo baselines. Verify before publishing them in `docs/COMPARISON.md`.

## Where to start

Follow the first-PR sequence in `TASKS.md`. Concretely:

1. **T0.3 size/dependency CI guard (~2h)** — land this first so it protects every later change.
2. **T0.1 official LongMemEval 500-Q (~1d)** — first public benchmark number; harness plumbing already exists.
3. Then T0.2 → T0.4, then Phase 1 starting with the **T1.0 driver bump**.

The three things most likely to bite: (a) the v1.47.0 driver bump shifting FTS5/query-planner behavior, so isolate it in its own PR; (b) proving the `vec0` KNN lane returns ranking parity with the brute-force cosine lane rather than assuming it; (c) keeping the deterministic CI embedders reproducible once a real ANN path exists.

## Build and verify

```bash
go build -trimpath ./cmd/ohara
go test ./...
go test -race ./...
go test ./bench/retrieval/ -v
go run ./bench/cmd/run-longmemeval/ -k 5 -sweep
```

Before coding, read the relevant `skills/*/SKILL.md` (per `AGENTS.md`): start with `architecture-guardrails`, `business-rules`, `testing-coverage`, then `branch-pr` and `commit-hygiene` for the PR. Every change must keep the SLO gates (Recall@3 >= 0.80, MRR >= 0.70, p95 <= 50 ms, abstention FP <= 0.10) green and must not break the single-binary / no-CGO / zero-hot-path-LLM constraints.
