// Package version checks for newer releases on GitHub.
// Disabled for this personal fork.
package version

// CheckStatus represents the result of a version check.
type CheckStatus string

const (
	StatusUpToDate        CheckStatus = "up_to_date"
	StatusUpdateAvailable CheckStatus = "update_available"
	StatusCheckFailed     CheckStatus = "check_failed"
)

// CheckResult holds the status of a version check.
type CheckResult struct {
	Status  CheckStatus
	Message string
}

// CheckLatest is disabled for the personal fork.
// This fork does not track against public releases.
// To update: go install github.com/ashwnn/ohara/cmd/ohara@latest
func CheckLatest(current string) CheckResult {
	// Always return up-to-date; no update checking for personal fork
	return CheckResult{Status: StatusUpToDate}
}
