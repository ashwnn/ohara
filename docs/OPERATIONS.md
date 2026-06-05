[← Back to README](../README.md)

# Operations

Ohara is designed for local, source-built operation.

## Runtime Defaults

- service command: `ohara serve`
- bind address: `127.0.0.1:7331`
- data directory: `~/.local/share/ohara`
- optional socket: `OHARA_SOCKET`

## Health And Validation

```bash
ohara check
ohara validate
ohara doctor
```

`doctor --fix` may modify local data to repair known issues.

## Backup And Maintenance

```bash
ohara backup
ohara maintain
ohara export
ohara import
ohara sync
ohara sync --import
```

Use backups for snapshots and sync for portable project-memory mirrors. For the
broader reference surface, including API and integration details, see
[DOCS.md](../DOCS.md).
