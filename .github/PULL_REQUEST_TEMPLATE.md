<!-- 
  ⚠️ READ BEFORE SUBMITTING
  
  Every PR must:
  1. Explain the problem, scope, risk, and verification
  2. Have exactly one type:* label
  3. Pass all required automated checks
  
  See CONTRIBUTING.md for the full workflow.
-->

## Problem / Scope

<!-- Required. If an issue exists, link it. If Issues are disabled, describe the proposal here. -->

- Problem or goal:
- User-visible behavior change:
- Migration, security, or data-handling risk:

---

## 🏷️ PR Type

<!-- REQUIRED: Check exactly ONE type below, then add the matching label to the PR. -->
<!-- Automated check: "Check PR Has type:* Label" verifies the label exists. -->

- [ ] `type:bug` — Bug fix
- [ ] `type:feature` — New feature
- [ ] `type:docs` — Documentation only
- [ ] `type:refactor` — Code refactoring (no behavior change)
- [ ] `type:chore` — Maintenance, dependencies, tooling
- [ ] `type:breaking-change` — Breaking change

---

## 📝 Summary

<!-- What does this PR do? Be concise — 1-3 bullet points. -->

- 

## 📂 Changes

<!-- Key files changed and what was modified in each. -->

| File | Change |
|------|--------|
| `path/to/file` | What changed |

## 🧪 Test Plan

<!-- How did you verify this works? -->

- [ ] Unit tests pass locally: `go test ./...`
- [ ] Race tests pass locally: `go test -race ./...`
- [ ] E2E tests pass locally: `go test -tags e2e ./internal/server/...`
- [ ] Build passes locally: `go build -trimpath ./cmd/ohara`
- [ ] Vet passes locally: `go vet ./...`
- [ ] Vuln scan passes locally: `govulncheck ./...`
- [ ] Manually tested the affected functionality

<!-- Describe any manual testing steps: -->

---

## 🤖 Automated Checks

These run automatically and **all must pass** before merge:

| Check | What it verifies | Status |
|-------|-----------------|--------|
| **Check PR Has type:\* Label** | PR has exactly one `type:*` label | ⏳ |
| **Unit Tests** | `go test ./...` passes | ⏳ |
| **Race Tests** | `go test -race ./...` passes | ⏳ |
| **E2E Tests** | `go test -tags e2e ./internal/server/...` passes | ⏳ |
| **Build** | `go build -trimpath ./cmd/ohara` passes | ⏳ |
| **Vet** | `go vet ./...` passes | ⏳ |
| **Vulnerability Scan** | `govulncheck ./...` passes | ⏳ |

---

## ✅ Contributor Checklist

- [ ] I described the problem, scope, risk, and verification
- [ ] I added exactly **one** `type:*` label to this PR
- [ ] I ran unit tests locally: `go test ./...`
- [ ] I ran race tests locally: `go test -race ./...`
- [ ] I ran e2e tests locally: `go test -tags e2e ./internal/server/...`
- [ ] I ran build locally: `go build -trimpath ./cmd/ohara`
- [ ] I ran vet locally: `go vet ./...`
- [ ] I ran vuln scan locally: `govulncheck ./...`
- [ ] Docs updated (if behavior changed)
- [ ] Commits follow [conventional commits](https://www.conventionalcommits.org/) format
- [ ] No `Co-Authored-By` trailers in commits

---

## 💬 Notes for Reviewers

<!-- Optional: anything the reviewer should know — context, tradeoffs, open questions. -->
