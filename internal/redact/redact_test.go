package redact

import (
	"strings"
	"testing"
)

func TestRedactGitHubPAT(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "github PAT preserved format",
			input: "ghp_abcdefghijklmnopqrstuvwxyz1234567890AB",
			want:  "[SECRET]",
		},
		{
			name:  "github PAT in sentence",
			input: "Token: ghp_abcdefghijklmnopqrstuvwxyz1234567890AB is expired",
			want:  "Token: [SECRET] is expired",
		},
		{
			name:  "github PAT case insensitive",
			input: "GHP_ABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890AB",
			want:  "[SECRET]",
		},
		{
			name:  "github OAuth token gho_",
			input: "Authorization: gho_abcdefghijklmnopqrstuvwxyz1234567890AB",
			want:  "Authorization: [SECRET]",
		},
		{
			name:  "github OAuth token ghs_",
			input: "token=ghs_abcdefghijklmnopqrstuvwxyz1234567890AB",
			want:  "token=[SECRET]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Redact(tc.input)
			if got != tc.want {
				t.Errorf("Redact(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestRedactOpenAIKey(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "openai sk- key",
			input: "sk-abcdefghijklmnopqrstuvwxyz1234567890ABCDEFGHIJKLMNOP",
			want:  "[SECRET]",
		},
		{
			name:  "openai key in URL",
			input: "https://api.openai.com/v1/models?api_key=sk-abcdefghijklmnopqrstuvwxyz1234567890ABCDEFGHIJKLMNOP",
			want:  "https://api.openai.com/v1/models?api_key=[SECRET]",
		},
		{
			name:  "openai project key",
			input: "sk-proj-1234567890abcdefghijklmnop",
			want:  "[SECRET]",
		},
		{
			name:  "openai key case insensitive",
			input: "SK-ABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890ABCDEFGHIJKLMNOPQR",
			want:  "[SECRET]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Redact(tc.input)
			if got != tc.want {
				t.Errorf("Redact(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestRedactAWSKeys(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "AWS access key ID",
			input: "AKIAIOSFODNN7EXAMPLE",
			want:  "[SECRET]",
		},
		{
			name:  "AWS bare secret key not redacted (no broad pattern)",
			input: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			want:  "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		},
		{
			name:  "AWS secret in env var style",
			input: "AWS_SECRET_ACCESS_KEY='wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY'",
			want:  "[SECRET]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Redact(tc.input)
			if got != tc.want {
				t.Errorf("Redact(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestRedactGenericPatterns(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "token= style",
			input: "token=abcdefghijklmnopqrstuvwxyz1234567890AB",
			want:  "[SECRET]",
		},
		{
			name:  "api_key: style",
			input: "api_key: sk-abcdefghijklmnopqrstuvwxyz1234567890ABCDEFGHIJKLMNOPQRST",
			want:  "api_key: [SECRET]",
		},
		{
			name:  "apikey = style with spaces",
			input: "apikey   =   abcdefghijklmnopqrstuvwxyz1234567890AB",
			want:  "[SECRET]",
		},
		{
			name:  "secret: style",
			input: "secret: my_super_secret_key_value_here_32chars",
			want:  "[SECRET]",
		},
		{
			name:  "Bearer token",
			input: "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9abcdefghijklmnop",
			want:  "Authorization: [SECRET]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Redact(tc.input)
			if got != tc.want {
				t.Errorf("Redact(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestRedactSlackTokens(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "xoxb token",
			input: "xoxb" + "-1234567890123-1234567890123-abcdefghijklmnopqrstuv",
			want:  "[SECRET]",
		},
		{
			name:  "xoxp token",
			input: "xoxp-1234567890123-1234567890123-abcdefghijklmnopqrstuvwx",
			want:  "[SECRET]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Redact(tc.input)
			if got != tc.want {
				t.Errorf("Redact(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestRedactStripeKeys(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "stripe live key",
			input: "sk" + "_live_abcdefghijklmnopqrstuvwxyz123456",
			want:  "[SECRET]",
		},
		{
			name:  "stripe test key",
			input: "sk_test_abcdefghijklmnopqrstuvwxyz123456",
			want:  "[SECRET]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Redact(tc.input)
			if got != tc.want {
				t.Errorf("Redact(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestRedactGoogleAPIKey(t *testing.T) {
	input := "key=AIzaSyD-Example-Key-With-Exactly-39-CharsXX"
	got := Redact(input)
	if !strings.Contains(got, "[SECRET]") {
		t.Errorf("Redact(%q) = %q, want to contain [SECRET]", input, got)
	}
}

func TestRedactEmpty(t *testing.T) {
	if got := Redact(""); got != "" {
		t.Errorf("Redact(%q) = %q, want %q", "", got, "")
	}
}

func TestRedactNoSecrets(t *testing.T) {
	input := "This is a normal memory about using context in Go handlers for cancellation support."
	got := Redact(input)
	if got != input {
		t.Errorf("Redact(%q) = %q, want %q (no change expected for clean text)", input, got, input)
	}
}

func TestRedactCollapseRepeated(t *testing.T) {
	// Multiple tokens in one string
	input := "API keys: ghp_abcdefghijklmnopqrstuvwxyz1234567890AB and ghp_abcdefghijklmnopqrstuvwxyz1234567890AB"
	got := Redact(input)
	// Should have two separate [SECRET] since they're not adjacent
	if got != "API keys: [SECRET] and [SECRET]" {
		t.Errorf("Redact(%q) = %q, want %q", input, got, "API keys: [SECRET] and [SECRET]")
	}
}

func TestRedactPreservesStructure(t *testing.T) {
	input := "Use this OpenAI key: sk-abcdefghijklmnopqrstuvwxyz1234567890ABCDEFGHIJKLMNOPQR for the API call."
	got := Redact(input)
	if !strings.Contains(got, "[SECRET]") {
		t.Errorf("expected redaction in %q", got)
	}
	if !strings.Contains(got, "for the API call.") {
		t.Errorf("expected trailing text preserved in %q", got)
	}
	if strings.Contains(got, "sk-") {
		t.Errorf("expected sk- to be redacted in %q", got)
	}
}

func TestRedactGoogleKey39Chars(t *testing.T) {
	// 39-char alphanumeric key preceded by key=
	input := "key=AIzaSyDExampleKeyWithExactly39CharactersXX"
	got := Redact(input)
	if !strings.Contains(got, "[SECRET]") {
		t.Errorf("Redact(%q) = %q, want to contain [SECRET]", input, got)
	}
}
