// Package setup handles agent plugin installation.
//
// Supported agents and their config locations:
//
//   - opencode:     copies embedded plugin to ~/.config/opencode/plugins/
//     and injects MCP in opencode.json(opencode.jsonc).
//   - claude-code:  ~/.claude/settings.json
//   - cursor:       ~/.cursor/mcp.json
//   - windsurf:     ~/.windsurf/mcp.json
//   - gemini-cli:   ~/.gemini/settings.json
//   - vscode-copilot: ~/.config/Code/User/globalStorage/github.copilot/mcp.json
//
// Each agent's install is idempotent — running install when already configured
// is a no-op (check returns "already configured"). Remove undoes the entry.
package setup

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ashwnn/ohara/internal/util"
)

var (
	runtimeGOOS      = runtime.GOOS
	userHomeDir      = os.UserHomeDir
	osExecutable     = os.Executable
	openCodeReadFile = func(path string) ([]byte, error) {
		return openCodeFS.ReadFile(path)
	}
	statFn              = os.Stat
	openCodeWriteFileFn = os.WriteFile
	readFileFn          = os.ReadFile
	writeFileFn         = os.WriteFile
	jsonMarshalFn       = json.Marshal
	jsonMarshalIndentFn = json.MarshalIndent
	injectOpenCodeMCPFn = injectOpenCodeMCP
	// Check functions per agent (seams for testing)
	checkOpenCodeFn      = checkOpenCode
	checkClaudeCodeFn    = checkClaudeCode
	checkCursorFn        = checkCursor
	checkWindsurfFn      = checkWindsurf
	checkGeminiCLIFn     = checkGeminiCLI
	checkVSCodeCopilotFn = checkVSCodeCopilot
	// Remove functions per agent (seams for testing)
	removeOpenCodeFn      = removeOpenCode
	removeClaudeCodeFn    = removeClaudeCode
	removeCursorFn        = removeCursor
	removeWindsurfFn      = removeWindsurf
	removeGeminiCLIFn     = removeGeminiCLI
	removeVSCodeCopilotFn = removeVSCodeCopilot
)

//go:embed plugins/opencode/*
var openCodeFS embed.FS

// Agent represents a supported AI coding agent.
type Agent struct {
	Name        string
	Description string
	InstallDir  string
	ConfigPath  string // full path to the agent's config file
}

// ConfigStatus represents the current state of an agent's configuration.
type ConfigStatus struct {
	Agent      string
	Configured bool   // true if ohara entry exists and is enabled
	Status     string // "configured", "not_found", "error"
	Message    string // human-readable details
}

// Result holds the outcome of an installation.
type Result struct {
	Agent       string
	Destination string
	Files       int
}

// SupportedAgents returns the list of agents that have plugins available.
func SupportedAgents() []Agent {
	return []Agent{
		{
			Name:        "opencode",
			Description: "OpenCode — TypeScript plugin with session tracking, compaction recovery, and Memory Protocol",
			InstallDir:  openCodePluginDir(),
			ConfigPath:  openCodeConfigPath(),
		},
		{
			Name:        "claude-code",
			Description: "Anthropic Claude Code — adds ohara memory to claude",
			InstallDir:  "~/.claude",
			ConfigPath:  claudeCodeConfigPath(),
		},
		{
			Name:        "cursor",
			Description: "Cursor AI — adds ohara as MCP server",
			InstallDir:  "~/.cursor",
			ConfigPath:  cursorConfigPath(),
		},
		{
			Name:        "windsurf",
			Description: "Windsurf AI — adds ohara as MCP server",
			InstallDir:  "~/.windsurf",
			ConfigPath:  windsurfConfigPath(),
		},
		{
			Name:        "gemini-cli",
			Description: "Google Gemini CLI — adds ohara as MCP server",
			InstallDir:  "~/.gemini",
			ConfigPath:  geminiCLIConfigPath(),
		},
		{
			Name:        "vscode-copilot",
			Description: "VS Code Copilot — adds ohara as MCP server via Copilot extension storage",
			InstallDir:  "~/.config/Code/User/globalStorage/github.copilot",
			ConfigPath:  vscodeCopilotConfigPath(),
		},
	}
}

// ─── OpenCode ────────────────────────────────────────────────────────────────

// patchOharaBINLine rewrites the OHARA_BIN constant declaration in the
// plugin source so the installed copy contains an absolute fallback path.
//
// Original line in source: `const OHARA_BIN = process.env.OHARA_BIN ?? "ohara"`
// Patched line in installed copy: `const OHARA_BIN = process.env.OHARA_BIN ?? Bun.which("ohara") ?? "/abs/path/ohara"`
//
// Priority (left to right, first truthy wins):
//  1. OHARA_BIN env var — explicit user override, always respected.
//  2. Bun.which("ohara") — runtime PATH lookup; works in interactive shells.
//  3. Absolute baked-in path — works in headless/systemd where PATH is stripped.
//
// If absBin is already bare "ohara" (os.Executable fallback) we don't add it
// as the third fallback because it would be redundant with Bun.which("ohara").
func patchOharaBINLine(src []byte, absBin string) []byte {
	const marker = `const OHARA_BIN = process.env.OHARA_BIN ?? "ohara"`

	var replacement string
	if absBin == "ohara" {
		// os.Executable failed — add Bun.which but no baked-in absolute path
		replacement = `const OHARA_BIN = process.env.OHARA_BIN ?? Bun.which("ohara") ?? "ohara"`
	} else {
		// Normal case: bake in the absolute path as final fallback
		replacement = fmt.Sprintf(
			`const OHARA_BIN = process.env.OHARA_BIN ?? Bun.which("ohara") ?? %q`,
			absBin,
		)
	}

	return []byte(strings.Replace(string(src), marker, replacement, 1))
}

func installOpenCode() (*Result, error) {
	dir := openCodePluginDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create plugin dir %s: %w", dir, err)
	}

	data, err := openCodeReadFile("plugins/opencode/ohara.ts")
	if err != nil {
		return nil, fmt.Errorf("read embedded ohara.ts: %w", err)
	}

	// Patch OHARA_BIN in the installed copy so the plugin can find the binary
	// in headless/systemd environments where PATH may not include user tool dirs.
	// The installed file gets a baked-in absolute path while still honoring
	// process.env.OHARA_BIN (explicit user override) and Bun.which("ohara")
	// (runtime PATH lookup when PATH is available). The source plugin file is
	// not modified — it keeps the simple env-var form for development flexibility.
	data = patchOharaBINLine(data, resolveOharaCommand())

	dest := filepath.Join(dir, "ohara.ts")
	if err := openCodeWriteFileFn(dest, data, 0644); err != nil {
		return nil, fmt.Errorf("write %s: %w", dest, err)
	}

	// Register ohara MCP server in opencode.json
	files := 1
	if err := injectOpenCodeMCPFn(); err != nil {
		// Non-fatal: plugin works, MCP just needs manual config
		cmd := resolveOharaCommand()
		fmt.Fprintf(os.Stderr, "warning: could not auto-register MCP server in opencode.json: %v\n", err)
		fmt.Fprintf(os.Stderr, "  Add manually to your opencode.json under \"mcp\":\n")
		fmt.Fprintf(os.Stderr, "  \"ohara\": { \"type\": \"local\", \"command\": [%q, \"mcp\", \"--tools=agent\"], \"enabled\": true }\n", cmd)
	} else {
		files = 2
	}

	return &Result{
		Agent:       "opencode",
		Destination: dir,
		Files:       files,
	}, nil
}

// injectOpenCodeMCP adds the ohara MCP server entry to opencode.json.
// It reads the existing config, adds/updates the ohara entry under "mcp",
// and writes it back preserving all other settings.
func injectOpenCodeMCP() error {
	configPath := openCodeConfigPath()

	// Read existing config (or start with empty object)
	var config map[string]json.RawMessage
	data, err := readFileFn(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			config = make(map[string]json.RawMessage)
		} else {
			return fmt.Errorf("read config: %w", err)
		}
	} else {
		cleaned := util.StripJSONC(data)
		if err := json.Unmarshal(cleaned, &config); err != nil {
			return fmt.Errorf("parse config: %w", err)
		}
	}

	// Parse or create the "mcp" block
	var mcpBlock map[string]json.RawMessage
	if raw, exists := config["mcp"]; exists {
		if err := json.Unmarshal(raw, &mcpBlock); err != nil {
			return fmt.Errorf("parse mcp block: %w", err)
		}
	} else {
		mcpBlock = make(map[string]json.RawMessage)
	}

	// Upsert ohara MCP entry. If entry already exists, preserve user choices where
	// possible but ensure modern defaults are present (mcp + --tools=agent).
	oharaEntry := map[string]interface{}{}
	if raw, exists := mcpBlock["ohara"]; exists {
		_ = json.Unmarshal(raw, &oharaEntry)
	}

	if _, ok := oharaEntry["type"]; !ok {
		oharaEntry["type"] = "local"
	}

	// Normalize command array and enforce minimum shape:
	//   <bin> mcp --tools=agent
	cmd := []string{}
	if rawCmd, ok := oharaEntry["command"]; ok {
		switch v := rawCmd.(type) {
		case []string:
			cmd = append(cmd, v...)
		case []interface{}:
			for _, p := range v {
				if s, ok := p.(string); ok && strings.TrimSpace(s) != "" {
					cmd = append(cmd, s)
				}
			}
		}
	}
	if len(cmd) == 0 {
		cmd = []string{resolveOharaCommand()}
	}

	hasMCP := false
	hasToolsFlag := false
	for _, part := range cmd {
		if part == "mcp" {
			hasMCP = true
		}
		if part == "--tools" || strings.HasPrefix(part, "--tools=") {
			hasToolsFlag = true
		}
	}
	if !hasMCP {
		cmd = append(cmd, "mcp")
	}
	if !hasToolsFlag {
		cmd = append(cmd, "--tools=agent")
	}
	oharaEntry["command"] = cmd
	oharaEntry["enabled"] = true

	entryJSON, err := jsonMarshalFn(oharaEntry)
	if err != nil {
		return fmt.Errorf("marshal ohara entry: %w", err)
	}
	mcpBlock["ohara"] = json.RawMessage(entryJSON)

	// Write mcp block back to config
	mcpJSON, err := jsonMarshalFn(mcpBlock)
	if err != nil {
		return fmt.Errorf("marshal mcp block: %w", err)
	}
	config["mcp"] = json.RawMessage(mcpJSON)

	// Write config back with indentation
	output, err := jsonMarshalIndentFn(config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := writeFileFn(configPath, output, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}

// openCodeConfigPath returns the path to the OpenCode config file.
// It checks for opencode.jsonc first (preferred), then falls back to opencode.json.
func openCodeConfigPath() string {
	dir := openCodeConfigDir()
	jsonc := filepath.Join(dir, "opencode.jsonc")
	if _, err := statFn(jsonc); err == nil {
		return jsonc
	}
	return filepath.Join(dir, "opencode.json")
}

// openCodeConfigDir returns the directory containing the OpenCode config.
func openCodeConfigDir() string {
	home, _ := userHomeDir()

	// OpenCode reads from ~/.config/opencode/ on ALL platforms (including Windows),
	// ignoring the Windows %APPDATA% convention. Match that behavior.
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "opencode")
	}
	return filepath.Join(home, ".config", "opencode")
}

// resolveOharaCommand returns the absolute path to the ohara binary.
// It uses os.Executable() so that headless/systemd environments (where PATH
// is not reliably inherited by child processes) still find the binary.
// EvalSymlinks makes the path stable across package-manager upgrades.
// Falls back to bare "ohara" only if os.Executable() itself fails.
func resolveOharaCommand() string {
	exe, err := osExecutable()
	if err != nil {
		return "ohara" // fallback to PATH-based name
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe
}

// ─── Platform paths ──────────────────────────────────────────────────────────

func openCodePluginDir() string {
	return filepath.Join(openCodeConfigDir(), "plugins")
}

func claudeCodeConfigPath() string {
	home, _ := userHomeDir()
	return filepath.Join(home, ".claude", "settings.json")
}

func cursorConfigPath() string {
	home, _ := userHomeDir()
	return filepath.Join(home, ".cursor", "mcp.json")
}

func windsurfConfigPath() string {
	home, _ := userHomeDir()
	return filepath.Join(home, ".windsurf", "mcp.json")
}

func geminiCLIConfigPath() string {
	home, _ := userHomeDir()
	return filepath.Join(home, ".gemini", "settings.json")
}

func vscodeCopilotConfigPath() string {
	home, _ := userHomeDir()
	return filepath.Join(home, ".config", "Code", "User", "globalStorage", "github.copilot", "mcp.json")
}

// ─── Agent-specific install, check, remove ───────────────────────────────────

// oharaMCPEntry returns the MCP server entry for ohara.
func oharaMCPEntry() map[string]interface{} {
	return map[string]interface{}{
		"command": []string{resolveOharaCommand(), "mcp", "--tools=agent"},
	}
}

// Install installs the plugin/config for the given agent.
func Install(agentName string) (*Result, error) {
	switch agentName {
	case "opencode":
		return installOpenCode()
	case "claude-code":
		return installGenericAgent("claude-code", claudeCodeConfigPath(), "mcpServers", nil)
	case "cursor":
		return installGenericAgent("cursor", cursorConfigPath(), "mcpServers", nil)
	case "windsurf":
		return installGenericAgent("windsurf", windsurfConfigPath(), "mcpServers", nil)
	case "gemini-cli":
		return installGenericAgent("gemini-cli", geminiCLIConfigPath(), "mcpServers", nil)
	case "vscode-copilot":
		return installGenericAgent("vscode-copilot", vscodeCopilotConfigPath(), "mcp", nil)
	default:
		supported := make([]string, 0, len(SupportedAgents()))
		for _, a := range SupportedAgents() {
			supported = append(supported, a.Name)
		}
		return nil, fmt.Errorf("unknown agent: %q (supported: %v)", agentName, supported)
	}
}

// Check checks whether ohara is configured for the given agent.
func Check(agentName string) (*ConfigStatus, error) {
	switch agentName {
	case "opencode":
		return checkOpenCodeFn()
	case "claude-code":
		return checkClaudeCodeFn()
	case "cursor":
		return checkCursorFn()
	case "windsurf":
		return checkWindsurfFn()
	case "gemini-cli":
		return checkGeminiCLIFn()
	case "vscode-copilot":
		return checkVSCodeCopilotFn()
	default:
		return nil, fmt.Errorf("unknown agent: %q", agentName)
	}
}

// Remove removes the ohara configuration for the given agent.
func Remove(agentName string) error {
	switch agentName {
	case "opencode":
		return removeOpenCodeFn()
	case "claude-code":
		return removeClaudeCodeFn()
	case "cursor":
		return removeCursorFn()
	case "windsurf":
		return removeWindsurfFn()
	case "gemini-cli":
		return removeGeminiCLIFn()
	case "vscode-copilot":
		return removeVSCodeCopilotFn()
	default:
		return fmt.Errorf("unknown agent: %q", agentName)
	}
}

// installGenericAgent installs ohara as an MCP server in a JSON config file.
func installGenericAgent(name, configPath, mcpKey string, customEntry map[string]interface{}) (*Result, error) {
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create config dir %s: %w", dir, err)
	}

	// Read existing config or create empty
	var config map[string]json.RawMessage
	data, err := readFileFn(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			config = make(map[string]json.RawMessage)
		} else {
			return nil, fmt.Errorf("read config: %w", err)
		}
	} else {
		if err := json.Unmarshal(data, &config); err != nil {
			return nil, fmt.Errorf("parse config: %w", err)
		}
	}

	// Parse or create the MCP block
	var mcpBlock map[string]json.RawMessage
	if raw, exists := config[mcpKey]; exists {
		if err := json.Unmarshal(raw, &mcpBlock); err != nil {
			return nil, fmt.Errorf("parse %s block: %w", mcpKey, err)
		}
	} else {
		mcpBlock = make(map[string]json.RawMessage)
	}

	// Check if ohara is already registered
	if _, exists := mcpBlock["ohara"]; exists {
		// Already configured — idempotent, just report success
		return &Result{
			Agent:       name,
			Destination: configPath,
			Files:       1,
		}, nil
	}

	// Add ohara entry
	entry := customEntry
	if entry == nil {
		entry = oharaMCPEntry()
	}
	entryJSON, err := jsonMarshalFn(entry)
	if err != nil {
		return nil, fmt.Errorf("marshal ohara entry: %w", err)
	}
	mcpBlock["ohara"] = json.RawMessage(entryJSON)

	// Write mcp block back
	mcpJSON, err := jsonMarshalFn(mcpBlock)
	if err != nil {
		return nil, fmt.Errorf("marshal %s block: %w", mcpKey, err)
	}
	config[mcpKey] = json.RawMessage(mcpJSON)

	// Write config back
	output, err := jsonMarshalIndentFn(config, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	if err := writeFileFn(configPath, output, 0644); err != nil {
		return nil, fmt.Errorf("write config: %w", err)
	}

	return &Result{
		Agent:       name,
		Destination: configPath,
		Files:       1,
	}, nil
}

// checkGenericAgent checks if ohara is configured in a JSON config file.
func checkGenericAgent(name, configPath, mcpKey string) (*ConfigStatus, error) {
	data, err := readFileFn(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &ConfigStatus{
				Agent:      name,
				Configured: false,
				Status:     "not_found",
				Message:    "config file does not exist",
			}, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	var config map[string]json.RawMessage
	if err := json.Unmarshal(data, &config); err != nil {
		return &ConfigStatus{
			Agent:      name,
			Configured: false,
			Status:     "error",
			Message:    fmt.Sprintf("parse error: %v", err),
		}, nil
	}

	raw, exists := config[mcpKey]
	if !exists {
		return &ConfigStatus{
			Agent:      name,
			Configured: false,
			Status:     "not_found",
			Message:    "ohara not configured (mcpServers block missing)",
		}, nil
	}

	var mcpBlock map[string]json.RawMessage
	if err := json.Unmarshal(raw, &mcpBlock); err != nil {
		return &ConfigStatus{
			Agent:      name,
			Configured: false,
			Status:     "error",
			Message:    fmt.Sprintf("parse %s block: %v", mcpKey, err),
		}, nil
	}

	oharaRaw, exists := mcpBlock["ohara"]
	if !exists {
		return &ConfigStatus{
			Agent:      name,
			Configured: false,
			Status:     "not_found",
			Message:    "ohara entry not found in " + mcpKey,
		}, nil
	}

	// Verify it's a valid object
	var oharaEntry interface{}
	if err := json.Unmarshal(oharaRaw, &oharaEntry); err != nil || oharaEntry == nil {
		return &ConfigStatus{
			Agent:      name,
			Configured: false,
			Status:     "error",
			Message:    "ohara entry is malformed",
		}, nil
	}

	return &ConfigStatus{
		Agent:      name,
		Configured: true,
		Status:     "configured",
		Message:    "ohara is configured",
	}, nil
}

// removeGenericAgent removes ohara from a JSON config file.
func removeGenericAgent(name, configPath, mcpKey string) error {
	data, err := readFileFn(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Nothing to remove
			return nil
		}
		return fmt.Errorf("read config: %w", err)
	}

	var config map[string]json.RawMessage
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	raw, exists := config[mcpKey]
	if !exists {
		// Nothing to remove
		return nil
	}

	var mcpBlock map[string]json.RawMessage
	if err := json.Unmarshal(raw, &mcpBlock); err != nil {
		return fmt.Errorf("parse %s block: %w", mcpKey, err)
	}

	if _, exists := mcpBlock["ohara"]; !exists {
		// ohara not present, nothing to do
		return nil
	}

	// Remove ohara entry
	delete(mcpBlock, "ohara")

	// If mcp block is now empty, remove the whole block
	if len(mcpBlock) == 0 {
		delete(config, mcpKey)
	} else {
		mcpJSON, err := jsonMarshalFn(mcpBlock)
		if err != nil {
			return fmt.Errorf("marshal %s block: %w", mcpKey, err)
		}
		config[mcpKey] = json.RawMessage(mcpJSON)
	}

	// Write config back
	output, err := jsonMarshalIndentFn(config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := writeFileFn(configPath, output, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}

// ─── Per-agent check implementations ────────────────────────────────────────

func checkOpenCode() (*ConfigStatus, error) {
	configPath := openCodeConfigPath()
	data, err := readFileFn(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &ConfigStatus{
				Agent:      "opencode",
				Configured: false,
				Status:     "not_found",
				Message:    "opencode config does not exist",
			}, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	cleaned := util.StripJSONC(data)
	var config map[string]json.RawMessage
	if err := json.Unmarshal(cleaned, &config); err != nil {
		return &ConfigStatus{
			Agent:      "opencode",
			Configured: false,
			Status:     "error",
			Message:    fmt.Sprintf("parse error: %v", err),
		}, nil
	}

	raw, exists := config["mcp"]
	if !exists {
		return &ConfigStatus{
			Agent:      "opencode",
			Configured: false,
			Status:     "not_found",
			Message:    "ohara not configured (mcp block missing)",
		}, nil
	}

	var mcpBlock map[string]json.RawMessage
	if err := json.Unmarshal(raw, &mcpBlock); err != nil {
		return &ConfigStatus{
			Agent:      "opencode",
			Configured: false,
			Status:     "error",
			Message:    fmt.Sprintf("parse mcp block: %v", err),
		}, nil
	}

	oharaRaw, exists := mcpBlock["ohara"]
	if !exists {
		return &ConfigStatus{
			Agent:      "opencode",
			Configured: false,
			Status:     "not_found",
			Message:    "ohara entry not found in mcp block",
		}, nil
	}

	var oharaEntry interface{}
	if err := json.Unmarshal(oharaRaw, &oharaEntry); err != nil || oharaEntry == nil {
		return &ConfigStatus{
			Agent:      "opencode",
			Configured: false,
			Status:     "error",
			Message:    "ohara entry is malformed",
		}, nil
	}

	return &ConfigStatus{
		Agent:      "opencode",
		Configured: true,
		Status:     "configured",
		Message:    "ohara is configured",
	}, nil
}

func checkClaudeCode() (*ConfigStatus, error) {
	return checkGenericAgent("claude-code", claudeCodeConfigPath(), "mcpServers")
}

func checkCursor() (*ConfigStatus, error) {
	return checkGenericAgent("cursor", cursorConfigPath(), "mcpServers")
}

func checkWindsurf() (*ConfigStatus, error) {
	return checkGenericAgent("windsurf", windsurfConfigPath(), "mcpServers")
}

func checkGeminiCLI() (*ConfigStatus, error) {
	return checkGenericAgent("gemini-cli", geminiCLIConfigPath(), "mcpServers")
}

func checkVSCodeCopilot() (*ConfigStatus, error) {
	return checkGenericAgent("vscode-copilot", vscodeCopilotConfigPath(), "mcp")
}

// ─── Per-agent remove implementations ────────────────────────────────────────

func removeOpenCode() error {
	configPath := openCodeConfigPath()
	data, err := readFileFn(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read config: %w", err)
	}

	cleaned := util.StripJSONC(data)
	var config map[string]json.RawMessage
	if err := json.Unmarshal(cleaned, &config); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	raw, exists := config["mcp"]
	if !exists {
		return nil
	}

	var mcpBlock map[string]json.RawMessage
	if err := json.Unmarshal(raw, &mcpBlock); err != nil {
		return fmt.Errorf("parse mcp block: %w", err)
	}

	if _, exists := mcpBlock["ohara"]; !exists {
		return nil
	}

	delete(mcpBlock, "ohara")

	if len(mcpBlock) == 0 {
		delete(config, "mcp")
	} else {
		mcpJSON, err := jsonMarshalFn(mcpBlock)
		if err != nil {
			return fmt.Errorf("marshal mcp block: %w", err)
		}
		config["mcp"] = json.RawMessage(mcpJSON)
	}

	output, err := jsonMarshalIndentFn(config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := writeFileFn(configPath, output, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}

func removeClaudeCode() error {
	return removeGenericAgent("claude-code", claudeCodeConfigPath(), "mcpServers")
}

func removeCursor() error {
	return removeGenericAgent("cursor", cursorConfigPath(), "mcpServers")
}

func removeWindsurf() error {
	return removeGenericAgent("windsurf", windsurfConfigPath(), "mcpServers")
}

func removeGeminiCLI() error {
	return removeGenericAgent("gemini-cli", geminiCLIConfigPath(), "mcpServers")
}

func removeVSCodeCopilot() error {
	return removeGenericAgent("vscode-copilot", vscodeCopilotConfigPath(), "mcp")
}
