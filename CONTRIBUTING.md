# Contributing to Ohara

Thanks for contributing. Ohara is a source-build project. GitHub Issues may be
disabled, so contribution review is **proposal-first**: describe the problem and
scope in the PR body before implementation review.

---

## Contribution Workflow

```
Discuss scope → Open PR with rationale → Add type:* label → Review & Merge
```

### Step 1: Describe the Change

If Issues are enabled, open an issue first. If Issues are disabled, include this
in the PR body:

- Problem or goal
- User-visible behavior change
- Files/packages touched
- Verification commands run
- Any migration, security, or data-handling risk

### Step 2: Open a Pull Request

1. Fork the repo and create a branch from `main`
2. Implement one focused change
3. Open a PR with the rationale and verification notes
4. Add exactly **one `type:*` label** to the PR (see label system below)

### Step 3: Automated PR Checks

CI checks run automatically on every PR:

#### PR Validation

| Check | What it verifies |
|-------|-----------------|
| **Check PR Has type:* Label** | PR has exactly one `type:*` label |

#### CI Tests

| Check | What it runs |
|-------|-------------|
| **Unit Tests** | `go test ./...` — all tests except those tagged with `//go:build e2e` |
| **Race Tests** | `go test -race ./...` |
| **E2E Tests** | `go test -tags e2e ./internal/server/...` — end-to-end integration tests |
| **Build** | `go build -trimpath ./cmd/ohara` |
| **Vet** | `go vet ./...` |
| **Vulnerability Scan** | `govulncheck ./...` |

All required checks must pass before a PR can be merged.

> **Repo admin note:** Set these as required status checks in branch protection rules for `main`: `Unit Tests`, `Race Tests`, `E2E Tests`, `Build`, `Vet`, `Vulnerability Scan`, and `PR Validation`.

---

## Label System

### Type Labels (required on every PR — pick exactly one)

| Label | Color | Use for |
|-------|-------|---------|
| `type:bug` | 🔴 | Bug fixes |
| `type:feature` | 🔵 | New features |
| `type:docs` | 🔵 | Documentation-only changes |
| `type:refactor` | 🟣 | Code refactoring with no behavior change |
| `type:chore` | ⚪ | Maintenance, tooling, dependencies |
| `type:breaking-change` | 🔴 | Breaking changes (requires major version bump) |

### Status Labels (set by maintainers)

| Label | Meaning |
|-------|---------|
| `status:needs-review` | Awaiting maintainer review |
| `status:approved` | Approved for implementation or merge |
| `status:in-progress` | Actively being worked on — auto-exempt from stale bot |
| `status:blocked` | Blocked by another issue or external dependency |
| `status:stale` | No activity for 30 days — auto-applied by stale bot |
| `status:wontfix` | Intentionally not fixing — applied when closing stale/rejected items |

### Priority Labels (set by maintainers)

`priority:high`, `priority:medium`, `priority:low`

> If Issues are disabled, use priority/effort labels only on PRs.

### Effort Labels (set by maintainers, for contributor guidance)

| Label | Meaning |
|-------|---------|
| `effort:small` | < 1 hour — good starting point for new contributors |
| `effort:medium` | 1–4 hours |
| `effort:large` | > 4 hours or spans multiple files |

---

## PR Rules

- Keep PR scope focused — one logical change per PR
- Use [conventional commits](https://www.conventionalcommits.org/) format
- Ensure all tests pass locally before pushing:
  - Unit: `go test ./...`
  - E2E: `go test -tags e2e ./internal/server/...`
- Update docs in the same PR when behavior changes
- Do not reference endpoints/scripts that do not exist in code
- Do not include `Co-Authored-By` trailers in commits

### Conventional Commit Format

```
<type>(<scope>): <short description>

[optional body]

[optional footer]
```

**Examples:**

```
feat(cli): add --json flag to session list command

fix(store): prevent duplicate observation insert on retry

docs(contributing): add label system documentation

refactor(internal): extract search query sanitizer

chore(deps): bump github.com/charmbracelet/bubbletea to v0.26

fix!: change session ID format (breaking change)
BREAKING CHANGE: session IDs are now UUIDs instead of integers
```

Types map to labels: `feat` → `type:feature`, `fix` → `type:bug`, `docs` → `type:docs`, `refactor` → `type:refactor`, `chore` → `type:chore`.

---

## Skill Authoring Standard

Repository skills live in `skills/`.

Use a **hybrid format**:

1. Structured base (purpose, when to use, critical rules, checklists)
2. Cookbook section (`If / Then / Example`) for repetitive actions

Why hybrid:
- Structured base protects correctness and architecture intent
- Cookbook improves execution consistency for common flows

---

## Maintainer Triage Cadence

Ohara uses a lightweight, regular cadence so contributors know what to expect.

| Activity | Frequency | What Happens |
|----------|-----------|-------------|
| New issue triage | Within 2 days | Maintainer labels + approves or closes |
| PR review | Within 7 days | Maintainer reviews + requests changes or merges |
| Backlog sweep | Weekly (Monday) | Stale bot runs; approved/blocked issues reassessed |
| Label audit | Monthly | Orphan labels removed; accuracy check |
| Dependabot PRs | Weekly | Review merged or deferred |

If you haven't received a response within 7 days on a PR or issue, a single ping comment is welcome.

---

## What Gets Closed Without Merging

- PRs that fail CI and aren't updated within 30 days
- PRs without a clear problem statement and verification notes
- Issues or PRs that are vague, duplicate, or belong in Discussions
- Issues or PRs with no response to a maintainer question after 14 days

---

## Agent Skill Linking

Run:

```bash
./setup.sh
```

This links repo `skills/*` into project-local:
- `.claude/skills/*`
- `.codex/skills/*`
- `.gemini/skills/*`
