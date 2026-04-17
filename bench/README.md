# Bench Harness

This directory contains lightweight evaluation harnesses for retrieval quality.

## Precision@k

Run:

```bash
go run ./bench/precision -k 3
```

The command seeds a temporary Ohara store with fixed fixtures and reports
`precision@k` over a small deterministic query set.

## Forgetting Quality

Run:

```bash
go test ./bench/forgetting -v
```

Coverage includes:

- stale recall prevention
- false forget prevention for foundational memories
- conflict-survival relation integrity after forget operations
