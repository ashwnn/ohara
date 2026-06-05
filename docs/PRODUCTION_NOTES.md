# Production Notes

Ohara is intended for local, single-user or tightly controlled source-built
use. That is the support boundary to assume unless the code and tests prove
more.

## Current Position

- SQLite-backed persistence and local recovery workflows are the primary target
- remote multi-tenant deployment is not the default operating model
- passive capture is heuristic and not authoritative on its own
- redaction is best-effort hygiene, not a security boundary

## Verification Standard

```bash
go test ./...
ohara validate
ohara doctor
```

For the detailed reference surface, see [DOCS.md](../DOCS.md).
