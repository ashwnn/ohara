package store

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/ashwnn/ohara/internal/token"
)

const (
	packGlobalCandidateLimit  = 80
	packProjectCandidateLimit = 140
	packSessionCandidateLimit = 60
)

type scoredPackCandidate struct {
	Item          MemoryItem
	Score         float64
	TokenEstimate int
	Components    map[string]float64
}

func (c scoredPackCandidate) Explain(included bool, reason string) PackExplainEntry {
	return PackExplainEntry{
		MemoryID:        c.Item.ID,
		Title:           c.Item.Title,
		Kind:            c.Item.Kind,
		Scope:           c.Item.Scope,
		Classification:  c.Item.Classification,
		Score:           c.Score,
		ScoreComponents: c.Components,
		TokenEstimate:   c.TokenEstimate,
		Included:        included,
		Reason:          reason,
	}
}

func (s *Store) collectPackCandidates(p PackParams) ([]MemoryItem, error) {
	merged := map[int64]MemoryItem{}

	globalItems, err := s.GetMemories("", MemoryScopeGlobal, "", MemoryStatusActive, packGlobalCandidateLimit)
	if err != nil {
		return nil, err
	}
	for _, item := range filterPackItems(globalItems, p.Domain, p.Asof) {
		merged[item.ID] = item
	}

	if p.ProjectID != "" {
		projectItems, err := s.GetMemories(p.ProjectID, MemoryScopeProject, "", MemoryStatusActive, packProjectCandidateLimit)
		if err != nil {
			return nil, err
		}
		for _, item := range filterPackItems(projectItems, p.Domain, p.Asof) {
			merged[item.ID] = item
		}
	}

	if p.SessionID != "" {
		sessionItems, err := s.getSessionPackItems(p.ProjectID, p.SessionID, packSessionCandidateLimit)
		if err != nil {
			return nil, err
		}
		for _, item := range filterPackItems(sessionItems, p.Domain, p.Asof) {
			merged[item.ID] = item
		}
	}

	out := make([]MemoryItem, 0, len(merged))
	for _, item := range merged {
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].UpdatedAt > out[j].UpdatedAt
	})
	return out, nil
}

func (s *Store) getSessionPackItems(projectID, sessionID string, limit int) ([]MemoryItem, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = packSessionCandidateLimit
	}

	query := `SELECT id, created_at, updated_at, project_id, actor_id, kind, scope,
		       title, body, tags, source, status, superseded_by, expires_at,
		       domain, evidence_json, applies_to_json, related_json, classification,
		       access_count, last_accessed, valid_from, valid_to, superseded_at, session_id, trust_level,
		       ingested_at, written_by, trigger_condition, utility_weight, consolidated_from,
		       0 AS relevance_score
		FROM memory_items
		WHERE status = ?
		  AND session_id = ?
		  AND (expires_at IS NULL OR expires_at = '' OR expires_at > strftime('%Y-%m-%dT%H:%M:%f','now'))`
	args := []any{MemoryStatusActive, sessionID}
	if strings.TrimSpace(projectID) != "" {
		query += ` AND project_id = ?`
		args = append(args, projectID)
	}
	query += ` ORDER BY updated_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.queryItHook(s.db, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]MemoryItem, 0, limit)
	for rows.Next() {
		item, err := s.scanMemoryRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (s *Store) scorePackCandidates(items []MemoryItem, p PackParams) ([]scoredPackCandidate, error) {
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	structuralWeight, relationWeight, err := s.packStructuralSignals(ids)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	scored := make([]scoredPackCandidate, 0, len(items))
	for _, item := range items {
		score, components := scorePackItem(item, p, structuralWeight[item.ID], relationWeight[item.ID], now)
		scored = append(scored, scoredPackCandidate{
			Item:          item,
			Score:         score,
			TokenEstimate: token.Count(formatMemorySection(item)),
			Components:    components,
		})
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score == scored[j].Score {
			if scored[i].Item.UpdatedAt == scored[j].Item.UpdatedAt {
				return scored[i].Item.ID < scored[j].Item.ID
			}
			return scored[i].Item.UpdatedAt > scored[j].Item.UpdatedAt
		}
		return scored[i].Score > scored[j].Score
	})
	return scored, nil
}

func scorePackItem(item MemoryItem, p PackParams, entityWeight, relationWeight float64, now time.Time) (float64, map[string]float64) {
	components := map[string]float64{
		"base_retrieval_score":   0,
		"rrf_score":              0,
		"project_relevance":      0,
		"session_relevance":      0,
		"domain_relevance":       0,
		"kind_priority":          0,
		"classification_weight":  0,
		"recency_boost":          0,
		"utility_weight":         0,
		"structural_weight":      0,
		"relation_weight":        0,
		"temporal_overlap_boost": 0,
		"usage_weight":           0,
		"stale_penalty":          0,
		"superseded_penalty":     0,
		"expiry_penalty":         0,
		"low_trust_penalty":      0,
		"final_score":            0,
	}

	if p.ProjectID != "" {
		if item.ProjectID == p.ProjectID {
			components["project_relevance"] = 0.26
		} else if item.Scope == MemoryScopeGlobal {
			components["project_relevance"] = 0.14
		} else {
			components["project_relevance"] = -0.10
		}
	}

	if p.SessionID != "" && item.SessionID == p.SessionID {
		components["session_relevance"] = 0.36
	}
	if p.Domain != "" {
		if item.Domain == p.Domain {
			components["domain_relevance"] = 0.16
		} else if item.Domain != "" {
			components["domain_relevance"] = -0.06
		}
	}

	components["kind_priority"] = packKindPriority(item.Kind)
	classificationWeight := packClassificationWeight(item.Classification)
	if p.SessionID != "" && item.SessionID == p.SessionID && item.Classification == "observational" {
		classificationWeight = 0
	}
	components["classification_weight"] = classificationWeight
	components["utility_weight"] = clamp(item.UtilityWeight*0.04, -0.06, 0.16)
	components["structural_weight"] = clamp(entityWeight, 0, 0.16)
	components["relation_weight"] = clamp(relationWeight, -0.06, 0.14)
	components["temporal_overlap_boost"] = temporalOverlapBoost(item, p.Asof)
	components["usage_weight"] = packUsageWeight(item, now)
	components["recency_boost"] = packRecencyBoost(item.UpdatedAt, now)
	components["stale_penalty"] = packStalePenalty(item.UpdatedAt, now)
	components["expiry_penalty"] = packExpiryPenalty(item, now)
	components["superseded_penalty"] = packSupersededPenalty(item)
	components["low_trust_penalty"] = packTrustPenalty(item.TrustLevel)
	components["base_retrieval_score"] = components["project_relevance"] + components["session_relevance"] + components["domain_relevance"]

	score := 0.0
	for key, value := range components {
		if key == "final_score" {
			continue
		}
		score += value
	}
	components["final_score"] = score
	return score, components
}

func packKindPriority(kind string) float64 {
	switch kind {
	case MemoryKindIdentity:
		return 0.26
	case MemoryKindDecision:
		return 0.24
	case MemoryKindProcedure:
		return 0.22
	case MemoryKindPattern:
		return 0.19
	case MemoryKindBugfix:
		return 0.17
	case MemoryKindConfig:
		return 0.15
	case MemoryKindUserPreference:
		return 0.14
	case MemoryKindGlossary:
		return 0.13
	case MemoryKindPostmortem:
		return 0.12
	case MemoryKindDiscovery:
		return 0.02
	default:
		return 0.05
	}
}

func packClassificationWeight(classification string) float64 {
	switch classification {
	case "foundational":
		return 0.16
	case "tactical":
		return 0.08
	case "observational":
		return -0.12
	default:
		return 0
	}
}

func packUsageWeight(item MemoryItem, now time.Time) float64 {
	weight := math.Log1p(float64(item.AccessCount)) * 0.015
	if item.LastAccessed != nil {
		if ts, ok := parsePackTime(*item.LastAccessed); ok {
			age := now.Sub(ts)
			if age <= 14*24*time.Hour {
				weight += 0.03
			} else if age > 120*24*time.Hour {
				weight -= 0.01
			}
		}
	}
	return clamp(weight, -0.04, 0.10)
}

func packRecencyBoost(updatedAt string, now time.Time) float64 {
	ts, ok := parsePackTime(updatedAt)
	if !ok {
		return 0
	}
	age := now.Sub(ts)
	switch {
	case age <= 7*24*time.Hour:
		return 0.08
	case age <= 30*24*time.Hour:
		return 0.05
	case age <= 90*24*time.Hour:
		return 0.02
	default:
		return 0
	}
}

func packStalePenalty(updatedAt string, now time.Time) float64 {
	ts, ok := parsePackTime(updatedAt)
	if !ok {
		return 0
	}
	age := now.Sub(ts)
	switch {
	case age > 365*24*time.Hour:
		return -0.08
	case age > 180*24*time.Hour:
		return -0.04
	default:
		return 0
	}
}

func packExpiryPenalty(item MemoryItem, now time.Time) float64 {
	if item.ExpiresAt != nil && *item.ExpiresAt != "" {
		if expires, ok := parsePackTime(*item.ExpiresAt); ok && !expires.After(now) {
			return -0.40
		}
	}
	return 0
}

func packSupersededPenalty(item MemoryItem) float64 {
	if item.Status == MemoryStatusSuperseded || item.Status == MemoryStatusArchived {
		return -0.30
	}
	if item.SupersededBy != nil && *item.SupersededBy != 0 {
		return -0.24
	}
	return 0
}

func packTrustPenalty(trustLevel string) float64 {
	if strings.EqualFold(strings.TrimSpace(trustLevel), "untrusted") {
		return -0.05
	}
	return 0
}

// temporalOverlapBoost computes an Allen-interval temporal overlap bonus for pack scoring (T2.4).
// When the query has a timeframe (asof), memories whose validity window overlaps the query point
// receive a boost. Non-temporal queries (asof empty) receive zero boost.
//
// The boost follows the Allen-interval "contains" relation: if the query point falls within
// the memory's [valid_from, valid_to) window, the memory gets a positive contribution.
// Memories with no temporal binding (no valid_from/valid_to) are neutral (zero bonus).
func temporalOverlapBoost(item MemoryItem, asof string) float64 {
	if asof == "" {
		return 0
	}
	asofTime, ok := parsePackTime(asof)
	if !ok {
		return 0
	}

	hasFrom := item.ValidFrom != nil && strings.TrimSpace(*item.ValidFrom) != ""
	hasTo := item.ValidTo != nil && strings.TrimSpace(*item.ValidTo) != ""

	if !hasFrom && !hasTo {
		return 0 // no temporal binding — neutral
	}

	fromOk := true
	toOk := true

	if hasFrom {
		fromTime, ok := parsePackTime(*item.ValidFrom)
		if !ok {
			return 0
		}
		fromOk = !fromTime.After(asofTime) // valid_from <= asof
	}

	if hasTo {
		toTime, ok := parsePackTime(*item.ValidTo)
		if !ok {
			return 0
		}
		toOk = toTime.After(asofTime) // asof < valid_to (exclusive)
	}

	if fromOk && toOk {
		// Allen overlap confirmed: query point is within the memory's validity window.
		return 0.07
	}
	// Temporal mismatch penalty: memory's validity window does not contain the query point.
	// This is a mild penalty (not exclusion) — the memory may still be relevant by other signals.
	return -0.03
}

func parsePackTime(ts string) (time.Time, bool) {
	ts = strings.TrimSpace(ts)
	if ts == "" {
		return time.Time{}, false
	}
	if parsed, err := time.Parse(time.RFC3339Nano, ts); err == nil {
		return parsed, true
	}
	if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
		return parsed, true
	}
	return time.Time{}, false
}

func (s *Store) packStructuralSignals(ids []int64) (map[int64]float64, map[int64]float64, error) {
	entity := map[int64]float64{}
	relation := map[int64]float64{}
	if len(ids) == 0 {
		return entity, relation, nil
	}
	idSet := make(map[int64]bool, len(ids))
	ph := make([]string, 0, len(ids))
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		idSet[id] = true
		ph = append(ph, "?")
		args = append(args, id)
	}

	entityQuery := `
		SELECT oe1.obs_id, COUNT(DISTINCT oe2.obs_id) AS co_count
		FROM obs_entities oe1
		JOIN obs_entities oe2 ON oe1.entity_id = oe2.entity_id AND oe1.obs_id <> oe2.obs_id
		JOIN memory_items m2 ON m2.id = oe2.obs_id
		WHERE oe1.obs_id IN (` + strings.Join(ph, ",") + `)
		  AND m2.status = ?
		  AND (m2.expires_at IS NULL OR m2.expires_at = '' OR m2.expires_at > strftime('%Y-%m-%dT%H:%M:%f','now'))
		GROUP BY oe1.obs_id`
	entityArgs := append(append([]any{}, args...), MemoryStatusActive)
	rows, err := s.queryItHook(s.db, entityQuery, entityArgs...)
	if err != nil {
		return nil, nil, fmt.Errorf("pack structural entities: %w", err)
	}
	for rows.Next() {
		var obsID int64
		var coCount int
		if err := rows.Scan(&obsID, &coCount); err != nil {
			rows.Close()
			return nil, nil, fmt.Errorf("pack structural entities scan: %w", err)
		}
		entity[obsID] = clamp(float64(coCount)*0.01, 0, 0.16)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, fmt.Errorf("pack structural entities rows: %w", err)
	}
	rows.Close()

	relArgs := make([]any, 0, len(args)*2)
	relArgs = append(relArgs, args...)
	relArgs = append(relArgs, args...)
	relQuery := `
		SELECT from_obs_id, to_obs_id, relation
		FROM memory_relations
		WHERE from_obs_id IN (` + strings.Join(ph, ",") + `)
		   OR to_obs_id IN (` + strings.Join(ph, ",") + `)`
	rows, err = s.queryItHook(s.db, relQuery, relArgs...)
	if err != nil {
		return nil, nil, fmt.Errorf("pack structural relations: %w", err)
	}
	degree := make(map[int64]int)
	for rows.Next() {
		var fromID int64
		var toID int64
		var rel string
		if err := rows.Scan(&fromID, &toID, &rel); err != nil {
			rows.Close()
			return nil, nil, fmt.Errorf("pack structural relations scan: %w", err)
		}
		w := relationTypeWeight(rel)
		if idSet[fromID] {
			relation[fromID] += w
			degree[fromID]++
		}
		if idSet[toID] {
			relation[toID] += w
			degree[toID]++
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, fmt.Errorf("pack structural relations rows: %w", err)
	}
	rows.Close()
	for id, d := range degree {
		relation[id] = clamp(relation[id]+float64(d)*0.008, -0.06, 0.14)
	}

	return entity, relation, nil
}

func relationTypeWeight(relation string) float64 {
	switch relation {
	case RelationSupersedes:
		return 0.032
	case RelationResolves:
		return 0.030
	case RelationImplements:
		return 0.026
	case RelationRelatedTo:
		return 0.020
	case RelationCaused:
		return 0.018
	case RelationContradicts:
		return 0.010
	default:
		return 0.012
	}
}

func clamp(v, minV, maxV float64) float64 {
	if v < minV {
		return minV
	}
	if v > maxV {
		return maxV
	}
	return v
}
