# Contributing to Ohara

Ohara is source-build software. Keep changes small, explicit, and easy to
review.

## Workflow

1. Describe the problem and scope in the PR body.
2. Keep the change focused to one behavior or one cleanup.
3. Run the smallest verification set that proves the change.
4. Update docs in the same PR when behavior or commands change.

## PR Expectations

Include:

- what changed
- why it changed
- files or packages touched
- commands you ran
- any migration, security, or data-handling risk

If GitHub Issues are enabled, open one first for larger changes. If not, put
the rationale directly in the PR.

## Review Standard

- Product behavior, code, tests, and docs should agree.
- New abstractions need a real coupling or ownership benefit.
- Server-side rules belong on the server, not only in clients.
- Stale files and dead paths should be removed, not documented around.

## Labels

Apply exactly one `type:*` label to each PR.

## Verification

- `go test ./...`
- benchmark-affecting changes: see [DOCS.md](DOCS.md)
- confirm examples and commands still match the repo
