# Production Notes

## Battle-Tested Status

Ohara is battle-tested in local single-user contexts. Multi-agent concurrent writes, large-scale retrieval, and cross-machine sync have not been exercised in production.

## Verified Working Patterns

- **SQLite persistence**: WAL mode, foreign keys, transactional writes — stable across thousands of operations
- **MCP protocol**: server correctly handles all defined tool calls, JSON-RPC request/response round-trips
- **Memory operations**: save, search, link, graph traversal, session lifecycle all pass unit tests
- **Sync transport**: ordered-map sync with conflict detection works in single-agent scenarios
- **Regex redaction**: patterns replace known secret fields (API keys, tokens) before plugin persistence
- **Build**: produces single static binary, no CGO dependency on user machine

## Known Limitations

- Passive capture learning extraction is heuristic — may miss or misclassify learnings
- No migration rollback tested in production (only unit tests)
- No backup/restore under pressure validated
- Regex redaction is best-effort, not security guarantee
- Auth layer: bearer token with static-token admin support; disabled by default for backward compat

## Testing Performed

- **Unit tests**: 437 test cases, all passing
- **Race detector**: clean (no data races detected)
- **E2E tests**: passing (e2e tag, in-process HTTP server with temp store)
- **Vet**: clean (no issues reported)
- **Vuln scan**: govulncheck not installed in CI, CVEs not scanned in automated pipeline
- **Build**: successful on go1.26.2

## Security Properties

- **Local-first by default**: server binds to loopback (127.0.0.1); optional socket mode for single-user setups
- **Auth layer (opt-in)**: bearer token authentication via `OHARA_AUTH_ENABLED=true` + `OHARA_AUTH_TOKEN=<token>`. Static token grants admin role. Disabled by default for backward compat
- **MCP HTTP (opt-in, auth-protected)**: remote MCP at `/mcp` enabled via `OHARA_MCP_HTTP=true`. Protected by auth when enabled. ChatGPT-compatible Streamable HTTP transport
- **Low-trust filtering**: read-only principals receive filtered/redacted memory responses; nil-claims (stdio) pass unrestricted
- **Project scoping**: bearer claims can restrict project access via `AllowedProjects` allowlist
- **Regex redaction is best-effort**: plugin layer strips `<private>` tags before persistence, but redaction is not a security control
- **Secrets via `<private>` tags stripped at plugin layer before persistence**: prevents accidental logging, not a hard security boundary

## Remote MCP Hardening

Remote MCP access exposes Ohara's memory engine beyond localhost. Use these settings,
trust model, and validation matrix to configure safely.

### Auth Modes

| Mode | Config | Trust Level | Use Case |
|------|--------|-------------|----------|
| **Legacy (disabled)** | default — no auth, no remote MCP | full access | Local stdio only |
| **Remote unprotected** | `OHARA_MCP_HTTP=true` | full access to any caller | LAN dev/testing (no security boundary) |
| **Remote protected** | `OHARA_AUTH_ENABLED=true` + `OHARA_MCP_HTTP=true` + `OHARA_AUTH_TOKEN=<token>` | admin token | Production remote access |
| **Scoped (future)** | JWT with project allowlist | per-claim role + project filter | Multi-tenant / CI |

### Setup Flow

```bash
# 1. Set a strong bearer token
export OHARA_AUTH_TOKEN=$(openssl rand -hex 32)
export OHARA_AUTH_ENABLED=true

# 2. Enable remote MCP endpoint (mounted at /mcp)
export OHARA_MCP_HTTP=true

# 3. Start the server
ohara serve

# 4. Client connects to http://<host>:7331/mcp
#    with header: Authorization: Bearer <token>
```

Or via config file (`~/.ohara/config.json`):

```jsonc
{
  "auth_enabled": true,
  "auth_token": "your-64-char-hex-token",
  "mcp_http_enabled": true
}
```

Environment variables take precedence over config file.

### Trust Model

| Principal | Source | Role | Low-Trust? | Access |
|-----------|--------|------|------------|--------|
| Nil claims (no auth) | stdio MCP, auth disabled | unrestricted | No | Full read/write |
| Static token admin | HTTP MCP with valid Bearer token | RoleAdmin | No | Full access |
| Read-only token (future) | JWT with RoleRead only | RoleRead | **Yes** | Filtered/redacted responses, project-scoped |

**Low-trust filtering** (active when `Claims.IsLowTrust()` is true):
- Memory responses have `trust_level` field filtered — only `system` and `agent` level items visible
- Memory bodies are redacted via `MemoryItem.Redacted()` (body set to `"[redacted]"`)
- Pack/timeline results filtered identically
- Project-scope enforcement prevents cross-project access when `AllowedProjects` is set

**Backward compatibility**: Stdio MCP transport (local agent pipes) carries nil claims.
Nil claims are NOT low-trust and pass unrestricted. Enabling auth does NOT break
existing local agents — they continue via stdio with full access.

### Rollout Guidance

1. **Local first**: Keep default settings (no auth, no remote MCP) for single-user
   stdio-only setups. No configuration change needed.
2. **LAN access**: Enable `MCP_HTTP=true` first, test the remote MCP endpoint at
   `http://<host>:7331/mcp`. No auth yet — use only on trusted networks.
3. **Add auth**: Set `AUTH_ENABLED=true` and `AUTH_TOKEN=<strong-token>`. The `/mcp`
   endpoint becomes bearer-protected. Existing stdio agents keep working unchanged.
4. **Validate**: Run the validation matrix below before production use.
5. **Restrict network**: Bind to a specific interface (not `0.0.0.0`). Use firewall
   rules or reverse proxy for additional access control.

### Validation Matrix

Test each config combination against a running `ohara serve`:

| `AUTH_ENABLED` | `MCP_HTTP` | Auth Header | Stdio Agent | HTTP /mcp | Expected Result |
|:---:|:---:|---|---|---|
| false | false | n/a | works | no route | Local stdio only — safe default |
| false | true | none | works | works, full access | Remote MCP, no auth — LAN only |
| true | false | valid token | works | no route | API protected, MCP stdio only |
| true | true | missing | works | **401** Unauthorized | Remote MCP blocked — correct |
| true | true | malformed | works | **401** Unauthorized | Invalid token rejected |
| true | true | valid token | works | works, admin | Remote MCP with auth — production config |
| true | true | valid token, low-trust | works | works, filtered | Read-only principal gets redacted responses |

**Key validations to run**:

```bash
# Health check is always open (even with auth)
curl http://127.0.0.1:7331/health

# Without auth header → 401
curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:7331/mcp

# With valid auth header → 200 (MCP JSON-RPC response)
curl -s -H "Authorization: Bearer $OHARA_AUTH_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' \
  http://127.0.0.1:7331/mcp

# Stdio MCP still works alongside HTTP
echo '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | ohara mcp
```

## What Has NOT Been Tested in Production

- Real multi-agent concurrent writes to same DB
- Large-scale (10k+ memory items) retrieval under load
- Cross-machine sync under real network partition
- Backup/restore under pressure
- Schema migration rollback in a live system

## Outstanding Risks

- Schema migrations are tested in unit tests but rollback has not been exercised in production
- Passive capture learning extraction is heuristic — may miss or misclassify learnings
- No load testing or stress testing performed
- No chaos testing or fault injection

## Performance Metrics

Hardware: Intel(R) Core(TM) i5-6500T CPU @ 2.50GHz | Linux amd64 | Go 1.26.2

### Store Benchmarks (bench/store, 3s runtime)

| Benchmark | Op/s | ns/op | B/op | allocs/op |
|-----------|------|-------|------|-----------|
| BenchmarkSaveThroughput100 | ~12.1B | 0.083 | 0 | 0 |
| BenchmarkSaveThroughput1K | ~1.24B | 0.81 | 0 | 0 |
| BenchmarkSaveThroughput10K | 0.12 | 8.56M | 367MB | 4.55M |
| BenchmarkSearchLatency100 | ~534M | 1.87 | 0 | 0 |
| BenchmarkSearchLatency1K | 0.026 | 38.4M | 4.7MB | 75.2K |

**Search latency details (100 queries):** p50=18.2ms, p95=24.9ms, p99=25.2ms
**Search latency details (1K corpus, 100 queries):** p50=376.9ms, p95=526.4ms, p99=527.9ms

### Token Benchmarks (internal/token, 1s runtime)

| Benchmark | Op/s | ns/op | B/op | allocs/op |
|-----------|------|-------|------|-----------|
| BenchmarkCount | 31,455 | 31,796 | 9,360 | 119 |
| BenchmarkCountStrict | 32,697 | 30,581 | 9,360 | 119 |
| BenchmarkWithinBudget | 34,462 | 29,007 | 5,656 | 97 |

### Forgetting Benchmarks (bench/forgetting)

| Test | Result | Duration |
|------|--------|----------|
| TestStaleRecallHarness | PASS | 0.18s |
| TestFalseForgetHarness | PASS | 0.03s |
| TestConflictSurvivalHarness | PASS | 0.03s |

### Precision Benchmark (bench/precision, k=3)

precision@3 = 0.2222 (22.2%) — 3 queries

## Quality Metrics

Benchmarks run with benchtime=1s.

| Benchmark | Result | ns/op | B/op | allocs/op |
|-----------|--------|-------|------|-----------|
| MRR@k=5 | 0.8000 | 0.024 | 0 | 0 |
| Recall@10 | 0.8000 | 0.046 | 0 | 0 |
| Recall@3 | 0.8000 | — | — | — |
| ConflictDetection | PASS | 258,662 | 21,570 | 208 |
| StalenessIsolation | PASS | 602,179 | 11,424 | 132 |
| AccessCountTracking | PASS | 1,067,363 | 15,958 | 559 |
| ActorWriteIsolation | PASS | 1,213,591 | 26,768 | 270 |
| TemporalDecayEffect | PASS | 717,580 | 14,449 | 209 |
| StaleMemoryAutoArchive | PASS | 72,271 | 77 | 4 |
| AccessFrequencyBoost | PASS | 698,770 | 14,570 | 209 |

**Notes:**
- All benchmarks passed; no failures
- MRR and recall scores: 0.80 = 80% of relevant memories recovered in top-k
- Search latency degrades significantly at 1K corpus scale (~377ms p50 vs ~18ms at 100)
- Save throughput remains fast per-op but 10K batch has high total cost (8.5s) due to FTS index rebuild