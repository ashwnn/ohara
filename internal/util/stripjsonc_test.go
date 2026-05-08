package util

import (
	"testing"
)

func TestStripJSONC(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no comments",
			input:    `{"key": "value"}`,
			expected: `{"key": "value"}`,
		},
		{
			name:     "single-line comment",
			input:    `{"key": "value"} // this is a comment`,
			expected: `{"key": "value"} `,
		},
		{
			name:     "single-line comment with newline",
			input:    "{\"key\": \"value\"} // this is a comment\n\"key2\": \"value2\"",
			expected: "{\"key\": \"value\"} \n\"key2\": \"value2\"",
		},
		{
			name:     "multi-line comment",
			input:    `{"key": "value"} /* this is a comment */`,
			expected: `{"key": "value"} `,
		},
		{
			name:     "multi-line comment spanning lines",
			input:    "{\"key\": \"value\"} /* this is\na\ncomment */\n\"key2\": \"value2\"",
			expected: "{\"key\": \"value\"} \n\"key2\": \"value2\"",
		},
		{
			name:     "comment inside string",
			input:    `{"key": "value // not a comment /* also not */"}`,
			expected: `{"key": "value // not a comment /* also not */"}`,
		},
		{
			name:     "escaped quote inside string",
			input:    `{"key": "value \" // not a comment"}`,
			expected: `{"key": "value \" // not a comment"}`,
		},
		{
			name:     "escaped backslash before quote",
			input:    `{"key": "value \\"} // comment`,
			expected: `{"key": "value \\"} `,
		},
		{
			name:     "empty input",
			input:    ``,
			expected: ``,
		},
		{
			name:     "unclosed multi-line comment",
			input:    `{"key": "value"} /* unclosed`,
			expected: `{"key": "value"} `,
		},
		{
			name:     "only comments",
			input:    `// comment 1` + "\n" + `/* comment 2 */`,
			expected: "\n",
		},
		{
			name:     "json structure with various comments",
			input: `{
	// A comment
	"key1": "value1", /* inline comment */
	"key2": "value2" /* multi-line
	comment */
}`,
			expected: `{

	"key1": "value1",
	"key2": "value2"
}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := StripJSONC([]byte(tt.input))
			// Empty byte slice is nil or empty, let's compare as string for better error messages
			if string(result) != tt.expected {
				t.Errorf("StripJSONC() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestStripJSONC_UnclosedQuote(t *testing.T) {
	input := `{"key": "value`
	expected := `{"key": "value`
	result := StripJSONC([]byte(input))
	if string(result) != expected {
		t.Errorf("StripJSONC() = %q, want %q", result, expected)
	}
}

func TestStripJSONC_SingleSlash(t *testing.T) {
	input := `{"key": "value"} / `
	expected := `{"key": "value"} / `
	result := StripJSONC([]byte(input))
	if string(result) != expected {
		t.Errorf("StripJSONC() = %q, want %q", result, expected)
	}
}

func TestStripJSONC_EdgeCaseMultilineClose(t *testing.T) {
	input := `/* comment *`
	expected := ``
	result := StripJSONC([]byte(input))
	if string(result) != expected {
		t.Errorf("StripJSONC() = %q, want %q", result, expected)
	}
}
