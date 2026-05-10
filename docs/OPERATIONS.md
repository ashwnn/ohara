[← Back to README](../README.md)

# Operations Runbook

Ohara is a local-first source-build service. Production readiness here means a
repeatable source build, safe local defaults, recoverable data, and visible
health checks.

## Runtime Model

- Binary: built locally from `./cmd/ohara`
- Server: `ohara serve`
- Default data directory: `~/.local/share/ohara`
- Default database: SQLite under the data directory
- Default network exposure: loopback only (`127.0.0.1`)
- Optional local socket: `OHARA_SOCKET`

## Health Checks

```bash
ohara check
ohara validate
ohara doctor
```

- `check` runs SQLite integrity checks and reports schema version.
- `validate` verifies schema/data invariants and is suitable for CI.
- `doctor` reports health issues; `doctor --fix` may modify local data.

## Backups

Create an immediate snapshot:

```bash
ohara backup
```

Run full maintenance, including archive/backup/integrity tasks:

```bash
ohara maintain
```

Preview maintenance without writes:

```bash
ohara maintain --dry-run
```

Configure snapshot retention in `~/.local/share/ohara/config.json`:

```jsonc
{
  "snapshot_dir": "~/.local/share/ohara/snapshots",
  "retain_snapshots": 7
}
```

## Restore

1. Stop `ohara serve` or the `ohara.service` user unit.
2. Copy the current data directory aside for safety.
3. Replace the database with the chosen snapshot.
4. Start Ohara again.
5. Run `ohara check` and `ohara validate`.

Do not restore over a running server.

## Migrations

Migrations run when the store opens. Before pulling a large schema change:

```bash
ohara backup
```

If validation fails after upgrade, stop the server and restore the pre-upgrade
snapshot.

## Service Mode

User service files live in `systemd/`.

```bash
mkdir -p ~/.config/systemd/user
cp systemd/ohara.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now ohara.service
journalctl --user -u ohara.service -f
```

Maintenance service files are also provided:

```bash
cp systemd/ohara-maintain.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user start ohara-maintain.service
```

## Security Operations

- Keep `ohara serve` local. The server is designed for local agent clients, not
  Internet exposure.
- Prefer Unix socket mode for single-user local setups.
- Do not sync `.ohara/` chunks containing sensitive project memory to public
  repositories.
- Use `<private>...</private>` for content that must never be persisted.
- Treat regex redaction as best effort, not a secret-management system.

### Remote MCP Access

Remote MCP exposes memory tools over HTTP at `/mcp`. Enable only when needed:

```bash
# Generate a strong token
OHARA_AUTH_TOKEN=$(openssl rand -hex 32)

# Enable auth + remote MCP
export OHARA_AUTH_ENABLED=true
export OHARA_AUTH_TOKEN=$OHARA_AUTH_TOKEN
export OHARA_MCP_HTTP=true
ohara serve
```

The `/mcp` endpoint uses Streamable HTTP transport (ChatGPT-compatible).
Stdio MCP continues working without auth — enabling remote access does not
break existing local agents. See PRODUCTION_NOTES.md for the full trust model
and validation matrix.

## Debugging Plugin Writes

For OpenCode plugin diagnostics:

```bash
OHARA_DEBUG=1 opencode
```

This logs failed plugin HTTP calls to stderr without changing memory behavior.
