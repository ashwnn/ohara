[← Back to README](../README.md)

# HTTP API Contract

Base URL: `http://127.0.0.1:7331`

The HTTP API is local-first and intended for local plugins, MCP adapters, and
developer tooling. It is not an authenticated public API.

## Stability Rules

- Routes listed here are the supported contract.
- Legacy aliases may exist, but new integrations should use the canonical routes.
- Request and response bodies are JSON unless noted.
- Error responses use non-2xx HTTP status codes with a JSON or text error body.
- Breaking route or payload changes require docs and tests in the same change.

## Health

```http
GET /health
```

Returns service status, version, database size, and memory count.

## Sessions

```http
POST /sessions
PATCH /sessions/{id}
POST /sessions/{id}/end
GET /sessions/{id}/context
GET /sessions/recent
DELETE /sessions/{id}
```

`POST /sessions/{id}/end` is a legacy alias. Prefer `PATCH /sessions/{id}`.

## Prompts

```http
POST /prompts
GET /prompts/recent
GET /prompts/search?query=...
DELETE /prompts/{id}
```

## Passive Capture

```http
POST /capture/passive
POST /observe
```

Extracts structured learnings from text and saves them as curated discovery
memories. This endpoint is optional plugin support, not raw tool-call logging.
`POST /observe` stores raw session-scoped observations (events/metadata) for
later consolidation; it does not auto-promote them to authoritative memories.

## Context

```http
GET /context?project=...&scope=...
POST /pack
GET /files/history?path=...&project=...&limit=...
POST /files/context
```

`POST /pack` builds a token-budget-aware context pack from typed memories.
Set `{"explain": true}` in `POST /pack` to include score-component explain rows.
`GET /files/history` and `POST /files/context` provide file-aware retrieval for
recent bugfixes/decisions/procedures tied to a path.

## Memory Items

```http
POST /memories
GET /memories
GET /memories/search?query=...&project=...&scope=...&kind=...&domain=...&limit=...
GET /memories/{id}
PATCH /memories/{id}
GET /memories/{id}/timeline
GET /memories/{id}/revisions
DELETE /memories/{id}
```

Memory items are the current typed memory model. Older observation terminology
is legacy and should not be used by new integrations.

## Import / Export

```http
GET /export
POST /import
```

Use for local backup/restore workflows or controlled data movement. Validate
after import with `ohara validate`.

## Project Management

```http
POST /projects/migrate
```

Merges memories from an old detected project name into the new canonical name.

## Stats And Sync

```http
GET /stats
GET /sync/status
```

`/sync/status` reports local autosync phase, last error, backoff, and last sync
timestamp when a sync status provider is configured.

## Security Boundary

The API assumes a trusted local caller. Do not expose it directly to a network.
For production-like remote MCP exposure, use the dedicated remote MCP mode and
deployment guidance in [docs/remote-mcp-production.md](./remote-mcp-production.md).
