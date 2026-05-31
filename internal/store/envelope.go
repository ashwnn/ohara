package store

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	projectpkg "github.com/ashwnn/ohara/internal/project"
)

const EnvelopeSchemaVersion = 1

var trustedLocalClients = map[string]bool{
	"cli":             true,
	"mcp":             true,
	"http":            true,
	"opencode":        true,
	"mission-control": true,
}

type MemoryEnvelope struct {
	SchemaVersion int    `json:"schema_version"`
	Scope         string `json:"scope"`
	ProjectID     string `json:"project_id"`
	SessionID     string `json:"session_id,omitempty"`
	RunID         string `json:"run_id,omitempty"`
	Source        struct {
		Client    string `json:"client,omitempty"`
		Agent     string `json:"agent,omitempty"`
		Executor  string `json:"executor,omitempty"`
		WrittenBy string `json:"written_by,omitempty"`
	} `json:"source"`
	Memory struct {
		Kind           string `json:"kind"`
		Classification string `json:"classification"`
		TopicKey       string `json:"topic_key,omitempty"`
		Title          string `json:"title"`
		Content        string `json:"content"`
	} `json:"memory"`
	Evidence struct {
		CWD          string   `json:"cwd,omitempty"`
		GitRemote    string   `json:"git_remote,omitempty"`
		GitRoot      string   `json:"git_root,omitempty"`
		GitBranch    string   `json:"git_branch,omitempty"`
		GitCommit    string   `json:"git_commit,omitempty"`
		Files        []string `json:"files,omitempty"`
		Worktree     string   `json:"worktree,omitempty"`
		ExternalRefs []string `json:"external_refs,omitempty"`
	} `json:"evidence"`
	Trust struct {
		Level  string `json:"level"`
		Reason string `json:"reason,omitempty"`
	} `json:"trust"`
	Lifecycle struct {
		Status     string   `json:"status"`
		ExpiresAt  string   `json:"expires_at,omitempty"`
		Supersedes []string `json:"supersedes,omitempty"`
	} `json:"lifecycle"`
}

func normalizeKindAlias(kind string) string {
	k := strings.TrimSpace(strings.ToLower(kind))
	switch k {
	case "learned", "learning":
		return MemoryKindDiscovery
	case "preference":
		return MemoryKindUserPreference
	case "resume_state":
		return MemoryKindConfig
	default:
		return k
	}
}

func normalizeScopeAlias(scope string) string {
	s := strings.TrimSpace(strings.ToLower(scope))
	switch s {
	case "", "project":
		return MemoryScopeProject
	case "global":
		return MemoryScopeGlobal
	case "task":
		return "task"
	case "personal":
		return "personal"
	default:
		return s
	}
}

func buildEnvelopeDefaults(p *AddMemoryParams) MemoryEnvelope {
	env := MemoryEnvelope{}
	env.SchemaVersion = EnvelopeSchemaVersion
	env.Scope = normalizeScopeAlias(p.Scope)
	env.ProjectID = p.ProjectID
	env.SessionID = p.SessionID
	env.Source.Client = strings.TrimSpace(strings.ToLower(firstNonEmpty(os.Getenv("OHARA_CLIENT"), p.Source, "unknown")))
	env.Source.Agent = strings.TrimSpace(firstNonEmpty(os.Getenv("OHARA_AGENT"), p.ActorID))
	env.Source.Executor = strings.TrimSpace(firstNonEmpty(os.Getenv("OHARA_EXECUTOR"), "local"))
	env.Source.WrittenBy = strings.TrimSpace(strings.ToLower(firstNonEmpty(p.WrittenBy, os.Getenv("OHARA_WRITTEN_BY"))))
	if env.Source.WrittenBy == "" {
		switch env.Source.Client {
		case "cli":
			env.Source.WrittenBy = "user"
		case "import":
			env.Source.WrittenBy = "import"
		default:
			env.Source.WrittenBy = "agent"
		}
	}
	env.Memory.Kind = p.Kind
	env.Memory.Classification = p.Classification
	env.Memory.Title = p.Title
	env.Memory.Content = p.Body
	env.Memory.TopicKey = normalizeTopicKey(firstNonEmpty(os.Getenv("OHARA_TOPIC_KEY"), ""))

	cwd := firstNonEmpty(os.Getenv("OHARA_CWD"), ".")
	id := projectpkg.DetectIdentity(cwd)
	env.Evidence.CWD = firstNonEmpty(os.Getenv("OHARA_CWD"), id.CWD)
	env.Evidence.GitRemote = id.GitRemote
	env.Evidence.GitRoot = id.GitRoot
	env.Evidence.GitBranch = id.GitBranch
	env.Evidence.GitCommit = id.GitCommit
	env.Evidence.Worktree = firstNonEmpty(os.Getenv("OHARA_WORKTREE"), id.Worktree)

	env.Lifecycle.Status = MemoryStatusActive
	env.Lifecycle.ExpiresAt = p.ExpiresAt
	return env
}

func validateEnvelope(env MemoryEnvelope) error {
	if env.SchemaVersion <= 0 {
		return fmt.Errorf("invalid schema_version")
	}
	if env.Memory.Title == "" || env.Memory.Content == "" {
		return fmt.Errorf("title and content are required")
	}
	if !ValidMemoryKinds[env.Memory.Kind] {
		return fmt.Errorf("invalid memory kind %q", env.Memory.Kind)
	}
	validClass := map[string]bool{"foundational": true, "tactical": true, "observational": true}
	if !validClass[env.Memory.Classification] {
		return fmt.Errorf("invalid classification %q", env.Memory.Classification)
	}
	validScope := map[string]bool{MemoryScopeProject: true, MemoryScopeGlobal: true, "task": true, "personal": true}
	if !validScope[env.Scope] {
		return fmt.Errorf("invalid scope %q", env.Scope)
	}
	validTrust := map[string]bool{"high": true, "medium": true, "low": true}
	if !validTrust[env.Trust.Level] {
		return fmt.Errorf("invalid trust level %q", env.Trust.Level)
	}
	validStatus := map[string]bool{MemoryStatusActive: true, "expired": true, MemoryStatusArchived: true, MemoryStatusSuperseded: true, MemoryStatusCandidate: true}
	if !validStatus[env.Lifecycle.Status] {
		return fmt.Errorf("invalid lifecycle status %q", env.Lifecycle.Status)
	}
	return nil
}

func applyEnvelopeDefaults(p *AddMemoryParams) (MemoryEnvelope, error) {
	p.Kind = normalizeKindAlias(p.Kind)
	p.Scope = normalizeScopeAlias(p.Scope)
	if p.Scope == "" {
		p.Scope = MemoryScopeProject
	}
	if p.Classification == "" {
		p.Classification = defaultClassificationForKind(p.Kind)
	}
	env := buildEnvelopeDefaults(p)

	if env.Evidence.GitCommit != "" && trustedLocalClients[env.Source.Client] {
		env.Trust.Level = "high"
		env.Trust.Reason = "local client with git commit evidence"
	} else if env.Evidence.CWD != "" || env.Evidence.GitRoot != "" {
		env.Trust.Level = "medium"
		env.Trust.Reason = "local path or git metadata available"
	} else {
		env.Trust.Level = "low"
		env.Trust.Reason = "insufficient provenance metadata"
	}

	switch env.Source.WrittenBy {
	case "import", "consolidation":
		env.Trust.Level = "low"
		if env.Trust.Reason == "" {
			env.Trust.Reason = "imported or inferred source"
		}
	}
	if err := validateEnvelope(env); err != nil {
		return MemoryEnvelope{}, err
	}
	data, _ := json.Marshal(env)
	p.EvidenceJSON = string(data)
	if p.TrustLevel == "" {
		switch {
		case env.Trust.Level == "low":
			p.TrustLevel = "untrusted"
		case strings.EqualFold(p.Source, "tool") || strings.EqualFold(p.Source, "passive"):
			p.TrustLevel = "tool"
		default:
			p.TrustLevel = "system"
		}
	}
	p.WrittenBy = env.Source.WrittenBy
	return env, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
