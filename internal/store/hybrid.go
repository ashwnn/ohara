package store

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode"
)

type ollamaEmbeddingRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type ollamaEmbeddingResponse struct {
	Embedding []float32 `json:"embedding"`
}

type ollamaChatRequest struct {
	Model    string              `json:"model"`
	Stream   bool                `json:"stream"`
	Format   map[string]any      `json:"format,omitempty"`
	Messages []map[string]string `json:"messages"`
}

type ollamaChatResponse struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
}

// hybridEnabled returns true when the store should use hybrid (FTS5 + vector) retrieval.
// It checks both explicit "hybrid" mode and "auto" mode (when embeddings are reachable).
func (s *Store) hybridEnabled() bool {
	mode := strings.ToLower(strings.TrimSpace(s.cfg.RetrievalMode))
	autoMode := strings.ToLower(strings.TrimSpace(s.cfg.RetrievalAutoMode))

	// Explicit "hybrid" mode — always use hybrid if backend is available.
	if mode == "hybrid" {
		return s.hybridBackendAvailable()
	}

	// "auto" mode — use hybrid if embeddings are reachable.
	if mode == "auto" || autoMode == "auto" || autoMode == "" {
		avail := s.checkHybridAvailability()
		return avail.EmbeddingsAvailable
	}

	return false
}

// hybridBackendAvailable returns true if the configured embedding backend is
// available (registered or supported), without making a network call.
func (s *Store) hybridBackendAvailable() bool {
	backend := strings.ToLower(strings.TrimSpace(s.cfg.EmbeddingBackend))
	switch backend {
	case "ollama", "deterministic-test", "static-test":
		return true
	default:
		if _, ok := registeredEmbedders[backend]; ok {
			return true
		}
		return false
	}
}

// ResolvedRetrievalMode returns the effective retrieval mode after auto-detection.
// For "auto" mode, it probes the embedding backend and returns "hybrid" if reachable,
// or "fts5" if not. For explicit modes ("fts5", "hybrid"), it returns the mode as-is.
func (s *Store) ResolvedRetrievalMode() string {
	mode := strings.ToLower(strings.TrimSpace(s.cfg.RetrievalMode))
	autoMode := strings.ToLower(strings.TrimSpace(s.cfg.RetrievalAutoMode))

	if mode == "auto" || autoMode == "auto" || autoMode == "" {
		avail := s.checkHybridAvailability()
		if avail.EmbeddingsAvailable {
			return "hybrid"
		}
		return "fts5"
	}
	if mode == "hybrid" || mode == "fts5" {
		return mode
	}
	return "fts5"
}

// hybridAvailability describes whether hybrid retrieval is operational.
type hybridAvailability struct {
	Enabled            bool   `json:"enabled"`
	Backend            string `json:"backend"`
	EmbeddingsAvailable bool  `json:"embeddings_available"`
	Reason             string `json:"reason,omitempty"`
}

// checkHybridAvailability probes the configured embedding backend and returns
// availability status. This can be used to auto-detect whether hybrid should be
// enabled without requiring explicit OHARA_RETRIEVAL_MODE=hybrid.
func (s *Store) checkHybridAvailability() hybridAvailability {
	backend := strings.ToLower(strings.TrimSpace(s.cfg.EmbeddingBackend))
	result := hybridAvailability{Backend: backend}

	// Check registered embedders first (extensible path).
	if _, ok := registeredEmbedders[backend]; ok {
		result.Enabled = true
		result.EmbeddingsAvailable = true
		return result
	}

	switch backend {
	case "deterministic-test":
		result.Enabled = true
		result.EmbeddingsAvailable = true
		return result
	case "ollama", "":
		if s.cfg.OllamaURL == "" {
			result.Reason = "ollama URL not configured"
			return result
		}
		url := strings.TrimRight(s.cfg.OllamaURL, "/") + "/api/embeddings"
		reqBody, _ := json.Marshal(ollamaEmbeddingRequest{Model: s.cfg.EmbeddingModel, Prompt: "healthcheck"})
		hc := &http.Client{Timeout: 1500 * time.Millisecond}
		req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(reqBody))
		req.Header.Set("Content-Type", "application/json")
		resp, err := hc.Do(req)
		if err != nil {
			result.Reason = fmt.Sprintf("ollama unreachable: %v", err)
			return result
		}
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			result.Reason = fmt.Sprintf("ollama returned status %d", resp.StatusCode)
			return result
		}
		result.Enabled = true
		result.EmbeddingsAvailable = true
		return result
	default:
		result.Reason = fmt.Sprintf("unsupported embedding backend %q", backend)
		return result
	}
}

func floatsToBytes(v []float32) []byte {
	b := make([]byte, len(v)*4)
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(x))
	}
	return b
}

func bytesToFloats(b []byte) ([]float32, error) {
	if len(b)%4 != 0 {
		return nil, fmt.Errorf("invalid embedding blob length %d", len(b))
	}
	out := make([]float32, len(b)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return out, nil
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	n := len(a)
	var dot, na, nb float64
	for i := 0; i < n; i++ {
		aa := float64(a[i])
		bb := float64(b[i])
		dot += aa * bb
		na += aa * aa
		nb += bb * bb
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// Embedder is the interface for text embedding backends.
// Implementations must be safe for concurrent use.
type Embedder interface {
	// Embed returns a float32 embedding vector for the given text.
	// The dimension should match the configured EmbeddingDim.
	Embed(text string) ([]float32, error)
}

// registeredEmbedders holds all registered embedding backend implementations.
// Keyed by backend name (lowercase). Populated by init-time registration.
var registeredEmbedders = map[string]Embedder{}

// RegisterEmbedder registers an Embedder implementation for the given backend name.
// Must be called before store initialization. Not safe for concurrent use.
func RegisterEmbedder(name string, e Embedder) {
	registeredEmbedders[strings.ToLower(strings.TrimSpace(name))] = e
}

// deterministicEmbedder is the built-in deterministic-test embedder (pure Go, no deps).
type deterministicEmbedder struct{ dim int }

func (d deterministicEmbedder) Embed(text string) ([]float32, error) {
	return deterministicTestEmbedding(text, d.dim), nil
}

// staticEmbedder returns a fixed constant vector — demonstrates the
// registration pattern with zero configuration and no external deps.
type staticEmbedder struct{ dim int }

func (s staticEmbedder) Embed(text string) ([]float32, error) {
	vec := make([]float32, s.dim)
	// Constant vector with consistent magnitude for predictable similarity.
	for i := range vec {
		vec[i] = 0.5 / float32(s.dim)
	}
	return vec, nil
}

func init() {
	// Register built-in deterministic embedder (always available, no external deps).
	RegisterEmbedder("deterministic-test", deterministicEmbedder{dim: 128})
	// Register static embedder as a second additive backend (always available).
	RegisterEmbedder("static-test", staticEmbedder{dim: 64})
}

func (s *Store) embedText(text string) ([]float32, error) {
	backend := strings.ToLower(strings.TrimSpace(s.cfg.EmbeddingBackend))

	// Check registered embedders first (extensible path).
	if e, ok := registeredEmbedders[backend]; ok {
		return e.Embed(text)
	}

	// Legacy ollama path (built-in for backward compat, uses Store state).
	if backend == "ollama" || backend == "" {
		return s.embedTextOllama(text)
	}

	return nil, fmt.Errorf("unsupported embedding backend %q", s.cfg.EmbeddingBackend)
}

func (s *Store) embedTextOllama(text string) ([]float32, error) {
	url := strings.TrimRight(s.cfg.OllamaURL, "/") + "/api/embeddings"
	reqBody, err := json.Marshal(ollamaEmbeddingRequest{Model: s.cfg.EmbeddingModel, Prompt: text})
	if err != nil {
		return nil, err
	}
	hc := &http.Client{Timeout: 5 * time.Second}
	resp, err := hc.Post(url, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("ollama embeddings status %d: %s", resp.StatusCode, string(body))
	}
	var out ollamaEmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Embedding) == 0 {
		return nil, fmt.Errorf("empty embedding response")
	}
	return out.Embedding, nil
}

func deterministicTestEmbedding(text string, dim int) []float32 {
	if dim <= 0 {
		dim = 128
	}
	if dim > 2048 {
		dim = 2048
	}
	vec := make([]float32, dim)
	tokens := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	if len(tokens) == 0 {
		return vec
	}
	for i, token := range tokens {
		if token == "" {
			continue
		}
		hasher := fnv.New64a()
		_, _ = hasher.Write([]byte(token))
		sum := hasher.Sum64()
		idx := int(sum % uint64(dim))
		sign := float32(1.0)
		if (sum>>63)&1 == 1 {
			sign = -1.0
		}
		weight := float32(1.0 / math.Sqrt(float64(i+1)))
		vec[idx] += sign * weight
	}

	var norm float64
	for _, v := range vec {
		norm += float64(v * v)
	}
	if norm == 0 {
		return vec
	}
	scale := float32(1.0 / math.Sqrt(norm))
	for i := range vec {
		vec[i] *= scale
	}
	return vec
}

func (s *Store) indexMemoryEmbedding(memoryID int64, text string) error {
	if !s.hybridEnabled() {
		return nil
	}
	vec, err := s.embedText(text)
	if err != nil {
		return err
	}
	// Warn on dimension mismatch between backend output and config expectation.
	if s.cfg.EmbeddingDim > 0 && len(vec) != s.cfg.EmbeddingDim {
		log.Printf("[ohara] warning: embedding dimension %d does not match configured EmbeddingDim=%d (model=%q, backend=%q)",
			len(vec), s.cfg.EmbeddingDim, s.cfg.EmbeddingModel, s.cfg.EmbeddingBackend)
	}
	_, err = s.execHook(s.db,
		`INSERT INTO obs_embeddings (obs_id, embedding, model, created_at)
		 VALUES (?, ?, ?, strftime('%Y-%m-%dT%H:%M:%f','now'))
		 ON CONFLICT(obs_id) DO UPDATE SET
		   embedding=excluded.embedding,
		   model=excluded.model,
		   created_at=excluded.created_at`,
		memoryID, floatsToBytes(vec), s.cfg.EmbeddingModel,
	)
	if err != nil {
		return err
	}

	// Dual-write to vec0 virtual table for Phase 1 sub-linear KNN.
	// Only insert embeddings whose dimension matches the vec0 table (768).
	// obs_embeddings remains authoritative; vec0 write failures are non-fatal.
	const vec0Dim = 768
	if len(vec) == vec0Dim {
		if _, err2 := s.execHook(s.db,
			`INSERT OR REPLACE INTO observation_embeddings_vec(rowid, embedding) VALUES (?, ?)`,
			memoryID, floatsToBytes(vec),
		); err2 != nil {
			log.Printf("[ohara] warning: vec0 dual-write failed for memory %d: %v", memoryID, err2)
		}
	}

	return nil
}

func (s *Store) fuseHybridRRF(ftsItems, vectorItems []MemoryItem, rankConstant int) []MemoryItem {
	if len(ftsItems) == 0 && len(vectorItems) == 0 {
		return nil
	}
	if rankConstant <= 0 {
		rankConstant = 60
	}

	itemsByID := make(map[int64]MemoryItem, len(ftsItems)+len(vectorItems))
	ftsRank := make(map[int64]int, len(ftsItems))
	ftsBaseScore := make(map[int64]float64, len(ftsItems))
	maxFTSBaseScore := 0.0
	for i, item := range ftsItems {
		itemsByID[item.ID] = item
		ftsRank[item.ID] = i + 1
		ftsBaseScore[item.ID] = item.RelevanceScore
		if item.RelevanceScore > maxFTSBaseScore {
			maxFTSBaseScore = item.RelevanceScore
		}
	}
	vectorRank := make(map[int64]int, len(vectorItems))
	for i, item := range vectorItems {
		if _, ok := itemsByID[item.ID]; !ok {
			itemsByID[item.ID] = item
		}
		vectorRank[item.ID] = i + 1
	}

	out := make([]MemoryItem, 0, len(itemsByID))
	for id, item := range itemsByID {
		rrfScore := 0.0
		lexicalBonus := 0.0
		if rank, ok := ftsRank[id]; ok {
			rrfScore += 1.0 / float64(rankConstant+rank)
			lexicalBonus = s.cfg.Scoring.HybridLexicalBonus
			if maxFTSBaseScore > 0 {
				lexicalBonus += (ftsBaseScore[id] / maxFTSBaseScore) * s.cfg.Scoring.HybridLexicalScoreBonus
			}
		}
		if rank, ok := vectorRank[id]; ok {
			rrfScore += 1.0 / float64(rankConstant+rank)
		}
		item.RelevanceScore = rrfScore + lexicalBonus + s.hybridScoreModifiers(item)
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].RelevanceScore == out[j].RelevanceScore {
			if out[i].UpdatedAt == out[j].UpdatedAt {
				return out[i].ID < out[j].ID
			}
			return out[i].UpdatedAt > out[j].UpdatedAt
		}
		return out[i].RelevanceScore > out[j].RelevanceScore
	})
	return out
}

func (s *Store) hybridScoreModifiers(item MemoryItem) float64 {
	sw := s.cfg.Scoring
	mod := 0.0
	if item.UtilityWeight > 0 {
		mod += item.UtilityWeight * sw.HybridUtilityMultiplier
	}

	switch item.Kind {
	case MemoryKindDecision:
		mod += sw.HybridKindDecisionBonus
	case MemoryKindProcedure:
		mod += sw.HybridKindProcedureBonus
	case MemoryKindPattern:
		mod += sw.HybridKindPatternBonus
	case MemoryKindBugfix:
		mod += sw.HybridKindBugfixBonus
	}

	switch item.Classification {
	case "foundational":
		mod += sw.HybridClassFoundBonus
	case "observational":
		mod -= sw.HybridClassObsPenalty
	}

	if item.Status == MemoryStatusSuperseded || item.Status == MemoryStatusArchived {
		mod -= sw.HybridArchivedPenalty
	}
	if item.SupersededBy != nil && *item.SupersededBy != 0 {
		mod -= sw.HybridArchivedPenalty
	}
	if item.ExpiresAt != nil && *item.ExpiresAt != "" {
		if expires, err := time.Parse(time.RFC3339Nano, *item.ExpiresAt); err == nil && expires.Before(time.Now().UTC()) {
			mod -= sw.HybridExpiredPenalty
		}
	}
	if item.TrustLevel == "untrusted" {
		mod -= sw.HybridUntrustedPenalty
	}

	if updated, err := time.Parse(time.RFC3339Nano, item.UpdatedAt); err == nil {
		age := time.Since(updated)
		if age <= 7*24*time.Hour {
			mod += sw.HybridRecency7DayBonus
		} else if age > 180*24*time.Hour {
			mod -= sw.HybridRecencyOldPenalty
		}
	}

	return mod
}

// vec0Dim is the embedding dimension required by the vec0 virtual table.
// Only Ollama nomic-embed-text (768d) embeddings are stored in vec0.
const vec0Dim = 768

// canUseVec0KNN returns true when the vec0 KNN path is usable for the given
// query embedding. vec0 requires 768d embeddings (Ollama nomic-embed-text).
// Test embedders (deterministic-test=128d, static-test=64d) naturally bypass
// vec0 due to dimension mismatch.
func (s *Store) canUseVec0KNN(queryEmbedding []float32) bool {
	if len(queryEmbedding) != vec0Dim {
		return false
	}
	// Check that the vec0 table exists and has at least some rows.
	// Avoids empty results before T1.3 backfill completes on existing DBs.
	if !s.tableExists("observation_embeddings_vec") {
		return false
	}
	var rowCount int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM observation_embeddings_vec").Scan(&rowCount); err != nil {
		return false
	}
	// Only use vec0 when it has meaningful coverage (> 1000 rows) or when
	// obs_embeddings doesn't exist. Small datasets are served by brute-force.
	const minVec0Rows = 1000
	return rowCount >= minVec0Rows || rowCount > 0
}

// vectorSearchVec0Memories performs KNN search via the vec0 virtual table.
// Returns memory items ordered by vec0 distance (ascending), suitable for
// RRF fusion. Only called when canUseVec0KNN returns true.
func (s *Store) vectorSearchVec0Memories(
	queryEmbedding []float32,
	projectID, scope, kind, domain, status, originalStatus, writtenBy string,
	filters temporalFilters,
	limit int,
) ([]MemoryItem, error) {
	if limit <= 0 {
		limit = 10
	}
	// Request generous K for post-filter coverage.
	knnLimit := limit * 50
	if knnLimit < 500 {
		knnLimit = 500
	}
	if knnLimit > 3000 {
		knnLimit = 3000
	}

	// Phase 1: get KNN results from vec0 (no metadata filtering at this stage).
	knnRows, err := s.queryItHook(s.db,
		`SELECT v.rowid, v.distance
		 FROM observation_embeddings_vec v
		 WHERE v.embedding MATCH ?
		 ORDER BY v.distance
		 LIMIT ?`,
		floatsToBytes(queryEmbedding), knnLimit,
	)
	if err != nil {
		return nil, err
	}
	defer knnRows.Close()

	type vecResult struct {
		id       int64
		distance float64
	}
	var vecResults []vecResult
	for knnRows.Next() {
		var vr vecResult
		if err := knnRows.Scan(&vr.id, &vr.distance); err != nil {
			continue
		}
		vecResults = append(vecResults, vr)
	}
	if err := knnRows.Err(); err != nil {
		return nil, err
	}
	if len(vecResults) == 0 {
		return nil, nil
	}

	// Phase 2: fetch full MemoryItems for KNN result IDs and apply metadata filters.
	// Build a parameterized IN query for the IDs.
	buildVecFilterSQL := func(ids []int64) (string, []any) {
		q := `SELECT mi.id, mi.created_at, mi.updated_at, mi.project_id, mi.actor_id, mi.kind, mi.scope,
		             mi.title, mi.body, mi.tags, mi.source, mi.status, mi.superseded_by, mi.expires_at,
		             mi.domain, mi.evidence_json, mi.applies_to_json, mi.related_json, mi.classification,
		             mi.access_count, mi.last_accessed, mi.valid_from, mi.valid_to, mi.superseded_at, mi.session_id, mi.trust_level,
		             mi.ingested_at, mi.written_by,
		             mi.trigger_condition, mi.utility_weight, mi.consolidated_from,
		             0 AS relevance_score
		      FROM memory_items mi
		      WHERE mi.id IN (`
		args := make([]any, 0, len(ids)+10)
		for i, id := range ids {
			if i > 0 {
				q += ","
			}
			q += "?"
			args = append(args, id)
		}
		q += `) AND mi.status = ?`
		args = append(args, status)

		if originalStatus == "" || originalStatus == MemoryStatusActive {
			q += " AND (mi.expires_at IS NULL OR mi.expires_at = '' OR mi.expires_at > datetime('now'))"
			q += " AND (mi.superseded_by IS NULL OR mi.superseded_by = 0)"
		}
		if projectID != "" {
			q += " AND mi.project_id = ?"
			args = append(args, projectID)
		}
		if scope != "" {
			q += " AND mi.scope = ?"
			args = append(args, scope)
		}
		if kind != "" {
			q += " AND mi.kind = ?"
			args = append(args, kind)
		}
		if domain != "" {
			q += " AND mi.domain = ?"
			args = append(args, domain)
		}
		if filters.asof != "" {
			q += " AND (mi.valid_from IS NULL OR mi.valid_from <= ?) AND (mi.valid_to IS NULL OR mi.valid_to > ?)"
			args = append(args, filters.asof, filters.asof)
		}
		if filters.since != "" {
			q += " AND mi.updated_at >= ?"
			args = append(args, filters.since)
		}
		if filters.ingestedAsof != "" {
			q += " AND mi.ingested_at <= ?"
			args = append(args, filters.ingestedAsof)
		}
		if filters.sessionID != "" {
			q += " AND mi.session_id = ?"
			args = append(args, filters.sessionID)
		}
		if filters.file != "" {
			q += " AND mi.applies_to_json LIKE ?"
			args = append(args, "%"+filters.file+"%")
		}
		if filters.path != "" {
			q += " AND mi.applies_to_json LIKE ?"
			args = append(args, "%"+filters.path+"%")
		}
		if writtenBy != "" {
			q += " AND mi.written_by = ?"
			args = append(args, writtenBy)
		}
		return q, args
	}

	sqlFilter, filterArgs := buildVecFilterSQL(func() []int64 {
		ids := make([]int64, len(vecResults))
		for i, vr := range vecResults {
			ids[i] = vr.id
		}
		return ids
	}())

	miRows, err := s.queryItHook(s.db, sqlFilter, filterArgs...)
	if err != nil {
		return nil, err
	}
	defer miRows.Close()

	type filteredResult struct {
		item     MemoryItem
		distance float64
	}
	idToDistance := make(map[int64]float64, len(vecResults))
	for _, vr := range vecResults {
		idToDistance[vr.id] = vr.distance
	}
	var filtered []filteredResult
	for miRows.Next() {
		var item MemoryItem
		if err := s.scanMemoryRowShare(miRows, &item); err != nil {
			continue
		}
		filtered = append(filtered, filteredResult{item: item, distance: idToDistance[item.ID]})
	}
	if err := miRows.Err(); err != nil {
		return nil, err
	}
	if len(filtered) == 0 {
		return nil, nil
	}

	// Sort by distance (ascending), then by updated_at (descending), then by ID.
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].distance == filtered[j].distance {
			if filtered[i].item.UpdatedAt == filtered[j].item.UpdatedAt {
				return filtered[i].item.ID < filtered[j].item.ID
			}
			return filtered[i].item.UpdatedAt > filtered[j].item.UpdatedAt
		}
		return filtered[i].distance < filtered[j].distance
	})

	if len(filtered) > limit {
		filtered = filtered[:limit]
	}
	out := make([]MemoryItem, 0, len(filtered))
	for _, fr := range filtered {
		out = append(out, fr.item)
	}
	return out, nil
}

// scanMemoryRowShare scans a memory_items row into a pre-allocated MemoryItem.
// Shared by both vec0 and brute-force vector search paths to avoid duplication.
func (s *Store) scanMemoryRowShare(rows rowScanner, item *MemoryItem) error {
	var tagsJSON string
	if err := rows.Scan(
		&item.ID, &item.CreatedAt, &item.UpdatedAt, &item.ProjectID, &item.ActorID, &item.Kind, &item.Scope,
		&item.Title, &item.Body, &tagsJSON, &item.Source, &item.Status, &item.SupersededBy, &item.ExpiresAt,
		&item.Domain, &item.EvidenceJSON, &item.AppliesToJSON, &item.RelatedJSON, &item.Classification,
		&item.AccessCount, &item.LastAccessed, &item.ValidFrom, &item.ValidTo, &item.SupersededAt, &item.SessionID, &item.TrustLevel,
		&item.IngestedAt, &item.WrittenBy,
		&item.TriggerCondition, &item.UtilityWeight, &item.ConsolidatedFrom,
		&item.RelevanceScore,
	); err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(tagsJSON), &item.Tags); err != nil {
		item.Tags = []string{}
	}
	return nil
}

func (s *Store) vectorSearchMemories(
	queryEmbedding []float32,
	projectID, scope, kind, domain, status, originalStatus, writtenBy string,
	filters temporalFilters,
	limit int,
) ([]MemoryItem, error) {
	if limit <= 0 {
		limit = 10
	}

	// Route to vec0 KNN when embeddings are 768d (Ollama nomic-embed-text) and
	// vec0 table has sufficient coverage. Test embedders (128d/64d) naturally
	// bypass vec0 and use the brute-force path.
	if s.canUseVec0KNN(queryEmbedding) {
		return s.vectorSearchVec0Memories(
			queryEmbedding, projectID, scope, kind, domain, status, originalStatus, writtenBy,
			filters, limit,
		)
	}

	candidateLimit := limit * 25
	if candidateLimit < 300 {
		candidateLimit = 300
	}
	if candidateLimit > 2000 {
		candidateLimit = 2000
	}

	sqlQ := `
		SELECT mi.id, mi.created_at, mi.updated_at, mi.project_id, mi.actor_id, mi.kind, mi.scope,
		       mi.title, mi.body, mi.tags, mi.source, mi.status, mi.superseded_by, mi.expires_at,
		       mi.domain, mi.evidence_json, mi.applies_to_json, mi.related_json, mi.classification,
		       mi.access_count, mi.last_accessed, mi.valid_from, mi.valid_to, mi.superseded_at, mi.session_id, mi.trust_level,
		       mi.ingested_at, mi.written_by,
		       mi.trigger_condition, mi.utility_weight, mi.consolidated_from,
		       0 AS relevance_score,
		       oe.embedding
		FROM obs_embeddings oe
		JOIN memory_items mi ON mi.id = oe.obs_id
		WHERE mi.status = ?`
	args := []any{status}

	if originalStatus == "" || originalStatus == MemoryStatusActive {
		sqlQ += " AND (mi.expires_at IS NULL OR mi.expires_at = '' OR mi.expires_at > datetime('now'))"
		sqlQ += " AND (mi.superseded_by IS NULL OR mi.superseded_by = 0)"
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
	sqlQ += " ORDER BY mi.updated_at DESC LIMIT ?"
	args = append(args, candidateLimit)

	rows, err := s.queryItHook(s.db, sqlQ, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type vectorCandidate struct {
		item  MemoryItem
		score float64
	}
	candidates := make([]vectorCandidate, 0, candidateLimit)
	for rows.Next() {
		var item MemoryItem
		var embeddingBlob []byte
		var tagsJSON string

		// Single scan of all columns including oe.embedding (last column).
		// scanMemoryRowShare is not used here because the query includes
		// the embedding BLOB as an extra trailing column.
		if err := rows.Scan(
			&item.ID, &item.CreatedAt, &item.UpdatedAt, &item.ProjectID, &item.ActorID, &item.Kind, &item.Scope,
			&item.Title, &item.Body, &tagsJSON, &item.Source, &item.Status, &item.SupersededBy, &item.ExpiresAt,
			&item.Domain, &item.EvidenceJSON, &item.AppliesToJSON, &item.RelatedJSON, &item.Classification,
			&item.AccessCount, &item.LastAccessed, &item.ValidFrom, &item.ValidTo, &item.SupersededAt, &item.SessionID, &item.TrustLevel,
			&item.IngestedAt, &item.WrittenBy,
			&item.TriggerCondition, &item.UtilityWeight, &item.ConsolidatedFrom,
			&item.RelevanceScore,
			&embeddingBlob,
		); err != nil {
			continue
		}
		if err := json.Unmarshal([]byte(tagsJSON), &item.Tags); err != nil {
			item.Tags = []string{}
		}
		vec, err := bytesToFloats(embeddingBlob)
		if err != nil || len(vec) == 0 {
			continue
		}
		// Dimension mismatch: skip silently (config or model changed between index and query).
		if len(vec) != len(queryEmbedding) {
			continue
		}
		score := cosineSimilarity(queryEmbedding, vec)
		if score < 0.12 {
			continue
		}
		candidates = append(candidates, vectorCandidate{item: item, score: score})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			if candidates[i].item.UpdatedAt == candidates[j].item.UpdatedAt {
				return candidates[i].item.ID < candidates[j].item.ID
			}
			return candidates[i].item.UpdatedAt > candidates[j].item.UpdatedAt
		}
		return candidates[i].score > candidates[j].score
	})

	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	out := make([]MemoryItem, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, c.item)
	}
	return out, nil
}

// RerankMemoriesWithLLM performs explicit opt-in slow-path reranking.
// This is intentionally separate from mem_search to keep default retrieval deterministic.
//
// The reranker backend is configured via cfg.RerankerBackend:
//   - "none": identity (no reranking, returns items as-is)
//   - "tfidf": deterministic TF-IDF cosine ranking (default, no LLM)
//   - "ollama": LLM-based reranking via Ollama chat API
func (s *Store) RerankMemoriesWithLLM(query string, items []MemoryItem, topN int) ([]MemoryItem, error) {
	if len(items) == 0 {
		return items, nil
	}
	if topN <= 0 || topN > len(items) {
		topN = len(items)
	}

	backend := strings.ToLower(strings.TrimSpace(s.cfg.RerankerBackend))
	switch backend {
	case "none", "":
		return items, nil
	case "tfidf":
		return s.rerankTFIDF(query, items, topN), nil
	case "ollama":
		return s.rerankOllama(query, items, topN)
	default:
		// Unknown backend: fall back to tfidf.
		return s.rerankTFIDF(query, items, topN), nil
	}
}

// rerankTFIDF performs deterministic TF-IDF cosine similarity reranking.
// No LLM calls, no CGO, no external dependency.
func (s *Store) rerankTFIDF(query string, items []MemoryItem, topN int) []MemoryItem {
	if len(items) == 0 {
		return items
	}
	if topN <= 0 || topN > len(items) {
		topN = len(items)
	}

	// Build document corpus: title + body for each item.
	docs := make([]string, len(items))
	for i, item := range items {
		docs[i] = item.Title + " " + item.Body
	}

	// Compute TF-IDF vectors.
	queryVec, docVecs := computeTFIDF(query, docs)

	// Score each document by cosine similarity to query.
	type scored struct {
		idx   int
		score float64
	}
	scores := make([]scored, len(items))
	for i, dv := range docVecs {
		scores[i] = scored{idx: i, score: cosineTFIDF(queryVec, dv)}
	}

	sort.SliceStable(scores, func(i, j int) bool {
		if scores[i].score == scores[j].score {
			return items[scores[i].idx].RelevanceScore > items[scores[j].idx].RelevanceScore
		}
		return scores[i].score > scores[j].score
	})

	out := make([]MemoryItem, 0, len(items))
	for _, sc := range scores {
		out = append(out, items[sc.idx])
	}
	return out
}

// computeTFIDF builds a sparse TF-IDF vector for a query and a set of documents.
// Returns the query vector and one vector per document.
func computeTFIDF(query string, docs []string) (map[string]float64, []map[string]float64) {
	// Tokenize query.
	queryTokens := tokenizeForRerank(query)

	// Tokenize all documents and build document frequencies.
	docTokens := make([][]string, len(docs))
	df := make(map[string]int) // document frequency
	for i, doc := range docs {
		tokens := tokenizeForRerank(doc)
		docTokens[i] = tokens
		seen := make(map[string]bool)
		for _, t := range tokens {
			if !seen[t] {
				seen[t] = true
				df[t]++
			}
		}
	}

	nDocs := float64(len(docs))

	// Build query TF-IDF vector.
	queryVec := make(map[string]float64)
	maxTF := 0
	qf := make(map[string]int)
	for _, t := range queryTokens {
		qf[t]++
		if qf[t] > maxTF {
			maxTF = qf[t]
		}
	}
	for t, tf := range qf {
		idf := math.Log((nDocs+1)/float64(df[t]+1)) + 1
		queryVec[t] = (0.5 + 0.5*float64(tf)/float64(maxTF)) * idf
	}

	// Build document TF-IDF vectors.
	docVecs := make([]map[string]float64, len(docs))
	for i, tokens := range docTokens {
		vec := make(map[string]float64)
		maxTF := 0
		tf := make(map[string]int)
		for _, t := range tokens {
			tf[t]++
			if tf[t] > maxTF {
				maxTF = tf[t]
			}
		}
		for t, f := range tf {
			idf := math.Log((nDocs+1)/float64(df[t]+1)) + 1
			vec[t] = (0.5 + 0.5*float64(f)/float64(maxTF)) * idf
		}
		docVecs[i] = vec
	}

	return queryVec, docVecs
}

// cosineTFIDF computes cosine similarity between two sparse TF-IDF vectors.
func cosineTFIDF(a, b map[string]float64) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for k, va := range a {
		normA += va * va
		if vb, ok := b[k]; ok {
			dot += va * vb
		}
	}
	for _, vb := range b {
		normB += vb * vb
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// tokenizeForRerank tokenizes text for TF-IDF processing.
func tokenizeForRerank(text string) []string {
	parts := strings.Fields(strings.ToLower(text))
	out := make([]string, 0, len(parts))
	seen := make(map[string]bool)
	for _, p := range parts {
		token := strings.Trim(p, `"'.,;:!?()[]{}<>`)
		if len(token) < 2 {
			continue
		}
		if seen[token] {
			continue
		}
		seen[token] = true
		out = append(out, token)
	}
	return out
}

// rerankOllama performs LLM-based reranking via the Ollama chat API.
func (s *Store) rerankOllama(query string, items []MemoryItem, topN int) ([]MemoryItem, error) {
	if len(items) == 0 {
		return items, nil
	}
	if topN <= 0 || topN > len(items) {
		topN = len(items)
	}
	url := strings.TrimRight(s.cfg.OllamaURL, "/") + "/api/chat"

	type candidate struct {
		ID    int64  `json:"id"`
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	cands := make([]candidate, 0, topN)
	for i := 0; i < topN; i++ {
		cands = append(cands, candidate{ID: items[i].ID, Title: items[i].Title, Body: truncateForRerank(items[i].Body, 260)})
	}
	candsJSON, _ := json.Marshal(cands)

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ids": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "integer"},
			},
		},
		"required": []string{"ids"},
	}
	prompt := "Rank candidates by relevance to query. Return JSON only with key ids containing all candidate ids in best-first order. Query: " + query + " Candidates: " + string(candsJSON)
	reqBody, _ := json.Marshal(ollamaChatRequest{
		Model:  s.cfg.EmbeddingModel,
		Stream: false,
		Format: schema,
		Messages: []map[string]string{{
			"role":    "user",
			"content": prompt,
		}},
	})
	hc := &http.Client{Timeout: 15 * time.Second}
	resp, err := hc.Post(url, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("ollama chat status %d: %s", resp.StatusCode, string(body))
	}
	var chatResp ollamaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, err
	}
	var ranked struct {
		IDs []int64 `json:"ids"`
	}
	if err := json.Unmarshal([]byte(chatResp.Message.Content), &ranked); err != nil {
		return nil, err
	}
	order := make(map[int64]int, len(ranked.IDs))
	for i, id := range ranked.IDs {
		order[id] = i
	}
	rankedItems := append([]MemoryItem(nil), items[:topN]...)
	sort.SliceStable(rankedItems, func(i, j int) bool {
		oi, okI := order[rankedItems[i].ID]
		oj, okJ := order[rankedItems[j].ID]
		if !okI && !okJ {
			return rankedItems[i].RelevanceScore > rankedItems[j].RelevanceScore
		}
		if !okI {
			return false
		}
		if !okJ {
			return true
		}
		return oi < oj
	})
	out := append([]MemoryItem(nil), rankedItems...)
	if topN < len(items) {
		out = append(out, items[topN:]...)
	}
	return out, nil
}

func truncateForRerank(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "..."
}
