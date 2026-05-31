package project

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const gitTimeout = 2 * time.Second

type Identity struct {
	ProjectID string `json:"project_id"`
	CWD       string `json:"cwd,omitempty"`
	GitRoot   string `json:"git_root,omitempty"`
	GitRemote string `json:"git_remote,omitempty"`
	GitBranch string `json:"git_branch,omitempty"`
	GitCommit string `json:"git_commit,omitempty"`
	Worktree  string `json:"worktree,omitempty"`
}

func DetectIdentity(cwd string) Identity {
	id := Identity{CWD: cwd}
	if cwd == "" {
		id.ProjectID = "unknown-" + shortHash("unknown")
		return id
	}
	id.GitRoot = gitOut(cwd, "rev-parse", "--show-toplevel")
	id.GitRemote = gitOut(cwd, "remote", "get-url", "origin")
	id.GitBranch = gitOut(cwd, "rev-parse", "--abbrev-ref", "HEAD")
	id.GitCommit = gitOut(cwd, "rev-parse", "HEAD")
	id.Worktree = gitOut(cwd, "rev-parse", "--git-common-dir")

	switch {
	case id.GitRemote != "":
		repo := extractRepoName(id.GitRemote)
		id.ProjectID = slug(repo) + "-" + shortHash(normalizeRemote(id.GitRemote))
	case id.GitRoot != "":
		base := filepath.Base(id.GitRoot)
		id.ProjectID = slug(base) + "-" + shortHash(strings.ToLower(filepath.Clean(id.GitRoot)))
	default:
		base := filepath.Base(cwd)
		id.ProjectID = slug(base) + "-" + shortHash(strings.ToLower(filepath.Clean(cwd)))
	}
	return id
}

func gitOut(cwd string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", cwd}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func normalizeRemote(v string) string {
	s := strings.TrimSpace(strings.ToLower(v))
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimRight(s, "/")
	return s
}

func shortHash(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])[:8]
}

func slug(v string) string {
	s := strings.ToLower(strings.TrimSpace(v))
	if s == "" {
		return "unknown"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		isAlnum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlnum {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "unknown"
	}
	return out
}
