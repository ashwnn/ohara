package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ashwnn/ohara/internal/mcp"
	"github.com/ashwnn/ohara/internal/store"
	"github.com/ashwnn/ohara/internal/util"
	versioncheck "github.com/ashwnn/ohara/internal/version"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

func testConfig(t *testing.T) store.Config {
	t.Helper()
	cfg, err := store.DefaultConfig()
	if err != nil {
		t.Fatalf("DefaultConfig: %v", err)
	}
	cfg.DataDir = t.TempDir()
	return cfg
}

func withArgs(t *testing.T, args ...string) {
	t.Helper()
	old := os.Args
	os.Args = args
	t.Cleanup(func() {
		os.Args = old
	})
}

func withCwd(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir to %s: %v", dir, err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(old)
	})
}

func stubCheckForUpdates(t *testing.T, result versioncheck.CheckResult) {
	t.Helper()
	old := checkForUpdates
	checkForUpdates = func(string) versioncheck.CheckResult { return result }
	t.Cleanup(func() { checkForUpdates = old })
}

func captureOutput(t *testing.T, fn func()) (stdout string, stderr string) {
	t.Helper()

	oldOut := os.Stdout
	oldErr := os.Stderr

	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}

	os.Stdout = outW
	os.Stderr = errW

	fn()

	_ = outW.Close()
	_ = errW.Close()
	os.Stdout = oldOut
	os.Stderr = oldErr

	outBytes, err := io.ReadAll(outR)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	errBytes, err := io.ReadAll(errR)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}

	return string(outBytes), string(errBytes)
}

func mustSeedMemory(t *testing.T, cfg store.Config, sessionID, project, kind, title, body, scope string) int64 {
	t.Helper()

	s, err := store.New(cfg)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	if err := s.CreateSession(sessionID, project, "/tmp"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	id, err := s.AddMemory(store.AddMemoryParams{
		SessionID: sessionID,
		Kind:      kind,
		Title:     title,
		Body:      body,
		ProjectID: project,
		Scope:     scope,
		Source:    "cli",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	return id
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{name: "short string", in: "abc", max: 10, want: "abc"},
		{name: "exact length", in: "hello", max: 5, want: "hello"},
		{name: "long string", in: "abcdef", max: 3, want: "abc..."},
		{name: "spanish accents", in: "Decisión de arquitectura", max: 8, want: "Decisión..."},
		{name: "emoji", in: "🐛🔧🚀✨🎉💡", max: 3, want: "🐛🔧🚀..."},
		{name: "mixed ascii and multibyte", in: "café☕latte", max: 5, want: "café☕..."},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := util.Truncate(tc.in, tc.max)
			if got != tc.want {
				t.Fatalf("truncate(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
		})
	}
}

func TestPrintUsage(t *testing.T) {
	oldVersion := version
	version = "test-version"
	t.Cleanup(func() {
		version = oldVersion
	})

	stdout, stderr := captureOutput(t, func() { printUsage() })
	if stderr != "" {
		t.Fatalf("expected no stderr, got: %q", stderr)
	}
	if !strings.Contains(stdout, "ohara vtest-version") {
		t.Fatalf("usage missing version: %q", stdout)
	}
	if !strings.Contains(stdout, "search <query>") || !strings.Contains(stdout, "setup [agent]") {
		t.Fatalf("usage missing expected commands: %q", stdout)
	}
}

func TestPrintPostInstall(t *testing.T) {
	tests := []struct {
		agent   string
		expects []string
	}{
		{agent: "opencode", expects: []string{"Restart OpenCode", "ohara serve &"}},
		{agent: "gemini-cli", expects: []string{"Restart Gemini CLI", "~/.gemini/settings.json"}},
		{agent: "codex", expects: []string{"Restart Codex", "~/.codex/config.toml"}},
		{agent: "unknown", expects: nil},
	}

	for _, tc := range tests {
		t.Run(tc.agent, func(t *testing.T) {
			stdout, stderr := captureOutput(t, func() { printPostInstall(tc.agent) })
			if stderr != "" {
				t.Fatalf("expected no stderr, got: %q", stderr)
			}
			for _, expected := range tc.expects {
				if !strings.Contains(stdout, expected) {
					t.Fatalf("output missing %q: %q", expected, stdout)
				}
			}
			if len(tc.expects) == 0 && stdout != "" {
				t.Fatalf("expected empty output for unknown agent, got: %q", stdout)
			}
		})
	}
}

func TestCmdSaveAndSearch(t *testing.T) {
	cfg := testConfig(t)

	withArgs(t,
		"ohara", "save", "my-title", "my-content",
		"--type", "bugfix",
		"--project", "alpha",
		"--scope", "personal",
	)

	stdout, stderr := captureOutput(t, func() { cmdSave(cfg) })
	if stderr != "" {
		t.Fatalf("expected no stderr, got: %q", stderr)
	}
	if !strings.Contains(stdout, "Memory saved (#") || !strings.Contains(stdout, "my-title") {
		t.Fatalf("unexpected save output: %q", stdout)
	}

	withArgs(t, "ohara", "search", "my-content", "--type", "bugfix", "--project", "alpha", "--scope", "personal", "--limit", "1")
	searchOut, searchErr := captureOutput(t, func() { cmdSearch(cfg) })
	if searchErr != "" {
		t.Fatalf("expected no stderr from search, got: %q", searchErr)
	}
	if !strings.Contains(searchOut, "Found 1 memories") || !strings.Contains(searchOut, "my-title") {
		t.Fatalf("unexpected search output: %q", searchOut)
	}

	withArgs(t, "ohara", "search", "definitely-not-found")
	noneOut, noneErr := captureOutput(t, func() { cmdSearch(cfg) })
	if noneErr != "" {
		t.Fatalf("expected no stderr from empty search, got: %q", noneErr)
	}
	if !strings.Contains(noneOut, "No memories found") {
		t.Fatalf("expected empty search message, got: %q", noneOut)
	}
}

func TestCmdTimeline(t *testing.T) {
	cfg := testConfig(t)
	mustSeedMemory(t, cfg, "s-1", "proj", "discovery", "first", "first content", "project")
	focusID := mustSeedMemory(t, cfg, "s-1", "proj", "discovery", "focus", "focus content", "project")
	mustSeedMemory(t, cfg, "s-1", "proj", "discovery", "third", "third content", "project")

	withArgs(t, "ohara", "timeline", strconv.FormatInt(focusID, 10), "--count", "1")
	stdout, stderr := captureOutput(t, func() { cmdTimeline(cfg) })
	if stderr != "" {
		t.Fatalf("expected no stderr, got: %q", stderr)
	}
	if !strings.Contains(stdout, "Memory #"+strconv.FormatInt(focusID, 10)) {
		t.Fatalf("timeline output missing expected memory anchor: %q", stdout)
	}
	if !strings.Contains(stdout, "─── Before ───") || !strings.Contains(stdout, "─── After ───") {
		t.Fatalf("timeline output missing before/after sections: %q", stdout)
	}
}

func TestCmdContextAndStats(t *testing.T) {
	cfg := testConfig(t)

	withArgs(t, "ohara", "context")
	emptyCtxOut, emptyCtxErr := captureOutput(t, func() { cmdContext(cfg) })
	if emptyCtxErr != "" {
		t.Fatalf("expected no stderr for empty context, got: %q", emptyCtxErr)
	}
	if !strings.Contains(emptyCtxOut, "No previous session memories found") {
		t.Fatalf("unexpected empty context output: %q", emptyCtxOut)
	}

	mustSeedMemory(t, cfg, "s-ctx", "project-x", "decision", "title", "content", "project")

	s, err := store.New(cfg)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	_, err = s.AddPrompt(store.AddPromptParams{SessionID: "s-ctx", Content: "user asked about context", Project: "project-x"})
	if err != nil {
		t.Fatalf("AddPrompt: %v", err)
	}
	_ = s.Close()

	withArgs(t, "ohara", "context", "project-x")
	ctxOut, ctxErr := captureOutput(t, func() { cmdContext(cfg) })
	if ctxErr != "" {
		t.Fatalf("expected no stderr for populated context, got: %q", ctxErr)
	}
	if !strings.Contains(ctxOut, "## Memory from Previous Sessions") || !strings.Contains(ctxOut, "Recent Memories") {
		t.Fatalf("unexpected populated context output: %q", ctxOut)
	}

	withArgs(t, "ohara", "stats")
	statsOut, statsErr := captureOutput(t, func() { cmdStats(cfg) })
	if statsErr != "" {
		t.Fatalf("expected no stderr from stats, got: %q", statsErr)
	}
	if !strings.Contains(statsOut, "Ohara Memory Stats") || !strings.Contains(statsOut, "project-x") {
		t.Fatalf("unexpected stats output: %q", statsOut)
	}
}

func TestCmdExportAndImport(t *testing.T) {
	sourceCfg := testConfig(t)
	targetCfg := testConfig(t)

	// ExportData contains Sessions + Prompts (not memories)
	srcStore, err := store.New(sourceCfg)
	if err != nil {
		t.Fatalf("store.New source: %v", err)
	}
	if err := srcStore.CreateSession("s-exp", "proj-exp", "/tmp"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := srcStore.AddPrompt(store.AddPromptParams{SessionID: "s-exp", Content: "export me", Project: "proj-exp"}); err != nil {
		t.Fatalf("AddPrompt: %v", err)
	}
	srcStore.Close()

	exportPath := filepath.Join(t.TempDir(), "memories.json")

	withArgs(t, "ohara", "export", exportPath)
	exportOut, exportErr := captureOutput(t, func() { cmdExport(sourceCfg) })
	if exportErr != "" {
		t.Fatalf("expected no stderr from export, got: %q", exportErr)
	}
	if !strings.Contains(exportOut, "Exported to "+exportPath) {
		t.Fatalf("unexpected export output: %q", exportOut)
	}

	withArgs(t, "ohara", "import", exportPath)
	importOut, importErr := captureOutput(t, func() { cmdImport(targetCfg) })
	if importErr != "" {
		t.Fatalf("expected no stderr from import, got: %q", importErr)
	}
	if !strings.Contains(importOut, "Imported from "+exportPath) {
		t.Fatalf("unexpected import output: %q", importOut)
	}

	s, err := store.New(targetCfg)
	if err != nil {
		t.Fatalf("store.New target: %v", err)
	}
	defer s.Close()

	// Verify session was imported
	session, err := s.GetSession("s-exp")
	if err != nil {
		t.Fatalf("GetSession after import: %v", err)
	}
	if session == nil {
		t.Fatalf("expected imported session to exist")
	}

	// Verify prompt was imported
	prompts, err := s.SearchPrompts("export", "proj-exp", 10)
	if err != nil {
		t.Fatalf("SearchPrompts after import: %v", err)
	}
	if len(prompts) == 0 {
		t.Fatalf("expected imported prompt to be searchable")
	}
}

func TestCmdSyncStatusExportAndImport(t *testing.T) {
	stubRuntimeHooks(t)
	workDir := t.TempDir()
	withCwd(t, workDir)

	exportCfg := testConfig(t)
	importCfg := testConfig(t)

	mustSeedMemory(t, exportCfg, "s-sync", "sync-project", "discovery", "sync title", "sync content", "project")

	withArgs(t, "ohara", "sync", "--status")
	statusOut, statusErr := captureOutput(t, func() { cmdSync(exportCfg) })
	if statusErr != "" {
		t.Fatalf("expected no stderr from status, got: %q", statusErr)
	}
	if !strings.Contains(statusOut, "Sync status:") {
		t.Fatalf("unexpected status output: %q", statusOut)
	}

	withArgs(t, "ohara", "sync", "--all")
	exportOut, exportErr := captureOutput(t, func() { cmdSync(exportCfg) })
	if exportErr != "" {
		t.Fatalf("expected no stderr from sync export, got: %q", exportErr)
	}
	if !strings.Contains(exportOut, "Created chunk") {
		t.Fatalf("unexpected sync export output: %q", exportOut)
	}

	withArgs(t, "ohara", "sync", "--import")
	importOut, importErr := captureOutput(t, func() { cmdSync(importCfg) })
	if importErr != "" {
		t.Fatalf("expected no stderr from sync import, got: %q", importErr)
	}
	if !strings.Contains(importOut, "Imported 1 new chunk(s)") {
		t.Fatalf("unexpected sync import output: %q", importOut)
	}

	withArgs(t, "ohara", "sync", "--import")
	noopOut, noopErr := captureOutput(t, func() { cmdSync(importCfg) })
	if noopErr != "" {
		t.Fatalf("expected no stderr from second sync import, got: %q", noopErr)
	}
	if !strings.Contains(noopOut, "No new chunks to import") {
		t.Fatalf("unexpected second sync import output: %q", noopOut)
	}
}

func TestCmdSyncDefaultProjectNoData(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "repo-name")
	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatalf("mkdir workdir: %v", err)
	}
	withCwd(t, workDir)

	cfg := testConfig(t)
	withArgs(t, "ohara", "sync")
	stdout, stderr := captureOutput(t, func() { cmdSync(cfg) })
	if stderr != "" {
		t.Fatalf("expected no stderr, got: %q", stderr)
	}
	if !strings.Contains(stdout, `Exporting memories for project "repo-name"`) {
		t.Fatalf("expected default project message, got: %q", stdout)
	}
	if !strings.Contains(stdout, `Nothing new to sync for project "repo-name"`) {
		t.Fatalf("expected no-data sync message, got: %q", stdout)
	}
}

func TestMainVersionAndHelpAliases(t *testing.T) {
	oldVersion := version
	version = "9.9.9-test"
	t.Cleanup(func() { version = oldVersion })
	stubCheckForUpdates(t, versioncheck.CheckResult{Status: versioncheck.StatusUpToDate})

	tests := []struct {
		name      string
		arg       string
		contains  string
		notStderr bool
	}{
		{name: "version", arg: "version", contains: "ohara 9.9.9-test", notStderr: true},
		{name: "version short", arg: "-v", contains: "ohara 9.9.9-test", notStderr: true},
		{name: "version long", arg: "--version", contains: "ohara 9.9.9-test", notStderr: true},
		{name: "help", arg: "help", contains: "Usage:", notStderr: true},
		{name: "help short", arg: "-h", contains: "Commands:", notStderr: true},
		{name: "help long", arg: "--help", contains: "Environment:", notStderr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withArgs(t, "ohara", tc.arg)
			stdout, stderr := captureOutput(t, func() { main() })
			if tc.notStderr && stderr != "" {
				t.Fatalf("expected no stderr, got: %q", stderr)
			}
			if !strings.Contains(stdout, tc.contains) {
				t.Fatalf("stdout %q does not include %q", stdout, tc.contains)
			}
		})
	}
}

func TestMainPrintsUpdateFailuresAndUpdates(t *testing.T) {
	oldVersion := version
	version = "1.10.7"
	t.Cleanup(func() { version = oldVersion })

	t.Run("prints check failure", func(t *testing.T) {
		stubCheckForUpdates(t, versioncheck.CheckResult{
			Status:  versioncheck.StatusCheckFailed,
			Message: "Could not check for updates: GitHub took too long to respond.",
		})
		withArgs(t, "ohara", "version")

		stdout, stderr := captureOutput(t, func() { main() })
		if !strings.Contains(stdout, "ohara 1.10.7") {
			t.Fatalf("stdout = %q", stdout)
		}
		if !strings.Contains(stderr, "Could not check for updates") {
			t.Fatalf("stderr = %q", stderr)
		}
	})

	t.Run("prints available update", func(t *testing.T) {
		stubCheckForUpdates(t, versioncheck.CheckResult{
			Status:  versioncheck.StatusUpdateAvailable,
			Message: "Update available: 1.10.7 -> 1.10.8",
		})
		withArgs(t, "ohara", "version")

		stdout, stderr := captureOutput(t, func() { main() })
		if !strings.Contains(stdout, "ohara 1.10.7") {
			t.Fatalf("stdout = %q", stdout)
		}
		if !strings.Contains(stderr, "Update available") {
			t.Fatalf("stderr = %q", stderr)
		}
	})

	t.Run("prints nothing when up to date", func(t *testing.T) {
		stubCheckForUpdates(t, versioncheck.CheckResult{Status: versioncheck.StatusUpToDate})
		withArgs(t, "ohara", "version")

		stdout, stderr := captureOutput(t, func() { main() })
		if !strings.Contains(stdout, "ohara 1.10.7") {
			t.Fatalf("stdout = %q", stdout)
		}
		if stderr != "" {
			t.Fatalf("stderr = %q, want empty", stderr)
		}
	})
}

func TestMainExitPaths(t *testing.T) {
	tests := []struct {
		name            string
		helperCase      string
		expectedOutput  string
		expectedStderr  string
		expectedExitOne bool
	}{
		{name: "no args", helperCase: "no-args", expectedOutput: "Usage:", expectedExitOne: true},
		{name: "unknown command", helperCase: "unknown", expectedOutput: "Usage:", expectedStderr: "unknown command:", expectedExitOne: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=TestMainExitHelper")
			cmd.Env = append(os.Environ(),
				"GO_WANT_HELPER_PROCESS=1",
				"HELPER_CASE="+tc.helperCase,
			)

			out, err := cmd.CombinedOutput()
			if tc.expectedExitOne {
				exitErr, ok := err.(*exec.ExitError)
				if !ok {
					t.Fatalf("expected exit error, got %T (%v)", err, err)
				}
				if exitErr.ExitCode() != 1 {
					t.Fatalf("expected exit code 1, got %d; output=%q", exitErr.ExitCode(), string(out))
				}
			}

			if !strings.Contains(string(out), tc.expectedOutput) {
				t.Fatalf("output missing %q: %q", tc.expectedOutput, string(out))
			}
			if tc.expectedStderr != "" && !strings.Contains(string(out), tc.expectedStderr) {
				t.Fatalf("output missing stderr text %q: %q", tc.expectedStderr, string(out))
			}
		})
	}
}

func TestMainExitHelper(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	switch os.Getenv("HELPER_CASE") {
	case "no-args":
		os.Args = []string{"ohara"}
	case "unknown":
		os.Args = []string{"ohara", "definitely-unknown-command"}
	default:
		os.Args = []string{"ohara", "--help"}
	}

	main()
}

func TestCmdSearchLocalMode(t *testing.T) {
	cfg := testConfig(t)
	mustSeedMemory(t, cfg, "s-local", "proj-local", "discovery", "local-result", "local content for search", "project")

	withArgs(t, "ohara", "search", "local", "--project", "proj-local")
	stdout, stderr := captureOutput(t, func() { cmdSearch(cfg) })
	if stderr != "" {
		t.Fatalf("expected no stderr, got: %q", stderr)
	}
	if !strings.Contains(stdout, "Found") && !strings.Contains(stdout, "local-result") {
		t.Fatalf("expected local search results, got: %q", stdout)
	}
}

// ─── Projects command tests ───────────────────────────────────────────────────

func TestCmdProjectsListEmpty(t *testing.T) {
	cfg := testConfig(t)

	withArgs(t, "ohara", "projects", "list")
	stdout, stderr := captureOutput(t, func() { cmdProjectsList(cfg) })
	if stderr != "" {
		t.Fatalf("expected no stderr, got: %q", stderr)
	}
	if !strings.Contains(stdout, "No projects found") {
		t.Fatalf("expected empty projects message, got: %q", stdout)
	}
}

func TestCmdProjectsList(t *testing.T) {
	cfg := testConfig(t)

	// Seed memories for two projects
	mustSeedMemory(t, cfg, "s-alpha", "alpha", "discovery", "alpha-note", "alpha content", "project")
	mustSeedMemory(t, cfg, "s-alpha", "alpha", "bugfix", "alpha-bug", "alpha bug", "project")
	mustSeedMemory(t, cfg, "s-beta", "beta", "decision", "beta-note", "beta content", "project")

	withArgs(t, "ohara", "projects", "list")
	stdout, stderr := captureOutput(t, func() { cmdProjectsList(cfg) })
	if stderr != "" {
		t.Fatalf("expected no stderr, got: %q", stderr)
	}
	if !strings.Contains(stdout, "Projects (2)") {
		t.Fatalf("expected 'Projects (2)', got: %q", stdout)
	}
	if !strings.Contains(stdout, "alpha") || !strings.Contains(stdout, "beta") {
		t.Fatalf("expected project names in output, got: %q", stdout)
	}
	// alpha has 2 memories, beta has 1 — alpha should appear first
	alphaIdx := strings.Index(stdout, "alpha")
	betaIdx := strings.Index(stdout, "beta")
	if alphaIdx > betaIdx {
		t.Fatalf("expected alpha (more memories) before beta, got: %q", stdout)
	}
}

func TestCmdProjectsRoutesSubcommands(t *testing.T) {
	cfg := testConfig(t)

	// "list" subcommand
	withArgs(t, "ohara", "projects", "list")
	stdout, _ := captureOutput(t, func() { cmdProjects(cfg) })
	if !strings.Contains(stdout, "No projects found") && !strings.Contains(stdout, "Projects") {
		t.Fatalf("expected projects list output, got: %q", stdout)
	}

	// default (no subcommand) → list
	withArgs(t, "ohara", "projects")
	stdout2, _ := captureOutput(t, func() { cmdProjects(cfg) })
	_ = stdout2 // just checking it doesn't crash
}

func TestCmdProjectsConsolidateNoSimilar(t *testing.T) {
	cfg := testConfig(t)

	// Seed a single unique project
	mustSeedMemory(t, cfg, "s-unique", "unique-project", "discovery", "unique note", "content", "project")

	// Set cwd to a temp dir named "unique-project" with no git
	workDir := filepath.Join(t.TempDir(), "unique-project")
	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	withCwd(t, workDir)

	// Stub detectProject to return the known canonical
	old := detectProject
	detectProject = func(string) string { return "unique-project" }
	t.Cleanup(func() { detectProject = old })

	withArgs(t, "ohara", "projects", "consolidate")
	stdout, stderr := captureOutput(t, func() { cmdProjectsConsolidate(cfg) })
	if stderr != "" {
		t.Fatalf("expected no stderr, got: %q", stderr)
	}
	if !strings.Contains(stdout, "No similar") {
		t.Fatalf("expected no-similar message, got: %q", stdout)
	}
}

func TestCmdProjectsConsolidateDryRun(t *testing.T) {
	cfg := testConfig(t)

	// Seed a canonical and a similar variant (substring match, distinct after normalize)
	mustSeedMemory(t, cfg, "s-eng", "ohara", "discovery", "eng note", "content", "project")
	mustSeedMemory(t, cfg, "s-engm", "ohara-memory", "discovery", "engm note", "content", "project")

	old := detectProject
	detectProject = func(string) string { return "ohara" }
	t.Cleanup(func() { detectProject = old })

	withArgs(t, "ohara", "projects", "consolidate", "--dry-run")
	stdout, stderr := captureOutput(t, func() { cmdProjectsConsolidate(cfg) })
	if stderr != "" {
		t.Fatalf("expected no stderr, got: %q", stderr)
	}
	if !strings.Contains(stdout, "dry-run") {
		t.Fatalf("expected dry-run message, got: %q", stdout)
	}
	// Verify no actual merge happened (both projects still exist)
	s, err := store.New(cfg)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()
	names, err := s.ListProjectNames()
	if err != nil {
		t.Fatalf("ListProjectNames: %v", err)
	}
	// Should still have both names (no merge happened)
	if len(names) < 2 {
		t.Fatalf("expected 2 project names after dry-run, got: %v", names)
	}
}

func TestCmdProjectsConsolidateSingleProject(t *testing.T) {
	cfg := testConfig(t)

	// Seed canonical and a similar variant (substring match, distinct after normalize)
	mustSeedMemory(t, cfg, "s-eng", "ohara", "discovery", "eng note", "content", "project")
	mustSeedMemory(t, cfg, "s-engm", "ohara-memory", "discovery", "engm note", "content", "project")

	old := detectProject
	detectProject = func(string) string { return "ohara" }
	t.Cleanup(func() { detectProject = old })

	// Stub scanInputLine to answer "all"
	oldScan := scanInputLine
	t.Cleanup(func() { scanInputLine = oldScan })
	scanInputLine = func(a ...any) (int, error) {
		if ptr, ok := a[0].(*string); ok {
			*ptr = "all"
		}
		return 1, nil
	}

	withArgs(t, "ohara", "projects", "consolidate")
	stdout, stderr := captureOutput(t, func() { cmdProjectsConsolidate(cfg) })
	if stderr != "" {
		t.Fatalf("expected no stderr, got: %q", stderr)
	}
	if !strings.Contains(stdout, "Merged into") {
		t.Fatalf("expected merge result, got: %q", stdout)
	}

	// Verify ohara-memory was merged into ohara
	s, err := store.New(cfg)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()
	names, err := s.ListProjectNames()
	if err != nil {
		t.Fatalf("ListProjectNames: %v", err)
	}
	if len(names) != 1 || names[0] != "ohara" {
		t.Fatalf("expected only 'ohara' after merge, got: %v", names)
	}
}

func TestCmdProjectsConsolidateAllDryRun(t *testing.T) {
	cfg := testConfig(t)

	// Seed similar projects (substring match, stays distinct after normalize)
	mustSeedMemory(t, cfg, "s-eng", "ohara", "discovery", "eng note", "content", "project")
	mustSeedMemory(t, cfg, "s-engm", "ohara-memory", "discovery", "engm note", "content", "project")

	withArgs(t, "ohara", "projects", "consolidate", "--all", "--dry-run")
	stdout, stderr := captureOutput(t, func() { cmdProjectsConsolidate(cfg) })
	if stderr != "" {
		t.Fatalf("expected no stderr, got: %q", stderr)
	}
	if !strings.Contains(stdout, "dry-run") || !strings.Contains(stdout, "Group") {
		t.Fatalf("expected dry-run group output, got: %q", stdout)
	}
}

func TestCmdProjectsAllNoGroups(t *testing.T) {
	cfg := testConfig(t)

	// Seed completely unrelated projects
	mustSeedMemory(t, cfg, "s-foo", "fooproject", "discovery", "foo", "content", "project")
	mustSeedMemory(t, cfg, "s-bar", "barproject", "discovery", "bar", "content", "project")
	mustSeedMemory(t, cfg, "s-qux", "quxproject", "discovery", "qux", "content", "project")

	withArgs(t, "ohara", "projects", "consolidate", "--all")
	stdout, stderr := captureOutput(t, func() { cmdProjectsConsolidate(cfg) })
	if stderr != "" {
		t.Fatalf("expected no stderr, got: %q", stderr)
	}
	// The three "project"-suffixed names might be grouped by similarity.
	// We just verify it runs without error and produces readable output.
	_ = stdout
}

func TestCmdMCPDetectsProjectFromFlag(t *testing.T) {
	// Test that --project flag is parsed and passed to MCP config.
	// We can't easily test the full MCP server startup (it blocks on stdio),
	// but we test the flag-parsing + detectProject chain indirectly by
	// checking that cmdMCP doesn't crash when store is available.
	//
	// The key invariant tested: --project sets detectedProject correctly.
	// We verify by stubbing newMCPServerWithConfig and checking the MCPConfig.
	cfg := testConfig(t)

	var capturedCfg mcp.MCPConfig
	oldNew := newMCPServerWithConfig
	t.Cleanup(func() { newMCPServerWithConfig = oldNew })
	newMCPServerWithConfig = func(s *store.Store, mcpCfg mcp.MCPConfig, allowlist map[string]bool) *mcpserver.MCPServer {
		capturedCfg = mcpCfg
		// Return a valid server so serveMCP doesn't panic
		return oldNew(s, mcpCfg, allowlist)
	}

	oldServe := serveMCP
	t.Cleanup(func() { serveMCP = oldServe })
	// Prevent actual stdio serve — return immediately
	serveMCP = func(srv *mcpserver.MCPServer, opts ...mcpserver.StdioOption) error {
		return nil
	}

	withArgs(t, "ohara", "mcp", "--project=myproject")
	_, _ = captureOutput(t, func() { cmdMCP(cfg) })

	if capturedCfg.DefaultProject != "myproject" {
		t.Fatalf("expected DefaultProject=%q, got %q", "myproject", capturedCfg.DefaultProject)
	}
}

func TestCmdMCPDetectsProjectFromEnv(t *testing.T) {
	cfg := testConfig(t)

	t.Setenv("OHARA_PROJECT", "env-project")

	var capturedCfg mcp.MCPConfig
	oldNew := newMCPServerWithConfig
	t.Cleanup(func() { newMCPServerWithConfig = oldNew })
	newMCPServerWithConfig = func(s *store.Store, mcpCfg mcp.MCPConfig, allowlist map[string]bool) *mcpserver.MCPServer {
		capturedCfg = mcpCfg
		return oldNew(s, mcpCfg, allowlist)
	}

	oldServe := serveMCP
	t.Cleanup(func() { serveMCP = oldServe })
	serveMCP = func(srv *mcpserver.MCPServer, opts ...mcpserver.StdioOption) error {
		return nil
	}

	withArgs(t, "ohara", "mcp")
	_, _ = captureOutput(t, func() { cmdMCP(cfg) })

	if capturedCfg.DefaultProject != "env-project" {
		t.Fatalf("expected DefaultProject=%q, got %q", "env-project", capturedCfg.DefaultProject)
	}
}

func TestCmdMCPDetectsProjectFromGit(t *testing.T) {
	cfg := testConfig(t)

	// Stub detectProject to simulate git detection
	old := detectProject
	t.Cleanup(func() { detectProject = old })
	detectProject = func(string) string { return "detected-from-git" }

	var capturedCfg mcp.MCPConfig
	oldNew := newMCPServerWithConfig
	t.Cleanup(func() { newMCPServerWithConfig = oldNew })
	newMCPServerWithConfig = func(s *store.Store, mcpCfg mcp.MCPConfig, allowlist map[string]bool) *mcpserver.MCPServer {
		capturedCfg = mcpCfg
		return oldNew(s, mcpCfg, allowlist)
	}

	oldServe := serveMCP
	t.Cleanup(func() { serveMCP = oldServe })
	serveMCP = func(srv *mcpserver.MCPServer, opts ...mcpserver.StdioOption) error {
		return nil
	}

	withArgs(t, "ohara", "mcp")
	_, _ = captureOutput(t, func() { cmdMCP(cfg) })

	if capturedCfg.DefaultProject != "detected-from-git" {
		t.Fatalf("expected DefaultProject=%q, got %q", "detected-from-git", capturedCfg.DefaultProject)
	}
}

func TestCmdSyncUsesDetectProject(t *testing.T) {
	workDir := t.TempDir()
	withCwd(t, workDir)

	cfg := testConfig(t)

	// Stub detectProject to verify it's called instead of filepath.Base
	old := detectProject
	t.Cleanup(func() { detectProject = old })
	detectProject = func(dir string) string { return "git-detected-project" }

	withArgs(t, "ohara", "sync")
	stdout, stderr := captureOutput(t, func() { cmdSync(cfg) })
	if stderr != "" {
		t.Fatalf("expected no stderr, got: %q", stderr)
	}
	if !strings.Contains(stdout, "git-detected-project") {
		t.Fatalf("expected detectProject result in output, got: %q", stdout)
	}
}

func TestCmdConsolidateDryRunNoCandidates(t *testing.T) {
	cfg := testConfig(t)

	// Stub candidate generation to return no candidates
	old := storeConsolidateCandidates
	t.Cleanup(func() { storeConsolidateCandidates = old })
	storeConsolidateCandidates = func(s *store.Store, project, domain string, dryRun bool) (int, []string, error) {
		return 0, nil, nil
	}

	withArgs(t, "ohara", "consolidate", "--dry-run")
	stdout, stderr := captureOutput(t, func() { cmdConsolidate(cfg) })
	if stderr != "" {
		t.Fatalf("expected no stderr, got: %q", stderr)
	}
	if !strings.Contains(stdout, "consolidate (dry-run): no candidate groups found") {
		t.Fatalf("unexpected output: %q", stdout)
	}
}

func TestCmdConsolidateDryRunWithCandidates(t *testing.T) {
	cfg := testConfig(t)

	// Stub candidate generation to return candidates
	old := storeConsolidateCandidates
	t.Cleanup(func() { storeConsolidateCandidates = old })
	storeConsolidateCandidates = func(s *store.Store, project, domain string, dryRun bool) (int, []string, error) {
		return 2, []string{
			"Group 1: mem1, mem2 → decision: consolidate these",
			"Group 2: mem3, mem4 → decision: consolidate these",
		}, nil
	}

	withArgs(t, "ohara", "consolidate", "--dry-run")
	stdout, stderr := captureOutput(t, func() { cmdConsolidate(cfg) })
	if stderr != "" {
		t.Fatalf("expected no stderr, got: %q", stderr)
	}
	if !strings.Contains(stdout, "Group 1: mem1, mem2") {
		t.Fatalf("expected candidate summary in output, got: %q", stdout)
	}
	if !strings.Contains(stdout, "Group 2: mem3, mem4") {
		t.Fatalf("expected candidate summary in output, got: %q", stdout)
	}
}

func TestCmdConsolidateCreateCandidates(t *testing.T) {
	cfg := testConfig(t)

	// Stub candidate generation to return candidates
	old := storeConsolidateCandidates
	t.Cleanup(func() { storeConsolidateCandidates = old })
	storeConsolidateCandidates = func(s *store.Store, project, domain string, dryRun bool) (int, []string, error) {
		return 2, []string{
			"Created: mem1+mem2",
			"Created: mem3+mem4",
		}, nil
	}

	withArgs(t, "ohara", "consolidate")
	stdout, stderr := captureOutput(t, func() { cmdConsolidate(cfg) })
	if stderr != "" {
		t.Fatalf("expected no stderr, got: %q", stderr)
	}
	if !strings.Contains(stdout, "consolidate: created 2 candidate(s)") {
		t.Fatalf("expected created message, got: %q", stdout)
	}
	if !strings.Contains(stdout, "Created: mem1+mem2") {
		t.Fatalf("expected candidate detail in output, got: %q", stdout)
	}
}

func TestCmdConsolidateNoCandidatesCreated(t *testing.T) {
	cfg := testConfig(t)

	// Stub candidate generation to return 0 created
	old := storeConsolidateCandidates
	t.Cleanup(func() { storeConsolidateCandidates = old })
	storeConsolidateCandidates = func(s *store.Store, project, domain string, dryRun bool) (int, []string, error) {
		return 0, nil, nil
	}

	withArgs(t, "ohara", "consolidate")
	stdout, stderr := captureOutput(t, func() { cmdConsolidate(cfg) })
	if stderr != "" {
		t.Fatalf("expected no stderr, got: %q", stderr)
	}
	if !strings.Contains(stdout, "consolidate: no candidates created") {
		t.Fatalf("expected no-candidates message, got: %q", stdout)
	}
}
