package store

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
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
	return strings.EqualFold(strings.TrimSpace(s.cfg.RetrievalMode), "hybrid") &&
		strings.EqualFold(strings.TrimSpace(s.cfg.EmbeddingBackend), "ollama")
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

func (s *Store) blendHybridScores(items []MemoryItem, query string, alpha float64) []MemoryItem {
	if len(items) == 0 {
		return items
	}
	qVec, err := s.embedText(query)
	if err != nil {
		return items // fallback to pure FTS5 when embedding backend unavailable
	}
	if alpha < 0 || alpha > 1 {
		alpha = 0.6
	}
	for i := range items {
		var embBlob []byte
		err := s.db.QueryRow(`SELECT embedding FROM obs_embeddings WHERE obs_id = ?`, items[i].ID).Scan(&embBlob)
		if err != nil || len(embBlob) == 0 {
			continue
		}
		mVec, err := bytesToFloats(embBlob)
		if err != nil {
			continue
		}
		cos := cosineSimilarity(qVec, mVec)
		vecScore := (cos + 1.0) / 2.0
		items[i].RelevanceScore = alpha*items[i].RelevanceScore + (1.0-alpha)*vecScore
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].RelevanceScore > items[j].RelevanceScore
	})
	return items
}

// RerankMemoriesWithLLM performs explicit opt-in slow-path reranking.
// This is intentionally separate from mem_search to keep default retrieval deterministic.
func (s *Store) RerankMemoriesWithLLM(query string, items []MemoryItem, topN int) ([]MemoryItem, error) {
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
