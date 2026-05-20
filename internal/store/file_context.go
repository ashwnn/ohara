package store

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ashwnn/ohara/internal/token"
	"github.com/ashwnn/ohara/internal/util"
)

type FileContextResult struct {
	Path         string       `json:"path"`
	Context      string       `json:"context"`
	TokenCount   int          `json:"token_count"`
	ItemCount    int          `json:"item_count"`
	BudgetTokens int          `json:"budget_tokens"`
	MemoryItems  []MemoryItem `json:"memory_items"`
}

type scoredFileMemory struct {
	item        MemoryItem
	matchScore  int
	matchReason string
}

var pathFieldHints = map[string]bool{
	"file":         true,
	"files":        true,
	"path":         true,
	"paths":        true,
	"filepath":     true,
	"file_path":    true,
	"applies_to":   true,
	"related_file": true,
	"renamed_from": true,
	"renamed_to":   true,
}

// FileHistory returns recent memories related to a file path.
func (s *Store) FileHistory(path, projectID string, limit int) ([]MemoryItem, error) {
	if strings.TrimSpace(path) == "" {
		return []MemoryItem{}, nil
	}
	if limit <= 0 {
		limit = 10
	}
	if projectID != "" {
		projectID, _ = NormalizeProject(projectID)
	}

	candidateLimit := limit * 40
	if candidateLimit < 200 {
		candidateLimit = 200
	}
	if candidateLimit > 1200 {
		candidateLimit = 1200
	}

	sqlQ := `
		SELECT id, created_at, updated_at, project_id, actor_id, kind, scope, title, body, tags,
		       source, status, superseded_by, expires_at,
		       domain, evidence_json, applies_to_json, related_json, classification,
		       access_count, last_accessed, valid_from, valid_to, superseded_at, session_id, trust_level,
		       ingested_at, written_by,
		       trigger_condition, utility_weight, consolidated_from,
		       0 AS relevance_score
		FROM memory_items
		WHERE status = ?
		  AND (expires_at IS NULL OR expires_at = '' OR expires_at > datetime('now'))`
	args := []any{MemoryStatusActive}
	sqlQ += " AND (superseded_by IS NULL OR superseded_by = 0)"
	if projectID != "" {
		sqlQ += " AND project_id = ?"
		args = append(args, projectID)
	}
	sqlQ += " ORDER BY updated_at DESC LIMIT ?"
	args = append(args, candidateLimit)

	rows, err := s.queryItHook(s.db, sqlQ, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	normTarget := normalizeFilePath(path)
	scored := make([]scoredFileMemory, 0, candidateLimit)
	for rows.Next() {
		item, err := s.scanMemoryRow(rows)
		if err != nil {
			return nil, err
		}
		score, reason := scoreFileMatch(*item, path, normTarget)
		if score <= 0 {
			continue
		}
		scored = append(scored, scoredFileMemory{
			item:        *item,
			matchScore:  score,
			matchReason: reason,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].matchScore == scored[j].matchScore {
			if scored[i].item.UpdatedAt == scored[j].item.UpdatedAt {
				return scored[i].item.ID < scored[j].item.ID
			}
			return scored[i].item.UpdatedAt > scored[j].item.UpdatedAt
		}
		return scored[i].matchScore > scored[j].matchScore
	})

	out := make([]MemoryItem, 0, minInt(limit, len(scored)))
	for i := 0; i < len(scored) && i < limit; i++ {
		out = append(out, scored[i].item)
	}
	return out, nil
}

// FileContext builds a compact token-budgeted context pack for a specific file.
func (s *Store) FileContext(path, projectID string, budgetTokens int) (*FileContextResult, error) {
	if strings.TrimSpace(path) == "" {
		return &FileContextResult{
			Path:         path,
			Context:      "",
			TokenCount:   0,
			ItemCount:    0,
			BudgetTokens: budgetTokens,
			MemoryItems:  []MemoryItem{},
		}, nil
	}
	if budgetTokens <= 0 {
		budgetTokens = 300
	}
	if budgetTokens > 1200 {
		budgetTokens = 1200
	}

	items, err := s.FileHistory(path, projectID, 20)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return &FileContextResult{
			Path:         path,
			Context:      "",
			TokenCount:   0,
			ItemCount:    0,
			BudgetTokens: budgetTokens,
			MemoryItems:  []MemoryItem{},
		}, nil
	}

	var bugfixes, decisions, procedures, gotchas, summaries []MemoryItem
	for _, item := range items {
		switch item.Kind {
		case MemoryKindBugfix:
			bugfixes = append(bugfixes, item)
		case MemoryKindDecision, MemoryKindPattern:
			decisions = append(decisions, item)
		case MemoryKindProcedure:
			procedures = append(procedures, item)
		case MemoryKindPostmortem:
			summaries = append(summaries, item)
		default:
			gotchas = append(gotchas, item)
		}
	}

	type section struct {
		heading string
		items   []MemoryItem
	}
	sections := []section{
		{heading: "Prior bugfixes", items: bugfixes},
		{heading: "Decisions and patterns", items: decisions},
		{heading: "Known gotchas", items: gotchas},
		{heading: "Procedures", items: procedures},
		{heading: "Recent session summaries", items: summaries},
	}

	var selected []MemoryItem
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("## File Context: %s\n\n", path))

	for _, sec := range sections {
		if len(sec.items) == 0 {
			continue
		}
		sectionHeader := fmt.Sprintf("### %s\n", sec.heading)
		if token.Count(builder.String()+sectionHeader) > budgetTokens {
			break
		}
		builder.WriteString(sectionHeader)
		for _, item := range sec.items {
			line := fmt.Sprintf("- #%d **%s** (%s): %s\n",
				item.ID,
				item.Title,
				item.Kind,
				util.Truncate(item.Body, 220),
			)
			if token.Count(builder.String()+line) > budgetTokens {
				break
			}
			builder.WriteString(line)
			selected = append(selected, item)
		}
		builder.WriteString("\n")
	}

	context := strings.TrimSpace(builder.String())
	return &FileContextResult{
		Path:         path,
		Context:      context,
		TokenCount:   token.Count(context),
		ItemCount:    len(selected),
		BudgetTokens: budgetTokens,
		MemoryItems:  selected,
	}, nil
}

func scoreFileMatch(item MemoryItem, rawPath, normTarget string) (int, string) {
	best := 0
	reason := ""

	for _, p := range extractFileHints(item) {
		score := pathMatchScore(normTarget, normalizeFilePath(p))
		if score > best {
			best = score
			reason = "structured field match"
		}
	}

	lowerPath := strings.ToLower(strings.TrimSpace(rawPath))
	base := strings.ToLower(filepath.Base(rawPath))
	titleBody := strings.ToLower(item.Title + "\n" + item.Body)
	switch {
	case lowerPath != "" && strings.Contains(titleBody, lowerPath):
		if best < 35 {
			best = 35
			reason = "title/body exact path fallback"
		}
	case base != "" && strings.Contains(titleBody, base):
		if best < 18 {
			best = 18
			reason = "title/body basename fallback"
		}
	}
	return best, reason
}

func extractFileHints(item MemoryItem) []string {
	out := make([]string, 0, 16)
	seen := map[string]bool{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		key := normalizeFilePath(value)
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		out = append(out, value)
	}

	// tags: allow file:path and path-like tags
	for _, tag := range item.Tags {
		tag = strings.TrimSpace(tag)
		if strings.HasPrefix(strings.ToLower(tag), "file:") {
			add(strings.TrimSpace(tag[5:]))
			continue
		}
		if isPathLike(tag) {
			add(tag)
		}
	}

	collectPathsFromJSON(item.AppliesToJSON, add)
	collectPathsFromJSON(item.EvidenceJSON, add)
	collectPathsFromJSON(item.RelatedJSON, add)
	return out
}

func collectPathsFromJSON(raw string, add func(string)) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" || raw == "[]" {
		return
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return
	}
	walkJSONForPaths("", value, add)
}

func walkJSONForPaths(parentKey string, value any, add func(string)) {
	switch v := value.(type) {
	case string:
		if pathFieldHints[parentKey] || isPathLike(v) {
			add(v)
		}
	case []any:
		for _, e := range v {
			walkJSONForPaths(parentKey, e, add)
		}
	case map[string]any:
		for key, e := range v {
			lower := strings.ToLower(strings.TrimSpace(key))
			walkJSONForPaths(lower, e, add)
		}
	}
}

func pathMatchScore(target, candidate string) int {
	if target == "" || candidate == "" {
		return 0
	}
	targetLooksDir := !strings.Contains(filepath.Base(target), ".")
	candidateLooksDir := !strings.Contains(filepath.Base(candidate), ".")
	if target == candidate {
		return 120
	}
	if strings.HasSuffix(target, "/"+candidate) || strings.HasSuffix(candidate, "/"+target) {
		return 100
	}

	targetBase := strings.ToLower(filepath.Base(target))
	candBase := strings.ToLower(filepath.Base(candidate))
	if targetBase != "" && targetBase == candBase {
		if strings.Contains(target, "/") && strings.Contains(candidate, "/") {
			return 75
		}
		return 55
	}

	// Directory-level context
	if strings.HasSuffix(candidate, "/") && strings.HasPrefix(target, candidate) {
		return 70
	}
	if strings.HasSuffix(target, "/") && strings.HasPrefix(candidate, target) {
		return 68
	}
	if targetLooksDir {
		if candidate == target || strings.HasPrefix(candidate, target+"/") {
			return 70
		}
	}
	if candidateLooksDir {
		if target == candidate || strings.HasPrefix(target, candidate+"/") {
			return 68
		}
	}

	targetDir := strings.TrimSuffix(strings.ToLower(filepath.Dir(target)), ".")
	candDir := strings.TrimSuffix(strings.ToLower(filepath.Dir(candidate)), ".")
	if targetDir != "" && candDir != "" {
		if targetDir == candDir {
			return 22
		}
		if !targetLooksDir && !candidateLooksDir &&
			(strings.HasPrefix(targetDir, candDir+"/") || strings.HasPrefix(candDir, targetDir+"/")) {
			return 62
		}
	}

	if strings.Contains(target, candidate) || strings.Contains(candidate, target) {
		return 48
	}
	return 0
}

func normalizeFilePath(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	value = strings.TrimPrefix(value, "./")
	value = strings.TrimPrefix(value, "/")
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, "/")
	return strings.ToLower(value)
}

func isPathLike(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if strings.Contains(value, "/") {
		return true
	}
	extensions := []string{".go", ".ts", ".tsx", ".js", ".py", ".sql", ".yaml", ".yml", ".json", ".md", ".txt"}
	for _, ext := range extensions {
		if strings.HasSuffix(strings.ToLower(value), ext) {
			return true
		}
	}
	return false
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
