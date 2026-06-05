[← Back to README](README.md)

# Documentation

This is the canonical reference for Ohara outside the README. The goal is one
maintained reference file plus the small set of standalone files that GitHub
and the README already expect.

## Core Entry Points

- [docs/INSTALLATION.md](docs/INSTALLATION.md) — install and source-build entry
- [docs/OPERATIONS.md](docs/OPERATIONS.md) — run, validate, back up, and repair
- [docs/PRODUCTION_NOTES.md](docs/PRODUCTION_NOTES.md) — tested scope and known
  limits
- [CONTRIBUTING.md](CONTRIBUTING.md) — contribution workflow
- [SECURITY.md](SECURITY.md) — vulnerability reporting
- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) — contributor standards

## Agent Integration

Ohara has two integration paths:

- OpenCode plugin for the full local experience
- MCP for any agent that speaks standard MCP

Recommended setup:

- OpenCode: `ohara setup opencode`
- Other agents: `ohara mcp` for stdio, or remote MCP over HTTP when network
  transport is required

The MCP server supports these profiles:

- `agent` — focused memory operations
- `admin` — curation and maintenance tools
- `all` — everything

Use `agent` unless you are deliberately doing maintenance work.

## Plugin Behavior

Ohara ships a thin OpenCode plugin. Everything else should integrate through
MCP unless there is a strong reason to add client-specific behavior.

The plugin can:

- ensure a local server is available
- create sessions on demand
- inject memory protocol guidance into the system prompt
- preserve context across compaction
- optionally send passive observations to `POST /observe`
- strip `<private>` tags before persistence

Relevant environment variables:

- `OHARA_BIN` — explicit binary path override
- `OHARA_PORT` — local HTTP port, default `7331`
- `OHARA_MEMORY_INJECTION` — disable protocol injection with `0`
- `OHARA_MEMORY_AGENTS` — comma-separated agent allowlist for injection
- `OHARA_PASSIVE_CAPTURE_LEVEL` — `off|prompts|metadata|tools|full`
- `OHARA_DEBUG` — enable plugin diagnostics

## HTTP API

Base URL: `http://127.0.0.1:7331`

Contract rules:

- routes listed here are the supported surface
- request and response bodies are JSON unless noted otherwise
- route or payload changes require docs and tests in the same change

Health:

```http
GET /health
GET /ready
```

Sessions:

```http
POST /sessions
PATCH /sessions/{id}
POST /sessions/{id}/end
GET /sessions/{id}/context
GET /sessions/recent
DELETE /sessions/{id}
```

`POST /sessions/{id}/end` is a legacy alias. Prefer `PATCH /sessions/{id}`.

Prompts and observation:

```http
POST /prompts
GET /prompts/recent
GET /prompts/search
DELETE /prompts/{id}
POST /capture/passive
POST /observe
```

Memory retrieval:

```http
GET /context
GET /files/history
POST /files/context
GET /memories
GET /memories/search
GET /memories/{id}
GET /memories/{id}/timeline
GET /memories/{id}/revisions
POST /pack
GET /stats
GET /sync/status
```

Memory mutation:

```http
POST /memories
PATCH /memories/{id}
DELETE /memories/{id}
POST /projects/migrate
GET /export
POST /import
```

Remote MCP:

```http
GET /mcp
POST /mcp
GET /mcp/sse
POST /mcp/message
```

Use `streamable-http` as the canonical remote transport. Keep auth enabled if
you expose remote MCP.

## Architecture

Ohara is a local-first memory layer for coding agents. The boundaries are:

- CLI and HTTP expose the product surface
- MCP exposes agent-facing memory tools
- the OpenCode plugin adds lifecycle behavior on top of MCP
- SQLite is the system of record

Runtime shape:

```text
agent/plugin -> MCP or HTTP -> store -> SQLite
                           -> post-write jobs
```

Storage and retrieval:

- canonical data lives in SQLite
- full-text retrieval uses FTS5
- derived work such as embeddings or relation extraction runs through durable
  SQLite-backed jobs after writes commit
- hybrid retrieval can add vector ranking when embeddings are available

Integration boundaries:

- plugin behavior in `plugin/opencode`
- MCP handlers in `internal/mcp`
- HTTP handlers in `internal/server`
- persistence and retrieval logic in `internal/store`

Design rules:

- the agent decides what is worth remembering
- Ohara stores curated memories, not a full raw transcript pipeline
- local-first defaults beat network-first complexity

## Comparison

Compared with `claude-mem`, Ohara is optimized for:

- one Go binary instead of a multi-runtime stack
- OpenCode plus generic MCP agent support
- curated memory instead of raw transcript capture
- SQLite and FTS5 first, with optional hybrid retrieval on top

Choose `claude-mem` if you specifically want transcript-style capture inside the
Claude Code ecosystem.

## Benchmarks

Run benchmarks when changing retrieval quality, storage behavior, token
counting, or memory lifecycle logic.

Core commands:

```bash
go test ./bench/quality/... -v -bench=. -benchtime=1s
go test ./bench/store/ -bench=. -benchmem -benchtime=1s
go test ./bench/forgetting/ -v
go run ./bench/precision/ -k 3
go test ./bench/retrieval/ -v
go run ./bench/run_retrieval.go -k 5
go test ./internal/token/ -bench=. -benchmem -benchtime=1s
```

Useful retrieval variants:

```bash
OHARA_RETRIEVAL_MODE=fts5 go run ./bench/run_retrieval.go -k 5
OHARA_RETRIEVAL_MODE=hybrid go run ./bench/run_retrieval.go -k 5
OHARA_RETRIEVAL_MODE=hybrid OHARA_EMBEDDING_BACKEND=deterministic-test go run ./bench/run_retrieval.go -k 5
OHARA_RETRIEVAL_MODE=hybrid OHARA_EMBEDDING_BACKEND=ollama go run ./bench/run_retrieval.go -k 5
```

## Documentation Rules

- Keep the README as the product front page.
- Prefer one canonical page per topic over parallel guides.
- Remove stale plans, reports, and duplicate references instead of keeping them
  around as live documentation.
