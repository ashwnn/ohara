package setup

// Sync embedded plugin copies from the source of truth (plugin/ directory).
// Only OpenCode plugin is embedded.
// Run: go generate ./internal/setup/
//go:generate sh -c "rm -rf plugins/opencode && mkdir -p plugins/opencode && cp ../../plugin/opencode/ohara.ts plugins/opencode/"
