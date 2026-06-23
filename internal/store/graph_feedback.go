package store

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

type MemoryEntity struct {
	ID         int64
	Name       string
	Type       string
	ProjectKey string
}

var tokenRE = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_\-/]{2,}`)        // identifiers, paths, file names
var capitalRE = regexp.MustCompile(`\b[A-Z][a-z]{2,}(?:[A-Z][a-z]{2,})+\b`) // ProperCase / CamelCase terms
var isoDateRE = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}\b`)                  // YYYY-MM-DD dates
var urlHostRE = regexp.MustCompile(`https?://([\w.\-]+)`)                     // URL hostnames

// knownTools maps case-normalized tool/language/framework names for entity extraction.
var knownTools = map[string]bool{
	"docker": true, "kubernetes": true, "k8s": true, "postgres": true,
	"postgresql": true, "sqlite": true, "redis": true, "mongodb": true,
	"grafana": true, "prometheus": true, "nginx": true, "apache": true,
	"git": true, "github": true, "gitlab": true, "jenkins": true,
	"terraform": true, "ansible": true, "helm": true, "istio": true,
	"ollama": true, "openai": true, "claude": true, "gemini": true,
	"opencode": true, "codex": true, "cursor": true, "vscode": true,
	"playwright": true, "cypress": true, "selenium": true, "vitest": true,
	"nextjs": true, "react": true, "angular": true, "vue": true,
	"typescript": true, "python": true, "go": true, "rust": true,
	"golang": true, "node": true, "nodejs": true, "deno": true,
}

func (s *Store) UpsertEntity(name, typ, project string) (int64, error) {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(typ) == "" || strings.TrimSpace(project) == "" {
		return 0, fmt.Errorf("name, type, and project are required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.execHook(s.db,
		`INSERT INTO entities (name, type, project_key, first_seen_at, last_seen_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(name, type, project_key) DO UPDATE SET
		   last_seen_at = excluded.last_seen_at`,
		name, typ, project, now, now,
	)
	if err != nil {
		return 0, err
	}
	var id int64
	if err := s.db.QueryRow(`SELECT id FROM entities WHERE name = ? AND type = ? AND project_key = ?`, name, typ, project).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Store) LinkMemoryEntity(memoryID, entityID int64) error {
	_, err := s.execHook(s.db,
		`INSERT OR IGNORE INTO obs_entities (obs_id, entity_id) VALUES (?, ?)`,
		memoryID, entityID,
	)
	return err
}

func ExtractEntitiesHeuristic(text string) []string {
	seen := make(map[string]bool)
	var out []string

	add := func(candidate string) {
		candidate = strings.TrimSpace(candidate)
		if len(candidate) < 3 {
			return
		}
		lower := strings.ToLower(candidate)
		if seen[lower] {
			return
		}
		seen[lower] = true
		out = append(out, candidate)
	}

	// 1. Token-based extraction (identifiers, paths, file names).
	for _, m := range tokenRE.FindAllString(text, -1) {
		add(m)
	}

	// 2. ProperCase / CamelCase terms (e.g. "OpenCode", "ReactNative").
	for _, m := range capitalRE.FindAllString(text, -1) {
		add(m)
	}

	// 3. ISO date patterns (YYYY-MM-DD).
	for _, m := range isoDateRE.FindAllString(text, -1) {
		add("date-" + m)
	}

	// 4. URL hosts.
	for _, m := range urlHostRE.FindAllStringSubmatch(text, -1) {
		if len(m) >= 2 {
			add(m[1])
		}
	}

	// 5. Known tool / language / framework names (case-insensitive scan).
	for _, m := range tokenRE.FindAllString(text, -1) {
		if knownTools[strings.ToLower(m)] {
			add(m)
		}
	}

	sort.Strings(out)
	if len(out) > 20 {
		out = out[:20]
	}
	return out
}

func (s *Store) AttachExtractedEntities(memoryID int64, project string, names []string) (int, error) {
	created := 0
	for _, n := range names {
		typ := "token"
		if strings.Contains(n, "/") {
			typ = "path"
		} else if strings.Contains(strings.ToLower(n), "api") || strings.Contains(strings.ToLower(n), "http") {
			typ = "component"
		}
		eid, err := s.UpsertEntity(n, typ, project)
		if err != nil {
			return created, err
		}
		if err := s.LinkMemoryEntity(memoryID, eid); err != nil {
			return created, err
		}
		created++
	}
	return created, nil
}

func (s *Store) GraphContext(project, entity string, limit int) ([]MemoryItem, error) {
	if limit <= 0 {
		limit = 10
	}
	q := `SELECT m.id, m.created_at, m.updated_at, m.project_id, m.actor_id, m.kind, m.scope,
		     m.title, m.body, m.tags, m.source, m.status, m.superseded_by, m.expires_at,
		     m.domain, m.evidence_json, m.applies_to_json, m.related_json, m.classification,
		     m.access_count, m.last_accessed, m.valid_from, m.valid_to, m.superseded_at, m.session_id, m.trust_level,
		     m.ingested_at, m.written_by, m.trigger_condition, m.utility_weight, m.consolidated_from,
		     0 AS relevance_score
		FROM entities e
		JOIN obs_entities oe ON oe.entity_id = e.id
		JOIN memory_items m ON m.id = oe.obs_id
		WHERE m.status = ?`
	args := []any{MemoryStatusActive}
	if project != "" {
		q += ` AND e.project_key = ?`
		args = append(args, project)
	}
	if entity != "" {
		q += ` AND lower(e.name) = lower(?)`
		args = append(args, entity)
	}
	q += ` ORDER BY m.updated_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.queryItHook(s.db, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []MemoryItem
	for rows.Next() {
		item, err := s.scanMemoryRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (s *Store) AppendFeedback(memoryID int64, reward float64, notes, actorID string) error {
	if actorID == "" {
		actorID = "agent"
	}
	if reward > 1 {
		reward = 1
	}
	if reward < -1 {
		reward = -1
	}
	_, err := s.execHook(s.db,
		`INSERT INTO memory_feedback (memory_id, reward, notes, actor_id) VALUES (?, ?, ?, ?)`,
		memoryID, reward, notes, actorID,
	)
	if err != nil {
		return err
	}
	_, err = s.execHook(s.db,
		`UPDATE memory_items
		 SET utility_weight = CASE
		   WHEN utility_weight + (? * 0.2) < 0.1 THEN 0.1
		   ELSE utility_weight + (? * 0.2)
		 END,
		 updated_at = strftime('%Y-%m-%dT%H:%M:%f','now')
		 WHERE id = ?`,
		reward, reward, memoryID,
	)
	return err
}
