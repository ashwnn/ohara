// Package token provides token-counting utilities for Ohara's memory system.
// Uses tiktoken BPE counting for accurate token counts, with a conservative
// word-count fallback if the tokenizer cannot be initialized.
package token

import (
	"strings"
	"sync"

	tiktoken "github.com/pkoukk/tiktoken-go"
)

// encoder wraps a tiktoken tokenizer for thread-safe access.
// The encoder is initialized once and reused across all Count calls.
var (
	encoder     *tiktoken.Tiktoken
	encoderOnce sync.Once
	encoderErr  error
)

// initEncoder attempts to load the cl100k_base BPE tokenizer.
// This is called once at package initialization time.
func initEncoder() {
	encoderOnce.Do(func() {
		// cl100k_base is the tokenizer used by GPT-4 and ChatGPT models.
		// It provides accurate BPE token counts for English text and code.
		encoder, encoderErr = tiktoken.GetEncoding("cl100k_base")
	})
}

// isFallback reports whether the tokenizer failed to initialize.
// When true, Count uses a conservative word-count approximation.
func isFallback() bool {
	return encoder == nil
}

// Count returns the token count for the given text using BPE tokenization.
//
// Uses tiktoken's cl100k_base encoder for accurate BPE token counts (~1-2 token
// accuracy). If the tokenizer cannot be initialized (e.g., model data not
// available), falls back to a conservative word-count approximation:
// word_count * 1.35 + safety_margin, accurate to within ~20-30%.
func Count(text string) int {
	if text == "" {
		return 0
	}

	// Ensure tokenizer is initialized (lazy initialization on first call).
	initEncoder()

	// Fast path: use real BPE counting when tokenizer is available.
	if encoder != nil {
		tokens := encoder.Encode(text, nil, nil)
		return len(tokens)
	}

	// Fallback: conservative word-based approximation.
	// Only used when tiktoken init failed.
	words := len(strings.Fields(text))
	estimate := int(float64(words)*1.35 + 0.5)
	if estimate < 1 {
		return 1
	}
	return estimate
}

// CountStrict returns an upper-bound token estimate.
// Uses real BPE count when available; falls back to word_count * 1.5 otherwise.
// Use this when the penalty for overestimating is lower than underestimating
// (e.g., budget enforcement where going over budget is worse).
func CountStrict(text string) int {
	if text == "" {
		return 0
	}

	// Ensure tokenizer is initialized (lazy initialization on first call).
	initEncoder()

	if encoder != nil {
		tokens := encoder.Encode(text, nil, nil)
		return len(tokens)
	}

	// Strict fallback: overestimate rather than underestimate.
	words := len(strings.Fields(text))
	return int(float64(words)*1.5 + 0.5)
}

// WithinBudget returns true if the given texts can fit within budgetTokens.
// Texts are counted individually and summed. Uses accurate BPE counting when
// available, falling back to conservative estimation only if tokenizer init failed.
func WithinBudget(texts []string, budgetTokens int) bool {
	total := 0
	for _, t := range texts {
		total += Count(t)
		if total > budgetTokens {
			return false
		}
	}
	return true
}

// TokenizerAvailable reports whether the BPE tokenizer was successfully initialized.
// Useful for diagnostics and testing to confirm fallback behavior.
// Triggers lazy initialization if not yet attempted.
func TokenizerAvailable() bool {
	initEncoder()
	return encoder != nil
}
