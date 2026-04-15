// Ohara is a local-first persistent memory system for AI coding agents.
// This binary exposes a CLI and a background server for memory operations.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ashwnn/ohara/internal/config"
	"github.com/ashwnn/ohara/internal/maintain"
	"github.com/ashwnn/ohara/internal/mcp"
	"github.com/ashwnn/ohara/internal/server"
	"github.com/ashwnn/ohara/internal/setup"
	"github.com/ashwnn/ohara/internal/store"
	oharasync "github.com/ashwnn/ohara/internal/sync"
	versionpkg "github.com/ashwnn/ohara/internal/version"

	mcpserver "github.com/mark3labs/mcp-go/server"
)

// version is set at build time via -ldflags.
var version = "dev"

// exitFunc is called on fatal errors. Stubbed in tests.
var exitFunc = func(code int) { os.Exit(code) }

// checkForUpdates returns version check results. Stubbed in tests.
var checkForUpdates = versionpkg.CheckLatest

// storeNew opens a store. Stubbed in tests.
var storeNew = store.New

// newHTTPServer creates an HTTP server. Stubbed in tests.
// socketPath is empty when TCP mode is used.
var newHTTPServer = func(s *store.Store, port int, socketPath string) *server.Server {
	if socketPath != "" {
		return server.New(s, port, server.WithSocketPath(socketPath))
	}
	return server.New(s, port)
}

// startHTTP starts the HTTP server. Stubbed in tests.
var startHTTP = func(srv *server.Server) error { return nil }

// newMCPServer creates an MCP server. Stubbed in tests.
var newMCPServer = func(s *store.Store) *mcpserver.MCPServer { return nil }

// newMCPServerWithTools creates an MCP server with tools. Stubbed in tests.
var newMCPServerWithTools = func(s *store.Store, allowlist map[string]bool) *mcpserver.MCPServer { return nil }

// newMCPServerWithConfig creates an MCP server with config. Stubbed in tests.
var newMCPServerWithConfig = func(s *store.Store, mcpCfg mcp.MCPConfig, allowlist map[string]bool) *mcpserver.MCPServer { return nil }

// serveMCP starts the MCP stdio server. Stubbed in tests.
var serveMCP = func(srv *mcpserver.MCPServer, opts ...mcpserver.StdioOption) error { return nil }

// setupSupportedAgents lists supported agents. Stubbed in tests.
var setupSupportedAgents = func() []setup.Agent { return nil }

// setupInstallAgent installs an agent plugin. Stubbed in tests.
var setupInstallAgent = func(agent string) (*setup.Result, error) { return nil, nil }

// scanInputLine reads a line from stdin. Stubbed in tests.
var scanInputLine = func(a ...interface{}) (int, error) { return 0, nil }

// storeSearch searches memories. Stubbed in tests.
var storeSearch = func(s *store.Store, query string, opts store.SearchOptions) ([]store.SearchResult, error) {
	return s.Search(query, opts)
}

// storeAddObservation adds an observation. Stubbed in tests.
var storeAddObservation = func(s *store.Store, p store.AddObservationParams) (int64, error) {
	return s.AddObservation(p)
}

// storeTimeline returns a timeline. Stubbed in tests.
var storeTimeline = func(s *store.Store, obsID int64, before, after int) (*store.TimelineResult, error) {
	return s.Timeline(obsID, before, after)
}

// storeFormatContext formats context. Stubbed in tests.
var storeFormatContext = func(s *store.Store, project, scope string) (string, error) {
	return s.FormatContext(project, scope)
}

// storeStats returns stats. Stubbed in tests.
var storeStats = func(s *store.Store) (*store.Stats, error) { return s.Stats() }

// storeExport exports data. Stubbed in tests.
var storeExport = func(s *store.Store) (*store.ExportData, error) { return s.Export() }

// storeImport imports data. Stubbed in tests.
var storeImport = func(s *store.Store, data *store.ExportData) (*store.ImportResult, error) { return s.Import(data) }

// syncStatus returns sync status. Stubbed in tests.
var syncStatus = func(sy *oharasync.Syncer) (int, int, int, error) {
	return 0, 0, 0, nil
}

// syncImport imports sync data. Stubbed in tests.
var syncImport = func(sy *oharasync.Syncer) (*oharasync.ImportResult, error) {
	return &oharasync.ImportResult{}, nil
}

// syncExport exports sync data. Stubbed in tests.
var syncExport = func(sy *oharasync.Syncer, createdBy, project string) (*oharasync.SyncResult, error) {
	return &oharasync.SyncResult{IsEmpty: true}, nil
}

// jsonMarshalIndent marshals JSON. Stubbed in tests.
var jsonMarshalIndent = json.MarshalIndent

// jsonUnmarshal unmarshals JSON. Stubbed in tests.
var jsonUnmarshal = json.Unmarshal

// detectProject detects the project from a directory. Stubbed in tests.
var detectProject = func(dir string) string {
	name := filepath.Base(dir)
	name = strings.TrimSuffix(name, "-git")
	return strings.ToLower(name)
}

// storeConsolidate merges project records. Stubbed in tests.
var storeConsolidate = func(s *store.Store, sources []string, canonical string) (*store.MergeResult, error) {
	return s.MergeProjects(sources, canonical)
}

// newObsidianWatcher creates an obsidian watcher. Stubbed in tests.
var newObsidianWatcher = func(c interface{}) interface{} { return nil }

// loadRuntimeConfig loads the runtime config. Stubbed in tests.
var loadRuntimeConfig = func(cfgPath string) (config.RuntimeConfig, error) {
	return config.Load(cfgPath)
}

// loadRuntimeMaintain loads the runtime config for maintenance commands.
// Uses the same two-phase loading as cmdServe (env-derived DataDir → config file).
// Stubbed in tests.
var loadRuntimeMaintain = func() (config.RuntimeConfig, error) {
	baseCfg, err := loadRuntimeConfig("")
	if err != nil {
		return config.RuntimeConfig{}, err
	}
	// OHARA_DATA_DIR is applied by loadRuntimeConfig, but we also need to
	// apply it here for the store.Config used by maintain/backup.
	if env := os.Getenv("OHARA_DATA_DIR"); env != "" {
		baseCfg.DataDir = env
	}
	configFile := filepath.Join(baseCfg.DataDir, config.DefaultConfigFile)
	return loadRuntimeConfig(configFile)
}

// newTUIModel creates a TUI model. Stubbed in tests.
var newTUIModel = func(s *store.Store) interface{} { return nil }

// newTeaProgram creates a tea.Program. Stubbed in tests.
var newTeaProgram = func(m interface{}) interface{} { return nil }

// runTeaProgram runs a tea.Program. Stubbed in tests.
var runTeaProgram = func(p interface{}) (interface{}, error) { return nil, nil }

// ─── Command Variables (can be overridden by test stubs) ──────────────────────

var cmdMCP = realCmdMCP
var cmdTUI = realCmdTUI
var cmdSearch = realCmdSearch
var cmdSave = realCmdSave
var cmdTimeline = realCmdTimeline
var cmdContext = realCmdContext
var cmdStats = realCmdStats
var cmdExport = realCmdExport
var cmdImport = realCmdImport
var cmdSync = realCmdSync
var cmdSetup = realCmdSetup
var cmdProjects = realCmdProjects
var cmdProjectsList = realCmdProjectsList
var cmdProjectsConsolidate = realCmdProjectsConsolidate
var cmdObsidianExport = realCmdObsidianExport
var cmdMaintain = realCmdMaintain
var cmdBackup = realCmdBackup
var cmdCheck = realCmdCheck
var cmdServe = realCmdServe

// ─── fatal / exit helpers ────────────────────────────────────────────────────

func fatal(msg interface{}) {
	fmt.Fprintln(os.Stderr, "ohara: "+fmt.Sprint(msg))
	exitFunc(1)
}

// ─── Usage ──────────────────────────────────────────────────────────────────

func printUsage() {
	fmt.Println("ohara v" + version + " — local memory for AI agents")
	fmt.Println()
	fmt.Println("Usage: ohara <command> [flags]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  serve              Start the HTTP API server (foreground)")
	fmt.Println("  mcp                Start the MCP stdio server")
	fmt.Println("  tui                Start the terminal UI")
	fmt.Println("  search <query>     Search memories")
	fmt.Println("  save <title> <content>  Save a new memory")
	fmt.Println("  timeline <id>       Browse timeline around an observation")
	fmt.Println("  context [project]  Show context from previous sessions")
	fmt.Println("  stats              Print database statistics")
	fmt.Println("  export [path]      Export memories to JSON")
	fmt.Println("  import <path>      Import memories from JSON")
	fmt.Println("  sync               Sync memories with cloud")
	fmt.Println("  projects <sub>     Manage projects (list, consolidate)")
	fmt.Println("  maintain          Run maintenance (archive, backup, integrity)")
	fmt.Println("  backup             Create a database snapshot")
	fmt.Println("  check              Run integrity checks")
	fmt.Println("  setup [agent]      Set up plugin for an agent")
	fmt.Println("  obsidian-export    Export memories to Obsidian vault")
	fmt.Println()
	fmt.Println("Flags:")
	fmt.Println("  --help, -h         Show this help")
	fmt.Println()
	fmt.Println("Environment:")
	fmt.Println("  OHARA_DATA_DIR     Data directory (default ~/.ohara)")
	fmt.Println("  OHARA_PORT         HTTP server port (default 7437)")
	fmt.Println("  OHARA_SOCKET       Unix socket path (takes priority over port)")
	fmt.Println("  OHARA_PROJECT     Project name for session context")
}

func printPostInstall(agent string) {
	agents := map[string]struct {
		restart, config string
	}{
		"opencode":   {"Restart OpenCode", "ohara serve &"},
		"gemini-cli": {"Restart Gemini CLI", "~/.gemini/settings.json"},
		"codex":      {"Restart Codex", "~/.codex/config.toml"},
	}
	info, ok := agents[agent]
	if !ok {
		return
	}
	fmt.Printf("Setup complete for %s!\n", agent)
	fmt.Printf("1. %s\n", info.restart)
	fmt.Printf("2. Or copy the plugin config to %s\n", info.config)
}

// truncate truncates a string to max length.
func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}

// ─── Maintenance Commands ─────────────────────────────────────────────────────

func realCmdMaintain(cfg store.Config) {
	s, err := storeNew(cfg)
	if err != nil {
		fatal("store: " + err.Error())
	}
	defer s.Close()

	// Load RuntimeConfig for SnapshotDir and RetainSnapshots.
	// This follows the same two-phase loading as cmdServe.
	runtimeCfg, err := loadRuntimeMaintain()
	if err != nil {
		fatal("config: " + err.Error())
	}

	opts := maintain.DefaultOptions(cfg.DataDir)
	// Override maintain.Options from RuntimeConfig when set.
	if runtimeCfg.SnapshotDir != "" {
		opts.SnapshotDir = runtimeCfg.SnapshotDir
	}
	if runtimeCfg.RetainSnapshots > 0 {
		opts.RetainDays = runtimeCfg.RetainSnapshots
	}

	for _, arg := range os.Args[2:] {
		if arg == "--dry-run" || arg == "-n" {
			opts.DryRun = true
			break
		}
	}

	if opts.DryRun {
		n, err := maintain.ArchiveExpired(s, true)
		if err != nil {
			fatal("dry-run archive: " + err.Error())
		}
		fmt.Printf("dry-run: would archive %d expired memory item(s)\n", n)
		fmt.Println("maintenance: dry-run complete (no changes made)")
		return
	}

	stats, err := maintain.Run(s, opts)
	if err != nil {
		fmt.Println("maintenance: completed with errors:")
		for _, e := range stats.Errors {
			fmt.Printf("  - %s\n", e)
		}
		if stats.Archived > 0 {
			fmt.Printf("  archived: %d\n", stats.Archived)
		}
		if stats.IntegrityOK {
			fmt.Println("  integrity: ok")
		}
		if stats.FTSSOptimized > 0 {
			fmt.Printf("  fts optimized: %d table(s)\n", stats.FTSSOptimized)
		}
		if stats.BackupPath != "" {
			fmt.Printf("  backup: %s\n", stats.BackupPath)
		}
		exitFunc(1)
	}

	if stats.Archived > 0 {
		fmt.Printf("archived: %d expired memory item(s)\n", stats.Archived)
	}
	if stats.IntegrityOK {
		fmt.Println("integrity: ok")
	} else if stats.IntegrityResult != "" {
		fmt.Printf("integrity: %s\n", stats.IntegrityResult)
	}
	if stats.FTSSOptimized > 0 {
		fmt.Printf("fts optimized: %d table(s)\n", stats.FTSSOptimized)
	}
	if stats.BackupPath != "" {
		fmt.Printf("backup: %s\n", stats.BackupPath)
	}
	fmt.Println("maintenance: complete")
}

func realCmdBackup(cfg store.Config) {
	s, err := storeNew(cfg)
	if err != nil {
		fatal("store: " + err.Error())
	}
	defer s.Close()

	// Load RuntimeConfig for SnapshotDir. Uses same two-phase loading as cmdServe.
	runtimeCfg, err := loadRuntimeMaintain()
	if err != nil {
		fatal("config: " + err.Error())
	}

	snapshotDir := runtimeCfg.SnapshotDir
	if snapshotDir == "" {
		// Fallback: derive from DataDir if RuntimeConfig didn't set it.
		snapshotDir = filepath.Join(cfg.DataDir, "snapshots")
	}
	path, err := maintain.Backup(s, snapshotDir)
	if err != nil {
		fatal("backup: " + err.Error())
	}
	fmt.Printf("backup: %s\n", path)
}

func realCmdCheck(cfg store.Config) {
	s, err := storeNew(cfg)
	if err != nil {
		fatal("store: " + err.Error())
	}
	defer s.Close()

	ok, result, err := maintain.IntegrityCheck(s)
	if err != nil {
		fatal("integrity check: " + err.Error())
	}
	if ok {
		fmt.Println("integrity: ok")
	} else {
		fmt.Printf("integrity: WARN — %s\n", result)
		exitFunc(1)
	}
}

// ─── Stub implementations for other commands ─────────────────────────────────

func realCmdServe(cfg store.Config) {
	// Load config from {DataDir}/config.json with env-var overrides.
	// First load with empty path to get DataDir (which may be overridden by env).
	baseCfg, err := loadRuntimeConfig("")
	if err != nil {
		fatal("config: " + err.Error())
	}
	// Resolve the config file path from the resolved DataDir and reload.
	configFile := filepath.Join(baseCfg.DataDir, config.DefaultConfigFile)
	cfg2, err := loadRuntimeConfig(configFile)
	if err != nil {
		fatal("config: " + err.Error())
	}

	// Extract port from http_addr.
	_, port := config.HTTPAddrParts(cfg2.HTTPAddr)
	socketPath := cfg2.SocketPath

	// Positional port argument overrides config (e.g., "ohara serve 9001").
	if len(os.Args) >= 3 {
		if p, err := strconv.Atoi(os.Args[2]); err == nil {
			port = p
		}
	}

	// --port and --socket flags override config/env.
	for i, arg := range os.Args[2:] {
		if arg == "--socket" && i+1 < len(os.Args[2:]) {
			socketPath = os.Args[2:][i+1]
		} else if strings.HasPrefix(arg, "--socket=") {
			socketPath = strings.TrimPrefix(arg, "--socket=")
		} else if arg == "--port" && i+1 < len(os.Args[2:]) {
			p, err := strconv.Atoi(os.Args[2:][i+1])
			if err == nil {
				port = p
			}
		}
	}

	s, err := storeNew(cfg)
	if err != nil {
		fatal("store: " + err.Error())
	}
	defer s.Close()

	srv := newHTTPServer(s, port, socketPath)
	if err := startHTTP(srv); err != nil {
		fatal("serve: " + err.Error())
	}
}

func realCmdMCP(cfg store.Config) {
	s, err := storeNew(cfg)
	if err != nil {
		fatal("store: " + err.Error())
	}
	defer s.Close()

	// Parse --tools flag for tool allowlist.
	var allowlist map[string]bool
	for i, arg := range os.Args[2:] {
		if strings.HasPrefix(arg, "--tools=") {
			tools := strings.TrimPrefix(arg, "--tools=")
			allowlist = make(map[string]bool)
			for _, t := range strings.Split(tools, ",") {
				allowlist[strings.TrimSpace(t)] = true
			}
			break
		}
		if arg == "--tools" && i+1 < len(os.Args[2:]) {
			tools := os.Args[2:][i+1]
			allowlist = make(map[string]bool)
			for _, t := range strings.Split(tools, ",") {
				allowlist[strings.TrimSpace(t)] = true
			}
			break
		}
	}

	// Parse --project flag or use env/git detection.
	project := os.Getenv("OHARA_PROJECT")
	for i, arg := range os.Args[2:] {
		if strings.HasPrefix(arg, "--project=") {
			project = strings.TrimPrefix(arg, "--project=")
			break
		}
		if arg == "--project" && i+1 < len(os.Args[2:]) {
			project = os.Args[2:][i+1]
			break
		}
	}
	if project == "" {
		wd, _ := os.Getwd()
		project = detectProject(wd)
	}

	mcpCfg := mcp.MCPConfig{DefaultProject: project}
	srv := newMCPServerWithConfig(s, mcpCfg, allowlist)
	if err := serveMCP(srv); err != nil {
		fatal("mcp: " + err.Error())
	}
}

func realCmdTUI(cfg store.Config) {
	s, err := storeNew(cfg)
	if err != nil {
		fatal("store: " + err.Error())
	}
	defer s.Close()

	model := newTUIModel(s)
	program := newTeaProgram(model)
	if _, err := runTeaProgram(program); err != nil {
		fatal("tui: " + err.Error())
	}
}

func realCmdSearch(cfg store.Config) {
	opts := store.SearchOptions{}
	args := os.Args[2:]
	positional := []string{}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--project=") {
			opts.Project = strings.TrimPrefix(arg, "--project=")
		} else if arg == "--project" && i+1 < len(args) {
			opts.Project = args[i+1]
			i++
		} else if strings.HasPrefix(arg, "--type=") {
			opts.Type = strings.TrimPrefix(arg, "--type=")
		} else if arg == "--type" && i+1 < len(args) {
			opts.Type = args[i+1]
			i++
		} else if strings.HasPrefix(arg, "--scope=") {
			opts.Scope = strings.TrimPrefix(arg, "--scope=")
		} else if arg == "--scope" && i+1 < len(args) {
			opts.Scope = args[i+1]
			i++
		} else if strings.HasPrefix(arg, "--limit=") {
			if v, err := strconv.Atoi(strings.TrimPrefix(arg, "--limit=")); err == nil {
				opts.Limit = v
			}
		} else if arg == "--limit" && i+1 < len(args) {
			if v, err := strconv.Atoi(args[i+1]); err == nil {
				opts.Limit = v
			}
			i++
		} else if strings.HasPrefix(arg, "--") {
			// Ignore unknown flags (dangling flags).
		} else {
			positional = append(positional, arg)
		}
	}

	query := ""
	if len(positional) > 0 {
		query = positional[0]
	}

	// Validation: missing query with --limit flag is an error.
	hasLimitFlag := false
	for _, a := range args {
		if a == "--limit" || strings.HasPrefix(a, "--limit=") {
			hasLimitFlag = true
			break
		}
	}
	if query == "" && hasLimitFlag {
		fatal("search query is required")
	}
	if query == "" {
		fatal("usage: ohara search <query>")
	}

	s, err := storeNew(cfg)
	if err != nil {
		fatal("store: " + err.Error())
	}
	defer s.Close()

	results, err := storeSearch(s, query, opts)
	if err != nil {
		fatal("search: " + err.Error())
	}

	if len(results) == 0 {
		fmt.Println("No memories found")
		return
	}

	fmt.Printf("Found %d memories\n", len(results))
	for _, r := range results {
		fmt.Printf("\n[%s] %s\n%s\n", r.Type, r.Title, r.Content)
	}
}

func realCmdSave(cfg store.Config) {
	params := store.AddObservationParams{}
	args := os.Args[2:]
	positional := []string{}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--type=") {
			params.Type = strings.TrimPrefix(arg, "--type=")
		} else if arg == "--type" && i+1 < len(args) {
			params.Type = args[i+1]
			i++
		} else if strings.HasPrefix(arg, "--project=") {
			params.Project = strings.TrimPrefix(arg, "--project=")
		} else if arg == "--project" && i+1 < len(args) {
			params.Project = args[i+1]
			i++
		} else if strings.HasPrefix(arg, "--scope=") {
			params.Scope = strings.TrimPrefix(arg, "--scope=")
		} else if arg == "--scope" && i+1 < len(args) {
			params.Scope = args[i+1]
			i++
		} else if strings.HasPrefix(arg, "--topic=") {
			params.TopicKey = strings.TrimPrefix(arg, "--topic=")
		} else if arg == "--topic" && i+1 < len(args) {
			params.TopicKey = args[i+1]
			i++
		} else if strings.HasPrefix(arg, "--") {
			// Ignore dangling flags (e.g. --type without value).
		} else {
			positional = append(positional, arg)
		}
	}

	if len(positional) < 1 {
		fatal("usage: ohara save <title> <content>")
	}
	if len(positional) < 2 {
		fatal("usage: ohara save <title> <content>")
	}
	params.Title = positional[0]
	params.Content = positional[1]

	if params.Type == "" {
		params.Type = "note"
	}

	s, err := storeNew(cfg)
	if err != nil {
		fatal("store: " + err.Error())
	}
	defer s.Close()

	// Ensure a session exists so AddObservation satisfies the FK constraint.
	sessionID := "cli-session"
	if params.Project != "" {
		sessionID = "cli-session-" + params.Project
	}
	_ = s.CreateSession(sessionID, params.Project, "")
	params.SessionID = sessionID

	if _, err := storeAddObservation(s, params); err != nil {
		fatal("save: " + err.Error())
	}
	fmt.Printf("Memory saved: %s\n", params.Title)
}

func realCmdTimeline(cfg store.Config) {
	if len(os.Args) < 3 {
		fatal("usage: ohara timeline <id>")
	}

	idStr := os.Args[2]
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		fatal("invalid observation id: " + err.Error())
	}

	before, after := 1, 1
	for i := 2; i < len(os.Args); i++ {
		arg := os.Args[i]
		if arg == "--before" && i+1 < len(os.Args) {
			if v, err := strconv.Atoi(os.Args[i+1]); err == nil {
				before = v
			}
			i++
		} else if arg == "--after" && i+1 < len(os.Args) {
			if v, err := strconv.Atoi(os.Args[i+1]); err == nil {
				after = v
			}
			i++
		}
	}

	s, err := storeNew(cfg)
	if err != nil {
		fatal("store: " + err.Error())
	}
	defer s.Close()

	result, err := storeTimeline(s, id, before, after)
	if err != nil {
		fatal("timeline: " + err.Error())
	}

	if result.SessionInfo != nil {
		fmt.Printf("Session: %s\n", result.SessionInfo.Project)
		if result.SessionInfo.Summary != nil && *result.SessionInfo.Summary != "" {
			fmt.Printf("%s\n", *result.SessionInfo.Summary)
		}
	}

	fmt.Printf(">>> #%d\n", id)
	fmt.Printf("[%s] %s — %s\n", result.Focus.Type, result.Focus.Title, result.Focus.Content)

	if before > 0 && len(result.Before) > 0 {
		fmt.Println("─── Before ───")
		for _, e := range result.Before {
			fmt.Printf("[%s] %s — %s\n", e.Type, e.Title, e.Content)
		}
	}

	if after > 0 && len(result.After) > 0 {
		fmt.Println("─── After ───")
		for _, e := range result.After {
			fmt.Printf("[%s] %s — %s\n", e.Type, e.Title, e.Content)
		}
	}
}

func realCmdContext(cfg store.Config) {
	project := ""
	if len(os.Args) >= 3 {
		project = os.Args[2]
	}

	s, err := storeNew(cfg)
	if err != nil {
		fatal("store: " + err.Error())
	}
	defer s.Close()

	ctx, err := storeFormatContext(s, project, "project")
	if err != nil {
		fatal("context: " + err.Error())
	}
	if ctx == "" {
		fmt.Println("No previous session memories found")
		return
	}
	fmt.Print(ctx)
}

func realCmdStats(cfg store.Config) {
	s, err := storeNew(cfg)
	if err != nil {
		fatal("store: " + err.Error())
	}
	defer s.Close()

	stats, err := storeStats(s)
	if err != nil {
		fatal("stats: " + err.Error())
	}
	fmt.Println("Ohara Memory Stats")
	fmt.Println("─────────────────")
	fmt.Printf("Sessions:     %d\n", stats.TotalSessions)
	fmt.Printf("Observations: %d\n", stats.TotalObservations)
	fmt.Printf("Prompts:       %d\n", stats.TotalPrompts)
	if len(stats.Projects) > 0 {
		fmt.Printf("Projects:     %s\n", strings.Join(stats.Projects, ", "))
	} else {
		fmt.Println("Projects:     none yet")
	}
}

func realCmdExport(cfg store.Config) {
	path := "ohara-export.json"
	if len(os.Args) >= 3 {
		path = os.Args[2]
	}

	s, err := storeNew(cfg)
	if err != nil {
		fatal("store: " + err.Error())
	}
	defer s.Close()

	data, err := storeExport(s)
	if err != nil {
		fatal("export: " + err.Error())
	}

	jsonData, err := jsonMarshalIndent(data, "", "  ")
	if err != nil {
		fatal("marshal: " + err.Error())
	}

	if err := os.WriteFile(path, jsonData, 0644); err != nil {
		fatal("write " + path + ": " + err.Error())
	}

	fmt.Printf("Exported to %s\n", path)
}

func realCmdImport(cfg store.Config) {
	if len(os.Args) < 3 {
		fatal("usage: ohara import <path>")
	}
	path := os.Args[2]

	data, err := os.ReadFile(path)
	if err != nil {
		fatal("read " + path + ": " + err.Error())
	}

	var exportData store.ExportData
	if err := jsonUnmarshal(data, &exportData); err != nil {
		fatal("parse " + path + ": " + err.Error())
	}

	s, err := storeNew(cfg)
	if err != nil {
		fatal("store: " + err.Error())
	}
	defer s.Close()

	if _, err := storeImport(s, &exportData); err != nil {
		fatal("import: " + err.Error())
	}

	fmt.Printf("Imported from %s\n", path)
}

func realCmdSync(cfg store.Config) {
	// Parse flags
	showStatus := false
	importMode := false
	exportAll := false
	project := ""

	args := os.Args[2:]
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--status" {
			showStatus = true
		} else if arg == "--import" {
			importMode = true
		} else if arg == "--all" {
			exportAll = true
		} else if strings.HasPrefix(arg, "--project=") {
			project = strings.TrimPrefix(arg, "--project=")
		} else if arg == "--project" && i+1 < len(args) {
			project = args[i+1]
			i++
		}
	}

	// Open store
	s, err := storeNew(cfg)
	if err != nil {
		fatal("store: " + err.Error())
	}
	defer s.Close()

	// Determine sync directory: OHARA_SYNC_DIR env > cwd/.ohara
	syncDir := ".ohara"
	if envDir := os.Getenv("OHARA_SYNC_DIR"); envDir != "" {
		syncDir = envDir
	}

	// Create syncer
	sy := oharasync.New(s, syncDir)

	// Handle --status
	if showStatus {
		localChunks, remoteChunks, pendingImport, err := syncStatus(sy)
		if err != nil {
			fatal("sync status: " + err.Error())
		}
		fmt.Printf("Sync status: %d local chunks, %d remote chunks, %d pending import\n", localChunks, remoteChunks, pendingImport)
		return
	}

	// Handle --import
	if importMode {
		result, err := syncImport(sy)
		if err != nil {
			fatal("sync import: " + err.Error())
		}
		if result == nil || result.ChunksImported == 0 {
			fmt.Println("No new chunks to import")
			return
		}
		if result.ChunksSkipped > 0 {
			fmt.Printf("Skipped: %d chunk(s) (already imported)\n", result.ChunksSkipped)
		}
		fmt.Printf("Imported %d new chunk(s) (%d sessions, %d observations, %d prompts)\n",
			result.ChunksImported, result.SessionsImported, result.ObservationsImported, result.PromptsImported)
		return
	}

	// Export mode (default or --all)
	// Detect project if not specified
	if project == "" {
		wd, _ := os.Getwd()
		project = detectProject(wd)
	}

	// Get current user for created_by
	createdBy, _ := os.Hostname()
	if u := os.Getenv("USER"); u != "" {
		createdBy = u
	}

	if exportAll {
		// Export all projects
		fmt.Println("Exporting ALL memories")
		result, err := syncExport(sy, createdBy, "")
		if err != nil {
			fatal("sync export: " + err.Error())
		}
		if result.IsEmpty {
			fmt.Println("Nothing new to sync")
			return
		}
		fmt.Printf("Created chunk %s (%d sessions, %d observations, %d prompts)\n",
			result.ChunkID, result.SessionsExported, result.ObservationsExported, result.PromptsExported)
	} else {
		// Export specific project
		fmt.Printf("Exporting memories for project %q\n", project)
		result, err := syncExport(sy, createdBy, project)
		if err != nil {
			fatal("sync export: " + err.Error())
		}
		if result.IsEmpty {
			fmt.Printf("Nothing new to sync for project %q\n", project)
			return
		}
		fmt.Printf("Created chunk %s (%d sessions, %d observations, %d prompts)\n",
			result.ChunkID, result.SessionsExported, result.ObservationsExported, result.PromptsExported)
	}
}

func realCmdSetup(cfg store.Config) {
	agents := setupSupportedAgents()
	if len(agents) == 0 {
		fatal("setup: no supported agents found")
	}

	// Build a set of supported agent names for O(1) lookup.
	agentSet := make(map[string]bool, len(agents))
	for _, a := range agents {
		agentSet[a.Name] = true
	}

	// Direct agent setup: ohara setup <agent>
	if len(os.Args) >= 3 {
		agentName := os.Args[2]
		if agentSet[agentName] {
			// Known agent: attempt direct install; fatal on failure.
			result, err := setupInstallAgent(agentName)
			if err != nil {
				fatal("setup: " + err.Error())
			}
			fmt.Printf("Installed %s plugin\n", result.Agent)
			fmt.Printf("Destination: %s\n", result.Destination)
			return
		}
		// Unknown agent (including flags like --help): fall through to
		// interactive mode so the user can pick from the supported list.
	}

	// Interactive agent setup
	fmt.Println("Which agent do you want to set up?")
	for i, a := range agents {
		fmt.Printf("  %d. %s\n", i+1, a.Name)
	}
	var input string
	scanInputLine(&input)
	choice, err := strconv.Atoi(input)
	if err != nil || choice < 1 || choice > len(agents) {
		fatal("Invalid choice")
	}
	selected := agents[choice-1]
	fmt.Printf("Installing %s plugin\n", selected.Name)
	result, err := setupInstallAgent(selected.Name)
	if err != nil {
		fatal("setup: " + err.Error())
	}
	fmt.Printf("Installed %s plugin\n", result.Agent)
	fmt.Printf("Destination: %s\n", result.Destination)
}

func realCmdProjects(cfg store.Config) {
	cmdProjectsList(cfg)
}

func realCmdProjectsList(cfg store.Config) {
	s, err := storeNew(cfg)
	if err != nil {
		fatal("store: " + err.Error())
	}
	defer s.Close()

	names, err := s.ListProjectNames()
	if err != nil {
		fatal("list projects: " + err.Error())
	}
	if len(names) == 0 {
		fmt.Println("No projects found")
		return
	}
	fmt.Printf("Projects (%d)\n", len(names))
	for _, name := range names {
		fmt.Printf("  %s\n", name)
	}
}

// findSimilarProjectGroups returns groups of project names that are "similar"
// to each other, using a conservative heuristic: two names are similar if
// one is a substring of the other (case-insensitive) OR they share a common
// prefix of at least 4 characters (case-insensitive).
func findSimilarProjectGroups(names []string) [][]string {
	if len(names) < 2 {
		return nil
	}
	groups := make([][]string, 0)
	used := make([]bool, len(names))
	lower := make([]string, len(names))
	for i, n := range names {
		lower[i] = strings.ToLower(n)
	}
	for i := 0; i < len(names); i++ {
		if used[i] {
			continue
		}
		group := []string{names[i]}
		used[i] = true
		for j := i + 1; j < len(names); j++ {
			if used[j] {
				continue
			}
			li, lj := lower[i], lower[j]
			similar := false
			// Substring: one contains the other
			if strings.Contains(li, lj) || strings.Contains(lj, li) {
				similar = true
			}
			// Shared prefix (at least 4 chars)
			if !similar && len(li) >= 4 && len(lj) >= 4 {
				prefixLen := 4
				for prefixLen <= len(li) && prefixLen <= len(lj) && li[:prefixLen] == lj[:prefixLen] {
					prefixLen++
				}
				if prefixLen > 4 {
					similar = true
				}
			}
			if similar {
				group = append(group, names[j])
				used[j] = true
			}
		}
		if len(group) > 1 {
			groups = append(groups, group)
		}
	}
	return groups
}

func realCmdProjectsConsolidate(cfg store.Config) {
	// Parse flags.
	dryRun := false
	allMode := false
	project := ""
	args := os.Args[2:]
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--dry-run" || arg == "-n" {
			dryRun = true
		} else if arg == "--all" {
			allMode = true
		} else if strings.HasPrefix(arg, "--project=") {
			project = strings.TrimPrefix(arg, "--project=")
		} else if arg == "--project" && i+1 < len(args) {
			project = args[i+1]
			i++
		}
	}

	s, err := storeNew(cfg)
	if err != nil {
		fatal("store: " + err.Error())
	}
	defer s.Close()

	// Detect current project if not specified.
	if project == "" {
		wd, _ := os.Getwd()
		project = detectProject(wd)
	}

	names, err := s.ListProjectNames()
	if err != nil {
		fatal("list projects: " + err.Error())
	}

	groups := findSimilarProjectGroups(names)
	if len(groups) == 0 {
		fmt.Println("No similar projects found")
		return
	}

	// dry-run: show all groups and exit.
	if dryRun {
		fmt.Println("dry-run: would consolidate the following groups:")
		for _, g := range groups {
			fmt.Printf("  Group: %s\n", strings.Join(g, ", "))
		}
		return
	}

	// Process each group.
	for _, group := range groups {
		// Canonical = shortest name in group.
		canonical := group[0]
		for _, n := range group[1:] {
			if len(n) < len(canonical) {
				canonical = n
			}
		}
		sources := []string{}
		for _, n := range group {
			if n != canonical {
				sources = append(sources, n)
			}
		}

		if allMode {
			// Merge all silently.
			_, err := storeConsolidate(s, sources, canonical)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  merge %s → %s: %v\n", strings.Join(sources, ","), canonical, err)
				continue
			}
			fmt.Printf("Merged into %s: %s\n", canonical, strings.Join(sources, ", "))
		} else {
			// Interactive.
			fmt.Printf("Found similar projects: %s\n", strings.Join(group, ", "))
			fmt.Printf("  Canonical: %s\n", canonical)
			fmt.Println("  Merge the others into this one? [y(es)/a(ll)/s(kip)/q(uit)]")
			var input string
			scanInputLine(&input)
			input = strings.TrimSpace(strings.ToLower(input))
			switch input {
			case "q", "quit":
				return
			case "s", "skip":
				continue
			case "a", "all":
				allMode = true
				// fall through to merge
			case "y", "yes", "":
				// proceed with merge
			default:
				fmt.Println("  Skipping (unrecognized input)")
				continue
			}
			_, err := storeConsolidate(s, sources, canonical)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  merge error: %v\n", err)
				continue
			}
			fmt.Printf("Merged into %s: %s\n", canonical, strings.Join(sources, ", "))
		}
	}
}

func realCmdObsidianExport(cfg store.Config) {
	fatal("obsidian-export: not implemented in this binary")
}

// ─── Main Dispatch ──────────────────────────────────────────────────────────

func main() {
	if len(os.Args) < 2 {
		printUsage()
		exitFunc(1)
	}

	cmd := os.Args[1]

	// Version check.
	if cmd == "version" || cmd == "-v" || cmd == "--version" {
		fmt.Printf("ohara %s\n", version)
		result := checkForUpdates(version)
		if result.Status == versionpkg.StatusCheckFailed {
			fmt.Fprintln(os.Stderr, result.Message)
		} else if result.Status == versionpkg.StatusUpdateAvailable {
			fmt.Fprintln(os.Stderr, result.Message)
		}
		return
	}

	if cmd == "--help" || cmd == "-h" || cmd == "help" {
		printUsage()
		return
	}

	// Store-requiring commands.
	switch cmd {
	case "maintain":
		cfg, err := store.DefaultConfig()
		if err != nil {
			fatal("config: " + err.Error())
		}
		if env := os.Getenv("OHARA_DATA_DIR"); env != "" {
			cfg.DataDir = env
		}
		cmdMaintain(cfg)
		return
	case "backup":
		cfg, err := store.DefaultConfig()
		if err != nil {
			fatal("config: " + err.Error())
		}
		if env := os.Getenv("OHARA_DATA_DIR"); env != "" {
			cfg.DataDir = env
		}
		cmdBackup(cfg)
		return
	case "check":
		cfg, err := store.DefaultConfig()
		if err != nil {
			fatal("config: " + err.Error())
		}
		if env := os.Getenv("OHARA_DATA_DIR"); env != "" {
			cfg.DataDir = env
		}
		cmdCheck(cfg)
		return
	case "serve", "mcp", "tui", "search", "save", "timeline",
		"context", "stats", "export", "import", "sync",
		"setup", "projects", "obsidian-export":
		cfg, err := store.DefaultConfig()
		if err != nil {
			fatal("config: " + err.Error())
		}
		if env := os.Getenv("OHARA_DATA_DIR"); env != "" {
			cfg.DataDir = env
		}
		// Dispatch to the appropriate command variable.
		switch cmd {
		case "serve":
			cmdServe(cfg)
		case "mcp":
			cmdMCP(cfg)
		case "tui":
			cmdTUI(cfg)
		case "search":
			cmdSearch(cfg)
		case "save":
			cmdSave(cfg)
		case "timeline":
			cmdTimeline(cfg)
		case "context":
			cmdContext(cfg)
		case "stats":
			cmdStats(cfg)
		case "export":
			cmdExport(cfg)
		case "import":
			cmdImport(cfg)
		case "sync":
			cmdSync(cfg)
		case "setup":
			cmdSetup(cfg)
		case "projects":
			cmdProjects(cfg)
		case "obsidian-export":
			cmdObsidianExport(cfg)
		}
		return
	default:
		fmt.Fprintf(os.Stderr, "ohara: unknown command: %s\n\n", cmd)
		printUsage()
		exitFunc(1)
	}
}
