// Package store implements the persistent memory engine for Ohara.
// memories.go contains the CRUD operations for memory_items (Ohara v2 spec).
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// AddMemory creates a new memory item and returns its ID.
// It enforces body size limits per kind and records the initial revision.
func (s *Store) AddMemory(p AddMemoryParams) (int64, error) {
	// Validate kind
	if !ValidMemoryKinds[p.Kind] {
		return 0, fmt.Errorf("invalid memory kind %q", p.Kind)
	}

	// Normalize project
	projectID, _ := NormalizeProject(p.ProjectID)

	// Enforce body size limit per kind
	body := p.Body
	if limit := MemoryBodyLimit(p.Kind); limit > 0 && len(body) > limit {
		body = body[:limit] + "... [truncated]"
	}

	// Normalize scope
	scope := p.Scope
	if scope == "" {
		if isGlobalKind(p.Kind) {
			scope = MemoryScopeGlobal
		} else {
			scope = MemoryScopeProject
		}
	}

	// Set defaults
	if p.Source == "" {
		p.Source = "agent"
	}
	if p.ActorID == "" {
		p.ActorID = "agent"
	}

	tagsJSON := "[]"
	if len(p.Tags) > 0 {
		if data, err := json.Marshal(p.Tags); err == nil {
			tagsJSON = string(data)
		}
	}

	var memoryID int64
	err := s.withTx(func(tx *sql.Tx) error {
		res, err := s.execHook(tx,
			`INSERT INTO memory_items (project_id, actor_id, kind, scope, title, body, tags, source, status)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			projectID, p.ActorID, p.Kind, scope, p.Title, body, tagsJSON, p.Source, MemoryStatusActive,
		)
		if err != nil {
			return err
		}
		memoryID, err = res.LastInsertId()
		if err != nil {
			return err
		}

		// Record initial revision
		titleJSON, _ := json.Marshal(p.Title)
		bodyJSON, _ := json.Marshal(body)
		_, err = s.execHook(tx,
			`INSERT INTO memory_revisions (memory_id, actor_id, field, old_value, new_value, reason)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			memoryID, p.ActorID, "_created", nil, string(titleJSON), "Initial creation",
		)
		if err != nil {
			return err
		}
		_, err = s.execHook(tx,
			`INSERT INTO memory_revisions (memory_id, actor_id, field, old_value, new_value, reason)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			memoryID, p.ActorID, "body", nil, string(bodyJSON), "Initial creation",
		)
		return err
	})
	if err != nil {
		return 0, err
	}
	return memoryID, nil
}

// GetMemory retrieves a single memory item by ID.
func (s *Store) GetMemory(id int64) (*MemoryItem, error) {
	row := s.db.QueryRow(
		`SELECT id, created_at, updated_at, project_id, actor_id, kind, scope, title, body, tags,
		        source, status, superseded_by, expires_at
		 FROM memory_items WHERE id = ?`,
		id,
	)
	return s.scanMemoryItem(row)
}

// GetMemories retrieves memory items matching the given filters.
func (s *Store) GetMemories(projectID, scope, kind, status string, limit int) ([]MemoryItem, error) {
	if limit <= 0 {
		limit = 20
	}
	if status == "" {
		status = MemoryStatusActive
	}

	query := `
		SELECT id, created_at, updated_at, project_id, actor_id, kind, scope, title, body, tags,
		       source, status, superseded_by, expires_at
		FROM memory_items
		WHERE status = ?`
	args := []any{status}

	if projectID != "" {
		query += " AND project_id = ?"
		args = append(args, projectID)
	}
	if scope != "" {
		query += " AND scope = ?"
		args = append(args, scope)
	}
	if kind != "" {
		query += " AND kind = ?"
		args = append(args, kind)
	}

	query += " ORDER BY updated_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.queryItHook(s.db, query, args...)
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

// UpdateMemory updates a memory item and records a revision entry.
func (s *Store) UpdateMemory(id int64, p UpdateMemoryParams) (*MemoryItem, error) {
	var updated *MemoryItem
	err := s.withTx(func(tx *sql.Tx) error {
		existing, err := s.getMemoryTx(tx, id)
		if err != nil {
			return err
		}

		actorID := p.ActorID
		if actorID == "" {
			actorID = "agent"
		}

		// Build dynamic UPDATE
		setParts := []string{}
		setArgs := []any{}

		if p.Title != nil {
			title := *p.Title
			oldJSON, _ := json.Marshal(existing.Title)
			newJSON, _ := json.Marshal(title)
			_, err := s.execHook(tx,
				`INSERT INTO memory_revisions (memory_id, actor_id, field, old_value, new_value, reason) VALUES (?, ?, ?, ?, ?, ?)`,
				id, actorID, "title", string(oldJSON), string(newJSON), nullableString(p.Reason),
			)
			if err != nil {
				return err
			}
			setParts = append(setParts, "title = ?")
			setArgs = append(setArgs, title)
		}

		if p.Body != nil {
			// Enforce body limit
			body := *p.Body
			if limit := MemoryBodyLimit(existing.Kind); limit > 0 && len(body) > limit {
				body = body[:limit] + "... [truncated]"
			}
			oldJSON, _ := json.Marshal(existing.Body)
			newJSON, _ := json.Marshal(body)
			_, err := s.execHook(tx,
				`INSERT INTO memory_revisions (memory_id, actor_id, field, old_value, new_value, reason) VALUES (?, ?, ?, ?, ?, ?)`,
				id, actorID, "body", string(oldJSON), string(newJSON), nullableString(p.Reason),
			)
			if err != nil {
				return err
			}
			setParts = append(setParts, "body = ?")
			setArgs = append(setArgs, body)
		}

		if p.Tags != nil {
			tagsJSON, _ := json.Marshal(p.Tags)
			setParts = append(setParts, "tags = ?")
			setArgs = append(setArgs, string(tagsJSON))
		}

		if p.Status != nil {
			setParts = append(setParts, "status = ?")
			setArgs = append(setArgs, *p.Status)
		}

		if p.SupersededBy != nil {
			setParts = append(setParts, "superseded_by = ?")
			setArgs = append(setArgs, *p.SupersededBy)
		}

		if len(setParts) == 0 {
			// No changes
			updated = existing
			return nil
		}

		setParts = append(setParts, "updated_at = strftime('%Y-%m-%dT%H:%M:%f','now')")
		setArgs = append(setArgs, id)

		query := "UPDATE memory_items SET " + strings.Join(setParts, ", ") + " WHERE id = ?"
		if _, err := s.execHook(tx, query, setArgs...); err != nil {
			return err
		}

		updated, err = s.getMemoryTx(tx, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// SearchMemories performs FTS5 search over memory items with kind and recency boosts.
func (s *Store) SearchMemories(query string, projectID, scope, kind string, status string, limit int) ([]MemoryItem, error) {
	if limit <= 0 {
		limit = 10
	}
	if status == "" {
		status = MemoryStatusActive
	}

	ftsQuery := sanitizeFTS(query)

	// Composite ranking: FTS relevance * kind_boost * recency_boost
	// - Kind boost: decision(1.5), pattern(1.4), bugfix(1.3), discovery(1.2), procedure(1.1), others(1.0)
	// - Recency boost: exponential decay over 90 days (1.0 at now, 0.5 at 90 days old)
	sqlQ := `
		SELECT mi.id, mi.created_at, mi.updated_at, mi.project_id, mi.actor_id, mi.kind, mi.scope,
		       mi.title, mi.body, mi.tags, mi.source, mi.status, mi.superseded_by, mi.expires_at
		FROM memory_items_fts fts
		JOIN memory_items mi ON mi.id = fts.rowid
		WHERE memory_items_fts MATCH ? AND mi.status = ?`
	args := []any{ftsQuery, status}

	if projectID != "" {
		sqlQ += " AND mi.project_id = ?"
		args = append(args, projectID)
	}
	if scope != "" {
		sqlQ += " AND mi.scope = ?"
		args = append(args, scope)
	}
	if kind != "" {
		sqlQ += " AND mi.kind = ?"
		args = append(args, kind)
	}

	// Composite score: higher is better. fts.rank is BM25 (lower = more relevant),
	// so we use (1 / (1 + fts.rank)) to invert. Multiply by boosts.
	sqlQ += ` ORDER BY (
			(1.0 / (1.0 + fts.rank))
			* CASE mi.kind
				WHEN 'decision' THEN 1.5
				WHEN 'pattern' THEN 1.4
				WHEN 'bugfix' THEN 1.3
				WHEN 'discovery' THEN 1.2
				WHEN 'procedure' THEN 1.1
				ELSE 1.0
			  END
			* MAX(0.1, 1.0 - (CAST(julianday('now') - julianday(mi.updated_at) AS REAL) / 90.0 * 0.9))
		) DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.queryItHook(s.db, sqlQ, args...)
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

// GetMemoryRevisions returns all revision entries for a memory item.
func (s *Store) GetMemoryRevisions(memoryID int64) ([]MemoryRevision, error) {
	rows, err := s.queryItHook(s.db,
		`SELECT id, memory_id, ts, actor_id, field, old_value, new_value, reason
		 FROM memory_revisions WHERE memory_id = ? ORDER BY ts ASC`,
		memoryID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var revisions []MemoryRevision
	for rows.Next() {
		var r MemoryRevision
		if err := rows.Scan(&r.ID, &r.MemoryID, &r.TS, &r.ActorID, &r.Field, &r.OldValue, &r.NewValue, &r.Reason); err != nil {
			return nil, err
		}
		revisions = append(revisions, r)
	}
	return revisions, rows.Err()
}

// getMemoryTx retrieves a memory item within a transaction.
func (s *Store) getMemoryTx(tx *sql.Tx, id int64) (*MemoryItem, error) {
	row := tx.QueryRow(
		`SELECT id, created_at, updated_at, project_id, actor_id, kind, scope, title, body, tags,
		        source, status, superseded_by, expires_at
		 FROM memory_items WHERE id = ?`,
		id,
	)
	return s.scanMemoryItem(row)
}

// scanMemoryItem scans a memory item from a QueryRow.
func (s *Store) scanMemoryItem(row *sql.Row) (*MemoryItem, error) {
	var m MemoryItem
	var tagsJSON string
	if err := row.Scan(
		&m.ID, &m.CreatedAt, &m.UpdatedAt, &m.ProjectID, &m.ActorID, &m.Kind, &m.Scope,
		&m.Title, &m.Body, &tagsJSON, &m.Source, &m.Status, &m.SupersededBy, &m.ExpiresAt,
	); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(tagsJSON), &m.Tags); err != nil {
		m.Tags = []string{}
	}
	return &m, nil
}

// scanMemoryRow scans a memory item from a Query results iterator.
func (s *Store) scanMemoryRow(rows rowScanner) (*MemoryItem, error) {
	var m MemoryItem
	var tagsJSON string
	if err := rows.Scan(
		&m.ID, &m.CreatedAt, &m.UpdatedAt, &m.ProjectID, &m.ActorID, &m.Kind, &m.Scope,
		&m.Title, &m.Body, &tagsJSON, &m.Source, &m.Status, &m.SupersededBy, &m.ExpiresAt,
	); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(tagsJSON), &m.Tags); err != nil {
		m.Tags = []string{}
	}
	return &m, nil
}

// ConflictInfo describes a detected contradiction.
type ConflictInfo struct {
	ExistingMemory *MemoryItem `json:"existing_memory"`
	ConflictType   string      `json:"conflict_type"` // "title_overlap" | "body_contradiction"
	OverlapScore   float64     `json:"overlap_score"`
	Message        string      `json:"message"`
}

// kindsForConflictDetection returns true if the kind benefits from contradiction checking.
func kindsForConflictDetection(kind string) bool {
	return kind == MemoryKindDecision || kind == MemoryKindPattern || kind == MemoryKindConfig
}

// DetectConflict checks whether the proposed memory contradicts an existing one.
// It only applies to decision, pattern, and config kinds. Returns nil if no conflict.
func (s *Store) DetectConflict(p AddMemoryParams) (*ConflictInfo, error) {
	if !kindsForConflictDetection(p.Kind) {
		return nil, nil
	}

	projectID, _ := NormalizeProject(p.ProjectID)
	existing, err := s.GetMemories(projectID, "", p.Kind, MemoryStatusActive, 50)
	if err != nil {
		return nil, fmt.Errorf("detect conflict: %w", err)
	}

	newWords := significantWords(p.Title)
	if len(newWords) == 0 {
		return nil, nil
	}

	var best *ConflictInfo
	for i := range existing {
		existingWords := significantWords(existing[i].Title)
		score := keywordOverlap(newWords, existingWords)
		if score > 0.6 {
			ci := &ConflictInfo{
				ExistingMemory: &existing[i],
				ConflictType:   "title_overlap",
				OverlapScore:   score,
				Message: fmt.Sprintf(
					"existing %s memory %q has %d%% keyword overlap with new title %q",
					p.Kind, existing[i].Title, int(score*100), p.Title,
				),
			}
			if best == nil || score > best.OverlapScore {
				best = ci
			}
		}
	}

	return best, nil
}

// significantWords extracts lowercase words with 3+ chars, excluding common stopwords.
func significantWords(text string) []string {
	stopwords := map[string]bool{
		"the": true, "and": true, "for": true, "are": true, "but": true,
		"not": true, "you": true, "all": true, "can": true, "had": true,
		"her": true, "was": true, "one": true, "our": true, "out": true,
		"has": true, "have": true, "been": true, "were": true, "they": true,
		"this": true, "that": true, "with": true, "from": true, "will": true,
		"would": true, "there": true, "their": true, "what": true, "when": true,
		"which": true, "your": true, "also": true, "into": true, "more": true,
		"some": true, "them": true, "than": true, "then": true, "these": true,
		"just": true, "like": true, "using": true, "used": true, "need": true,
		"make": true, "made": true, "new": true, "via": true, "how": true,
		"why": true, "who": true, "where": true, "both": true, "each": true,
		"only": true, "over": true, "such": true, "any": true, "about": true,
	}
	var words []string
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return r == ' ' || r == '-' || r == '_' || r == '/' || r == '(' || r == ')' || r == ':' || r == ','
	}) {
		if len(w) >= 3 && !stopwords[w] {
			words = append(words, w)
		}
	}
	return words
}

// keywordOverlap computes the Jaccard similarity between two word sets.
func keywordOverlap(a, b []string) float64 {
	setA := make(map[string]bool)
	setB := make(map[string]bool)
	for _, w := range a {
		setA[w] = true
	}
	for _, w := range b {
		setB[w] = true
	}
	if len(setA) == 0 || len(setB) == 0 {
		return 0
	}
	var intersection int
	for w := range setA {
		if setB[w] {
			intersection++
		}
	}
	union := len(setA) + len(setB) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// isGlobalKind returns true if the kind is a global-scope kind.
func isGlobalKind(kind string) bool {
	return kind == MemoryKindIdentity || kind == MemoryKindUserPreference || kind == MemoryKindGlossary
}

// MemoryTimelineResult holds the result of a memory timeline query.
type MemoryTimelineResult struct {
	Anchor       MemoryItem   `json:"anchor"`
	Before       []MemoryItem `json:"before"`
	After        []MemoryItem `json:"after"`
	TotalInRange int          `json:"total_in_range"`
}

// MemoryTimeline returns memories near a specific memory item in time, within the same project.
func (s *Store) MemoryTimeline(memoryID int64, count int) (*MemoryTimelineResult, error) {
	if count <= 0 {
		count = 3
	}

	anchor, err := s.GetMemory(memoryID)
	if err != nil {
		return nil, fmt.Errorf("timeline: memory #%d not found: %w", memoryID, err)
	}

	// Get memories before the anchor (same project, older by updated_at)
	beforeRows, err := s.queryItHook(s.db, `
		SELECT id, created_at, updated_at, project_id, actor_id, kind, scope, title, body, tags,
		       source, status, superseded_by, expires_at
		FROM memory_items
		WHERE project_id = ? AND updated_at < ? AND status = 'active'
		ORDER BY updated_at DESC
		LIMIT ?`,
		anchor.ProjectID, anchor.UpdatedAt, count,
	)
	if err != nil {
		return nil, fmt.Errorf("timeline: before query: %w", err)
	}
	defer beforeRows.Close()

	var before []MemoryItem
	for beforeRows.Next() {
		item, err := s.scanMemoryRow(beforeRows)
		if err != nil {
			return nil, err
		}
		before = append(before, *item)
	}
	if err := beforeRows.Err(); err != nil {
		return nil, err
	}
	// Reverse to chronological order (oldest first)
	for i, j := 0, len(before)-1; i < j; i, j = i+1, j-1 {
		before[i], before[j] = before[j], before[i]
	}

	// Get memories after the anchor (same project, newer by updated_at)
	afterRows, err := s.queryItHook(s.db, `
		SELECT id, created_at, updated_at, project_id, actor_id, kind, scope, title, body, tags,
		       source, status, superseded_by, expires_at
		FROM memory_items
		WHERE project_id = ? AND updated_at > ? AND status = 'active'
		ORDER BY updated_at ASC
		LIMIT ?`,
		anchor.ProjectID, anchor.UpdatedAt, count,
	)
	if err != nil {
		return nil, fmt.Errorf("timeline: after query: %w", err)
	}
	defer afterRows.Close()

	var after []MemoryItem
	for afterRows.Next() {
		item, err := s.scanMemoryRow(afterRows)
		if err != nil {
			return nil, err
		}
		after = append(after, *item)
	}
	if err := afterRows.Err(); err != nil {
		return nil, err
	}

	// Count total in range for context
	var total int
	s.db.QueryRow(
		`SELECT COUNT(*) FROM memory_items WHERE project_id = ? AND status = 'active'`,
		anchor.ProjectID,
	).Scan(&total)

	return &MemoryTimelineResult{
		Anchor:       *anchor,
		Before:       before,
		After:        after,
		TotalInRange: total,
	}, nil
}
