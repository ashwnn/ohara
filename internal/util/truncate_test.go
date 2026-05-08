package util

import (
	"testing"
)

func TestTruncate(t *testing.T) {
	tests := []struct {
		name string
		s    string
		max  int
		want string
	}{
		{
			name: "empty string",
			s:    "",
			max:  5,
			want: "",
		},
		{
			name: "short string",
			s:    "hello",
			max:  10,
			want: "hello",
		},
		{
			name: "exact length",
			s:    "hello",
			max:  5,
			want: "hello",
		},
		{
			name: "long string",
			s:    "hello world",
			max:  5,
			want: "hello...",
		},
		{
			name: "max 0",
			s:    "hello",
			max:  0,
			want: "...",
		},
		{
			name: "multibyte characters",
			s:    "こんにちは世界",
			max:  5,
			want: "こんにちは...",
		},
		{
			name: "emoji",
			s:    "👋🌍 hello",
			max:  2,
			want: "👋🌍...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Truncate(tt.s, tt.max); got != tt.want {
				t.Errorf("Truncate() = %v, want %v", got, tt.want)
			}
		})
	}
}
