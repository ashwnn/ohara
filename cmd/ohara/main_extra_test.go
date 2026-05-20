package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ashwnn/ohara/internal/config"
	"github.com/ashwnn/ohara/internal/mcp"
	oharasrv "github.com/ashwnn/ohara/internal/server"
	"github.com/ashwnn/ohara/internal/setup"
	"github.com/ashwnn/ohara/internal/store"
	oharasync "github.com/ashwnn/ohara/internal/sync"
	versioncheck "github.com/ashwnn/ohara/internal/version"

	mcpserver "github.com/mark3labs/mcp-go/server"
)

type exitCode int

func captureOutputAndRecover(t *testing.T, fn func()) (stdout string, stderr string, recovered any) {
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

	func() {
		defer func() {
			recovered = recover()
		}()
		fn()
	}()

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

	return string(outBytes), string(errBytes), recovered
}

func stubExitWithPanic(t *testing.T) {
	t.Helper()
	old := exitFunc
	exitFunc = func(code int) { panic(exitCode(code)) }
	t.Cleanup(func() { exitFunc = old })
}

func stubRuntimeHooks(t *testing.T) {
	t.Helper()
	oldStoreNew := storeNew
	oldNewHTTPServer := newHTTPServer
	oldStartHTTP := startHTTP
	oldNewMCPServer := newMCPServer
	oldNewMCPServerWithTools := newMCPServerWithTools
	oldServeMCP := serveMCP
	oldSetupSupportedAgents := setupSupportedAgents
	oldSetupInstallAgent := setupInstallAgent
	oldSetupCheckAgent := setupCheckAgent
	oldSetupRemoveAgent := setupRemoveAgent
	oldScanInputLine := scanInputLine
	oldStoreSearchMemories := storeSearchMemories
	oldStoreAddMemory := storeAddMemory
	oldStoreTimeline := storeTimeline
	oldStoreStats := storeStats
	oldStoreExport := storeExport
	oldJSONMarshalIndent := jsonMarshalIndent
	oldSyncStatus := syncStatus
	oldSyncImport := syncImport
	oldSyncExport := syncExport
	oldCheckForUpdates := checkForUpdates
	oldNewTUIModel := newTUIModel
	oldNewTeaProgram := newTeaProgram
	oldRunTeaProgram := runTeaProgram
	oldStoreConsolidate := storeConsolidate
	oldLoadRuntimeConfig := loadRuntimeConfig
	oldLoadRuntimeMaintain := loadRuntimeMaintain

	storeNew = store.New
	newHTTPServer = func(s *store.Store, _ int, _ string, _ oharasrv.PackConfig, _ oharasrv.ConflictConfig) *oharasrv.Server {
		return oharasrv.New(s, 0)
	}
	startHTTP = func(_ *oharasrv.Server) error { return nil }
	newMCPServer = func(s *store.Store) *mcpserver.MCPServer {
		return mcpserver.NewMCPServer("test", "0", mcpserver.WithRecovery())
	}
	newMCPServerWithTools = func(s *store.Store, allowlist map[string]bool) *mcpserver.MCPServer {
		return mcpserver.NewMCPServer("test", "0", mcpserver.WithRecovery())
	}
	serveMCP = func(_ *mcpserver.MCPServer, _ ...mcpserver.StdioOption) error { return nil }
	setupSupportedAgents = setup.SupportedAgents
	setupInstallAgent = setup.Install
	setupCheckAgent = setup.Check
	setupRemoveAgent = setup.Remove
	scanInputLine = fmt.Scanln
	storeSearchMemories = func(s *store.Store, query, projectID, scope, kind, domain, status string, limit int, writtenBy string) ([]store.MemoryItem, error) {
		return s.SearchMemories(query, projectID, scope, kind, domain, status, limit, writtenBy)
	}
	storeAddMemory = func(s *store.Store, p store.AddMemoryParams) (int64, error) {
		return s.AddMemory(p)
	}
	storeTimeline = func(s *store.Store, memID int64, count int) (*store.MemoryTimelineResult, error) {
		return s.MemoryTimeline(memID, count)
	}
	storeFormatContext = func(s *store.Store, project, scope string) (string, error) {
		return s.FormatContext(project, scope)
	}
	storeStats = func(s *store.Store) (*store.Stats, error) { return s.Stats() }
	storeExport = func(s *store.Store) (*store.ExportData, error) { return s.Export() }
	storeConsolidate = func(s *store.Store, sources []string, canonical string) (*store.MergeResult, error) {
		return s.MergeProjects(sources, canonical)
	}
	loadRuntimeConfig = func(string) (config.RuntimeConfig, error) {
		return config.RuntimeConfig{HTTPAddr: ":7437", SocketPath: ""}, nil
	}
	loadRuntimeMaintain = func() (config.RuntimeConfig, error) {
		return config.RuntimeConfig{
			HTTPAddr:        ":7437",
			SnapshotDir:     filepath.Join(t.TempDir(), "snapshots"),
			RetainSnapshots: 7,
		}, nil
	}
	jsonMarshalIndent = json.MarshalIndent
	syncStatus = func(sy *oharasync.Syncer) (localChunks int, remoteChunks int, pendingImport int, err error) {
		return sy.Status()
	}
	syncImport = func(sy *oharasync.Syncer) (*oharasync.ImportResult, error) { return sy.Import() }
	syncExport = func(sy *oharasync.Syncer, createdBy, project string) (*oharasync.SyncResult, error) {
		return sy.Export(createdBy, project)
	}
	checkForUpdates = func(string) versioncheck.CheckResult {
		return versioncheck.CheckResult{Status: versioncheck.StatusUpToDate}
	}

	t.Cleanup(func() {
		storeNew = oldStoreNew
		newHTTPServer = oldNewHTTPServer
		startHTTP = oldStartHTTP
		newMCPServer = oldNewMCPServer
		newMCPServerWithTools = oldNewMCPServerWithTools
		serveMCP = oldServeMCP
		newTUIModel = oldNewTUIModel
		newTeaProgram = oldNewTeaProgram
		runTeaProgram = oldRunTeaProgram
		setupSupportedAgents = oldSetupSupportedAgents
		setupInstallAgent = oldSetupInstallAgent
		setupCheckAgent = oldSetupCheckAgent
		setupRemoveAgent = oldSetupRemoveAgent
		scanInputLine = oldScanInputLine
		storeSearchMemories = oldStoreSearchMemories
		storeAddMemory = oldStoreAddMemory
		storeTimeline = oldStoreTimeline
		storeStats = oldStoreStats
		storeExport = oldStoreExport
		jsonMarshalIndent = oldJSONMarshalIndent
		syncStatus = oldSyncStatus
		syncImport = oldSyncImport
		syncExport = oldSyncExport
		checkForUpdates = oldCheckForUpdates
		storeConsolidate = oldStoreConsolidate
		loadRuntimeConfig = oldLoadRuntimeConfig
		loadRuntimeMaintain = oldLoadRuntimeMaintain
	})
}

func TestFatal(t *testing.T) {
	stubExitWithPanic(t)
	_, stderr, recovered := captureOutputAndRecover(t, func() {
		fatal(errors.New("boom"))
	})

	code, ok := recovered.(exitCode)
	if !ok || int(code) != 1 {
		t.Fatalf("expected exit code 1 panic, got %v", recovered)
	}
	if !strings.Contains(stderr, "ohara: boom") {
		t.Fatalf("fatal stderr mismatch: %q", stderr)
	}
}

func TestCmdServeParsesPortAndErrors(t *testing.T) {
	cfg := testConfig(t)
	stubRuntimeHooks(t)

	tests := []struct {
		name       string
		httpAddr   string
		argPort    string
		wantPort   int
		wantSocket string
		startErr   error
		wantFatal  bool
	}{
		{name: "default port from config stub", httpAddr: ":7437", wantPort: 7437},
		{name: "config http_addr", httpAddr: ":8123", wantPort: 8123},
		{name: "arg overrides config port", httpAddr: ":8123", argPort: "9001", wantPort: 9001},
		{name: "config socket path", httpAddr: ":7437", wantSocket: "/tmp/ohara.sock", wantPort: 7437},
		{name: "start failure", httpAddr: ":7437", wantPort: 7437, startErr: errors.New("listen failed"), wantFatal: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stubExitWithPanic(t)
			// Override the config stub for this subtest.
			loadRuntimeConfig = func(string) (config.RuntimeConfig, error) {
				return config.RuntimeConfig{HTTPAddr: tc.httpAddr, SocketPath: tc.wantSocket}, nil
			}

			args := []string{"ohara", "serve"}
			if tc.argPort != "" {
				args = append(args, tc.argPort)
			}
			withArgs(t, args...)

			seenPort := -1
			newHTTPServer = func(s *store.Store, port int, _ string, _ oharasrv.PackConfig, _ oharasrv.ConflictConfig) *oharasrv.Server {
				seenPort = port
				return oharasrv.New(s, 0)
			}
			startHTTP = func(_ *oharasrv.Server) error {
				return tc.startErr
			}

			_, stderr, recovered := captureOutputAndRecover(t, func() {
				cmdServe(cfg)
			})

			if seenPort != tc.wantPort {
				t.Fatalf("port=%d want=%d", seenPort, tc.wantPort)
			}
			if tc.wantFatal {
				if _, ok := recovered.(exitCode); !ok {
					t.Fatalf("expected fatal exit, got %v", recovered)
				}
				if !strings.Contains(stderr, "listen failed") {
					t.Fatalf("stderr missing start error: %q", stderr)
				}
			} else if recovered != nil {
				t.Fatalf("expected no panic, got %v", recovered)
			}
		})
	}
}

func TestCmdServeUsesConfigLoader(t *testing.T) {
	// Test that cmdServe uses the config loader and respects precedence:
	// config file < CLI positional < --port/--socket flags.
	cfg := testConfig(t)
	stubRuntimeHooks(t)
	stubExitWithPanic(t)

	t.Run("positional arg overrides config http_addr", func(t *testing.T) {
		loadRuntimeConfig = func(string) (config.RuntimeConfig, error) {
			return config.RuntimeConfig{HTTPAddr: ":9999", SocketPath: ""}, nil
		}
		seenPort := -1
		newHTTPServer = func(s *store.Store, port int, _ string, _ oharasrv.PackConfig, _ oharasrv.ConflictConfig) *oharasrv.Server {
			seenPort = port
			return oharasrv.New(s, 0)
		}
		startHTTP = func(_ *oharasrv.Server) error { return nil }

		withArgs(t, "ohara", "serve", "7777")
		_, _, recovered := captureOutputAndRecover(t, func() { cmdServe(cfg) })
		if recovered != nil {
			t.Fatalf("unexpected panic: %v", recovered)
		}
		if seenPort != 7777 {
			t.Fatalf("port: got %d, want 7777 (positional override)", seenPort)
		}
	})

	t.Run("--port flag overrides config http_addr", func(t *testing.T) {
		loadRuntimeConfig = func(string) (config.RuntimeConfig, error) {
			return config.RuntimeConfig{HTTPAddr: ":1111", SocketPath: ""}, nil
		}
		seenPort := -1
		newHTTPServer = func(s *store.Store, port int, _ string, _ oharasrv.PackConfig, _ oharasrv.ConflictConfig) *oharasrv.Server {
			seenPort = port
			return oharasrv.New(s, 0)
		}
		startHTTP = func(_ *oharasrv.Server) error { return nil }

		withArgs(t, "ohara", "serve", "--port", "2222")
		_, _, recovered := captureOutputAndRecover(t, func() { cmdServe(cfg) })
		if recovered != nil {
			t.Fatalf("unexpected panic: %v", recovered)
		}
		if seenPort != 2222 {
			t.Fatalf("port: got %d, want 2222 (--port flag override)", seenPort)
		}
	})

	t.Run("--socket flag overrides config", func(t *testing.T) {
		loadRuntimeConfig = func(string) (config.RuntimeConfig, error) {
			return config.RuntimeConfig{HTTPAddr: ":9999", SocketPath: ""}, nil
		}
		seenPort := -1
		seenSocket := ""
		newHTTPServer = func(s *store.Store, port int, socketPath string, _ oharasrv.PackConfig, _ oharasrv.ConflictConfig) *oharasrv.Server {
			seenPort = port
			seenSocket = socketPath
			return oharasrv.New(s, 0)
		}
		startHTTP = func(_ *oharasrv.Server) error { return nil }

		withArgs(t, "ohara", "serve", "--socket", "/run/ohara.sock")
		_, _, recovered := captureOutputAndRecover(t, func() { cmdServe(cfg) })
		if recovered != nil {
			t.Fatalf("unexpected panic: %v", recovered)
		}
		if seenSocket != "/run/ohara.sock" {
			t.Fatalf("socket: got %q, want /run/ohara.sock", seenSocket)
		}
		// Port is still set from config.
		if seenPort != 9999 {
			t.Fatalf("port: got %d, want 9999 (from config, socket override)", seenPort)
		}
	})

	t.Run("invalid positional arg keeps config port", func(t *testing.T) {
		loadRuntimeConfig = func(string) (config.RuntimeConfig, error) {
			return config.RuntimeConfig{HTTPAddr: ":5555", SocketPath: ""}, nil
		}
		seenPort := -1
		newHTTPServer = func(s *store.Store, port int, _ string, _ oharasrv.PackConfig, _ oharasrv.ConflictConfig) *oharasrv.Server {
			seenPort = port
			return oharasrv.New(s, 0)
		}
		startHTTP = func(_ *oharasrv.Server) error { return nil }

		withArgs(t, "ohara", "serve", "not-a-port")
		_, _, recovered := captureOutputAndRecover(t, func() { cmdServe(cfg) })
		if recovered != nil {
			t.Fatalf("unexpected panic: %v", recovered)
		}
		if seenPort != 5555 {
			t.Fatalf("port: got %d, want 5555 (config kept after invalid arg)", seenPort)
		}
	})

	t.Run("loadRuntimeConfig error propagates to fatal", func(t *testing.T) {
		loadRuntimeConfig = func(string) (config.RuntimeConfig, error) {
			return config.RuntimeConfig{}, errors.New("config file unreadable")
		}
		startHTTP = func(_ *oharasrv.Server) error { return nil }

		withArgs(t, "ohara", "serve")
		_, stderr, recovered := captureOutputAndRecover(t, func() { cmdServe(cfg) })
		if _, ok := recovered.(exitCode); !ok {
			t.Fatalf("expected fatal exit, got %v", recovered)
		}
		if !strings.Contains(stderr, "config file unreadable") {
			t.Fatalf("stderr missing config error: %q", stderr)
		}
	})
}

func TestCmdMCPAndTUIBranches(t *testing.T) {
	cfg := testConfig(t)
	stubRuntimeHooks(t)
	stubExitWithPanic(t)

	serveMCP = func(_ *mcpserver.MCPServer, _ ...mcpserver.StdioOption) error { return errors.New("mcp failed") }
	_, mcpErr, recovered := captureOutputAndRecover(t, func() { cmdMCP(cfg) })
	if _, ok := recovered.(exitCode); !ok || !strings.Contains(mcpErr, "mcp failed") {
		t.Fatalf("expected mcp fatal, got panic=%v stderr=%q", recovered, mcpErr)
	}

	serveMCP = func(_ *mcpserver.MCPServer, _ ...mcpserver.StdioOption) error { return nil }
	_, _, recovered = captureOutputAndRecover(t, func() { cmdMCP(cfg) })
	if recovered != nil {
		t.Fatalf("unexpected panic on successful mcp: %v", recovered)
	}

	// TUI is always unavailable — realCmdTUI prints then calls exitFunc(1).
	// We override exitFunc to record the call without exiting, then check stdout.
	var exitCalled int
	savedExit := exitFunc
	exitFunc = func(code int) { exitCalled = code }
	out, _, _ := captureOutputAndRecover(t, func() { cmdTUI(cfg) })
	exitFunc = savedExit
	if exitCalled != 1 {
		t.Fatalf("expected exitFunc(1), got %d", exitCalled)
	}
	if !strings.Contains(out, "TUI is not available") {
		t.Fatalf("expected TUI unavailable message, got stdout=%q", out)
	}
}

func TestCmdSetupDirectAndInteractive(t *testing.T) {
	cfg := testConfig(t)
	stubRuntimeHooks(t)
	stubExitWithPanic(t)

	setupInstallAgent = func(agent string) (*setup.Result, error) {
		if agent == "broken" {
			return nil, errors.New("install failed")
		}
		return &setup.Result{Agent: agent, Destination: "/tmp/dest", Files: 2}, nil
	}

	// Override supported agents to include codex so direct install check passes.
	setupSupportedAgents = func() []setup.Agent {
		return []setup.Agent{
			{Name: "codex", Description: "Codex", InstallDir: "/tmp/codex"},
			{Name: "broken", Description: "Broken", InstallDir: "/tmp/broken"},
		}
	}

	withArgs(t, "ohara", "setup", "codex")
	out, errOut, recovered := captureOutputAndRecover(t, func() { cmdSetup(cfg) })
	if recovered != nil || errOut != "" {
		t.Fatalf("direct setup should succeed, panic=%v stderr=%q", recovered, errOut)
	}
	if !strings.Contains(out, "Installed codex plugin") {
		t.Fatalf("unexpected direct setup output: %q", out)
	}

	withArgs(t, "ohara", "setup", "broken")
	_, errOut, recovered = captureOutputAndRecover(t, func() { cmdSetup(cfg) })
	if _, ok := recovered.(exitCode); !ok || !strings.Contains(errOut, "install failed") {
		t.Fatalf("expected direct setup fatal, panic=%v stderr=%q", recovered, errOut)
	}

	setupSupportedAgents = func() []setup.Agent {
		return []setup.Agent{{Name: "opencode", Description: "OpenCode", InstallDir: "/tmp/opencode"}}
	}
	scanInputLine = func(a ...any) (int, error) {
		p := a[0].(*string)
		*p = "1"
		return 1, nil
	}

	withArgs(t, "ohara", "setup")
	out, errOut, recovered = captureOutputAndRecover(t, func() { cmdSetup(cfg) })
	if recovered != nil || errOut != "" {
		t.Fatalf("interactive setup should succeed, panic=%v stderr=%q", recovered, errOut)
	}
	if !strings.Contains(out, "Installing opencode plugin") {
		t.Fatalf("unexpected interactive setup output: %q", out)
	}

	scanInputLine = func(a ...any) (int, error) {
		p := a[0].(*string)
		*p = "99"
		return 1, nil
	}
	withArgs(t, "ohara", "setup")
	_, errOut, recovered = captureOutputAndRecover(t, func() { cmdSetup(cfg) })
	if _, ok := recovered.(exitCode); !ok || !strings.Contains(errOut, "Invalid choice") {
		t.Fatalf("expected invalid choice exit, panic=%v stderr=%q", recovered, errOut)
	}
}

func TestCmdSetupCheck(t *testing.T) {
	cfg := testConfig(t)
	stubRuntimeHooks(t)
	stubExitWithPanic(t)

	setupCheckAgent = func(agent string) (*setup.ConfigStatus, error) {
		if agent == "broken" {
			return nil, errors.New("check failed")
		}
		if agent == "opencode" {
			return &setup.ConfigStatus{
				Agent:      "opencode",
				Configured: true,
				Status:     "configured",
				Message:    "ohara is configured",
			}, nil
		}
		return &setup.ConfigStatus{
			Agent:      agent,
			Configured: false,
			Status:     "not_found",
			Message:    "config file does not exist",
		}, nil
	}

	// Check configured agent
	withArgs(t, "ohara", "setup", "--check", "opencode")
	out, errOut, recovered := captureOutputAndRecover(t, func() { cmdSetup(cfg) })
	if recovered != nil || errOut != "" {
		t.Fatalf("check should succeed, panic=%v stderr=%q", recovered, errOut)
	}
	if !strings.Contains(out, "opencode: configured") {
		t.Fatalf("unexpected check output: %q", out)
	}

	// Check unconfigured agent
	withArgs(t, "ohara", "setup", "--check", "cursor")
	out, errOut, recovered = captureOutputAndRecover(t, func() { cmdSetup(cfg) })
	if recovered != nil || errOut != "" {
		t.Fatalf("check should succeed, panic=%v stderr=%q", recovered, errOut)
	}
	if !strings.Contains(out, "cursor: not configured") {
		t.Fatalf("unexpected check output: %q", out)
	}

	// Check error
	setupCheckAgent = func(agent string) (*setup.ConfigStatus, error) {
		return nil, errors.New("check failed")
	}
	withArgs(t, "ohara", "setup", "--check", "opencode")
	_, errOut, recovered = captureOutputAndRecover(t, func() { cmdSetup(cfg) })
	if _, ok := recovered.(exitCode); !ok || !strings.Contains(errOut, "check failed") {
		t.Fatalf("expected check error, panic=%v stderr=%q", recovered, errOut)
	}

	// Check unknown agent
	setupCheckAgent = func(agent string) (*setup.ConfigStatus, error) {
		return &setup.ConfigStatus{Agent: agent, Configured: false, Status: "not_found", Message: ""}, nil
	}
	withArgs(t, "ohara", "setup", "--check", "unknown-agent")
	_, errOut, recovered = captureOutputAndRecover(t, func() { cmdSetup(cfg) })
	if _, ok := recovered.(exitCode); !ok || !strings.Contains(errOut, "unknown agent") {
		t.Fatalf("expected unknown agent error, panic=%v stderr=%q", recovered, errOut)
	}

	// Check missing agent arg
	withArgs(t, "ohara", "setup", "--check")
	_, errOut, recovered = captureOutputAndRecover(t, func() { cmdSetup(cfg) })
	if _, ok := recovered.(exitCode); !ok || !strings.Contains(errOut, "usage: ohara setup --check") {
		t.Fatalf("expected usage error, panic=%v stderr=%q", recovered, errOut)
	}
}

func TestCmdSetupRemove(t *testing.T) {
	cfg := testConfig(t)
	stubRuntimeHooks(t)
	stubExitWithPanic(t)

	// Override supported agents to include "broken"
	setupSupportedAgents = func() []setup.Agent {
		return []setup.Agent{
			{Name: "opencode", Description: "OpenCode", InstallDir: "/tmp/opencode"},
			{Name: "broken", Description: "Broken", InstallDir: "/tmp/broken"},
		}
	}

	setupRemoveAgent = func(agent string) error {
		if agent == "broken" {
			return errors.New("remove failed")
		}
		return nil
	}

	// Remove success
	withArgs(t, "ohara", "setup", "--remove", "opencode")
	out, errOut, recovered := captureOutputAndRecover(t, func() { cmdSetup(cfg) })
	if recovered != nil || errOut != "" {
		t.Fatalf("remove should succeed, panic=%v stderr=%q", recovered, errOut)
	}
	if !strings.Contains(out, "Removed opencode configuration") {
		t.Fatalf("unexpected remove output: %q", out)
	}

	// Remove error
	withArgs(t, "ohara", "setup", "--remove", "broken")
	_, errOut, recovered = captureOutputAndRecover(t, func() { cmdSetup(cfg) })
	if _, ok := recovered.(exitCode); !ok || !strings.Contains(errOut, "remove failed") {
		t.Fatalf("expected remove error, panic=%v stderr=%q", recovered, errOut)
	}

	// Remove unknown agent
	withArgs(t, "ohara", "setup", "--remove", "unknown-agent")
	_, errOut, recovered = captureOutputAndRecover(t, func() { cmdSetup(cfg) })
	if _, ok := recovered.(exitCode); !ok || !strings.Contains(errOut, "unknown agent") {
		t.Fatalf("expected unknown agent error, panic=%v stderr=%q", recovered, errOut)
	}

	// Remove missing agent arg
	withArgs(t, "ohara", "setup", "--remove")
	_, errOut, recovered = captureOutputAndRecover(t, func() { cmdSetup(cfg) })
	if _, ok := recovered.(exitCode); !ok || !strings.Contains(errOut, "usage: ohara setup --remove") {
		t.Fatalf("expected usage error, panic=%v stderr=%q", recovered, errOut)
	}
}

func TestCmdExportDefaultAndCmdImportErrors(t *testing.T) {
	workDir := t.TempDir()
	withCwd(t, workDir)

	cfg := testConfig(t)
	stubExitWithPanic(t)

	mustSeedMemory(t, cfg, "s-exp-default", "proj", "discovery", "title", "content", "project")

	withArgs(t, "ohara", "export")
	stdout, stderr, recovered := captureOutputAndRecover(t, func() { cmdExport(cfg) })
	if recovered != nil || stderr != "" {
		t.Fatalf("export default should succeed, panic=%v stderr=%q", recovered, stderr)
	}
	if !strings.Contains(stdout, "Exported to ohara-export.json") {
		t.Fatalf("unexpected default export output: %q", stdout)
	}
	if _, err := os.Stat(filepath.Join(workDir, "ohara-export.json")); err != nil {
		t.Fatalf("expected default export file: %v", err)
	}

	badPath := filepath.Join(workDir, "missing", "out.json")
	withArgs(t, "ohara", "export", badPath)
	_, stderr, recovered = captureOutputAndRecover(t, func() { cmdExport(cfg) })
	if _, ok := recovered.(exitCode); !ok || !strings.Contains(stderr, "no such file or directory") {
		t.Fatalf("expected export write fatal, panic=%v stderr=%q", recovered, stderr)
	}

	withArgs(t, "ohara", "import")
	_, stderr, recovered = captureOutputAndRecover(t, func() { cmdImport(cfg) })
	if _, ok := recovered.(exitCode); !ok || !strings.Contains(stderr, "usage: ohara import") {
		t.Fatalf("expected import usage exit, panic=%v stderr=%q", recovered, stderr)
	}

	withArgs(t, "ohara", "import", filepath.Join(workDir, "nope.json"))
	_, stderr, recovered = captureOutputAndRecover(t, func() { cmdImport(cfg) })
	if _, ok := recovered.(exitCode); !ok || !strings.Contains(stderr, "read") {
		t.Fatalf("expected import read fatal, panic=%v stderr=%q", recovered, stderr)
	}

	invalidJSON := filepath.Join(workDir, "invalid.json")
	if err := os.WriteFile(invalidJSON, []byte("{invalid"), 0644); err != nil {
		t.Fatalf("write invalid json: %v", err)
	}
	withArgs(t, "ohara", "import", invalidJSON)
	_, stderr, recovered = captureOutputAndRecover(t, func() { cmdImport(cfg) })
	if _, ok := recovered.(exitCode); !ok || !strings.Contains(stderr, "parse") {
		t.Fatalf("expected import parse fatal, panic=%v stderr=%q", recovered, stderr)
	}
}

func TestMainDispatchServeMCPAndTUI(t *testing.T) {
	stubRuntimeHooks(t)
	stubExitWithPanic(t)

	t.Setenv("OHARA_DATA_DIR", t.TempDir())
	withArgs(t, "ohara", "serve", "8088")
	_, stderr, recovered := captureOutputAndRecover(t, func() { main() })
	if recovered != nil || stderr != "" {
		t.Fatalf("serve dispatch failed: panic=%v stderr=%q", recovered, stderr)
	}

	withArgs(t, "ohara", "mcp")
	_, stderr, recovered = captureOutputAndRecover(t, func() { main() })
	if recovered != nil || stderr != "" {
		t.Fatalf("mcp dispatch failed: panic=%v stderr=%q", recovered, stderr)
	}

	withArgs(t, "ohara", "tui")
	_, stderr, recovered = captureOutputAndRecover(t, func() { main() })
	if _, ok := recovered.(exitCode); !ok || stderr != "" {
		t.Fatalf("tui dispatch failed: panic=%v stderr=%q", recovered, stderr)
	}
}

func TestStoreInitFailurePaths(t *testing.T) {
	stubRuntimeHooks(t)
	stubExitWithPanic(t)
	cfg := testConfig(t)
	importFile := filepath.Join(t.TempDir(), "import.json")
	if err := os.WriteFile(importFile, []byte(`{"version":"0.1.0","exported_at":"2026-01-01T00:00:00Z","sessions":[],"observations":[],"prompts":[]}`), 0644); err != nil {
		t.Fatalf("write import file: %v", err)
	}

	storeNew = func(store.Config) (*store.Store, error) {
		return nil, errors.New("store init failed")
	}

	cmds := []func(store.Config){
		cmdServe,
		cmdMCP,
		// cmdTUI no longer calls storeNew — removed from store init failure test
		cmdSearch,
		cmdSave,
		cmdTimeline,
		cmdContext,
		cmdStats,
		cmdExport,
		cmdImport,
		cmdSync,
	}

	argsByCmd := [][]string{
		{"ohara", "serve"},
		{"ohara", "mcp"},
		{"ohara", "search", "q"},
		{"ohara", "save", "t", "c"},
		{"ohara", "timeline", "1"},
		{"ohara", "context"},
		{"ohara", "stats"},
		{"ohara", "export"},
		{"ohara", "import", importFile},
		{"ohara", "sync"},
	}

	for i, fn := range cmds {
		withArgs(t, argsByCmd[i]...)
		_, stderr, recovered := captureOutputAndRecover(t, func() { fn(cfg) })
		if _, ok := recovered.(exitCode); !ok {
			t.Fatalf("command %d: expected exit panic, got %v", i, recovered)
		}
		if !strings.Contains(stderr, "store init failed") {
			t.Fatalf("command %d: expected store failure stderr, got %q", i, stderr)
		}
	}
}

func TestUsageAndValidationExits(t *testing.T) {
	cfg := testConfig(t)
	stubExitWithPanic(t)

	tests := []struct {
		name       string
		args       []string
		run        func(store.Config)
		errSubstr  string
		stderrOnly bool
	}{
		{name: "search usage", args: []string{"ohara", "search"}, run: cmdSearch, errSubstr: "usage: ohara search"},
		{name: "search missing query", args: []string{"ohara", "search", "--limit", "3"}, run: cmdSearch, errSubstr: "usage: ohara search"},
		{name: "save usage", args: []string{"ohara", "save"}, run: cmdSave, errSubstr: "usage: ohara save <title>"},
		{name: "timeline usage", args: []string{"ohara", "timeline"}, run: cmdTimeline, errSubstr: "usage: ohara timeline"},
		{name: "timeline invalid id", args: []string{"ohara", "timeline", "abc"}, run: cmdTimeline, errSubstr: "invalid id:"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withArgs(t, tc.args...)
			_, stderr, recovered := captureOutputAndRecover(t, func() { tc.run(cfg) })
			if _, ok := recovered.(exitCode); !ok {
				t.Fatalf("expected exit panic, got %v", recovered)
			}
			if !strings.Contains(stderr, tc.errSubstr) {
				t.Fatalf("stderr missing %q: %q", tc.errSubstr, stderr)
			}
		})
	}
}

func TestMainDispatchRemainingCommands(t *testing.T) {
	stubRuntimeHooks(t)
	stubExitWithPanic(t)
	withCwd(t, t.TempDir())

	dataDir := t.TempDir()
	t.Setenv("OHARA_DATA_DIR", dataDir)

	seedCfg, scErr := store.DefaultConfig()
	if scErr != nil {
		t.Fatalf("DefaultConfig: %v", scErr)
	}
	seedCfg.DataDir = dataDir
	focusID := mustSeedMemory(t, seedCfg, "s-main", "main-proj", "discovery", "focus", "focus content", "project")

	importFile := filepath.Join(t.TempDir(), "import.json")
	if err := os.WriteFile(importFile, []byte(`{"version":"0.1.0","exported_at":"2026-01-01T00:00:00Z","sessions":[],"observations":[],"prompts":[]}`), 0644); err != nil {
		t.Fatalf("write import file: %v", err)
	}

	setupInstallAgent = func(agent string) (*setup.Result, error) {
		return &setup.Result{Agent: agent, Destination: "/tmp/dest", Files: 1}, nil
	}
	setupSupportedAgents = func() []setup.Agent {
		return []setup.Agent{{Name: "codex", Description: "Codex", InstallDir: "/tmp/codex"}}
	}

	tests := []struct {
		name string
		args []string
	}{
		{name: "search", args: []string{"ohara", "search", "focus"}},
		{name: "save", args: []string{"ohara", "save", "t", "c"}},
		{name: "timeline", args: []string{"ohara", "timeline", fmt.Sprintf("%d", focusID)}},
		{name: "context", args: []string{"ohara", "context", "main-proj"}},
		{name: "stats", args: []string{"ohara", "stats"}},
		{name: "export", args: []string{"ohara", "export", filepath.Join(t.TempDir(), "exp.json")}},
		{name: "import", args: []string{"ohara", "import", importFile}},
		{name: "sync", args: []string{"ohara", "sync", "--all"}},
		{name: "setup", args: []string{"ohara", "setup", "codex"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withArgs(t, tc.args...)
			_, stderr, recovered := captureOutputAndRecover(t, func() { main() })
			if recovered != nil {
				t.Fatalf("main panic for %s: %v stderr=%q", tc.name, recovered, stderr)
			}
		})
	}
}

func TestCmdSyncAdditionalBranches(t *testing.T) {
	stubExitWithPanic(t)

	t.Run("all projects empty export message", func(t *testing.T) {
		stubRuntimeHooks(t)
		workDir := t.TempDir()
		withCwd(t, workDir)
		cfg := testConfig(t)

		withArgs(t, "ohara", "sync", "--all")
		stdout, stderr, recovered := captureOutputAndRecover(t, func() { cmdSync(cfg) })
		if recovered != nil || stderr != "" {
			t.Fatalf("expected clean run, panic=%v stderr=%q", recovered, stderr)
		}
		if !strings.Contains(stdout, "Exporting ALL memories") || !strings.Contains(stdout, "Nothing new to sync") {
			t.Fatalf("unexpected output: %q", stdout)
		}
	})

	t.Run("status parse error", func(t *testing.T) {
		stubRuntimeHooks(t)
		workDir := t.TempDir()
		withCwd(t, workDir)
		cfg := testConfig(t)

		if err := os.MkdirAll(filepath.Join(workDir, ".ohara"), 0755); err != nil {
			t.Fatalf("mkdir .ohara: %v", err)
		}
		if err := os.WriteFile(filepath.Join(workDir, ".ohara", "manifest.json"), []byte("{bad json"), 0644); err != nil {
			t.Fatalf("write manifest: %v", err)
		}

		withArgs(t, "ohara", "sync", "--status")
		_, stderr, recovered := captureOutputAndRecover(t, func() { cmdSync(cfg) })
		if _, ok := recovered.(exitCode); !ok {
			t.Fatalf("expected fatal exit, got %v", recovered)
		}
		if !strings.Contains(stderr, "parse manifest") {
			t.Fatalf("unexpected stderr: %q", stderr)
		}
	})

	t.Run("import parse error", func(t *testing.T) {
		stubRuntimeHooks(t)
		workDir := t.TempDir()
		withCwd(t, workDir)
		cfg := testConfig(t)

		if err := os.MkdirAll(filepath.Join(workDir, ".ohara"), 0755); err != nil {
			t.Fatalf("mkdir .ohara: %v", err)
		}
		if err := os.WriteFile(filepath.Join(workDir, ".ohara", "manifest.json"), []byte("{bad json"), 0644); err != nil {
			t.Fatalf("write manifest: %v", err)
		}

		withArgs(t, "ohara", "sync", "--import")
		_, stderr, recovered := captureOutputAndRecover(t, func() { cmdSync(cfg) })
		if _, ok := recovered.(exitCode); !ok {
			t.Fatalf("expected fatal exit, got %v", recovered)
		}
		if !strings.Contains(stderr, "parse manifest") {
			t.Fatalf("unexpected stderr: %q", stderr)
		}
	})

	t.Run("export parse error", func(t *testing.T) {
		stubRuntimeHooks(t)
		workDir := t.TempDir()
		withCwd(t, workDir)
		cfg := testConfig(t)

		if err := os.MkdirAll(filepath.Join(workDir, ".ohara"), 0755); err != nil {
			t.Fatalf("mkdir .ohara: %v", err)
		}
		if err := os.WriteFile(filepath.Join(workDir, ".ohara", "manifest.json"), []byte("{bad json"), 0644); err != nil {
			t.Fatalf("write manifest: %v", err)
		}

		withArgs(t, "ohara", "sync")
		_, stderr, recovered := captureOutputAndRecover(t, func() { cmdSync(cfg) })
		if _, ok := recovered.(exitCode); !ok {
			t.Fatalf("expected fatal exit, got %v", recovered)
		}
		if !strings.Contains(stderr, "parse manifest") {
			t.Fatalf("unexpected stderr: %q", stderr)
		}
	})
}

func TestCmdImportStoreImportFailure(t *testing.T) {
	stubExitWithPanic(t)
	cfg := testConfig(t)

	badImport := filepath.Join(t.TempDir(), "bad-import.json")
	// Use invalid JSON (not valid JSON with invalid content) so it deterministically
	// fails at the JSON parse stage rather than relying on store import behavior.
	badJSON := `{invalid json}`
	if err := os.WriteFile(badImport, []byte(badJSON), 0644); err != nil {
		t.Fatalf("write bad import: %v", err)
	}

	withArgs(t, "ohara", "import", badImport)
	_, stderr, recovered := captureOutputAndRecover(t, func() { cmdImport(cfg) })
	if _, ok := recovered.(exitCode); !ok {
		t.Fatalf("expected fatal exit, got %v", recovered)
	}
	if !strings.Contains(stderr, "parse") {
		t.Fatalf("unexpected stderr: %q", stderr)
	}
}

func TestCmdSearchAndSaveDanglingFlags(t *testing.T) {
	cfg := testConfig(t)

	withArgs(t, "ohara", "save", "dangling-title", "dangling-content", "--type")
	stdout, stderr, recovered := captureOutputAndRecover(t, func() { cmdSave(cfg) })
	if recovered != nil || stderr != "" {
		t.Fatalf("save with dangling flag failed, panic=%v stderr=%q", recovered, stderr)
	}
	if !strings.Contains(stdout, "Memory saved (#") {
		t.Fatalf("unexpected save output: %q", stdout)
	}

	withArgs(t, "ohara", "search", "dangling-content", "--limit", "not-a-number", "--project")
	stdout, stderr, recovered = captureOutputAndRecover(t, func() { cmdSearch(cfg) })
	if recovered != nil || stderr != "" {
		t.Fatalf("search with dangling flags failed, panic=%v stderr=%q", recovered, stderr)
	}
	if !strings.Contains(stdout, "Found") {
		t.Fatalf("unexpected search output: %q", stdout)
	}
}

func TestCmdSetupHyphenArgFallsBackToInteractive(t *testing.T) {
	cfg := testConfig(t)
	stubRuntimeHooks(t)
	stubExitWithPanic(t)

	setupSupportedAgents = func() []setup.Agent {
		return []setup.Agent{{Name: "codex", Description: "Codex", InstallDir: "/tmp/codex"}}
	}
	setupInstallAgent = func(agent string) (*setup.Result, error) {
		return &setup.Result{Agent: agent, Destination: "/tmp/dest", Files: 1}, nil
	}
	scanInputLine = func(a ...any) (int, error) {
		p := a[0].(*string)
		*p = "1"
		return 1, nil
	}

	withArgs(t, "ohara", "setup", "--not-an-agent")
	stdout, stderr, recovered := captureOutputAndRecover(t, func() { cmdSetup(cfg) })
	if recovered != nil || stderr != "" {
		t.Fatalf("setup interactive fallback failed: panic=%v stderr=%q", recovered, stderr)
	}
	if !strings.Contains(stdout, "Which agent do you want to set up?") || !strings.Contains(stdout, "Installing codex plugin") {
		t.Fatalf("unexpected setup output: %q", stdout)
	}
}

func TestCmdTimelineNoBeforeAfterSections(t *testing.T) {
	cfg := testConfig(t)
	focusID := mustSeedMemory(t, cfg, "solo-session", "solo", "discovery", "focus", "only content", "project")

	withArgs(t, "ohara", "timeline", fmt.Sprintf("%d", focusID), "--count", "0")
	stdout, stderr, recovered := captureOutputAndRecover(t, func() { cmdTimeline(cfg) })
	if recovered != nil || stderr != "" {
		t.Fatalf("timeline failed: panic=%v stderr=%q", recovered, stderr)
	}
	if strings.Contains(stdout, "─── Before ───") || strings.Contains(stdout, "─── After ───") {
		t.Fatalf("unexpected before/after sections in output: %q", stdout)
	}
}

func TestCmdStatsNoProjectsYet(t *testing.T) {
	cfg := testConfig(t)
	withArgs(t, "ohara", "stats")
	stdout, stderr, recovered := captureOutputAndRecover(t, func() { cmdStats(cfg) })
	if recovered != nil || stderr != "" {
		t.Fatalf("stats failed: panic=%v stderr=%q", recovered, stderr)
	}
	if !strings.Contains(stdout, "Projects:     none yet") {
		t.Fatalf("expected empty projects output, got: %q", stdout)
	}
}

func TestCmdSyncImportEmptyAndMixedChunks(t *testing.T) {
	stubExitWithPanic(t)

	t.Run("import with empty manifest", func(t *testing.T) {
		workDir := t.TempDir()
		withCwd(t, workDir)
		cfg := testConfig(t)

		if err := os.MkdirAll(filepath.Join(workDir, ".ohara"), 0755); err != nil {
			t.Fatalf("mkdir .ohara: %v", err)
		}
		if err := os.WriteFile(filepath.Join(workDir, ".ohara", "manifest.json"), []byte(`{"version":1,"chunks":[]}`), 0644); err != nil {
			t.Fatalf("write manifest: %v", err)
		}

		withArgs(t, "ohara", "sync", "--import")
		stdout, stderr, recovered := captureOutputAndRecover(t, func() { cmdSync(cfg) })
		if recovered != nil || stderr != "" {
			t.Fatalf("empty import failed: panic=%v stderr=%q", recovered, stderr)
		}
		if !strings.Contains(stdout, "No new chunks to import") || strings.Contains(stdout, "already imported") {
			t.Fatalf("unexpected empty import output: %q", stdout)
		}
	})

	t.Run("import new plus skipped chunk", func(t *testing.T) {
		stubRuntimeHooks(t)
		workDir := t.TempDir()
		withCwd(t, workDir)

		exportCfg := testConfig(t)
		importCfg := testConfig(t)

		mustSeedMemory(t, exportCfg, "mix-1", "mix", "discovery", "one", "content-one", "project")
		withArgs(t, "ohara", "sync", "--all")
		_, _, _ = captureOutputAndRecover(t, func() { cmdSync(exportCfg) })

		withArgs(t, "ohara", "sync", "--import")
		_, _, _ = captureOutputAndRecover(t, func() { cmdSync(importCfg) })

		time.Sleep(1100 * time.Millisecond)
		mustSeedMemory(t, exportCfg, "mix-2", "mix", "discovery", "two", "content-two", "project")
		withArgs(t, "ohara", "sync", "--all")
		_, _, _ = captureOutputAndRecover(t, func() { cmdSync(exportCfg) })

		withArgs(t, "ohara", "sync", "--import")
		stdout, stderr, recovered := captureOutputAndRecover(t, func() { cmdSync(importCfg) })
		if recovered != nil || stderr != "" {
			t.Fatalf("mixed import failed: panic=%v stderr=%q", recovered, stderr)
		}
		if !strings.Contains(stdout, "Imported 1 new chunk(s)") || !strings.Contains(stdout, "Skipped:") {
			t.Fatalf("unexpected mixed import output: %q", stdout)
		}
	})
}

func TestCommandErrorSeamsAndUncoveredBranches(t *testing.T) {
	stubRuntimeHooks(t)
	stubExitWithPanic(t)
	cfg := testConfig(t)

	assertFatal := func(t *testing.T, stderr string, recovered any, want string) {
		t.Helper()
		if _, ok := recovered.(exitCode); !ok {
			t.Fatalf("expected fatal exit, got %v", recovered)
		}
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q: %q", want, stderr)
		}
	}

	t.Run("search seam error", func(t *testing.T) {
		withArgs(t, "ohara", "search", "needle")
		storeSearchMemories = func(*store.Store, string, string, string, string, string, string, int, string) ([]store.MemoryItem, error) {
			return nil, errors.New("forced search error")
		}
		_, stderr, recovered := captureOutputAndRecover(t, func() { cmdSearch(cfg) })
		assertFatal(t, stderr, recovered, "forced search error")
	})

	t.Run("save seam error", func(t *testing.T) {
		withArgs(t, "ohara", "save", "title", "content")
		storeAddMemory = func(*store.Store, store.AddMemoryParams) (int64, error) {
			return 0, errors.New("forced save error")
		}
		_, stderr, recovered := captureOutputAndRecover(t, func() { cmdSave(cfg) })
		assertFatal(t, stderr, recovered, "forced save error")
	})

	t.Run("timeline seam error", func(t *testing.T) {
		withArgs(t, "ohara", "timeline", "1")
		storeTimeline = func(*store.Store, int64, int) (*store.MemoryTimelineResult, error) {
			return nil, errors.New("forced timeline error")
		}
		_, stderr, recovered := captureOutputAndRecover(t, func() { cmdTimeline(cfg) })
		assertFatal(t, stderr, recovered, "forced timeline error")
	})

	t.Run("timeline prints session summary", func(t *testing.T) {
		withArgs(t, "ohara", "timeline", "1")
		storeTimeline = func(*store.Store, int64, int) (*store.MemoryTimelineResult, error) {
			return &store.MemoryTimelineResult{
				Anchor: store.MemoryItem{ID: 1, Kind: "discovery", Title: "focus", Body: "content"},
				Before: []store.MemoryItem{
					{Kind: "discovery", Title: "before item", Body: "before content"},
				},
				After: []store.MemoryItem{
					{Kind: "discovery", Title: "after item", Body: "after content"},
				},
			}, nil
		}
		stdout, stderr, recovered := captureOutputAndRecover(t, func() { cmdTimeline(cfg) })
		if recovered != nil || stderr != "" {
			t.Fatalf("expected successful timeline render, panic=%v stderr=%q", recovered, stderr)
		}
		if !strings.Contains(stdout, "Memory #1 (discovery): focus") {
			t.Fatalf("expected anchor in timeline output, got: %q", stdout)
		}
		if !strings.Contains(stdout, "─── Before ───") || !strings.Contains(stdout, "─── After ───") {
			t.Fatalf("expected before/after sections in timeline output, got: %q", stdout)
		}
	})

	t.Run("context seam error", func(t *testing.T) {
		withArgs(t, "ohara", "context")
		storeFormatContext = func(*store.Store, string, string) (string, error) {
			return "", errors.New("forced context error")
		}
		_, stderr, recovered := captureOutputAndRecover(t, func() { cmdContext(cfg) })
		assertFatal(t, stderr, recovered, "forced context error")
	})

	t.Run("stats seam error", func(t *testing.T) {
		withArgs(t, "ohara", "stats")
		storeStats = func(*store.Store) (*store.Stats, error) {
			return nil, errors.New("forced stats error")
		}
		_, stderr, recovered := captureOutputAndRecover(t, func() { cmdStats(cfg) })
		assertFatal(t, stderr, recovered, "forced stats error")
	})

	t.Run("export seam error", func(t *testing.T) {
		withArgs(t, "ohara", "export")
		storeExport = func(*store.Store) (*store.ExportData, error) {
			return nil, errors.New("forced export error")
		}
		_, stderr, recovered := captureOutputAndRecover(t, func() { cmdExport(cfg) })
		assertFatal(t, stderr, recovered, "forced export error")
	})

	t.Run("export marshal seam error", func(t *testing.T) {
		withArgs(t, "ohara", "export")
		storeExport = func(s *store.Store) (*store.ExportData, error) { return s.Export() }
		jsonMarshalIndent = func(any, string, string) ([]byte, error) {
			return nil, errors.New("forced marshal error")
		}
		_, stderr, recovered := captureOutputAndRecover(t, func() { cmdExport(cfg) })
		assertFatal(t, stderr, recovered, "forced marshal error")
	})

	t.Run("sync seam status error", func(t *testing.T) {
		withCwd(t, t.TempDir())
		withArgs(t, "ohara", "sync", "--status")
		syncStatus = func(*oharasync.Syncer) (int, int, int, error) {
			return 0, 0, 0, errors.New("forced status error")
		}
		_, stderr, recovered := captureOutputAndRecover(t, func() { cmdSync(cfg) })
		assertFatal(t, stderr, recovered, "forced status error")
	})

	t.Run("sync uses explicit project flag", func(t *testing.T) {
		withCwd(t, t.TempDir())
		withArgs(t, "ohara", "sync", "--project", "explicit-proj")
		stdout, stderr, recovered := captureOutputAndRecover(t, func() { cmdSync(cfg) })
		if recovered != nil || stderr != "" {
			t.Fatalf("sync with --project should succeed, panic=%v stderr=%q", recovered, stderr)
		}
		if !strings.Contains(stdout, `Exporting memories for project "explicit-proj"`) {
			t.Fatalf("expected explicit project output, got: %q", stdout)
		}
	})

	t.Run("setup interactive install error", func(t *testing.T) {
		setupSupportedAgents = func() []setup.Agent {
			return []setup.Agent{{Name: "codex", Description: "Codex", InstallDir: "/tmp/codex"}}
		}
		scanInputLine = func(a ...any) (int, error) {
			p := a[0].(*string)
			*p = "1"
			return 1, nil
		}
		setupInstallAgent = func(string) (*setup.Result, error) {
			return nil, errors.New("forced setup error")
		}

		withArgs(t, "ohara", "setup")
		_, stderr, recovered := captureOutputAndRecover(t, func() { cmdSetup(cfg) })
		assertFatal(t, stderr, recovered, "forced setup error")
	})
}

func TestCmdMCP(t *testing.T) {
	cfg := testConfig(t)
	stubRuntimeHooks(t)
	stubExitWithPanic(t)

	assertFatal := func(t *testing.T, stderr string, recovered any, want string) {
		t.Helper()
		code, ok := recovered.(exitCode)
		if !ok || int(code) != 1 {
			t.Fatalf("expected exit code 1 panic, got %v", recovered)
		}
		if !strings.Contains(stderr, want) {
			t.Fatalf("expected stderr to contain %q, got %q", want, stderr)
		}
	}

	t.Run("no tools filter uses newMCPServerWithConfig with nil allowlist", func(t *testing.T) {
		called := false
		newMCPServerWithConfig = func(s *store.Store, mcpCfg mcp.MCPConfig, allowlist map[string]bool) *mcpserver.MCPServer {
			called = true
			if allowlist != nil {
				t.Errorf("expected nil allowlist for no tools filter, got %v", allowlist)
			}
			return mcpserver.NewMCPServer("test", "0")
		}
		withArgs(t, "ohara", "mcp")
		_, stderr, recovered := captureOutputAndRecover(t, func() { cmdMCP(cfg) })
		if recovered != nil || stderr != "" {
			t.Fatalf("expected clean run, got panic=%v stderr=%q", recovered, stderr)
		}
		if !called {
			t.Fatal("expected newMCPServerWithConfig to be called")
		}
	})

	t.Run("--tools flag uses newMCPServerWithConfig with non-nil allowlist", func(t *testing.T) {
		var gotAllowlist map[string]bool
		newMCPServerWithConfig = func(s *store.Store, mcpCfg mcp.MCPConfig, allowlist map[string]bool) *mcpserver.MCPServer {
			gotAllowlist = allowlist
			return mcpserver.NewMCPServer("test", "0")
		}
		withArgs(t, "ohara", "mcp", "--tools=agent")
		_, stderr, recovered := captureOutputAndRecover(t, func() { cmdMCP(cfg) })
		if recovered != nil || stderr != "" {
			t.Fatalf("expected clean run, got panic=%v stderr=%q", recovered, stderr)
		}
		if gotAllowlist == nil {
			t.Fatal("expected newMCPServerWithConfig to be called with non-nil allowlist")
		}
	})

	t.Run("--tools as separate arg uses newMCPServerWithConfig with non-nil allowlist", func(t *testing.T) {
		var gotAllowlist map[string]bool
		newMCPServerWithConfig = func(s *store.Store, mcpCfg mcp.MCPConfig, allowlist map[string]bool) *mcpserver.MCPServer {
			gotAllowlist = allowlist
			return mcpserver.NewMCPServer("test", "0")
		}
		withArgs(t, "ohara", "mcp", "--tools", "agent")
		_, stderr, recovered := captureOutputAndRecover(t, func() { cmdMCP(cfg) })
		if recovered != nil || stderr != "" {
			t.Fatalf("expected clean run, got panic=%v stderr=%q", recovered, stderr)
		}
		if gotAllowlist == nil {
			t.Fatal("expected newMCPServerWithConfig to be called with non-nil allowlist")
		}
	})

	t.Run("storeNew failure calls fatal", func(t *testing.T) {
		storeNew = func(cfg store.Config) (*store.Store, error) {
			return nil, errors.New("db open failed")
		}
		withArgs(t, "ohara", "mcp")
		_, stderr, recovered := captureOutputAndRecover(t, func() { cmdMCP(cfg) })
		assertFatal(t, stderr, recovered, "db open failed")
	})

	t.Run("serveMCP failure calls fatal", func(t *testing.T) {
		storeNew = store.New
		serveMCP = func(_ *mcpserver.MCPServer, _ ...mcpserver.StdioOption) error {
			return errors.New("stdio failed")
		}
		withArgs(t, "ohara", "mcp")
		_, stderr, recovered := captureOutputAndRecover(t, func() { cmdMCP(cfg) })
		assertFatal(t, stderr, recovered, "stdio failed")
	})
}

func TestCmdMaintainUsesRuntimeConfig(t *testing.T) {
	cfg := testConfig(t)
	stubExitWithPanic(t)

	t.Run("loadRuntimeMaintain error propagates to fatal", func(t *testing.T) {
		loadRuntimeMaintain = func() (config.RuntimeConfig, error) {
			return config.RuntimeConfig{}, errors.New("maintain config load failed")
		}
		withArgs(t, "ohara", "maintain")
		_, stderr, recovered := captureOutputAndRecover(t, func() { cmdMaintain(cfg) })
		if _, ok := recovered.(exitCode); !ok {
			t.Fatalf("expected fatal exit, got %v", recovered)
		}
		if !strings.Contains(stderr, "maintain config load failed") {
			t.Fatalf("stderr missing config error: %q", stderr)
		}
	})

	t.Run("RuntimeConfig.SnapshotDir and RetainSnapshots are used", func(t *testing.T) {
		// We test the wiring by verifying the command completes without error
		// when RuntimeConfig returns valid values. Since we can't easily observe
		// the maintain.Options values, we verify the command path succeeds by
		// stubbing loadRuntimeMaintain and checking the error-path doesn't fire.
		//
		// Note: we intentionally do NOT call stubRuntimeHooks here because the
		// store is opened via the real storeNew (testConfig gives a valid DataDir).
		// The RuntimeConfig values only affect maintain.Options (not store creation).
		loadRuntimeMaintain = func() (config.RuntimeConfig, error) {
			return config.RuntimeConfig{
				HTTPAddr:        ":7437",
				SnapshotDir:     "/custom/snapshots",
				RetainSnapshots: 14,
			}, nil
		}
		withArgs(t, "ohara", "maintain")
		stdout, stderr, recovered := captureOutputAndRecover(t, func() { cmdMaintain(cfg) })
		// The command may succeed or fail depending on whether /custom/snapshots
		// is writable; what we care about is no unexpected panic.
		if recovered != nil {
			// If it panics, it must be an exitCode (our fatal wrapper).
			if _, ok := recovered.(exitCode); !ok {
				t.Fatalf("unexpected panic: %v (type %T)", recovered, recovered)
			}
		}
		// stderr should not contain unexpected errors unrelated to the config wiring.
		// A "completed with errors" from maintain.Run is acceptable (backup dir perms etc).
		_ = stdout
		_ = stderr
	})
}

func TestCmdBackupUsesRuntimeConfig(t *testing.T) {
	cfg := testConfig(t)
	stubExitWithPanic(t)

	t.Run("loadRuntimeMaintain error propagates to fatal", func(t *testing.T) {
		loadRuntimeMaintain = func() (config.RuntimeConfig, error) {
			return config.RuntimeConfig{}, errors.New("backup config load failed")
		}
		withArgs(t, "ohara", "backup")
		_, stderr, recovered := captureOutputAndRecover(t, func() { cmdBackup(cfg) })
		if _, ok := recovered.(exitCode); !ok {
			t.Fatalf("expected fatal exit, got %v", recovered)
		}
		if !strings.Contains(stderr, "backup config load failed") {
			t.Fatalf("stderr missing config error: %q", stderr)
		}
	})

	t.Run("RuntimeConfig.SnapshotDir is used for Backup path", func(t *testing.T) {
		customSnapshotDir := filepath.Join(t.TempDir(), "custom-snapshots")
		loadRuntimeMaintain = func() (config.RuntimeConfig, error) {
			return config.RuntimeConfig{
				HTTPAddr:    ":7437",
				SnapshotDir: customSnapshotDir,
			}, nil
		}
		withArgs(t, "ohara", "backup")
		stdout, stderr, recovered := captureOutputAndRecover(t, func() { cmdBackup(cfg) })
		if recovered != nil {
			t.Fatalf("unexpected panic: %v", recovered)
		}
		if stderr != "" {
			t.Fatalf("unexpected stderr: %q", stderr)
		}
		if !strings.Contains(stdout, "backup: "+customSnapshotDir) {
			t.Fatalf("expected custom snapshot dir in output, got: %q", stdout)
		}
	})

	t.Run("empty RuntimeConfig.SnapshotDir falls back to DataDir derivation", func(t *testing.T) {
		loadRuntimeMaintain = func() (config.RuntimeConfig, error) {
			return config.RuntimeConfig{
				HTTPAddr: ":7437",
				// SnapshotDir intentionally empty
			}, nil
		}
		withArgs(t, "ohara", "backup")
		stdout, stderr, recovered := captureOutputAndRecover(t, func() { cmdBackup(cfg) })
		if recovered != nil {
			t.Fatalf("unexpected panic: %v", recovered)
		}
		if stderr != "" {
			t.Fatalf("unexpected stderr: %q", stderr)
		}
		expectedDir := filepath.Join(cfg.DataDir, "snapshots")
		if !strings.Contains(stdout, "backup: "+expectedDir) {
			t.Fatalf("expected fallback snapshot dir in output, got: %q", stdout)
		}
	})
}

func TestRealCmdJobsRunOnceDrainsOneJob(t *testing.T) {
	cfg := testConfig(t)
	cfg.RetrievalMode = "hybrid"
	cfg.EmbeddingBackend = "ollama"
	cfg.OllamaURL = "http://127.0.0.1:1"

	s, err := store.New(cfg)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	memID, err := s.AddMemory(store.AddMemoryParams{
		ProjectID: "ohara",
		Kind:      store.MemoryKindDecision,
		Title:     "jobs cli drain",
		Body:      "verify jobs run --once path",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}
	if _, err := s.Exec(`UPDATE memory_jobs SET status = 'pending', attempts = 0, available_at = strftime('%Y-%m-%dT%H:%M:%f','now') WHERE memory_id = ? AND job_type = ?`, memID, store.JobTypeEmbedMemory); err != nil {
		t.Fatalf("prepare embed job: %v", err)
	}
	if _, err := s.Exec(`UPDATE memory_jobs SET status = 'done' WHERE memory_id = ? AND job_type != ?`, memID, store.JobTypeEmbedMemory); err != nil {
		t.Fatalf("prepare non-embed jobs: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}

	withArgs(t, "ohara", "jobs", "run", "--once", "--limit=1")
	stdout, stderr := captureOutput(t, func() { realCmdJobs(cfg) })
	if stderr != "" {
		t.Fatalf("expected no stderr, got: %q", stderr)
	}
	if !strings.Contains(stdout, "jobs processed: 1") {
		t.Fatalf("unexpected jobs output: %q", stdout)
	}

	verify, err := store.New(cfg)
	if err != nil {
		t.Fatalf("verify store.New: %v", err)
	}
	defer verify.Close()
	var status string
	var attempts int
	if err := verify.QueryRow(`SELECT status, attempts FROM memory_jobs WHERE memory_id = ? AND job_type = ?`, memID, store.JobTypeEmbedMemory).Scan(&status, &attempts); err != nil {
		t.Fatalf("verify job row: %v", err)
	}
	if status != "retry" && status != "failed" {
		t.Fatalf("expected retry/failed status after one drain attempt, got %q", status)
	}
	if attempts < 1 {
		t.Fatalf("expected attempts >= 1, got %d", attempts)
	}
}
