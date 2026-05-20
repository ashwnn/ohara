# AgentMemory Modernization Plan

This plan modernizes Ohara as a durable memory engine (not an orchestrator), while keeping local-first SQLite/FTS5 behavior and deterministic fallbacks.

## Phase 0 Findings (Current Code Map)

- OpenCode passive capture: `plugin/opencode/ohara.ts` (runtime plugin) and `internal/setup/plugins/opencode/ohara.ts` (embedded setup copy). Current hooks are mostly `session.created`, `session.deleted`, `chat.message`, `tool.execute.after`, and compaction hook.
- HTTP capture endpoints: `internal/server/server.go` routes include `POST /prompts`, `POST /capture/passive`, `GET /context`, and memory APIs.
- MCP tools:
  - Tool registration and handlers: `internal/mcp/mcp.go`
  - Core handlers: `handleSave`, `handleSearch`, `handleContext`, `handleSessionSummary`, `handleCapturePassive`, `handlePack`
- Canonical write path: `internal/store/memories.go` → `Store.AddMemory(...)` and `Store.UpdateMemory(...)`.
- Hybrid retrieval path:
  - `internal/store/hybrid.go` (`embedText`, `indexMemoryEmbedding`, `blendHybridScores`)
  - Called from `SearchMemories(...)` in `internal/store/memories.go`.
- Context pack assembly: `internal/store/pack.go` (`BuildPack`, `FormatPackText`); also MCP `handlePrime` in `internal/mcp/mcp.go`, CLI `realCmdPrime` in `cmd/ohara/main.go`.
- Entity/relation helpers:
  - `internal/store/graph_feedback.go` (`ExtractEntitiesHeuristic`, `AttachExtractedEntities`, `GraphContext`, feedback helpers)
  - `internal/store/memories.go` relation methods (`AddRelation`, `RemoveRelation`, `GetRelated`)
- Migrations: `internal/store/store.go` (`currentSchemaVersion`, `runMigrations`, `applyMigration`)
- Tests:
  - Store: `internal/store/store_test.go`
  - MCP: `internal/mcp/mcp_test.go`
  - Server: `internal/server/server_test.go`
  - Benchmark suites: `bench/*`

## Implementation Plan

1. Passive capture expansion (plugin + server)
   - Add capture-level gating via `OHARA_PASSIVE_CAPTURE_LEVEL` (`off|prompts|metadata|tools|full`).
   - Expand OpenCode events capture (session status/updated/diff/error, tool before/after, file/todo/command/config, permission prompts when available).
   - Keep plugin fail-closed and non-blocking when Ohara is down; debug-only logs.
   - Keep sub-agent suppression and private-tag stripping.
   - Add a generic observation endpoint (`POST /observe`) that stores raw/session-scoped observations without auto-promoting to authoritative memory.

2. Durable async indexing jobs
   - Add `memory_jobs` table migration and job index.
   - Update `AddMemory`/`UpdateMemory` transaction flow: commit canonical memory + enqueue derived jobs in same transaction.
   - Add store job runner with retry/backoff fields (`attempts`, `last_error`, `available_at`, `status`), no in-memory-only queue.
   - Add CLI command `ohara jobs run --once` for deterministic test draining.

3. RRF retrieval lanes
   - Replace alpha blend with lane-based retrieval + Reciprocal Rank Fusion.
   - Lanes:
     - FTS lane from `memory_items_fts`
     - Vector lane from `obs_embeddings` (bounded batch scan, graceful fallback if embedding unavailable)
   - Preserve FTS-only default and hybrid degradation to FTS.
   - Add additive score modifiers (recency, utility, trust/status penalties, classification/kind priors).

4. File-aware memory APIs and MCP tools
   - Add store methods for `mem_file_history(path, project?)` and `mem_file_context(path, project?, budget_tokens?)` using `applies_to_json` + body/title fallback.
   - Add HTTP equivalents (`GET /files/history`, `POST /files/context`).
   - Add MCP tools and OpenCode file observation capture for context injection at `experimental.chat.system.transform`.

5. Structural pack scoring
   - Refactor pack ranking to combine relevance, kind priority, recency, utility, relation/entity signals, and stale/superseded penalties.
   - Add explain mode (`mem_pack_explain` and CLI `ohara pack --explain`) with per-memory score components and inclusion reasons.

6. Bench + docs alignment
   - Add/extend benchmark harness under `bench/` for Recall@K, MRR, hybrid fallback, stale/superseded behavior, file-context retrieval, and pack budget behavior.
   - Update docs: quickstart, plugin capture modes, privacy model, hybrid setup, jobs runner, file-aware retrieval, explain mode.

## Guardrails

- FTS5 remains default retrieval.
- No mandatory external vector DB.
- Memory writes must succeed even if embedding backend fails.
- Privacy controls remain enforced (private tag stripping, trust-level filtering, redaction, audit logging).
- Keep changes incremental and test-backed at each phase.
