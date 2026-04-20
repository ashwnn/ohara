// Ohara is a local-first persistent memory system for AI coding agents.
// This binary exposes a CLI and a background server for memory operations.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

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
var newHTTPServer = func(s *store.Store, port int, socketPath string, packCfg server.PackConfig, conflictCfg server.ConflictConfig) *server.Server {
	opts := []server.ServerOption{}
	if socketPath != "" {
		opts = append(opts, server.WithSocketPath(socketPath))
	}
	if packCfg.DefaultBudgetTokens > 0 || packCfg.MaxBudgetTokens > 0 {
		opts = append(opts, server.WithPackConfig(packCfg))
	}
	if conflictCfg.Enabled == server.ConflictEnabledOn {
		opts = append(opts, server.WithConflictConfig(conflictCfg))
	}
	return server.New(s, port, opts...)
}

// startHTTP starts the HTTP server. Stubbed in tests.
var startHTTP = func(srv *server.Server) error { return srv.Start() }

// newMCPServer creates an MCP server. Stubbed in tests.
var newMCPServer = func(s *store.Store) *mcpserver.MCPServer { return mcp.NewServer(s) }

// newMCPServerWithTools creates an MCP server with tools. Stubbed in tests.
var newMCPServerWithTools = func(s *store.Store, allowlist map[string]bool) *mcpserver.MCPServer {
	return mcp.NewServerWithTools(s, allowlist)
}

// newMCPServerWithConfig creates an MCP server with config. Stubbed in tests.
var newMCPServerWithConfig = func(s *store.Store, mcpCfg mcp.MCPConfig, allowlist map[string]bool) *mcpserver.MCPServer {
	return mcp.NewServerWithConfig(s, mcpCfg, allowlist)
}

// serveMCP starts the MCP stdio server. Stubbed in tests.
var serveMCP = func(srv *mcpserver.MCPServer, opts ...mcpserver.StdioOption) error {
	stdio := mcpserver.NewStdioServer(srv)
	return stdio.Listen(context.Background(), os.Stdin, os.Stdout)
}

// setupSupportedAgents lists supported agents. Stubbed in tests.
var setupSupportedAgents = setup.SupportedAgents

// setupInstallAgent installs an agent plugin. Stubbed in tests.
var setupInstallAgent = setup.Install

// setupCheckAgent checks agent configuration. Stubbed in tests.
var setupCheckAgent = setup.Check

// setupRemoveAgent removes agent configuration. Stubbed in tests.
var setupRemoveAgent = setup.Remove

// scanInputLine reads a line from stdin. Stubbed in tests.
var scanInputLine = func(a ...interface{}) (int, error) {
	out := make([]any, len(a))
	for i := range a {
		p, ok := a[i].(*string)
		if !ok {
			return 0, fmt.Errorf("scanInputLine: arg %d not *string", i)
		}
		out[i] = p
	}
	return fmt.Scanln(out...)
}

// storeSearchMemories searches memory_items. Stubbed in tests.
var storeSearchMemories = func(s *store.Store, query, projectID, scope, kind, domain, status string, limit int, writtenBy string) ([]store.MemoryItem, error) {
	return s.SearchMemories(query, projectID, scope, kind, domain, status, limit, writtenBy)
}

// storeAddMemory adds a memory item. Stubbed in tests.
var storeAddMemory = func(s *store.Store, p store.AddMemoryParams) (int64, error) {
	return s.AddMemory(p)
}

// storeTimeline returns a timeline. Stubbed in tests.
var storeTimeline = func(s *store.Store, memID int64, count int) (*store.MemoryTimelineResult, error) {
	return s.MemoryTimeline(memID, count)
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

// storeConsolidateCandidates runs consolidation candidate generation. Stubbed in tests.
var storeConsolidateCandidates = func(s *store.Store, project, domain string, dryRun bool) (int, []string, error) {
	return s.GenerateConsolidationCandidates(project, domain, dryRun)
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
var cmdMaintain = realCmdMaintain
var cmdBackup = realCmdBackup
var cmdCheck = realCmdCheck
var cmdServe = realCmdServe
var cmdPrime = realCmdPrime
var cmdValidate = realCmdValidate
var cmdDoctor = realCmdDoctor
var cmdConsolidate = realCmdConsolidate
var cmdTools = realCmdTools

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
	fmt.Println("  tools [profile]    List MCP tool names (agent, admin, all)")
	fmt.Println("  setup [agent]      Set up plugin for an agent")
	fmt.Println("  prime [project]   Build AI-optimised context pack for injection")
	fmt.Println("  validate          Validate database schema and data integrity")
	fmt.Println("  doctor [--fix]   Run health checks with optional auto-fix")
	fmt.Println("  consolidate       Generate consolidation candidates from observational memories")
	fmt.Println()
	fmt.Println("Flags:")
	fmt.Println("  --help, -h         Show this help")
	fmt.Println()
	fmt.Println("Environment:")
	fmt.Println("  OHARA_DATA_DIR     Data directory (default ~/.local/share/ohara)")
	fmt.Println("  OHARA_HTTP_ADDR    HTTP server address (default 127.0.0.1:7331)")
	fmt.Println("  OHARA_SOCKET      Unix socket path (takes priority over HTTP_ADDR)")
	fmt.Println("  OHARA_SYNC_DIR    Sync chunk directory (default .ohara/ in cwd)")
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
		fmt.Printf("integrity: ok (schema: v%d)\n", s.SchemaVersion())
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

	// Apply config-file retrieval settings to the store config (env vars
	// already handled by store.DefaultConfig, config.json values are not).
	if cfg2.RetrievalMode != "" {
		cfg.RetrievalMode = cfg2.RetrievalMode
	}
	if cfg2.EmbeddingBackend != "" {
		cfg.EmbeddingBackend = cfg2.EmbeddingBackend
	}
	if cfg2.EmbeddingModel != "" {
		cfg.EmbeddingModel = cfg2.EmbeddingModel
	}
	if cfg2.EmbeddingDim > 0 {
		cfg.EmbeddingDim = cfg2.EmbeddingDim
	}
	if cfg2.HybridAlpha > 0 {
		cfg.HybridAlpha = cfg2.HybridAlpha
	}
	if cfg2.OllamaURL != "" {
		cfg.OllamaURL = cfg2.OllamaURL
	}

	s, err := storeNew(cfg)
	if err != nil {
		fatal("store: " + err.Error())
	}
	defer s.Close()

	packCfg := server.PackConfig{
		DefaultBudgetTokens: cfg2.DefaultBudgetTokens,
		MaxBudgetTokens:     cfg2.MaxBudgetTokens,
	}
	conflictCfg := server.ConflictConfig{
		Enabled:   server.ConflictEnabledOff,
		Threshold: cfg2.ConflictThreshold,
	}
	if cfg2.ConflictEnabled {
		conflictCfg.Enabled = server.ConflictEnabledOn
	}
	srv := newHTTPServer(s, port, socketPath, packCfg, conflictCfg)
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
			allowlist = mcp.ResolveTools(tools)
			break
		}
		if arg == "--tools" && i+1 < len(os.Args[2:]) {
			tools := os.Args[2:][i+1]
			allowlist = mcp.ResolveTools(tools)
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

func realCmdTools(_ store.Config) {
	profile := "all"
	if len(os.Args) >= 3 && strings.TrimSpace(os.Args[2]) != "" {
		profile = strings.TrimSpace(os.Args[2])
	}

	sortedKeys := func(m map[string]bool) []string {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return keys
	}

	printList := func(title string, names []string) {
		fmt.Printf("%s (%d)\n", title, len(names))
		for _, name := range names {
			fmt.Printf("  - %s\n", name)
		}
		fmt.Println()
	}

	agentTools := sortedKeys(mcp.ProfileAgent)
	adminTools := sortedKeys(mcp.ProfileAdmin)

	switch profile {
	case "agent":
		printList("MCP agent tools", agentTools)
	case "admin":
		printList("MCP admin tools", adminTools)
	case "all":
		union := make(map[string]bool)
		for _, name := range agentTools {
			union[name] = true
		}
		for _, name := range adminTools {
			union[name] = true
		}
		printList("MCP tools", sortedKeys(union))
		printList("MCP agent tools", agentTools)
		printList("MCP admin tools", adminTools)
	default:
		fatal("usage: ohara tools [agent|admin|all]")
	}

	fmt.Println("Use with: ohara mcp --tools=<profile-or-comma-list>")
	fmt.Println("Examples: ohara mcp --tools=agent | ohara mcp --tools=mem_search,mem_save")
}

func realCmdSearch(cfg store.Config) {
	var project, kind, scope, domain, status, writtenBy string
	var limit int
	args := os.Args[2:]
	positional := []string{}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--project=") {
			project = strings.TrimPrefix(arg, "--project=")
		} else if arg == "--project" && i+1 < len(args) {
			project = args[i+1]
			i++
		} else if strings.HasPrefix(arg, "--kind=") {
			kind = strings.TrimPrefix(arg, "--kind=")
		} else if arg == "--kind" && i+1 < len(args) {
			kind = args[i+1]
			i++
		} else if strings.HasPrefix(arg, "--type=") {
			kind = strings.TrimPrefix(arg, "--type=")
		} else if arg == "--type" && i+1 < len(args) {
			kind = args[i+1]
			i++
		} else if strings.HasPrefix(arg, "--scope=") {
			scope = strings.TrimPrefix(arg, "--scope=")
		} else if arg == "--scope" && i+1 < len(args) {
			scope = args[i+1]
			i++
		} else if strings.HasPrefix(arg, "--domain=") {
			domain = strings.TrimPrefix(arg, "--domain=")
		} else if arg == "--domain" && i+1 < len(args) {
			domain = args[i+1]
			i++
		} else if strings.HasPrefix(arg, "--status=") {
			status = strings.TrimPrefix(arg, "--status=")
		} else if arg == "--status" && i+1 < len(args) {
			status = args[i+1]
			i++
		} else if strings.HasPrefix(arg, "--limit=") {
			if v, err := strconv.Atoi(strings.TrimPrefix(arg, "--limit=")); err == nil {
				limit = v
			}
		} else if arg == "--limit" && i+1 < len(args) {
			if v, err := strconv.Atoi(args[i+1]); err == nil {
				limit = v
			}
			i++
		} else if strings.HasPrefix(arg, "--") {
		} else {
			positional = append(positional, arg)
		}
	}

	query := ""
	if len(positional) > 0 {
		query = positional[0]
	}

	if query == "" {
		fatal("usage: ohara search <query> [--project=...] [--kind=...] [--domain=...] [--scope=...] [--limit=N]")
	}

	if project == "" {
		project = os.Getenv("OHARA_PROJECT")
	}

	s, err := storeNew(cfg)
	if err != nil {
		fatal("store: " + err.Error())
	}
	defer s.Close()

	results, err := storeSearchMemories(s, query, project, scope, kind, domain, status, limit, writtenBy)
	if err != nil {
		fatal("search: " + err.Error())
	}

	if len(results) == 0 {
		fmt.Println("No memories found")
		return
	}

	fmt.Printf("Found %d memories\n", len(results))
	for _, r := range results {
		fmt.Printf("\n[%s] %s (%s | %s | %s)\n%s\n", r.Kind, r.Title, r.Domain, r.Scope, r.Classification, r.Body)
	}
}

func realCmdSave(cfg store.Config) {
	params := store.AddMemoryParams{}
	args := os.Args[2:]
	positional := []string{}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--type=") || strings.HasPrefix(arg, "--kind=") {
			params.Kind = strings.TrimPrefix(arg, "--")
			params.Kind = strings.TrimPrefix(params.Kind, "type=")
			params.Kind = strings.TrimPrefix(params.Kind, "kind=")
		} else if (arg == "--type" || arg == "--kind") && i+1 < len(args) {
			params.Kind = args[i+1]
			i++
		} else if strings.HasPrefix(arg, "--project=") {
			params.ProjectID = strings.TrimPrefix(arg, "--project=")
		} else if arg == "--project" && i+1 < len(args) {
			params.ProjectID = args[i+1]
			i++
		} else if strings.HasPrefix(arg, "--scope=") {
			params.Scope = strings.TrimPrefix(arg, "--scope=")
		} else if arg == "--scope" && i+1 < len(args) {
			params.Scope = args[i+1]
			i++
		} else if strings.HasPrefix(arg, "--domain=") {
			params.Domain = strings.TrimPrefix(arg, "--domain=")
		} else if arg == "--domain" && i+1 < len(args) {
			params.Domain = args[i+1]
			i++
		} else if strings.HasPrefix(arg, "--session=") {
			params.SessionID = strings.TrimPrefix(arg, "--session=")
		} else if arg == "--session" && i+1 < len(args) {
			params.SessionID = args[i+1]
			i++
		} else if strings.HasPrefix(arg, "--classification=") {
			params.Classification = strings.TrimPrefix(arg, "--classification=")
		} else if arg == "--classification" && i+1 < len(args) {
			params.Classification = args[i+1]
			i++
		} else if strings.HasPrefix(arg, "--evidence=") {
			params.EvidenceJSON = strings.TrimPrefix(arg, "--evidence=")
		} else if arg == "--evidence" && i+1 < len(args) {
			params.EvidenceJSON = args[i+1]
			i++
		} else if strings.HasPrefix(arg, "--trigger=") {
			params.TriggerCondition = strings.TrimPrefix(arg, "--trigger=")
		} else if arg == "--trigger" && i+1 < len(args) {
			params.TriggerCondition = args[i+1]
			i++
		} else if strings.HasPrefix(arg, "--") {
		} else {
			positional = append(positional, arg)
		}
	}

	if len(positional) < 1 {
		fatal("usage: ohara save <title> [content] --kind=bugfix --project=myapp --domain=auth")
	}
	params.Title = positional[0]
	if len(positional) >= 2 {
		params.Body = positional[1]
	} else {
		params.Body = params.Title
	}

	if params.Kind == "" {
		params.Kind = store.MemoryKindDiscovery
	}
	if params.ProjectID == "" {
		params.ProjectID = os.Getenv("OHARA_PROJECT")
	}
	if params.ProjectID == "" {
		params.ProjectID = detectProject(".")
	}
	if params.Source == "" {
		params.Source = "cli"
	}
	if params.SessionID == "" {
		params.SessionID = "cli-session"
	}

	s, err := storeNew(cfg)
	if err != nil {
		fatal("store: " + err.Error())
	}
	defer s.Close()

	id, err := storeAddMemory(s, params)
	if err != nil {
		fatal("save: " + err.Error())
	}
	fmt.Printf("Memory saved (#%d): %s\n", id, params.Title)
}

func realCmdTimeline(cfg store.Config) {
	if len(os.Args) < 3 {
		fatal("usage: ohara timeline <id>")
	}

	idStr := os.Args[2]
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		fatal("invalid id: " + err.Error())
	}

	count := 10
	for i := 2; i < len(os.Args); i++ {
		arg := os.Args[i]
		if arg == "--count" && i+1 < len(os.Args) {
			if v, err := strconv.Atoi(os.Args[i+1]); err == nil {
				count = v
			}
			i++
		}
	}

	s, err := storeNew(cfg)
	if err != nil {
		fatal("store: " + err.Error())
	}
	defer s.Close()

	result, err := storeTimeline(s, id, count)
	if err != nil {
		fatal("timeline: " + err.Error())
	}

	fmt.Printf("Memory #%d (%s): %s\n\n", result.Anchor.ID, result.Anchor.Kind, result.Anchor.Title)
	fmt.Printf("%s\n\n", result.Anchor.Body)

	if len(result.Before) > 0 {
		fmt.Println("─── Before ───")
		for _, e := range result.Before {
			fmt.Printf("[%s] %s — %s\n", e.Kind, e.Title, e.Body)
		}
	}

	if len(result.After) > 0 {
		fmt.Println("─── After ───")
		for _, e := range result.After {
			fmt.Printf("[%s] %s — %s\n", e.Kind, e.Title, e.Body)
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
	fmt.Printf("Memories:     %d\n", stats.TotalMemories)
	fmt.Printf("Prompts:       %d\n", stats.TotalPrompts)
	if len(stats.Projects) > 0 {
		fmt.Printf("Projects:     %s\n", strings.Join(stats.Projects, ", "))
	} else {
		fmt.Println("Projects:     none yet")
	}
}

// realCmdPrime builds an AI-optimised context pack for system prompt injection.
func realCmdPrime(cfg store.Config) {
	args := os.Args[2:]
	project := ""
	domain := ""
	budget := 2000

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--project=") {
			project = strings.TrimPrefix(arg, "--project=")
		} else if arg == "--project" && i+1 < len(args) {
			project = args[i+1]
			i++
		} else if strings.HasPrefix(arg, "--domain=") {
			domain = strings.TrimPrefix(arg, "--domain=")
		} else if arg == "--domain" && i+1 < len(args) {
			domain = args[i+1]
			i++
		} else if strings.HasPrefix(arg, "--budget=") {
			if n, err := strconv.Atoi(strings.TrimPrefix(arg, "--budget=")); err == nil {
				budget = n
			}
		} else if !strings.HasPrefix(arg, "--") && project == "" {
			project = arg
		}
	}

	if project == "" {
		project = os.Getenv("OHARA_PROJECT")
	}
	if project == "" {
		fatal("project is required (use --project or OHARA_PROJECT)")
	}

	s, err := storeNew(cfg)
	if err != nil {
		fatal("store: " + err.Error())
	}
	defer s.Close()

	// Query memory items filtered by project
	items, err := s.GetMemories(project, "", "", store.MemoryStatusActive, 100)
	if err != nil {
		fatal("prime: " + err.Error())
	}

	// Filter by domain
	if domain != "" {
		filtered := make([]store.MemoryItem, 0)
		for _, it := range items {
			if it.Domain == domain {
				filtered = append(filtered, it)
			}
		}
		items = filtered
	}

	// Separate knowledge (foundational/tactical) vs episode (observational)
	var knowledge, episode []store.MemoryItem
	for _, it := range items {
		if it.Classification == "observational" {
			episode = append(episode, it)
		} else {
			knowledge = append(knowledge, it)
		}
	}

	sections := []struct {
		title string
		kind  string
		items []store.MemoryItem
	}{
		{"Decisions", store.MemoryKindDecision, nil},
		{"Patterns", store.MemoryKindPattern, nil},
		{"Known Failures", store.MemoryKindBugfix, nil},
		{"Procedures", store.MemoryKindProcedure, nil},
	}
	kindToIdx := map[string]int{
		store.MemoryKindDecision:  0,
		store.MemoryKindPattern:   1,
		store.MemoryKindBugfix:    2,
		store.MemoryKindProcedure: 3,
	}

	for _, it := range knowledge {
		if idx, ok := kindToIdx[it.Kind]; ok {
			sections[idx].items = append(sections[idx].items, it)
		}
	}
	// Episode tier: append to all sections after knowledge
	for _, it := range episode {
		if idx, ok := kindToIdx[it.Kind]; ok {
			sections[idx].items = append(sections[idx].items, it)
		}
	}

	// Estimate token count (rough: 1 token ≈ 4 chars)
	usedTokens := 0
	estimateTokens := func(s string) int { return (len(s) / 4) + 1 }

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Ohara Context: %s", project))
	if domain != "" {
		sb.WriteString(fmt.Sprintf(" [%s]", domain))
	}
	sb.WriteString(fmt.Sprintf("\nGenerated: %s | Budget: %d tokens\n\n",
		time.Now().Format(time.RFC3339), budget))

	for _, sec := range sections {
		if len(sec.items) == 0 {
			continue
		}
		sb.WriteString(fmt.Sprintf("### %s\n", sec.title))
		for _, it := range sec.items {
			tags := ""
			if len(it.Tags) > 0 {
				tags = " [" + strings.Join(it.Tags, ", ") + "]"
			}
			line := fmt.Sprintf("- **%s**%s (%s)\n%s\n\n",
				it.Title, tags, it.Kind, it.Body)
			if usedTokens+estimateTokens(line) > budget {
				break
			}
			sb.WriteString(line)
			usedTokens += estimateTokens(line)
		}
		sb.WriteString("\n")
	}

	fmt.Print(sb.String())
}

// realCmdValidate checks schema and data integrity. Exits non-zero on failures.
func realCmdValidate(cfg store.Config) {
	s, err := storeNew(cfg)
	if err != nil {
		fatal("store: " + err.Error())
	}
	defer s.Close()

	failures := 0

	// Check 1: required fields, valid kind, valid classification
	rows, err := s.Query(`
		SELECT id, kind, classification, trust_level, tags, evidence_json, related_json
		FROM memory_items`)
	if err != nil {
		fatal("validate: " + err.Error())
	}
	defer rows.Close()

	validClassifications := map[string]bool{"foundational": true, "tactical": true, "observational": true}
	validTrustLevels := map[string]bool{"user": true, "system": true, "tool": true, "untrusted": true}
	validKinds := store.ValidMemoryKinds

	for rows.Next() {
		var id int64
		var kind, classification, trustLevel, tagsJSON, evidenceJSON, relatedJSON string
		if err := rows.Scan(&id, &kind, &classification, &trustLevel, &tagsJSON, &evidenceJSON, &relatedJSON); err != nil {
			continue
		}
		if !validKinds[kind] {
			fmt.Printf("[FAIL] id=%d: invalid kind %q\n", id, kind)
			failures++
		}
		if !validClassifications[classification] {
			fmt.Printf("[FAIL] id=%d: invalid classification %q\n", id, classification)
			failures++
		}
		if !validTrustLevels[trustLevel] {
			fmt.Printf("[FAIL] id=%d: invalid trust_level %q\n", id, trustLevel)
			failures++
		}
		// JSON validity checks
		if tagsJSON != "" && tagsJSON != "[]" {
			var tags []string
			if err := json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
				fmt.Printf("[FAIL] id=%d: tags is not valid JSON: %v\n", id, err)
				failures++
			}
		}
		if evidenceJSON != "" && evidenceJSON != "{}" {
			var v any
			if err := json.Unmarshal([]byte(evidenceJSON), &v); err != nil {
				fmt.Printf("[FAIL] id=%d: evidence_json is not valid JSON: %v\n", id, err)
				failures++
			}
		}
		if relatedJSON != "" && relatedJSON != "{}" {
			var v any
			if err := json.Unmarshal([]byte(relatedJSON), &v); err != nil {
				fmt.Printf("[FAIL] id=%d: related_json is not valid JSON: %v\n", id, err)
				failures++
			}
		}
	}

	// Check 2: memory_revisions reference valid memory_items ids
	revRows, err := s.Query(`
		SELECT COUNT(*) FROM memory_revisions mr
		LEFT JOIN memory_items mi ON mi.id = mr.memory_id
		WHERE mi.id IS NULL`)
	if err == nil && revRows.Next() {
		var orphanCount int
		revRows.Scan(&orphanCount)
		if orphanCount > 0 {
			fmt.Printf("[FAIL] %d orphaned memory_revisions rows (no matching memory_items)\n", orphanCount)
			failures++
		}
		revRows.Close()
	}

	if failures == 0 {
		fmt.Println("[PASS] All validation checks passed.")
	} else {
		fmt.Printf("\n%d failure(s). Run 'ohara doctor' for health analysis.\n", failures)
		exitFunc(1)
	}
}

// realCmdDoctor runs health checks with optional auto-fix.
func realCmdDoctor(cfg store.Config) {
	args := os.Args[2:]
	doFix := false
	project := ""
	for _, arg := range args {
		if arg == "--fix" {
			doFix = true
		} else if strings.HasPrefix(arg, "--project=") {
			project = strings.TrimPrefix(arg, "--project=")
		} else if !strings.HasPrefix(arg, "--") {
			project = arg
		}
	}
	if project == "" {
		project = os.Getenv("OHARA_PROJECT")
	}

	s, err := storeNew(cfg)
	if err != nil {
		fatal("store: " + err.Error())
	}
	defer s.Close()

	warnings := 0
	failures := 0

	// Check 1: orphaned revisions
	{
		var count int
		row := s.QueryRow(`
			SELECT COUNT(*) FROM memory_revisions mr
			LEFT JOIN memory_items mi ON mi.id = mr.memory_id
			WHERE mi.id IS NULL`)
		row.Scan(&count)
		if count > 0 {
			fmt.Printf("[WARN] %d orphaned memory_revisions — run with --fix to delete.\n", count)
			warnings++
			if doFix {
				s.Exec(`DELETE FROM memory_revisions WHERE memory_id IN (SELECT mr.id FROM memory_revisions mr LEFT JOIN memory_items mi ON mi.id = mr.memory_id WHERE mi.id IS NULL)`)
				fmt.Printf("[PASS] Orphaned revisions removed.\n")
				warnings--
			}
		} else {
			fmt.Println("[PASS] No orphaned revisions.")
		}
	}

	// Check 2: stuck lifecycle (active but never accessed in 180+ days)
	{
		var count int
		row := s.QueryRow(`
			SELECT COUNT(*) FROM memory_items
			WHERE status = 'active'
			AND access_count = 0
			AND updated_at < datetime('now', '-180 days')`)
		row.Scan(&count)
		if count > 0 {
			fmt.Printf("[WARN] %d memories not accessed in 180+ days — run with --fix to expire.\n", count)
			warnings++
			if doFix {
				s.Exec(`
					UPDATE memory_items
					SET status = 'archived', updated_at = strftime('%Y-%m-%dT%H:%M:%f','now')
					WHERE status = 'active' AND access_count = 0
					AND updated_at < datetime('now', '-180 days')`)
				fmt.Printf("[PASS] Stuck memories expired.\n")
				warnings--
			}
		} else {
			fmt.Println("[PASS] No stuck lifecycle memories.")
		}
	}

	// Check 3: stale procedures/config (not updated in 90+ days)
	{
		var count int
		row := s.QueryRow(`
			SELECT COUNT(*) FROM memory_items
			WHERE kind IN ('procedure', 'config')
			AND status = 'active'
			AND updated_at < datetime('now', '-90 days')`)
		row.Scan(&count)
		if count > 0 {
			fmt.Printf("[WARN] %d stale procedure/config memories not updated in 90+ days.\n", count)
			warnings++
		} else {
			fmt.Println("[PASS] No stale procedure/config memories.")
		}
	}

	// Check 4: duplicate active memory content (same normalized body in same project/domain)
	{
		var dupCount int
		row := s.QueryRow(`
			SELECT COUNT(*)
			FROM memory_items a
			JOIN memory_items b ON a.id < b.id
			WHERE a.status = 'active' AND b.status = 'active'
			  AND a.project_id = b.project_id
			  AND ifnull(a.domain,'') = ifnull(b.domain,'')
			  AND lower(trim(a.body)) = lower(trim(b.body))`)
		row.Scan(&dupCount)
		if dupCount > 0 {
			fmt.Printf("[FAIL] %d duplicate active memory pair(s) detected (same normalized content).\n", dupCount)
			failures++
		} else {
			fmt.Println("[PASS] No duplicate active memory pairs.")
		}
	}

	// Check 5: memories with no domain set (flag only)
	{
		var count int
		projectCond := ""
		if project != "" {
			projectCond = fmt.Sprintf(" AND project_id = '%s'", project)
		}
		row := s.QueryRow(`
			SELECT COUNT(*) FROM memory_items
			WHERE status = 'active' AND domain = ''` + projectCond)
		row.Scan(&count)
		if count > 0 {
			fmt.Printf("[INFO] %d active memories have no domain set (consider adding one).\n", count)
		}
	}

	fmt.Println()
	fmt.Printf("[INFO] schema: v%d\n", s.SchemaVersion())

	fmt.Println("Conflict Resolution Guide (mem_resolve_conflict):")
	fmt.Println("  add         — Both memories are correct, describing different things")
	fmt.Println("  merge       — Partial overlap; should be one canonical memory")
	fmt.Println("  invalidate  — Old memory is actively wrong; new one replaces it")
	fmt.Println("  relate      — Memories are complementary, not contradictory")
	fmt.Println("  suppress    — Known acceptable coexistence of contradictory facts")
	fmt.Println()

	if failures > 0 {
		fmt.Printf("%d failure(s), %d warning(s).\n", failures, warnings)
		fmt.Println("Run 'ohara validate' for schema correctness checks.")
		exitFunc(1)
	} else if warnings > 0 {
		fmt.Printf("0 failures, %d warning(s). Run --fix to auto-remediate.\n", warnings)
	} else {
		fmt.Println("All health checks passed.")
	}
}

// RunConsolidationSweep performs one consolidation sweep and returns results.
// Extracted so it can be called by both the one-shot and daemon command paths.
func RunConsolidationSweep(s *store.Store, project, domain string, dryRun bool) (int, []string, error) {
	return storeConsolidateCandidates(s, project, domain, dryRun)
}

func realCmdConsolidate(cfg store.Config) {
	args := os.Args[2:]
	project := ""
	domain := ""
	dryRun := false
	daemon := false
	intervalMinutes := 60

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--dry-run" || arg == "-n" {
			dryRun = true
		} else if arg == "--daemon" {
			daemon = true
		} else if strings.HasPrefix(arg, "--domain=") {
			domain = strings.TrimPrefix(arg, "--domain=")
		} else if arg == "--domain" && i+1 < len(args) {
			domain = args[i+1]
			i++
		} else if strings.HasPrefix(arg, "--interval=") {
			if v, err := strconv.Atoi(strings.TrimPrefix(arg, "--interval=")); err == nil && v > 0 {
				intervalMinutes = v
			}
		} else if arg == "--interval" && i+1 < len(args) {
			if v, err := strconv.Atoi(args[i+1]); err == nil && v > 0 {
				intervalMinutes = v
			}
			i++
		} else if !strings.HasPrefix(arg, "--") && project == "" {
			project = arg
		}
	}

	s, err := storeNew(cfg)
	if err != nil {
		fatal("store: " + err.Error())
	}
	defer s.Close()

	if daemon {
		fmt.Printf("consolidation daemon: starting (interval=%d min, project=%q, domain=%q)\n",
			intervalMinutes, project, domain)
		// Run one immediate sweep.
		created, summaries, err := RunConsolidationSweep(s, project, domain, false)
		if err != nil {
			fmt.Fprintf(os.Stderr, "consolidation daemon: initial sweep error: %v\n", err)
		} else if created > 0 {
			fmt.Printf("consolidation daemon: created %d candidate(s)\n", created)
			for _, line := range summaries {
				fmt.Printf("  %s\n", line)
			}
		}
		ticker := time.NewTicker(time.Duration(intervalMinutes) * time.Minute)
		defer ticker.Stop()
		for {
			<-ticker.C
			created, summaries, err := RunConsolidationSweep(s, project, domain, false)
			if err != nil {
				fmt.Fprintf(os.Stderr, "consolidation daemon: sweep error: %v\n", err)
			} else if created > 0 {
				fmt.Printf("consolidation daemon: created %d candidate(s)\n", created)
				for _, line := range summaries {
					fmt.Printf("  %s\n", line)
				}
			}
		}
	}

	// One-shot mode.
	created, summaries, err := RunConsolidationSweep(s, project, domain, dryRun)
	if err != nil {
		fatal("consolidate: " + err.Error())
	}
	if dryRun {
		if len(summaries) == 0 {
			fmt.Println("consolidate (dry-run): no candidate groups found")
		} else {
			for _, line := range summaries {
				fmt.Println(line)
			}
		}
		return
	}
	if created == 0 {
		fmt.Println("consolidate: no candidates created")
		return
	}
	fmt.Printf("consolidate: created %d candidate(s)\n", created)
	for _, line := range summaries {
		fmt.Printf("  %s\n", line)
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
		localChunks, manifestChunks, pendingImport, err := syncStatus(sy)
		if err != nil {
			fatal("sync status: " + err.Error())
		}
		fmt.Printf("Sync status: %d local records, %d manifest chunks, %d pending import\n", localChunks, manifestChunks, pendingImport)
		// Show drift when counts diverge (local != manifest means some chunks haven't been pushed, or manifest has unpulled entries).
		if localChunks != manifestChunks {
			diff := localChunks - manifestChunks
			if diff < 0 {
				diff = -diff
			}
			fmt.Printf("drift: %d chunk(s) — run 'ohara sync --all' to push or 'ohara sync --import' to pull\n", diff)
		}
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
		fmt.Printf("Imported %d new chunk(s) (%d sessions, %d prompts)\n",
			result.ChunksImported, result.SessionsImported, result.PromptsImported)
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
		fmt.Printf("Created chunk %s (%d sessions, %d prompts)\n",
			result.ChunkID, result.SessionsExported, result.PromptsExported)
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
		fmt.Printf("Created chunk %s (%d sessions, %d prompts)\n",
			result.ChunkID, result.SessionsExported, result.PromptsExported)
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

	if len(os.Args) >= 3 {
		arg := os.Args[2]

		// Check for --check or --remove flag
		if arg == "--check" {
			if len(os.Args) < 4 {
				fatal("usage: ohara setup --check <agent>")
			}
			agentName := os.Args[3]
			if !agentSet[agentName] {
				fatal("unknown agent: " + agentName)
			}
			status, err := setupCheckAgent(agentName)
			if err != nil {
				fatal("setup --check: " + err.Error())
			}
			if status.Configured {
				fmt.Printf("%s: configured\n", status.Agent)
				fmt.Printf("  Status: %s\n", status.Status)
				fmt.Printf("  Message: %s\n", status.Message)
			} else {
				fmt.Printf("%s: not configured\n", status.Agent)
				fmt.Printf("  Status: %s\n", status.Status)
				fmt.Printf("  Message: %s\n", status.Message)
			}
			return
		}

		if arg == "--remove" {
			if len(os.Args) < 4 {
				fatal("usage: ohara setup --remove <agent>")
			}
			agentName := os.Args[3]
			if !agentSet[agentName] {
				fatal("unknown agent: " + agentName)
			}
			err := setupRemoveAgent(agentName)
			if err != nil {
				fatal("setup --remove: " + err.Error())
			}
			fmt.Printf("Removed %s configuration\n", agentName)
			return
		}

		// Direct agent setup: ohara setup <agent>
		if agentSet[arg] {
			result, err := setupInstallAgent(arg)
			if err != nil {
				fatal("setup: " + err.Error())
			}
			fmt.Printf("Installed %s plugin\n", result.Agent)
			fmt.Printf("Destination: %s\n", result.Destination)
			return
		}
		// Unknown arg (including flags like --help): fall through to
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
		"setup", "projects",
		"prime", "validate", "doctor", "consolidate", "tools":
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
		case "prime":
			cmdPrime(cfg)
		case "validate":
			cmdValidate(cfg)
		case "doctor":
			cmdDoctor(cfg)
		case "consolidate":
			cmdConsolidate(cfg)
		case "tools":
			cmdTools(cfg)
		}
		return
	default:
		fmt.Fprintf(os.Stderr, "ohara: unknown command: %s\n\n", cmd)
		printUsage()
		exitFunc(1)
	}
}
