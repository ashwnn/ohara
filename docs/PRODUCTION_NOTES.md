# Production Notes

## Status

Battle-tested in **local single-user contexts**. Multi-agent concurrent writes, cross-machine sync, and large-scale retrieval (10k+ items) have not been exercised in production.

## Verified Working

- SQLite WAL persistence — stable across thousands of ops
- MCP protocol — all tool calls, JSON-RPC round-trips pass tests
- Memory operations — save, search, link, graph, session lifecycle covered
- Build — single static binary, no CGO on target machine
- Auth — bearer token for remote MCP, stdio remains auth-free

## Known Limitations

- Passive capture extraction is heuristic — may miss/classify learnings
- No migration rollback tested in production (unit tests only)
- Backup/restore not validated under pressure
- Regex redaction is best-effort, not a security guarantee

## Security Properties

| Property | Detail |
|----------|--------|
| Default bind | Loopback only (`127.0.0.1`) |
| Auth | Bearer token (opt-in via `OHARA_AUTH_ENABLED=true` + `OHARA_AUTH_TOKEN`) |
| Remote MCP | Streamable HTTP at `/mcp` (opt-in via `OHARA_MCP_HTTP=true`) |
| Low-trust filtering | Read-only principals get redacted responses |
| Private tags | `<private>` stripped at plugin + store layers |

## Performance

Hardware: Intel i5-6500T @ 2.50GHz, Linux amd64, Go 1.26.2.

| Benchmark | Result |
|-----------|--------|
| Save throughput (100 items) | ~5K ops/s |
| Search p50 (100-item corpus) | ~18ms |
| Search p95 (100-item corpus) | ~25ms |
| Pack build (200 token budget) | <3ms |
| DB size per memory item | ~2-3KB |

Full metrics: `bench/README.md` and `bench/store/README.md`.
