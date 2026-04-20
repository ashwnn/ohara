// Package store implements the persistent memory engine for Ohara.
// memories.go contains the CRUD operations for memory_items (Ohara v2 spec).
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/ashwnn/ohara/internal/redact"
)

// nullableStringValue returns ptr's value if non-nil, otherwise returns fallback.
func nullableStringValue(ptr *string, fallback string) string {
	if ptr != nil && *ptr != "" {
		return *ptr
	}
	return fallback
}

// memoryAuditAction values for audit_log.action.
const (
	auditActionCreate  = "create"
	auditActionUpdate  = "update"
	auditActionArchive = "archive"
)

// logMemoryAudit writes an audit log entry for a memory operation.
// It runs inside the same transaction as the triggering write so the audit
// record and the memory change are atomic.
func (s *Store) logMemoryAudit(tx *sql.Tx, memoryID int64, action, actorID, sessionID, trustLevel string) error {
	snapshot, _ := json.Marshal(map[string]any{
		"id":    memoryID,
		"actor": actorID,
		"kind":  action, // action is also stored in snapshot for post-hoc analysis
	})
	_, err := s.execHook(tx,
		`INSERT INTO audit_log (obs_id, action, actor_id, session_id, trust_level, snapshot)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		fmt.Sprintf("mem-%d", memoryID), action, nullableString(actorID), nullableString(sessionID), nullableString(trustLevel), string(snapshot),
	)
	return err
}

// AddMemory creates a new memory item and returns its ID.
// It enforces body size limits per kind and records the initial revision.
func (s *Store) AddMemory(p AddMemoryParams) (int64, error) {
	// Validate kind
	if !ValidMemoryKinds[p.Kind] {
		return 0, fmt.Errorf("invalid memory kind %q", p.Kind)
	}

	// Normalize project
	projectID, _ := NormalizeProject(p.ProjectID)

	// Strip <private>...</private> tags before persisting anything.
	// This is the binary-side enforcement: the plugin also strips these tags,
	// but we need a second layer so no private content ever hits the DB.
	// Apply regex-based secret redaction first (GitHub tokens, OpenAI keys, etc.),
	// then strip private tags as the final layer.
	// Enforce body size limit per kind using token-based truncation.
	title := stripPrivateTags(redact.Redact(p.Title))
	body := TruncateBodyToTokenLimit(stripPrivateTags(redact.Redact(p.Body)), p.Kind)

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
	if p.Classification == "" {
		p.Classification = defaultClassificationForKind(p.Kind)
	}
	if p.WrittenBy == "" {
		switch p.Source {
		case "import":
			p.WrittenBy = "import"
		case "consolidation":
			p.WrittenBy = "consolidation"
		default:
			if strings.EqualFold(p.ActorID, "user") {
				p.WrittenBy = "user"
			} else {
				p.WrittenBy = "agent"
			}
		}
	}
	if p.TrustLevel == "" {
		p.TrustLevel = "system"
	}

	tagsJSON := "[]"
	if len(p.Tags) > 0 {
		if data, err := json.Marshal(p.Tags); err == nil {
			tagsJSON = string(data)
		}
	}

	// Auto-assign expires_at for discovery and postmortem memory kinds.
	expiresAt := MemoryExpiresAt(p.Kind)
	if p.ExpiresAt != "" {
		expiresAt = &p.ExpiresAt
	}

	var memoryID int64
	err := s.withTx(func(tx *sql.Tx) error {
		res, err := s.execHook(tx,
			`INSERT INTO memory_items (project_id, actor_id, kind, scope, title, body, tags, source, status, expires_at,
			 domain, evidence_json, applies_to_json, related_json, session_id, trust_level,
			 classification, written_by, trigger_condition, utility_weight, consolidated_from)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			projectID, p.ActorID, p.Kind, scope, title, body, tagsJSON, p.Source, MemoryStatusActive, expiresAt,
			p.Domain, p.EvidenceJSON, p.AppliesToJSON, p.RelatedJSON, p.SessionID, p.TrustLevel,
			p.Classification, p.WrittenBy, p.TriggerCondition, p.UtilityWeight, p.ConsolidatedFrom,
		)
		if err != nil {
			return err
		}
		memoryID, err = res.LastInsertId()
		if err != nil {
			return err
		}

		// Record initial revision
		titleJSON, _ := json.Marshal(title)
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
		if err != nil {
			return err
		}
		// Write audit log entry for initial creation.
		if err := s.logMemoryAudit(tx, memoryID, auditActionCreate, p.ActorID, p.SessionID, p.TrustLevel); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	if s.hybridEnabled() {
		text := title + "\n" + body
		go func(id int64, t string) {
			_ = s.indexMemoryEmbedding(id, t)
		}(memoryID, text)
	}
	return memoryID, nil
}

// GetMemory retrieves a single memory item by ID.
func (s *Store) GetMemory(id int64) (*MemoryItem, error) {
	row := s.db.QueryRow(
		`SELECT id, created_at, updated_at, project_id, actor_id, kind, scope, title, body, tags,
		        source, status, superseded_by, expires_at,
		        domain, evidence_json, applies_to_json, related_json, classification,
		        access_count, last_accessed, valid_from, valid_to, superseded_at, session_id, trust_level,
		        ingested_at, written_by,
		        trigger_condition, utility_weight, consolidated_from,
		        0 AS relevance_score
		 FROM memory_items WHERE id = ?`,
		id,
	)
	item, err := s.scanMemoryItem(row)
	if err != nil {
		return nil, err
	}

	// Update access stats for this retrieval (non-fatal, best-effort).
	// Synchronous to avoid SQLITE_BUSY races in tests.
	_ = s.logAccessStats(id)

	return item, nil
}

// logAccessStats updates access_count and last_accessed for a memory item.
// This is non-fatal - errors are logged but not returned.
func (s *Store) logAccessStats(id int64) error {
	_, err := s.db.Exec(
		`UPDATE memory_items SET access_count = access_count + 1,
		 last_accessed = strftime('%Y-%m-%dT%H:%M:%f','now') WHERE id = ?`,
		id,
	)
	return err
}

// GetMemories retrieves memory items matching the given filters.
// By default (status="" or status="active"), it excludes items that have expired
// (expires_at < now). When status is explicitly set to a non-active value (e.g.,
// "archived"), expired items ARE included so they remain retrievable for explicit queries.
func (s *Store) GetMemories(projectID, scope, kind, status string, limit int) ([]MemoryItem, error) {
	if limit <= 0 {
		limit = 20
	}
	if projectID != "" {
		projectID, _ = NormalizeProject(projectID)
	}

	// Preserve original status to determine if expires_at filter should apply.
	// The expires_at filter only applies when status is implicitly or explicitly "active".
	// When status is explicitly non-active (e.g., "archived"), expired items are included.
	originalStatus := status
	if status == "" {
		status = MemoryStatusActive
	}

	query := `
		SELECT id, created_at, updated_at, project_id, actor_id, kind, scope, title, body, tags,
		       source, status, superseded_by, expires_at,
		       domain, evidence_json, applies_to_json, related_json, classification,
		       access_count, last_accessed, valid_from, valid_to, superseded_at, session_id, trust_level,
		       ingested_at, written_by,
		       trigger_condition, utility_weight, consolidated_from,
		       0 AS relevance_score
		FROM memory_items
		WHERE status = ?`
	args := []any{status}

	// Only filter by expires_at when querying active items.
	// Explicit non-active queries (e.g., archived) should return all matching items.
	if originalStatus == "" || originalStatus == MemoryStatusActive {
		query += " AND (expires_at IS NULL OR expires_at = '' OR expires_at > datetime('now'))"
	}

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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// UpdateMemory updates a memory item and records a revision entry.
func (s *Store) UpdateMemory(id int64, p UpdateMemoryParams) (*MemoryItem, error) {
	var updated *MemoryItem
	reindexEmbedding := false
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
			reindexEmbedding = true
			title := stripPrivateTags(redact.Redact(*p.Title))
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
			reindexEmbedding = true
			// Apply regex redaction before stripping private tags, then enforce body limit.
			body := TruncateBodyToTokenLimit(stripPrivateTags(redact.Redact(*p.Body)), existing.Kind)
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

		// Track whether this update will archive the memory (for audit logging below).
		statusWasArchived := p.Status != nil && *p.Status == MemoryStatusArchived && existing.Status != MemoryStatusArchived

		if p.SupersededBy != nil {
			setParts = append(setParts, "superseded_by = ?")
			setArgs = append(setArgs, *p.SupersededBy)
		}

		if p.Domain != nil {
			setParts = append(setParts, "domain = ?")
			setArgs = append(setArgs, *p.Domain)
		}
		if p.EvidenceJSON != nil {
			setParts = append(setParts, "evidence_json = ?")
			setArgs = append(setArgs, *p.EvidenceJSON)
		}
		if p.AppliesToJSON != nil {
			setParts = append(setParts, "applies_to_json = ?")
			setArgs = append(setArgs, *p.AppliesToJSON)
		}
		if p.RelatedJSON != nil {
			setParts = append(setParts, "related_json = ?")
			setArgs = append(setArgs, *p.RelatedJSON)
		}
		if p.Classification != nil {
			setParts = append(setParts, "classification = ?")
			setArgs = append(setArgs, *p.Classification)
		}
		if p.SessionID != nil {
			setParts = append(setParts, "session_id = ?")
			setArgs = append(setArgs, *p.SessionID)
		}
		if p.TrustLevel != nil {
			setParts = append(setParts, "trust_level = ?")
			setArgs = append(setArgs, *p.TrustLevel)
		}
		if p.WrittenBy != nil {
			setParts = append(setParts, "written_by = ?")
			setArgs = append(setArgs, *p.WrittenBy)
		}
		if p.ExpiresAt != nil {
			setParts = append(setParts, "expires_at = ?")
			setArgs = append(setArgs, *p.ExpiresAt)
		}
		if p.TriggerCondition != nil {
			setParts = append(setParts, "trigger_condition = ?")
			setArgs = append(setArgs, *p.TriggerCondition)
		}
		if p.UtilityWeight != nil {
			setParts = append(setParts, "utility_weight = ?")
			setArgs = append(setArgs, *p.UtilityWeight)
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

		// Log 'update' audit entry for any field change.
		if err := s.logMemoryAudit(tx, id, auditActionUpdate, actorID,
			nullableStringValue(p.SessionID, existing.SessionID),
			nullableStringValue(p.TrustLevel, existing.TrustLevel)); err != nil {
			return err
		}

		// If the memory was just archived, emit a separate 'archive' audit event.
		if statusWasArchived {
			if err := s.logMemoryAudit(tx, id, auditActionArchive, actorID,
				nullableStringValue(p.SessionID, existing.SessionID),
				nullableStringValue(p.TrustLevel, existing.TrustLevel)); err != nil {
				return err
			}
		}

		updated, err = s.getMemoryTx(tx, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	if s.hybridEnabled() && reindexEmbedding && updated != nil {
		text := updated.Title + "\n" + updated.Body
		go func(mid int64, t string) {
			_ = s.indexMemoryEmbedding(mid, t)
		}(id, text)
	}
	return updated, nil
}

// temporalFilters holds parsed temporal filter operators from the query string.
type temporalFilters struct {
	asof         string // asof:<ISO_timestamp> - valid at a point in time
	since        string // since:<ISO_timestamp> - created/updated after
	ingestedAsof string // ingested_asof:<ISO_timestamp> - existed in DB at point in time
	sessionID    string // session:<session_id> - from specific session
	file         string // file:<path> - applies_to_json file match
	path         string // path:<path> - applies_to_json path match
}

// parseTemporalFilters extracts temporal operators from the query string.
// Operators: asof:<ts>, since:<ts>, ingested_asof:<ts>, session:<id>
// These are stripped from the returned ftsQuery after parsing.
func parseTemporalFilters(query string) (ftsQuery string, filters temporalFilters) {
	ftsQuery = query

	// Parse asof:<timestamp>
	if idx := strings.Index(ftsQuery, "asof:"); idx >= 0 {
		end := idx + len("asof:")
		if end < len(ftsQuery) {
			rest := ftsQuery[end:]
			spaceIdx := strings.Index(rest, " ")
			var ts string
			if spaceIdx >= 0 {
				ts = rest[:spaceIdx]
				ftsQuery = ftsQuery[:idx] + strings.TrimSpace(ftsQuery[end+spaceIdx:])
			} else {
				ts = rest
				ftsQuery = ftsQuery[:idx]
			}
			filters.asof = ts
		}
	}

	// Parse since:<timestamp>
	if idx := strings.Index(ftsQuery, "since:"); idx >= 0 {
		end := idx + len("since:")
		if end < len(ftsQuery) {
			rest := ftsQuery[end:]
			spaceIdx := strings.Index(rest, " ")
			var ts string
			if spaceIdx >= 0 {
				ts = rest[:spaceIdx]
				ftsQuery = ftsQuery[:idx] + strings.TrimSpace(ftsQuery[end+spaceIdx:])
			} else {
				ts = rest
				ftsQuery = ftsQuery[:idx]
			}
			filters.since = ts
		}
	}

	// Parse ingested_asof:<timestamp>
	if idx := strings.Index(ftsQuery, "ingested_asof:"); idx >= 0 {
		end := idx + len("ingested_asof:")
		if end < len(ftsQuery) {
			rest := ftsQuery[end:]
			spaceIdx := strings.Index(rest, " ")
			var ts string
			if spaceIdx >= 0 {
				ts = rest[:spaceIdx]
				ftsQuery = ftsQuery[:idx] + strings.TrimSpace(ftsQuery[end+spaceIdx:])
			} else {
				ts = rest
				ftsQuery = ftsQuery[:idx]
			}
			filters.ingestedAsof = ts
		}
	}

	// Parse session:<session_id>
	if idx := strings.Index(ftsQuery, "session:"); idx >= 0 {
		end := idx + len("session:")
		if end < len(ftsQuery) {
			rest := ftsQuery[end:]
			spaceIdx := strings.Index(rest, " ")
			var sid string
			if spaceIdx >= 0 {
				sid = rest[:spaceIdx]
				ftsQuery = ftsQuery[:idx] + strings.TrimSpace(ftsQuery[end+spaceIdx:])
			} else {
				sid = rest
				ftsQuery = ftsQuery[:idx]
			}
			filters.sessionID = sid
		}
	}

	// Parse file:<path>
	if idx := strings.Index(ftsQuery, "file:"); idx >= 0 {
		end := idx + len("file:")
		if end < len(ftsQuery) {
			rest := ftsQuery[end:]
			spaceIdx := strings.Index(rest, " ")
			var fp string
			if spaceIdx >= 0 {
				fp = rest[:spaceIdx]
				ftsQuery = strings.TrimSpace(ftsQuery[:idx] + " " + strings.TrimSpace(ftsQuery[end+spaceIdx:]))
			} else {
				fp = rest
				ftsQuery = strings.TrimSpace(ftsQuery[:idx])
			}
			filters.file = fp
		}
	}

	// Parse path:<path>
	if idx := strings.Index(ftsQuery, "path:"); idx >= 0 {
		end := idx + len("path:")
		if end < len(ftsQuery) {
			rest := ftsQuery[end:]
			spaceIdx := strings.Index(rest, " ")
			var pp string
			if spaceIdx >= 0 {
				pp = rest[:spaceIdx]
				ftsQuery = strings.TrimSpace(ftsQuery[:idx] + " " + strings.TrimSpace(ftsQuery[end+spaceIdx:]))
			} else {
				pp = rest
				ftsQuery = strings.TrimSpace(ftsQuery[:idx])
			}
			filters.path = pp
		}
	}

	ftsQuery = strings.TrimSpace(ftsQuery)
	return
}

// SearchMemories performs FTS5 search over memory items with kind and recency boosts.
// By default (status="" or status="active"), it excludes items that have expired
// (expires_at < now). When status is explicitly set to a non-active value (e.g.,
// "archived"), expired items ARE included so they remain searchable for explicit queries.
//
// Query string can contain temporal operators that are parsed and stripped before FTS:
//   - asof:<ISO_timestamp> — filter valid_from <= ts <= valid_to
//   - since:<ISO_timestamp> — filter updated_at >= ts
//   - ingested_asof:<ISO_timestamp> — filter ingested_at <= ts
//   - session:<session_id> — filter session_id = X
func (s *Store) SearchMemories(query string, projectID, scope, kind, domain string, status string, limit int, writtenBy string) ([]MemoryItem, error) {
	if limit <= 0 {
		limit = 10
	}
	if projectID != "" {
		projectID, _ = NormalizeProject(projectID)
	}

	// Parse temporal operators from query string before FTS sanitization
	ftsQuery, filters := parseTemporalFilters(query)
	ftsQuery = sanitizeFTS(ftsQuery)

	// Preserve original status to determine if expires_at filter should apply.
	// The expires_at filter only applies when status is implicitly or explicitly "active".
	// When status is explicitly non-active (e.g., "archived"), expired items are included.
	originalStatus := status
	if status == "" {
		status = MemoryStatusActive
	}

	outcomeBoostExpr := "1.0"
	if s.tableExists("memory_outcomes") {
		outcomeBoostExpr = `max(0.1,
				1.0
				+ 0.2 * (SELECT COUNT(*) FROM memory_outcomes mo WHERE mo.memory_id = mi.id AND mo.status = 'success')
				- 0.3 * (SELECT COUNT(*) FROM memory_outcomes mo WHERE mo.memory_id = mi.id AND mo.status = 'failure')
			 )`
	}

	// Composite ranking: FTS relevance * kind_boost * recency_boost
	// - Kind boost: decision(1.5), pattern(1.4), bugfix(1.3), discovery(1.2), procedure(1.1), others(1.0)
	// - Recency boost (Ohara v2 spec): 1.15x within 7 days, 1.05x within 30 days, 1.0 beyond 30 days
	sqlQ := `
		SELECT mi.id, mi.created_at, mi.updated_at, mi.project_id, mi.actor_id, mi.kind, mi.scope,
		       mi.title, mi.body, mi.tags, mi.source, mi.status, mi.superseded_by, mi.expires_at,
		       mi.domain, mi.evidence_json, mi.applies_to_json, mi.related_json, mi.classification,
		       mi.access_count, mi.last_accessed, mi.valid_from, mi.valid_to, mi.superseded_at, mi.session_id, mi.trust_level,
		       mi.ingested_at, mi.written_by,
		       mi.trigger_condition, mi.utility_weight, mi.consolidated_from,
		       (1.0 / (1.0 + fts.rank))
		       * CASE mi.kind
				WHEN 'decision' THEN 1.5
				WHEN 'pattern' THEN 1.4
				WHEN 'bugfix' THEN 1.3
				WHEN 'discovery' THEN 1.2
				WHEN 'procedure' THEN 1.1
				ELSE 1.0
			 END
		       * CASE
				WHEN CAST(julianday('now') - julianday(mi.updated_at) AS REAL) <= 7.0 THEN 1.15
				WHEN CAST(julianday('now') - julianday(mi.updated_at) AS REAL) <= 30.0 THEN 1.05
				ELSE 1.0
			 END
		       * ` + outcomeBoostExpr + `
		       * CASE
				WHEN mi.utility_weight > 0 THEN mi.utility_weight
				ELSE 1.0
			 END
		       AS relevance_score
		FROM memory_items_fts fts
		JOIN memory_items mi ON mi.id = fts.rowid
		WHERE memory_items_fts MATCH ? AND mi.status = ?`
	args := []any{ftsQuery, status}

	// Only filter by expires_at when querying active items.
	// Explicit non-active queries (e.g., archived) should return all matching items.
	if originalStatus == "" || originalStatus == MemoryStatusActive {
		sqlQ += " AND (mi.expires_at IS NULL OR mi.expires_at = '' OR mi.expires_at > datetime('now'))"
	}

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
	if domain != "" {
		sqlQ += " AND mi.domain = ?"
		args = append(args, domain)
	}

	// Temporal filters
	if filters.asof != "" {
		sqlQ += " AND (mi.valid_from IS NULL OR mi.valid_from <= ?) AND (mi.valid_to IS NULL OR mi.valid_to > ?)"
		args = append(args, filters.asof, filters.asof)
	}
	if filters.since != "" {
		sqlQ += " AND mi.updated_at >= ?"
		args = append(args, filters.since)
	}
	if filters.ingestedAsof != "" {
		sqlQ += " AND mi.ingested_at <= ?"
		args = append(args, filters.ingestedAsof)
	}
	if filters.sessionID != "" {
		sqlQ += " AND mi.session_id = ?"
		args = append(args, filters.sessionID)
	}
	if filters.file != "" {
		sqlQ += " AND mi.applies_to_json LIKE ?"
		args = append(args, "%"+filters.file+"%")
	}
	if filters.path != "" {
		sqlQ += " AND mi.applies_to_json LIKE ?"
		args = append(args, "%"+filters.path+"%")
	}
	if writtenBy != "" {
		sqlQ += " AND mi.written_by = ?"
		args = append(args, writtenBy)
	}

	// Order by the pre-computed relevance_score
	sqlQ += ` ORDER BY relevance_score DESC LIMIT ?`
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if s.hybridEnabled() {
		items = s.blendHybridScores(items, ftsQuery, s.cfg.HybridAlpha)
	}
	return items, nil
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
		        source, status, superseded_by, expires_at,
		        domain, evidence_json, applies_to_json, related_json, classification,
		        access_count, last_accessed, valid_from, valid_to, superseded_at, session_id, trust_level,
		        ingested_at, written_by,
		        trigger_condition, utility_weight, consolidated_from,
		        0 AS relevance_score
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
		&m.Domain, &m.EvidenceJSON, &m.AppliesToJSON, &m.RelatedJSON, &m.Classification,
		&m.AccessCount, &m.LastAccessed, &m.ValidFrom, &m.ValidTo, &m.SupersededAt, &m.SessionID, &m.TrustLevel,
		&m.IngestedAt, &m.WrittenBy,
		&m.TriggerCondition, &m.UtilityWeight, &m.ConsolidatedFrom,
		&m.RelevanceScore,
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
		&m.Domain, &m.EvidenceJSON, &m.AppliesToJSON, &m.RelatedJSON, &m.Classification,
		&m.AccessCount, &m.LastAccessed, &m.ValidFrom, &m.ValidTo, &m.SupersededAt, &m.SessionID, &m.TrustLevel,
		&m.IngestedAt, &m.WrittenBy,
		&m.TriggerCondition, &m.UtilityWeight, &m.ConsolidatedFrom,
		&m.RelevanceScore,
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

func defaultClassificationForKind(kind string) string {
	switch kind {
	case MemoryKindDecision, MemoryKindProcedure:
		return "foundational"
	case MemoryKindDiscovery:
		return "observational"
	default:
		return "tactical"
	}
}

func (s *Store) tableExists(name string) bool {
	var c int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name = ?`, name).Scan(&c)
	return err == nil && c > 0
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
		       source, status, superseded_by, expires_at,
		       domain, evidence_json, applies_to_json, related_json, classification,
		       access_count, last_accessed, valid_from, valid_to, superseded_at, session_id, trust_level,
		       ingested_at, written_by,
		       trigger_condition, utility_weight, consolidated_from,
		       0 AS relevance_score
		FROM memory_items
		WHERE project_id = ? AND updated_at < ? AND status = 'active'
		ORDER BY updated_at DESC, id DESC
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
		       source, status, superseded_by, expires_at,
		       domain, evidence_json, applies_to_json, related_json, classification,
		       access_count, last_accessed, valid_from, valid_to, superseded_at, session_id, trust_level,
		       ingested_at, written_by,
		       trigger_condition, utility_weight, consolidated_from,
		       0 AS relevance_score
		FROM memory_items
		WHERE project_id = ? AND updated_at > ? AND status = 'active'
		ORDER BY updated_at ASC, id ASC
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

// ForgetMemory archives a memory with a documented reason.
// Sets status='archived', valid_to=now, and writes an audit log entry.
// The memory remains retrievable but is excluded from default active queries.
func (s *Store) ForgetMemory(id int64, reason, actorID string) error {
	return s.withTx(func(tx *sql.Tx) error {
		existing, err := s.getMemoryTx(tx, id)
		if err != nil {
			return err
		}

		// Archive the memory
		_, err = s.execHook(tx,
			`UPDATE memory_items SET status = 'archived',
			 valid_to = strftime('%Y-%m-%dT%H:%M:%f','now'),
			 updated_at = strftime('%Y-%m-%dT%H:%M:%f','now')
			 WHERE id = ?`, id)
		if err != nil {
			return err
		}

		// Write audit log
		if err := s.logMemoryAudit(tx, id, auditActionArchive, actorID, existing.SessionID, existing.TrustLevel); err != nil {
			return err
		}

		return nil
	})
}

// AddRelation creates a typed relation between two memory items.
func (s *Store) AddRelation(fromID, toID int64, relation string) error {
	_, err := s.execHook(s.db,
		`INSERT OR IGNORE INTO memory_relations (from_obs_id, to_obs_id, relation) VALUES (?, ?, ?)`,
		fromID, toID, relation)
	return err
}

// RemoveRelation removes a relation between two memory items.
func (s *Store) RemoveRelation(fromID, toID int64, relation string) error {
	_, err := s.execHook(s.db,
		`DELETE FROM memory_relations WHERE from_obs_id = ? AND to_obs_id = ? AND relation = ?`,
		fromID, toID, relation)
	return err
}

// GetRelated returns memory items related to the given ID.
func (s *Store) GetRelated(id int64, relation string) ([]MemoryItem, error) {
	query := `SELECT m.id, m.created_at, m.updated_at, m.project_id, m.actor_id, m.kind, m.scope, m.title, m.body, m.tags,
	          source, status, superseded_by, expires_at,
	          domain, evidence_json, applies_to_json, related_json, classification,
	          access_count, last_accessed, valid_from, valid_to, superseded_at, session_id, trust_level,
	          ingested_at, written_by,
	          trigger_condition, utility_weight, consolidated_from,
	          0 AS relevance_score
	          FROM memory_items m
	          JOIN memory_relations r ON (r.to_obs_id = m.id OR r.from_obs_id = m.id)
	          WHERE (r.from_obs_id = ? OR r.to_obs_id = ?)`
	args := []any{id, id}
	if relation != "" {
		query += ` AND r.relation = ?`
		args = append(args, relation)
	}
	rows, err := s.queryItHook(s.db, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []MemoryItem
	for rows.Next() {
		item, err := s.scanMemoryRow(rows)
		if err == nil {
			if item.ID != id {
				items = append(items, *item)
			}
		}
	}
	return items, nil
}

// CandidateGroup holds a group of source memories that can be consolidated.
type CandidateGroup struct {
	ProjectID    string
	Domain       string
	Kind         string
	SourceIDs    []int64
	SourceTitles []string
}

// int64sToCommaString converts a slice of int64 to a comma-separated string.
func int64sToCommaString(ids []int64) string {
	var sb strings.Builder
	for i, id := range ids {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf("%d", id))
	}
	return sb.String()
}

// GenerateConsolidationCandidates finds groups of observational memories (≥3 per
// project+domain+kind) and creates candidate memories for review. When dryRun is
// true, no writes are performed. Returns the number of candidates created (0 if
// dryRun) and summaries describing each candidate.
func (s *Store) GenerateConsolidationCandidates(project, domain string, dryRun bool) (int, []string, error) {
	// Query observational, active memories.
	query := `
		SELECT id, title, project_id, COALESCE(domain, '') AS dom, kind
		FROM memory_items
		WHERE status = 'active' AND classification = 'observational'`
	args := []any{}
	if project != "" {
		query += ` AND project_id = ?`
		args = append(args, project)
	}
	if domain != "" {
		query += ` AND domain = ?`
		args = append(args, domain)
	}
	query += ` ORDER BY project_id, dom, kind, updated_at DESC`

	rows, err := s.queryItHook(s.db, query, args...)
	if err != nil {
		return 0, nil, fmt.Errorf("consolidation query: %w", err)
	}
	defer rows.Close()

	// Accumulate into groups keyed by (project_id, domain, kind).
	type groupKey struct{ projectID, dom, kind string }
	groups := make(map[groupKey]*CandidateGroup)

	for rows.Next() {
		var id int64
		var title, proj, dom, kind string
		if err := rows.Scan(&id, &title, &proj, &dom, &kind); err != nil {
			return 0, nil, fmt.Errorf("scan row: %w", err)
		}
		k := groupKey{projectID: proj, dom: dom, kind: kind}
		g, ok := groups[k]
		if !ok {
			g = &CandidateGroup{ProjectID: proj, Domain: dom, Kind: kind}
			groups[k] = g
		}
		g.SourceIDs = append(g.SourceIDs, id)
		g.SourceTitles = append(g.SourceTitles, title)
	}
	if err := rows.Err(); err != nil {
		return 0, nil, fmt.Errorf("rows iteration: %w", err)
	}

	var candidates []*CandidateGroup
	for _, g := range groups {
		if len(g.SourceIDs) >= 3 {
			candidates = append(candidates, g)
		}
	}

	var summaries []string
	created := 0
	for _, g := range candidates {
		idsStr := int64sToCommaString(g.SourceIDs)

		// Duplicate avoidance: skip if a candidate with the same consolidated_from already exists.
		var existing int
		_ = s.db.QueryRow(
			`SELECT COUNT(*) FROM memory_items WHERE source = 'consolidation' AND consolidated_from = ?`,
			idsStr,
		).Scan(&existing)
		if existing > 0 {
			continue
		}

		// Build candidate content.
		domainPrefix := g.Domain
		if domainPrefix == "" {
			domainPrefix = g.ProjectID
		}
		candTitle := fmt.Sprintf("Consolidation candidate: %s/%s", domainPrefix, g.Kind)
		candBody := strings.Join(g.SourceTitles, "\n")

		if dryRun {
			summaries = append(summaries, fmt.Sprintf(
				"[dry-run] would create: %s (%d sources: %s)",
				candTitle, len(g.SourceIDs), strings.Join(g.SourceTitles, " | "),
			))
			continue
		}

		_, err := s.execHook(s.db,
			`INSERT INTO memory_items
			 (project_id, actor_id, kind, scope, title, body, tags, source, status,
			  domain, classification, trust_level, consolidated_from, session_id, expires_at,
			  evidence_json, applies_to_json, related_json, written_by, utility_weight,
			  trigger_condition, consolidated_from)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			g.ProjectID, "agent", g.Kind, MemoryScopeProject,
			candTitle, candBody, "[]", "consolidation",
			MemoryStatusCandidate, g.Domain, "tactical", "system",
			idsStr, "", nil, "", "", "", "consolidation", 0.0, "", idsStr,
		)
		if err != nil {
			return created, summaries, fmt.Errorf("insert candidate %s: %w", candTitle, err)
		}
		created++
		summaries = append(summaries, fmt.Sprintf("created: %s (%d sources)", candTitle, len(g.SourceIDs)))
	}

	return created, summaries, nil
}

// CountCandidates returns the number of candidate-status memories for a project
// (or all projects if project is empty).
func (s *Store) CountCandidates(project string) (int, error) {
	query := `SELECT COUNT(*) FROM memory_items WHERE status = ?`
	args := []any{MemoryStatusCandidate}
	if project != "" {
		query += ` AND project_id = ?`
		args = append(args, project)
	}
	var count int
	if err := s.db.QueryRow(query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count candidates: %w", err)
	}
	return count, nil
}

// ConsolidationCandidateGroup represents one candidate plus the episodic source
// memories it was built from. The calling agent synthesizes the semantic summary.
type ConsolidationCandidateGroup struct {
	Candidate MemoryItem
	Sources   []MemoryItem
}

// GetConsolidationCandidates returns candidate groups for agent review. Each
// group includes the candidate record plus the underlying episodic source
// memories referenced by consolidated_from.
func (s *Store) GetConsolidationCandidates(project, domain string) ([]ConsolidationCandidateGroup, error) {
	query := `
		SELECT id, created_at, updated_at, project_id, actor_id, kind, scope,
		       title, body, tags, source, status, superseded_by, expires_at,
		       domain, evidence_json, applies_to_json, related_json, classification,
		       access_count, last_accessed, valid_from, valid_to, superseded_at,
		       session_id, trust_level, ingested_at, written_by,
		       trigger_condition, utility_weight, consolidated_from, 0 AS relevance_score
		FROM memory_items
		WHERE status = ?`
	args := []any{MemoryStatusCandidate}
	if project != "" {
		query += ` AND project_id = ?`
		args = append(args, project)
	}
	if domain != "" {
		query += ` AND domain = ?`
		args = append(args, domain)
	}
	query += ` ORDER BY domain, kind, updated_at DESC`

	rows, err := s.queryItHook(s.db, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get consolidation candidates: %w", err)
	}
	defer rows.Close()

	var groups []ConsolidationCandidateGroup
	for rows.Next() {
		candidate, err := s.scanMemoryRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan candidate: %w", err)
		}

		sourceIDs := strings.Split(candidate.ConsolidatedFrom, ",")
		sources := make([]MemoryItem, 0, len(sourceIDs))
		for _, sidStr := range sourceIDs {
			sidStr = strings.TrimSpace(sidStr)
			if sidStr == "" {
				continue
			}
			sid, err := strconv.ParseInt(sidStr, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("parse consolidated_from %q: %w", sidStr, err)
			}
			source, err := s.GetMemory(sid)
			if err != nil {
				return nil, fmt.Errorf("load source memory %d: %w", sid, err)
			}
			sources = append(sources, *source)
		}

		groups = append(groups, ConsolidationCandidateGroup{
			Candidate: *candidate,
			Sources:   sources,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration: %w", err)
	}
	return groups, nil
}

// MarkConsolidated archives a reviewed candidate and its source episodic
// memories after the agent has saved a semantic consolidation memory.
func (s *Store) MarkConsolidated(candidateID, consolidatedMemoryID int64) error {
	return s.withTx(func(tx *sql.Tx) error {
		candidate, err := s.getMemoryTx(tx, candidateID)
		if err != nil {
			return fmt.Errorf("get candidate: %w", err)
		}
		if candidate.Status != MemoryStatusCandidate {
			return fmt.Errorf("memory %d is not a candidate (status=%s)", candidateID, candidate.Status)
		}

		consolidated, err := s.getMemoryTx(tx, consolidatedMemoryID)
		if err != nil {
			return fmt.Errorf("get consolidated memory: %w", err)
		}
		if consolidated.Source != "consolidation" {
			return fmt.Errorf("memory %d must have source='consolidation'", consolidatedMemoryID)
		}

		nowExpr := `strftime('%Y-%m-%dT%H:%M:%f','now')`
		_, err = s.execHook(tx,
			`UPDATE memory_items
			 SET status = 'archived', superseded_by = ?, superseded_at = `+nowExpr+`, updated_at = `+nowExpr+`
			 WHERE id = ?`,
			consolidatedMemoryID, candidateID,
		)
		if err != nil {
			return fmt.Errorf("archive candidate: %w", err)
		}

		sourceIDs := strings.Split(candidate.ConsolidatedFrom, ",")
		for _, sidStr := range sourceIDs {
			sidStr = strings.TrimSpace(sidStr)
			if sidStr == "" {
				continue
			}
			sid, err := strconv.ParseInt(sidStr, 10, 64)
			if err != nil {
				return fmt.Errorf("parse source id %q: %w", sidStr, err)
			}
			_, err = s.execHook(tx,
				`UPDATE memory_items
				 SET status = 'archived', superseded_by = ?, superseded_at = `+nowExpr+`, updated_at = `+nowExpr+`, written_by = 'consolidation'
				 WHERE id = ?`,
				consolidatedMemoryID, sid,
			)
			if err != nil {
				return fmt.Errorf("archive source memory %d: %w", sid, err)
			}
			_, err = s.execHook(tx,
				`INSERT OR IGNORE INTO memory_relations (from_obs_id, to_obs_id, relation) VALUES (?, ?, ?)`,
				consolidatedMemoryID, sid, RelationSupersedes,
			)
			if err != nil {
				return fmt.Errorf("link consolidated memory %d to source %d: %w", consolidatedMemoryID, sid, err)
			}
		}

		_, err = s.execHook(tx,
			`INSERT OR IGNORE INTO memory_relations (from_obs_id, to_obs_id, relation) VALUES (?, ?, ?)`,
			consolidatedMemoryID, candidateID, RelationImplements,
		)
		if err != nil {
			return fmt.Errorf("link consolidated memory %d to candidate %d: %w", consolidatedMemoryID, candidateID, err)
		}
		return nil
	})
}
