// Package longmemevaljudge provides an Ollama-based answer-quality judge
// for the LongMemEval benchmark. It is only used in offline/scheduled bench
// jobs — never in the default retrieval path.
//
// The judge uses a local Ollama model (e.g. Qwen3 0.6B GGUF) with JSON
// output mode, zero temperature, fixed seed, and /no_think suppression for
// deterministic scoring.
//
// Expected local model setup (not automated):
//
//	# Create a Modelfile or use an existing Ollama-compatible GGUF:
//	ollama pull qwen3:0.6b
//	# Or from a local GGUF file:
//	#   ollama create my-judge -f Modelfile  # Modelfile: FROM ./qwen3-0.6b.gguf
//
// Then run the benchmark:
//
//	go run ./bench/cmd/run-longmemeval/ -k 5 -ollama-judge -ollama-model qwen3:0.6b
package longmemevaljudge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/ashwnn/ohara/bench/longmemeval"
)

// DefaultOllamaURL is the default Ollama API endpoint.
const DefaultOllamaURL = "http://localhost:11434"

// DefaultJudgeModel is the default model name for the Ollama judge.
const DefaultJudgeModel = "qwen3:0.6b"

// OllamaJudgeConfig configures the Ollama-based answer judge.
type OllamaJudgeConfig struct {
	// URL is the Ollama API endpoint (default: http://localhost:11434).
	URL string
	// Model is the Ollama model name (default: qwen3:0.6b).
	Model string
	// Temperature for generation. 0 = greedy/deterministic (default: 0).
	Temperature float64
	// Seed for reproducible sampling (default: 42).
	Seed int
	// Timeout for the HTTP request to Ollama (default: 30s).
	Timeout time.Duration
	// ContextLength is the model context window in tokens (default: 512).
	ContextLength int
}

// DefaultJudgeConfig returns a sensible default configuration.
func DefaultJudgeConfig() OllamaJudgeConfig {
	return OllamaJudgeConfig{
		URL:           DefaultOllamaURL,
		Model:         DefaultJudgeModel,
		Temperature:   0,
		Seed:          42,
		Timeout:       30 * time.Second,
		ContextLength: 512,
	}
}

// OllamaJudge implements longmemeval.JudgeModel using Ollama chat API with
// JSON output mode. Intended for offline benchmark jobs only.
type OllamaJudge struct {
	config OllamaJudgeConfig
	client *http.Client
}

// NewOllamaJudge creates a new OllamaJudge with the given config.
// If cfg is zero-valued, DefaultJudgeConfig() is used.
func NewOllamaJudge(cfg OllamaJudgeConfig) *OllamaJudge {
	if cfg.URL == "" {
		cfg.URL = DefaultOllamaURL
	}
	if cfg.Model == "" {
		cfg.Model = DefaultJudgeModel
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.ContextLength <= 0 {
		cfg.ContextLength = 512
	}
	return &OllamaJudge{
		config: cfg,
		client: &http.Client{Timeout: cfg.Timeout},
	}
}

// ---------------------------------------------------------------------------
// JudgeModel interface
// ---------------------------------------------------------------------------

// Score evaluates how well the retrieved bodies match the expected bodies.
// It sends a single LLM call per evaluation, asking the model to rate
// answer quality for each (query, retrieved, expected) tuple.
func (j *OllamaJudge) Score(query string, retrievedBodies []string, expectedBodies []string) float64 {
	if len(retrievedBodies) == 0 || len(expectedBodies) == 0 {
		return 0
	}

	prompt := buildJudgePrompt(query, retrievedBodies, expectedBodies)
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"score": map[string]any{
				"type": "number",
			},
			"reasoning": map[string]any{
				"type": "string",
			},
		},
		"required": []string{"score"},
	}

	reqBody, _ := json.Marshal(judgeRequest{
		Model:  j.config.Model,
		Stream: false,
		Format: schema,
		Options: map[string]any{
			"temperature":      j.config.Temperature,
			"seed":             j.config.Seed,
			"num_predict":      128,
			"num_ctx":          j.config.ContextLength,
		},
		Messages: []map[string]string{
			{"role": "system", "content": systemPrompt()},
			{"role": "user", "content": prompt},
		},
	})

	url := j.config.URL + "/api/chat"
	resp, err := j.client.Post(url, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return 0
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return 0
	}

	var chatResp judgeResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return 0
	}

	var result judgeScore
	if err := json.Unmarshal([]byte(chatResp.Message.Content), &result); err != nil {
		return 0
	}

	if result.Score < 0 {
		return 0
	}
	if result.Score > 1 {
		return 1
	}
	return result.Score
}

// ---------------------------------------------------------------------------
// prompts
// ---------------------------------------------------------------------------

func systemPrompt() string {
	return `You are an answer quality judge. Evaluate how well the retrieved answer text matches the expected correct answer.

Rules:
- Return JSON only with keys: score (float 0.0–1.0) and reasoning (short string).
- Score 1.0 = perfect semantic match. Score 0.0 = completely unrelated.
- Focus on semantic equivalence, not exact wording.
- Be strict: partial information gets partial credit.
- /no_think`
}

func buildJudgePrompt(query string, retrievedBodies []string, expectedBodies []string) string {
	expected := "Expected answer(s):\n"
	for i, b := range expectedBodies {
		expected += fmt.Sprintf("  [%d] %s\n", i+1, truncate(b, 300))
	}
	retrieved := "Retrieved answer(s):\n"
	for i, b := range retrievedBodies {
		retrieved += fmt.Sprintf("  [%d] %s\n", i+1, truncate(b, 300))
	}
	return fmt.Sprintf("Query: %s\n\n%s\n%s\n\nRate how well the retrieved answers match the expected answers. Output JSON with score (0.0-1.0) and reasoning.", query, expected, retrieved)
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}

// ---------------------------------------------------------------------------
// internal request/response types
// ---------------------------------------------------------------------------

type judgeRequest struct {
	Model    string              `json:"model"`
	Stream   bool                `json:"stream"`
	Format   map[string]any      `json:"format,omitempty"`
	Options  map[string]any      `json:"options,omitempty"`
	Messages []map[string]string `json:"messages"`
}

type judgeResponse struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
}

type judgeScore struct {
	Score     float64 `json:"score"`
	Reasoning string  `json:"reasoning,omitempty"`
}

// Compile-time interface check.
var _ longmemeval.JudgeModel = (*OllamaJudge)(nil)
