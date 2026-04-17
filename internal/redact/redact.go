// Package redact provides regex-based secret redaction for memory content.
// It strips API keys, tokens, and other credentials before persistence so
// sensitive data never enters the database.
package redact

import (
	"regexp"
	"strings"
)

// replacement is the literal replacement string used for all matched secrets.
const replacement = "[SECRET]"

// secretPatterns is the ordered list of compiled regexes applied by Redact.
// Ordering matters: specific patterns (GitHub, OpenAI, Bearer) are listed before
// generic catch-alls so they take precedence.
var secretPatterns = []*regexp.Regexp{
	// GitHub personal access tokens: ghp_ followed by 36+ base36 chars.
	regexp.MustCompile(`(?i)ghp_[a-zA-Z0-9]{36,}`),

	// GitHub OAuth tokens: gho_, ghu_, ghs_, ghr_ followed by 36+ chars.
	regexp.MustCompile(`(?i)gh[ohus]_[a-zA-Z0-9]{36,}`),

	// OpenAI API keys: sk- followed by 40+ alphanumeric chars.
	regexp.MustCompile(`(?i)sk-[a-zA-Z0-9]{40,}`),

	// OpenAI project keys: sk-proj- followed by 20+ chars.
	regexp.MustCompile(`(?i)sk-proj-[a-zA-Z0-9_-]{20,}`),

	// Bearer tokens in Authorization headers: Bearer <token> where token is 40+ chars.
	regexp.MustCompile(`(?i)Bearer\s+[a-zA-Z0-9_.-]{40,}`),

	// Generic key= patterns: token=, api_key=, apikey=, secret=, password= with 32+ char values.
	regexp.MustCompile(`(?i)(?:token|api_?key|apikey|secret|password)\s*[:=]\s*['"]?[a-zA-Z0-9_.-]{32,}['"]?`),

	// AWS Access Key ID: AKIA prefix + 16 alphanumeric chars (20 total).
	regexp.MustCompile(`(?i)AKIA[0-9A-Z]{16}`),

	// AWS Secret Access Key: the literal key name followed by a 40-char value.
	regexp.MustCompile(`(?i)aws_secret_access_key\s*[:=]\s*['"]?[a-zA-Z0-9/+=]{40}['"]?`),

	// Slack tokens: xox[baprs]- prefix + 10+ chars.
	regexp.MustCompile(`(?i)xox[baprs]-[0-9a-zA-Z-]{10,}`),

	// Stripe keys: sk_live_ or sk_test_ + 24+ chars.
	regexp.MustCompile(`(?i)sk_(?:live|test)_[a-zA-Z0-9]{24,}`),

	// Google API key: key= + 39-char alphanumeric value.
	regexp.MustCompile(`(?i)key\s*[:=]\s*['"]?[a-zA-Z0-9_-]{39}['"]?`),
}

// Redact scans text for known secret patterns and replaces each match with
// the literal string "[SECRET]". The returned string preserves original
// whitespace and structure; only the matched token is replaced.
//
// This is a best-effort redaction layer applied before persistence.
// It complements the explicit <private>...</private> tag stripping that
// operates at a higher semantic level.
func Redact(text string) string {
	if text == "" {
		return text
	}
	result := text
	for _, pattern := range secretPatterns {
		result = pattern.ReplaceAllString(result, replacement)
	}
	// Collapse multiple consecutive [SECRET] tags to avoid noise.
	result = collapseRepeated(result, replacement)
	return result
}

// collapseRepeated replaces two or more consecutive occurrences of substr
// with a single occurrence.
func collapseRepeated(s, substr string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		idx := strings.Index(s[i:], substr)
		if idx < 0 {
			b.WriteString(s[i:])
			break
		}
		b.WriteString(s[i : i+idx])
		b.WriteString(substr)
		j := i + idx + len(substr)
		for j < len(s) && strings.HasPrefix(s[j:], substr) {
			j += len(substr)
		}
		i = j
	}
	return b.String()
}
