package store

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
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

func (s *Store) hybridEnabled() bool {
	if !strings.EqualFold(strings.TrimSpace(s.cfg.RetrievalMode), "hybrid") {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(s.cfg.EmbeddingBackend)) {
	case "ollama", "deterministic-test":
		return true
	default:
		return false
	}
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
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
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

func (s *Store) embedText(text string) ([]float32, error) {
	switch strings.ToLower(strings.TrimSpace(s.cfg.EmbeddingBackend)) {
	case "ollama", "":
		return s.embedTextOllama(text)
	case "deterministic-test":
		return deterministicTestEmbedding(text, s.cfg.EmbeddingDim), nil
	default:
		return nil, fmt.Errorf("unsupported embedding backend %q", s.cfg.EmbeddingBackend)
	}
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
	_, err = s.execHook(s.db,
		`INSERT INTO obs_embeddings (obs_id, embedding, model, created_at)
		 VALUES (?, ?, ?, strftime('%Y-%m-%dT%H:%M:%f','now'))
		 ON CONFLICT(obs_id) DO UPDATE SET
		   embedding=excluded.embedding,
		   model=excluded.model,
		   created_at=excluded.created_at`,
		memoryID, floatsToBytes(vec), s.cfg.EmbeddingModel,
	)
	return err
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
			lexicalBonus = 0.010
			if maxFTSBaseScore > 0 {
				lexicalBonus += (ftsBaseScore[id] / maxFTSBaseScore) * 0.004
			}
		}
		if rank, ok := vectorRank[id]; ok {
			rrfScore += 1.0 / float64(rankConstant+rank)
		}
		item.RelevanceScore = rrfScore + lexicalBonus + hybridScoreModifiers(item)
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

func hybridScoreModifiers(item MemoryItem) float64 {
	mod := 0.0
	if item.UtilityWeight > 0 {
		mod += item.UtilityWeight * 0.004
	}

	switch item.Kind {
	case MemoryKindDecision:
		mod += 0.004
	case MemoryKindProcedure:
		mod += 0.0035
	case MemoryKindPattern:
		mod += 0.003
	case MemoryKindBugfix:
		mod += 0.0025
	}

	switch item.Classification {
	case "foundational":
		mod += 0.002
	case "observational":
		mod -= 0.0015
	}

	if item.Status == MemoryStatusSuperseded || item.Status == MemoryStatusArchived {
		mod -= 0.004
	}
	if item.SupersededBy != nil && *item.SupersededBy != 0 {
		mod -= 0.004
	}
	if item.ExpiresAt != nil && *item.ExpiresAt != "" {
		if expires, err := time.Parse(time.RFC3339Nano, *item.ExpiresAt); err == nil && expires.Before(time.Now().UTC()) {
			mod -= 0.004
		}
	}
	if item.TrustLevel == "untrusted" {
		mod -= 0.002
	}

	if updated, err := time.Parse(time.RFC3339Nano, item.UpdatedAt); err == nil {
		age := time.Since(updated)
		if age <= 7*24*time.Hour {
			mod += 0.002
		} else if age > 180*24*time.Hour {
			mod -= 0.002
		}
	}

	return mod
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
		var tagsJSON string
		var embeddingBlob []byte
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
