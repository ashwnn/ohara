package version

import "testing"

func TestCheckLatest_ReturnsUpToDate(t *testing.T) {
	// The package is stubbed for this personal fork.
	// CheckLatest always returns StatusUpToDate regardless of input.
	tests := []struct {
		name    string
		current string
	}{
		{"normal version", "1.10.7"},
		{"v-prefixed version", "v1.10.7"},
		{"dev build", "dev"},
		{"empty version", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CheckLatest(tt.current)
			if result.Status != StatusUpToDate {
				t.Errorf("CheckLatest(%q) = %q; want %q", tt.current, result.Status, StatusUpToDate)
			}
		})
	}
}
