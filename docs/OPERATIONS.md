# Operations

Local-first source-build service. Minimal runbook.

## Runtime

- Binary: `ohara serve` (loopback `127.0.0.1:7331` by default)
- Data: `~/.local/share/ohara/` (SQLite WAL mode)
- Socket mode: `ohara serve --socket /path/to/socket`

## Health

```bash
ohara check      # SQLite integrity + schema version
ohara validate   # Schema invariants (CI-safe, non-zero exit on failure)
ohara doctor     # Health analysis (--fix to auto-repair)
```

## Backups

```bash
ohara backup                # Immediate snapshot
ohara maintain              # Full maintenance (archive/backup/integrity)
ohara maintain --dry-run    # Preview without writes
```

Retention config in `~/.local/share/ohara/config.json`:
```json
{ "snapshot_dir": "~/.local/share/ohara/snapshots", "retain_snapshots": 7 }
```

## Lifecycle (Forgetting/Decay)

The maintenance lifecycle automatically decays old, unaccessed memories and archives stale candidates:

### What happens during lifecycle

| Operation | Effect | Protection |
|-----------|--------|------------|
| Utility decay | Reduces `utility_weight` of active memories older than 90 days by 0.9x | Foundational memories never decay |
| Stale candidate archive | Archives candidate-status memories older than 30 days (never reviewed) | N/A |
| Low-utility archive | Archives active memories whose utility_weight fell below 0.05 | Foundational memories never archived |

### Manual trigger

```bash
ohara maintain                        # Full maintenance (includes lifecycle)
ohara maintain --dry-run              # Preview without writes
```

### Programmatic access (Go SDK)

The `maintain.Scheduler` struct can be started programmatically:

```go
sched := maintain.NewScheduler(maintain.SchedulerConfig{
    DB:       store,
    Options:  maintain.DefaultOptions(dataDir),
    Interval: 60 * time.Minute,
})
sched.Start()
// ... later
sched.Stop()
```

Or trigger a single lifecycle run:

```go
decayed, stale, err := maintain.RunLifecycle(db, opts)
```

## Restore

1. Stop server. 2. Copy aside current data dir. 3. Replace DB with snapshot. 4. Start server. 5. Run `ohara check && ohara validate`.

## Migrations

Run automatically when store opens. Always `ohara backup` before pulling large schema changes.

## Service Mode (systemd)

```bash
mkdir -p ~/.config/systemd/user
cp systemd/ohara.service ~/.config/systemd/user/
systemctl --user daemon-reload && systemctl --user enable --now ohara.service
journalctl --user -u ohara.service -f
```

Maintenance timer: `systemd/ohara-maintain.service` (copy + start same way).

## Remote MCP

Remote MCP exposes memory tools over HTTP at `/mcp` (Streamable HTTP transport, ChatGPT-compatible). Stdio MCP continues working without auth — enabling remote mode does not break existing local agents.

### Mode Model

| Mode | Config | Use Case |
|------|--------|----------|
| Local stdio | `ohara mcp` (default) | Local MCP clients (OpenCode, Claude Code, Gemini CLI) |
| Remote readonly | `OHARA_MCP_ACCESS_MODE=readonly` | ChatGPT Web, low-trust integrations; safe read tools only |
| Remote full | `OHARA_MCP_ACCESS_MODE=full` | Trusted/private environments; includes write/admin tools |

### Auth

```bash
# Generate a strong token
OHARA_MCP_BEARER_TOKEN=$(openssl rand -hex 32)

# Enable auth + remote MCP
export OHARA_MCP_REMOTE_ENABLE=1
export OHARA_MCP_TRANSPORT=streamable-http
export OHARA_MCP_BIND_ADDR=127.0.0.1:7331
export OHARA_MCP_AUTH_MODE=bearer
export OHARA_MCP_REQUIRE_AUTH=1
export OHARA_MCP_BEARER_TOKEN=$OHARA_MCP_BEARER_TOKEN

ohara serve
```

Token comparison is constant-time (`crypto/subtle`). `/health` and `/ready` bypass auth. Legacy env vars (`OHARA_MCP_HTTP`, `OHARA_AUTH_ENABLED`, `OHARA_AUTH_TOKEN`) are still recognized.

### Key Env Vars

| Var | Purpose |
|-----|---------|
| `OHARA_MCP_REMOTE_ENABLE` | Enable remote MCP (0/1) |
| `OHARA_MCP_TRANSPORT` | `stdio` (default), `http`, `sse`, `streamable-http` |
| `OHARA_MCP_AUTH_MODE` | `off`, `bearer`, or `oauth` (not implemented) |
| `OHARA_MCP_ACCESS_MODE` | `readonly` or `full` |
| `OHARA_MCP_BEARER_TOKEN_FILE` | Token file path (preferred over env var) |
| `OHARA_MCP_TRUST_LEVEL` | `low` (redacted responses) or `trusted` |
| `OHARA_MCP_ALLOWED_ORIGINS` | CORS origins for web clients |

### Validation

```bash
# Health is always open
curl http://127.0.0.1:7331/health

# Without auth → 401
curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:7331/mcp

# With auth → 200
curl -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' \
  http://127.0.0.1:7331/mcp
```

### Reverse Proxy (nginx)

```nginx
location /mcp {
  proxy_pass http://127.0.0.1:7331/mcp;
  proxy_http_version 1.1;
  proxy_set_header Authorization $http_authorization;
}
```

For SSE transport, proxy `/mcp/sse` and `/mcp/message` separately (no buffering on SSE).

## Security

- Server binds loopback by default. Do not expose directly to network.
- Prefer Unix socket for single-user setups.
- Use `<private>...</private>` for content that must never persist.
- Regex redaction is best-effort, not a secret management system.
- Remote MCP: always enable auth. Disable when not needed.
