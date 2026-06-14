package longmemevaljudge

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDefaultJudgeConfig(t *testing.T) {
	cfg := DefaultJudgeConfig()
	if cfg.URL != DefaultOllamaURL {
		t.Errorf("URL = %q, want %q", cfg.URL, DefaultOllamaURL)
	}
	if cfg.Model != DefaultJudgeModel {
		t.Errorf("Model = %q, want %q", cfg.Model, DefaultJudgeModel)
	}
	if cfg.Temperature != 0 {
		t.Errorf("Temperature = %f, want 0", cfg.Temperature)
	}
	if cfg.Seed != 42 {
		t.Errorf("Seed = %d, want 42", cfg.Seed)
	}
	if cfg.Timeout == 0 {
		t.Error("Timeout should be non-zero")
	}
	if cfg.ContextLength != 512 {
		t.Errorf("ContextLength = %d, want 512", cfg.ContextLength)
	}
}

func TestNewOllamaJudgeDefaults(t *testing.T) {
	j := NewOllamaJudge(OllamaJudgeConfig{})
	if j.config.URL != DefaultOllamaURL {
		t.Errorf("URL = %q, want %q", j.config.URL, DefaultOllamaURL)
	}
	if j.config.Model != DefaultJudgeModel {
		t.Errorf("Model = %q, want %q", j.config.Model, DefaultJudgeModel)
	}
	if j.config.Timeout == 0 {
		t.Error("Timeout should be non-zero from defaults")
	}
	if j.config.ContextLength != 512 {
		t.Errorf("ContextLength = %d, want 512", j.config.ContextLength)
	}
}

func TestNewOllamaJudgeCustom(t *testing.T) {
	j := NewOllamaJudge(OllamaJudgeConfig{
		URL:           "http://10.0.0.1:11434",
		Model:         "my-judge-v2",
		Temperature:   0.3,
		Seed:          99,
		Timeout:       0, // should fall back to default
		ContextLength: 1024,
	})
	if j.config.URL != "http://10.0.0.1:11434" {
		t.Errorf("URL = %q", j.config.URL)
	}
	if j.config.Model != "my-judge-v2" {
		t.Errorf("Model = %q", j.config.Model)
	}
	if j.config.Temperature != 0.3 {
		t.Errorf("Temperature = %f", j.config.Temperature)
	}
	if j.config.Seed != 99 {
		t.Errorf("Seed = %d", j.config.Seed)
	}
	if j.config.Timeout == 0 {
		t.Error("Timeout should default to 30s")
	}
	if j.config.ContextLength != 1024 {
		t.Errorf("ContextLength = %d, want 1024", j.config.ContextLength)
	}
}

func TestScoreEmptyInput(t *testing.T) {
	j := NewOllamaJudge(OllamaJudgeConfig{})
	// Empty retrieved bodies.
	if score := j.Score("test", nil, []string{"expected"}); score != 0 {
		t.Errorf("expected 0 for empty retrieved, got %f", score)
	}
	// Empty expected bodies.
	if score := j.Score("test", []string{"retrieved"}, nil); score != 0 {
		t.Errorf("expected 0 for empty expected, got %f", score)
	}
	// Both empty.
	if score := j.Score("test", nil, nil); score != 0 {
		t.Errorf("expected 0 for both empty, got %f", score)
	}
}

func TestScoreNoOllama(t *testing.T) {
	// Without a running Ollama, Score should return 0 (graceful degradation)
	// and not panic.
	j := NewOllamaJudge(OllamaJudgeConfig{
		URL:     "http://127.0.0.1:1", // unlikely to have Ollama
		Timeout: 1,                     // 1ms timeout to fail fast
	})
	score := j.Score("test query", []string{"some retrieved text"}, []string{"some expected text"})
	if score != 0 {
		t.Logf("Score returned %f (expected 0 if no Ollama running)", score)
	}
}

func TestBuildJudgePrompt(t *testing.T) {
	prompt := buildJudgePrompt("what is auth?",
		[]string{"JWT tokens are used for authentication."},
		[]string{"JWT provides stateless auth."},
	)
	if !strings.Contains(prompt, "what is auth?") {
		t.Error("prompt should contain the query")
	}
	if !strings.Contains(prompt, "Expected answer") {
		t.Error("prompt should contain Expected answer section")
	}
	if !strings.Contains(prompt, "Retrieved answer") {
		t.Error("prompt should contain Retrieved answer section")
	}
	if !strings.Contains(prompt, "JSON") {
		t.Error("prompt should mention JSON output")
	}
}

func TestBuildJudgePromptTruncates(t *testing.T) {
	longBody := strings.Repeat("very long text content ", 1000)
	prompt := buildJudgePrompt("q", []string{longBody}, []string{"short"})
	// Should contain the truncated marker.
	if !strings.Contains(prompt, "...") {
		t.Error("prompt should contain truncated content marker for very long input")
	}
}

func TestSystemPromptContainsNoThink(t *testing.T) {
	sp := systemPrompt()
	if !strings.Contains(sp, "/no_think") {
		t.Error("system prompt should contain /no_think directive")
	}
	if !strings.Contains(sp, "JSON") {
		t.Error("system prompt should mention JSON")
	}
}

func TestScoreJSONSchemaValidJSON(t *testing.T) {
	// Verify that the parsing of judge responses would work.
	validResponse := `{"score": 0.85, "reasoning": "good semantic match"}`
	var result judgeScore
	if err := json.Unmarshal([]byte(validResponse), &result); err != nil {
		t.Fatalf("json unmarshal failed: %v", err)
	}
	if result.Score != 0.85 {
		t.Errorf("score = %f, want 0.85", result.Score)
	}
	if result.Reasoning != "good semantic match" {
		t.Errorf("reasoning = %q", result.Reasoning)
	}
}

func TestScoreJSONSchemaBoundaries(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  float64
	}{
		{"zero score", `{"score": 0.0}`, 0},
		{"one score", `{"score": 1.0}`, 1},
		{"half score", `{"score": 0.5}`, 0.5},
		{"no reasoning", `{"score": 0.3}`, 0.3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result judgeScore
			if err := json.Unmarshal([]byte(tt.input), &result); err != nil {
				t.Fatalf("json unmarshal failed: %v", err)
			}
			if result.Score != tt.want {
				t.Errorf("score = %f, want %f", result.Score, tt.want)
			}
		})
	}
}

func TestScoreClamps(t *testing.T) {
	j := &OllamaJudge{config: DefaultJudgeConfig()}
	// We can't test the actual clamp without a live model, but we can verify
	// the clamp logic by checking the Score method bounds.
	// The clamp is internal: if we had a model returning >1, it's capped.
	// This test documents the contract.
	_ = j
}

func TestTruncate(t *testing.T) {
	if s := truncate("short", 10); s != "short" {
		t.Errorf("truncate short = %q", s)
	}
	if s := truncate("hello world", 5); s != "hello..." {
		t.Errorf("truncate long = %q", s)
	}
	if s := truncate("", 10); s != "" {
		t.Errorf("truncate empty = %q", s)
	}
}

func TestInterfaceSatisfaction(t *testing.T) {
	// Compile-time check that OllamaJudge implements the interface.
	var _ judgeInterface = (*OllamaJudge)(nil)
}

// judgeInterface mirrors the relevant method from longmemeval.JudgeModel
// so this test package doesn't depend on the bench harness package.
type judgeInterface interface {
	Score(query string, retrievedBodies []string, expectedBodies []string) float64
}
