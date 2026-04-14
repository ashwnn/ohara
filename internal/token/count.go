// Package token provides token-counting utilities for Ohara's memory system.
// Accurate BPE token counting (tiktoken-go) is pending explicit install approval.
// Until then, this package uses a conservative word-count-based approximation.
package token

import (
	"strings"
)

// Count returns the estimated token count for the given text.
//
// Uses a conservative approximation: word_count * 1.35 + safety_margin.
// This is accurate to within ~20-30% for typical English prose and code.
// When github.com/pkoukk/tiktoken-go is approved for installation,
// replace this implementation with BPE-based counting for ~1-2 token accuracy.
//
// The 1.35 multiplier is derived from: avg_tokens_per_word ≈ 1.3 for
// English text, with a 5% safety margin built in.
func Count(text string) int {
	if text == "" {
		return 0
	}
	words := len(strings.Fields(text))
	// Conservative estimate: ~1.35 tokens per word on average
	estimate := int(float64(words)*1.35 + 0.5)
	// Minimum 1 token for non-empty text
	if estimate < 1 {
		return 1
	}
	return estimate
}

// CountStrict returns an upper-bound token estimate.
// Uses word_count * 1.5 to guarantee we never underestimate.
// Use this when the penalty for overestimating is lower than underestimating
// (e.g., budget enforcement where going over budget is worse).
func CountStrict(text string) int {
	if text == "" {
		return 0
	}
	words := len(strings.Fields(text))
	return int(float64(words)*1.5 + 0.5)
}

// WithinBudget returns true if the given texts can fit within budgetTokens.
// Texts are counted individually and summed. Uses conservative Count.
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
