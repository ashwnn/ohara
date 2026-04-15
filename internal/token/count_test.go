package token

import (
	"strings"
	"testing"
)

func TestCount_EmptyString(t *testing.T) {
	got := Count("")
	if got != 0 {
		t.Errorf("Count(%q) = %d; want 0", "", got)
	}
}

func TestCount_SingleWord(t *testing.T) {
	got := Count("hello")
	if got < 1 {
		t.Errorf("Count(%q) = %d; want >= 1", "hello", got)
	}
}

func TestCount_KnownTexts(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{"short sentence", "The quick brown fox"},
		{"code snippet", "func main() { fmt.Println(\"hello\") }"},
		{"longer text", "Tokenization is the process of breaking down text into smaller units called tokens. These tokens can be words, subwords, or characters, depending on the tokenization strategy used."},
		{"special characters", "Hello, world! @#$%^&*()[]{}|;':\",./<>?"},
		{"unicode", "こんにちは世界 مرحبا العالم 🎉"},
		{"repeated words", "test test test test test"},
		{"numbers", "123 456 789 0"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Count(tc.text)
			// BPE tokenization typically produces 1-2 tokens per word for English
			// For "The quick brown fox" - should be around 4-6 tokens
			if got < 1 {
				t.Errorf("Count(%q) = %d; want >= 1", tc.text, got)
			}
			// Sanity check: count should be reasonable for the text length
			// BPE tokenizes punctuation as separate tokens, so char-heavy text
			// can have many more tokens than space-separated words
			words := len(strings.Fields(tc.text))
			if words > 0 {
				// For text with lots of punctuation, BPE can give more tokens
				// than just word count. The upper bound is very generous.
				if got > words*10 {
					t.Errorf("Count(%q) = %d; seems too high for %d words", tc.text, got, words)
				}
			}
		})
	}
}

func TestCount_Consistency(t *testing.T) {
	text := "This is a test of token counting consistency"

	// Count should be consistent across multiple calls
	first := Count(text)
	for i := 0; i < 10; i++ {
		got := Count(text)
		if got != first {
			t.Errorf("Count() inconsistent: got %d on call %d, expected %d", got, i+1, first)
		}
	}
}

func TestCountStrict_EmptyString(t *testing.T) {
	got := CountStrict("")
	if got != 0 {
		t.Errorf("CountStrict(%q) = %d; want 0", "", got)
	}
}

func TestCountStrict_KnownTexts(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{"short sentence", "The quick brown fox"},
		{"code snippet", "func main() { fmt.Println(\"hello\") }"},
		{"repeated words", "test test test test test"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CountStrict(tc.text)
			if got < 1 {
				t.Errorf("CountStrict(%q) = %d; want >= 1", tc.text, got)
			}
		})
	}
}

func TestCountStrict_GreaterThanOrEqualToCount(t *testing.T) {
	texts := []string{
		"Hello world",
		"Token counting is important for LLM context management",
		"func add(a int, b int) int { return a + b }",
		"The quick brown fox jumps over the lazy dog",
	}

	for _, text := range texts {
		strict := CountStrict(text)
		regular := Count(text)
		if strict < regular {
			t.Errorf("CountStrict(%q) = %d; want >= Count(%d)", text, strict, regular)
		}
	}
}

func TestWithinBudget_EmptyTexts(t *testing.T) {
	if !WithinBudget([]string{}, 100) {
		t.Error("WithinBudget with empty texts should return true")
	}
}

func TestWithinBudget_SingleText(t *testing.T) {
	text := "hello world"
	count := Count(text)

	if !WithinBudget([]string{text}, count) {
		t.Error("WithinBudget should return true when total equals budget")
	}
	if WithinBudget([]string{text}, count-1) {
		t.Error("WithinBudget should return false when total exceeds budget")
	}
}

func TestWithinBudget_MultipleTexts(t *testing.T) {
	texts := []string{"hello", "world", "test"}

	// Sum of all token counts
	total := 0
	for _, t := range texts {
		total += Count(t)
	}

	if !WithinBudget(texts, total) {
		t.Error("WithinBudget should return true when total equals budget")
	}
	if WithinBudget(texts, total-1) {
		t.Error("WithinBudget should return false when total exceeds budget")
	}
}

func TestWithinBudget_BudgetZero(t *testing.T) {
	texts := []string{"hello", "world"}
	if WithinBudget(texts, 0) {
		t.Error("WithinBudget should return false when budget is 0 and there are texts")
	}
}

func TestWithinBudget_EarlyExit(t *testing.T) {
	// This tests that WithinBudget exits early when budget is exceeded
	texts := []string{"a very long piece of text that will definitely exceed the small budget",
		"this text should not even be reached because the first one exceeds budget"}

	// With a small budget, should return false quickly
	if WithinBudget(texts, 5) {
		t.Error("WithinBudget should return false when budget is very small")
	}
}

func TestTokenizerAvailable(t *testing.T) {
	// TokenizerAvailable should report whether the tokenizer is working
	available := TokenizerAvailable()

	// Just verify it returns a boolean without panicking
	// The actual value depends on whether tiktoken-go model data is available
	if available != TokenizerAvailable() {
		t.Error("TokenizerAvailable() returned inconsistent results")
	}
}

func TestCount_BPEMeaningful(t *testing.T) {
	// BPE tokenization should give different counts than naive word counting
	// for texts with subword patterns

	text := "tokenization" // A long word that BPE will split into subwords

	if TokenizerAvailable() {
		// When tokenizer is available, we expect BPE counting
		bpeCount := Count(text)

		// BPE should give us more than 1 token for "tokenization"
		// (typically 3-5 tokens for this word)
		if bpeCount < 2 {
			t.Errorf("Count(%q) = %d with BPE; expected > 1 for BPE tokenization", text, bpeCount)
		}
	}
}

func TestCount_DifferentTextsProduceDifferentCounts(t *testing.T) {
	texts := []string{
		"hi",
		"hello world",
		"the quick brown fox jumps over the lazy dog",
		"token counting is important for LLM context management",
		"in natural language processing applications with transformer models",
	}

	counts := make(map[int]bool)
	for _, text := range texts {
		count := Count(text)
		counts[count] = true
	}

	// With BPE, texts of very different lengths should generally produce different token counts
	if TokenizerAvailable() {
		if len(counts) < 3 {
			t.Errorf("Expected variety in token counts for different length texts, got only %d unique counts", len(counts))
		}
	}
}

func TestCount_LongText(t *testing.T) {
	// Test with a longer text to ensure the tokenizer handles it well
	longText := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 100)

	count := Count(longText)

	if count < 100 {
		t.Errorf("Count for long repeated text = %d; expected >= 100", count)
	}

	// Verify consistency
	for i := 0; i < 5; i++ {
		got := Count(longText)
		if got != count {
			t.Errorf("Count inconsistent for long text: got %d, want %d", got, count)
		}
	}
}

func TestCountStrict_DoesNotPanic(t *testing.T) {
	// CountStrict should handle any valid input without panicking
	texts := []string{
		"",
		"hello",
		"a b c d e f g h i j k l m n o p q r s t u v w x y z",
		"func() { /* code */ }",
		"🎉🚀💯",
	}

	for _, text := range texts {
		got := CountStrict(text)
		if got < 0 {
			t.Errorf("CountStrict(%q) returned negative value: %d", text, got)
		}
	}
}

func TestCount_UnicodeHandled(t *testing.T) {
	texts := []string{
		"你好世界",          // Chinese
		"こんにちは世界",       // Japanese
		"مرحبا بالعالم", // Arabic
		"🎉🎊🎁🎄🎅",         // Emoji
		"mixeḏ",         // Mixed
	}

	for _, text := range texts {
		got := Count(text)
		if got < 1 {
			t.Errorf("Count(%q) = %d; want >= 1", text, got)
		}
	}
}

func TestCount_CodeTokens(t *testing.T) {
	// Code typically has more tokens per character due to special characters
	code := `func main() {
    fmt.Println("Hello, World!")
    return 0
}`

	count := Count(code)

	// Code with keywords, punctuation, and strings should have reasonable token count
	if count < 5 {
		t.Errorf("Count for code snippet = %d; expected reasonable tokenization", count)
	}
}

func BenchmarkCount(b *testing.B) {
	text := "The quick brown fox jumps over the lazy dog. Tokenization is important for LLM context."

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Count(text)
	}
}

func BenchmarkCountStrict(b *testing.B) {
	text := "The quick brown fox jumps over the lazy dog. Tokenization is important for LLM context."

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CountStrict(text)
	}
}

func BenchmarkWithinBudget(b *testing.B) {
	texts := []string{
		"The quick brown fox",
		"Tokenization matters",
		"For context management",
		"In LLM applications",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		WithinBudget(texts, 100)
	}
}
