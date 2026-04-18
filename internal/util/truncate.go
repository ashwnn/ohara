package util

// Truncate truncates string s to at most max runes, appending "..." if truncated.
func Truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}
