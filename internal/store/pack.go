// Package store implements the persistent memory engine for Ohara.
// pack.go contains the context pack assembly logic (Ohara v2 spec).
package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ashwnn/ohara/internal/token"
)

const (
	defaultBudgetTokens = 400
	maxBudgetTokens     = 800
)

// BuildPack assembles a context pack from memory items within a token budget.
// Follows the Ohara v2 spec: always include global items, then project items
// up to budget.
func (s *Store) BuildPack(p PackParams) (*PackResult, error) {
	budget := p.BudgetTokens
	if budget <= 0 {
		budget = defaultBudgetTokens
	}
	if budget > maxBudgetTokens {
		budget = maxBudgetTokens
	}

	result := &PackResult{
		BudgetTokens: budget,
		MemoryItems:  []MemoryItem{},
	}

	candidates, err := s.collectPackCandidates(p)
	if err != nil {
		return nil, fmt.Errorf("build pack: collect candidates: %w", err)
	}
	if len(candidates) == 0 {
		return result, nil
	}

	scoredCandidates, err := s.scorePackCandidates(candidates, p)
	if err != nil {
		return nil, fmt.Errorf("build pack: score candidates: %w", err)
	}

	remainingBudget := budget - 20 // reserve for section headers
	globalSection := ""
	projectSection := ""

	for i := range scoredCandidates {
		cand := scoredCandidates[i]
		if cand.Item.Classification == "observational" && strings.TrimSpace(p.SessionID) == "" {
			if p.Explain {
				result.Explain = append(result.Explain, cand.Explain(false, "excluded: observational memory outside session scope"))
			}
			continue
		}

		itemSection := formatMemorySection(cand.Item)
		itemTokens := token.Count(itemSection)
		include := false
		reason := "excluded: exceeds remaining token budget"

		if itemTokens > remainingBudget && itemTokens <= remainingBudget+200 && remainingBudget > 60 {
			truncated := truncateToTokens(cand.Item.Body, remainingBudget-50)
			itemSection = formatMemorySectionWithBody(cand.Item, truncated)
			itemTokens = token.Count(itemSection)
			if itemTokens <= remainingBudget {
				include = true
				reason = "included: truncated to fit remaining budget"
			}
		} else if itemTokens <= remainingBudget {
			include = true
			reason = "included: within remaining budget"
		}

		if include {
			if cand.Item.Scope == MemoryScopeGlobal {
				globalSection += itemSection + "\n"
			} else {
				projectSection += itemSection + "\n"
			}
			result.MemoryItems = append(result.MemoryItems, cand.Item)
			result.ItemCount++
			remainingBudget -= itemTokens
		}
		if p.Explain {
			result.Explain = append(result.Explain, cand.Explain(include, reason))
		}
	}

	// Assemble final pack.
	var b strings.Builder
	if globalSection != "" {
		b.WriteString("## Global\n\n")
		b.WriteString(globalSection)
	}
	if projectSection != "" {
		b.WriteString("## Project\n\n")
		b.WriteString(projectSection)
	}

	result.Pack = strings.TrimSpace(b.String())
	result.TokenCount = token.Count(result.Pack)
	result.Truncated = result.TokenCount >= budget

	return result, nil
}

func filterPackItems(items []MemoryItem, domain, asof string) []MemoryItem {
	if domain == "" && asof == "" {
		return items
	}
	var out []MemoryItem
	for _, item := range items {
		if domain != "" && item.Domain != domain {
			continue
		}
		if asof != "" && !memoryActiveAsOf(item, asof) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func memoryActiveAsOf(item MemoryItem, asof string) bool {
	asofTime, err := time.Parse(time.RFC3339, asof)
	if err != nil {
		asofTime, err = time.Parse(time.RFC3339Nano, asof)
		if err != nil {
			return true
		}
	}
	if item.ValidFrom != nil && *item.ValidFrom != "" {
		vf, err := time.Parse(time.RFC3339Nano, *item.ValidFrom)
		if err == nil && vf.After(asofTime) {
			return false
		}
	}
	if item.ValidTo != nil && *item.ValidTo != "" {
		vt, err := time.Parse(time.RFC3339Nano, *item.ValidTo)
		if err == nil && !vt.After(asofTime) {
			return false
		}
	}
	return true
}

// formatMemorySection formats a single memory item as a section.
func formatMemorySection(item MemoryItem) string {
	tags := ""
	if len(item.Tags) > 0 {
		tags = " [" + joinStrings(item.Tags, ", ") + "]"
	}
	return fmt.Sprintf("**%s** (%s)%s\n%s",
		item.Title, item.Kind, tags, item.Body)
}

// formatMemorySectionWithBody formats a memory item with a custom (truncated) body.
func formatMemorySectionWithBody(item MemoryItem, body string) string {
	tags := ""
	if len(item.Tags) > 0 {
		tags = " [" + joinStrings(item.Tags, ", ") + "]"
	}
	return fmt.Sprintf("**%s** (%s)%s\n%s",
		item.Title, item.Kind, tags, body)
}

// joinStrings joins a slice of strings with a separator.
func joinStrings(items []string, sep string) string {
	if len(items) == 0 {
		return ""
	}
	b := strings.Builder{}
	b.WriteString(items[0])
	for i := 1; i < len(items); i++ {
		b.WriteString(sep)
		b.WriteString(items[i])
	}
	return b.String()
}

// truncateToTokens truncates text to approximately fit within targetTokens.
// Uses conservative word-based estimation.
func truncateToTokens(text string, targetTokens int) string {
	if targetTokens <= 0 {
		return ""
	}
	// Approximate: 1 token ≈ 1.35 words, so 1 word ≈ 0.74 tokens
	// We want text where token.Count(text) ≤ targetTokens
	// Start with targetTokens * 0.7 as word count approximation
	maxWords := int(float64(targetTokens) * 0.7)
	if maxWords <= 0 {
		return ""
	}

	words := strings.Fields(text)
	if len(words) <= maxWords {
		return text
	}

	truncated := strings.Join(words[:maxWords], " ")
	// Make sure it actually fits (double-check)
	for token.Count(truncated) > targetTokens && maxWords > 5 {
		maxWords--
		truncated = strings.Join(words[:maxWords], " ")
	}

	return truncated + "..."
}

// FormatPackText returns a plain-text representation of a pack result.
func FormatPackText(p *PackResult) string {
	if p == nil || p.Pack == "" {
		return "No memory context available."
	}
	var b strings.Builder
	b.WriteString("## Memory Context\n\n")
	b.WriteString(p.Pack)
	b.WriteString(fmt.Sprintf("\n\n*%d tokens | %d items*", p.TokenCount, p.ItemCount))
	if p.Truncated {
		b.WriteString(" (truncated)")
	}
	return b.String()
}

// FormatPackExplain returns pack explain/debug output with score components.
func FormatPackExplain(p *PackResult) string {
	if p == nil {
		return "No pack explain data available."
	}
	if len(p.Explain) == 0 {
		return "No pack explain data available."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Pack explain (%d evaluated, budget=%d, selected=%d):\n\n", len(p.Explain), p.BudgetTokens, p.ItemCount)
	for i, row := range p.Explain {
		status := "excluded"
		if row.Included {
			status = "included"
		}
		fmt.Fprintf(&b, "[%d] #%d %s (%s/%s) score=%.4f tokens=%d %s\n", i+1, row.MemoryID, row.Title, row.Kind, row.Scope, row.Score, row.TokenEstimate, status)
		componentKeys := []string{
			"base_retrieval_score",
			"rrf_score",
			"project_relevance",
			"session_relevance",
			"domain_relevance",
			"kind_priority",
			"classification_weight",
			"recency_boost",
			"utility_weight",
			"structural_weight",
			"relation_weight",
			"usage_weight",
			"stale_penalty",
			"superseded_penalty",
			"expiry_penalty",
			"low_trust_penalty",
			"final_score",
		}
		parts := make([]string, 0, len(componentKeys))
		for _, key := range componentKeys {
			if value, ok := row.ScoreComponents[key]; ok && value != 0 {
				parts = append(parts, fmt.Sprintf("%s=%+.4f", key, value))
			}
		}
		if len(parts) == 0 {
			parts = append(parts, "none")
		}
		fmt.Fprintf(&b, "    components: %s\n", strings.Join(parts, ", "))
		fmt.Fprintf(&b, "    reason: %s\n\n", row.Reason)
	}
	return strings.TrimSpace(b.String())
}

// PackStats holds aggregate statistics for memory items.
type PackStats struct {
	TotalMemoryItems int            `json:"total_memory_items"`
	ByKind           map[string]int `json:"by_kind"`
	ByScope          map[string]int `json:"by_scope"`
	ByStatus         map[string]int `json:"by_status"`
	ByDomain         map[string]int `json:"by_domain"`
	ByClassification map[string]int `json:"by_classification"`
	ByActor          map[string]int `json:"by_actor"`
}

// MemoryStats returns aggregate statistics for memory items.
func (s *Store) MemoryStats() (*PackStats, error) {
	stats := &PackStats{
		ByKind:           make(map[string]int),
		ByScope:          make(map[string]int),
		ByStatus:         make(map[string]int),
		ByDomain:         make(map[string]int),
		ByClassification: make(map[string]int),
		ByActor:          make(map[string]int),
	}

	// Total count
	s.db.QueryRow("SELECT COUNT(*) FROM memory_items").Scan(&stats.TotalMemoryItems)

	// By kind
	rows, err := s.queryItHook(s.db, "SELECT kind, COUNT(*) FROM memory_items GROUP BY kind")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var kind string
			var count int
			if err := rows.Scan(&kind, &count); err == nil {
				stats.ByKind[kind] = count
			}
		}
	}

	// By scope
	rows2, err := s.queryItHook(s.db, "SELECT scope, COUNT(*) FROM memory_items GROUP BY scope")
	if err == nil {
		defer rows2.Close()
		for rows2.Next() {
			var scope string
			var count int
			if err := rows2.Scan(&scope, &count); err == nil {
				stats.ByScope[scope] = count
			}
		}
	}

	// By status
	rows3, err := s.queryItHook(s.db, "SELECT status, COUNT(*) FROM memory_items GROUP BY status")
	if err == nil {
		defer rows3.Close()
		for rows3.Next() {
			var status string
			var count int
			if err := rows3.Scan(&status, &count); err == nil {
				stats.ByStatus[status] = count
			}
		}
	}

	// By domain
	rows4, err := s.queryItHook(s.db, "SELECT domain, COUNT(*) FROM memory_items WHERE domain != '' GROUP BY domain")
	if err == nil {
		defer rows4.Close()
		for rows4.Next() {
			var domain string
			var count int
			if err := rows4.Scan(&domain, &count); err == nil {
				stats.ByDomain[domain] = count
			}
		}
	}

	// By classification
	rows5, err := s.queryItHook(s.db, "SELECT classification, COUNT(*) FROM memory_items WHERE classification != '' GROUP BY classification")
	if err == nil {
		defer rows5.Close()
		for rows5.Next() {
			var class string
			var count int
			if err := rows5.Scan(&class, &count); err == nil {
				stats.ByClassification[class] = count
			}
		}
	}

	// By actor (written_by)
	rows6, err := s.queryItHook(s.db, "SELECT written_by, COUNT(*) FROM memory_items WHERE written_by != '' GROUP BY written_by")
	if err == nil {
		defer rows6.Close()
		for rows6.Next() {
			var actor string
			var count int
			if err := rows6.Scan(&actor, &count); err == nil {
				stats.ByActor[actor] = count
			}
		}
	}

	return stats, nil
}

// MarshalTags converts a JSON tags array to []string.
func MarshalTags(tags []string) string {
	data, _ := json.Marshal(tags)
	return string(data)
}
