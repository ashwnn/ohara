// Package setup handles agent plugin installation.
//
//   - OpenCode: copies embedded plugin file to ~/.config/opencode/plugins/
//     (patching OHARA_BIN to bake in the absolute binary path as a final
//     fallback) and injects MCP registration in opencode.json using the
//     resolved absolute binary path so child processes never require PATH
//     resolution in headless/systemd environments.
package setup

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
)

//go:embed plugins/opencode/*
var openCodeFS embed.FS

// Agent represents a supported AI coding agent.
type Agent struct {
	Name        string
	Description string
	InstallDir  string
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
		},
	}
}

// Install installs the plugin for the given agent.
func Install(agentName string) (*Result, error) {
	switch agentName {
	case "opencode":
		return installOpenCode()
	default:
		return nil, fmt.Errorf("unknown agent: %q (supported: opencode)", agentName)
	}
}

// ─── OpenCode ────────────────────────────────────────────────────────────────

// patchOharaBINLine rewrites the OHARA_BIN constant declaration in the
// plugin source so the installed copy contains an absolute fallback path.
//
// Original line in source:
//
//	const OHARA_BIN = process.env.OHARA_BIN ?? "ohara"
//
// Patched line in installed copy:
//
//	const OHARA_BIN = process.env.OHARA_BIN ?? Bun.which("ohara") ?? "/abs/path/ohara"
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
		cleaned := stripJSONC(data)
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

	// Check if ohara is already registered
	if _, exists := mcpBlock["ohara"]; exists {
		return nil // already registered, nothing to do
	}

	// Add ohara MCP entry (agent profile — only tools agents need).
	// Use resolveOharaCommand() so Windows users (and headless Linux setups
	// where PATH is not inherited) get the absolute binary path.
	oharaEntry := map[string]interface{}{
		"type":    "local",
		"command": []string{resolveOharaCommand(), "mcp", "--tools=agent"},
		"enabled": true,
	}
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

// stripJSONC removes single-line (//) and multi-line (/* */) comments
// from JSONC content, returning valid JSON. Comments inside quoted strings
// are preserved.
func stripJSONC(data []byte) []byte {
	var out []byte
	i := 0
	for i < len(data) {
		// Handle strings — pass through verbatim
		if data[i] == '"' {
			out = append(out, data[i])
			i++
			for i < len(data) && data[i] != '"' {
				if data[i] == '\\' && i+1 < len(data) {
					out = append(out, data[i], data[i+1])
					i += 2
					continue
				}
				out = append(out, data[i])
				i++
			}
			if i < len(data) {
				out = append(out, data[i])
				i++
			}
			continue
		}
		// Single-line comment
		if i+1 < len(data) && data[i] == '/' && data[i+1] == '/' {
			for i < len(data) && data[i] != '\n' {
				i++
			}
			continue
		}
		// Multi-line comment
		if i+1 < len(data) && data[i] == '/' && data[i+1] == '*' {
			i += 2
			for i+1 < len(data) && !(data[i] == '*' && data[i+1] == '/') {
				i++
			}
			if i+1 < len(data) {
				i += 2 // skip past */
			} else {
				i = len(data) // unterminated: consume everything
			}
			continue
		}
		out = append(out, data[i])
		i++
	}
	return out
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
