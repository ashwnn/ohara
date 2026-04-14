package setup

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func resetSetupSeams(t *testing.T) {
	t.Helper()
	oldRuntimeGOOS := runtimeGOOS
	oldUserHomeDir := userHomeDir
	oldStatFn := statFn
	oldOpenCodeReadFile := openCodeReadFile
	oldOpenCodeWriteFileFn := openCodeWriteFileFn
	oldReadFileFn := readFileFn
	oldWriteFileFn := writeFileFn
	oldJSONMarshalFn := jsonMarshalFn
	oldJSONMarshalIndentFn := jsonMarshalIndentFn
	oldInjectOpenCodeMCPFn := injectOpenCodeMCPFn
	oldOsExecutable := osExecutable

	t.Cleanup(func() {
		runtimeGOOS = oldRuntimeGOOS
		userHomeDir = oldUserHomeDir
		statFn = oldStatFn
		openCodeReadFile = oldOpenCodeReadFile
		openCodeWriteFileFn = oldOpenCodeWriteFileFn
		readFileFn = oldReadFileFn
		writeFileFn = oldWriteFileFn
		jsonMarshalFn = oldJSONMarshalFn
		jsonMarshalIndentFn = oldJSONMarshalIndentFn
		injectOpenCodeMCPFn = oldInjectOpenCodeMCPFn
		osExecutable = oldOsExecutable
	})
}

func useTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	userHomeDir = func() (string, error) { return home, nil }
	return home
}

func TestInstallUnknownAgent(t *testing.T) {
	resetSetupSeams(t)
	_, err := Install("unknown")
	if err == nil || !strings.Contains(err.Error(), "unknown agent") {
		t.Fatalf("expected unknown agent error, got %v", err)
	}
}

func TestInstallOpenCodeSuccessAndMCPRegistered(t *testing.T) {
	resetSetupSeams(t)
	home := useTestHome(t)
	runtimeGOOS = "linux"
	xdg := filepath.Join(home, "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdg)

	result, err := installOpenCode()
	if err != nil {
		t.Fatalf("installOpenCode failed: %v", err)
	}
	if result.Files != 2 {
		t.Fatalf("expected 2 files after MCP registration, got %d", result.Files)
	}

	pluginPath := filepath.Join(xdg, "opencode", "plugins", "ohara.ts")
	if _, err := os.Stat(pluginPath); err != nil {
		t.Fatalf("expected plugin file to exist: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(xdg, "opencode", "opencode.json"))
	if err != nil {
		t.Fatalf("read opencode config: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse opencode config: %v", err)
	}
	mcp, ok := cfg["mcp"].(map[string]any)
	if !ok {
		t.Fatalf("expected mcp object in opencode.json")
	}
	if _, ok := mcp["ohara"]; !ok {
		t.Fatalf("expected mcp.ohara registration")
	}
}

func TestInstallOpenCodeReadEmbeddedError(t *testing.T) {
	resetSetupSeams(t)
	home := useTestHome(t)
	runtimeGOOS = "linux"
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	openCodeReadFile = func(string) ([]byte, error) {
		return nil, errors.New("boom")
	}

	_, err := installOpenCode()
	if err == nil || !strings.Contains(err.Error(), "read embedded ohara.ts") {
		t.Fatalf("expected read embedded error, got %v", err)
	}
}

func TestInstallOpenCodeWriteError(t *testing.T) {
	resetSetupSeams(t)
	home := useTestHome(t)
	runtimeGOOS = "linux"
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	openCodeWriteFileFn = func(string, []byte, os.FileMode) error {
		return errors.New("write boom")
	}

	_, err := installOpenCode()
	if err == nil || !strings.Contains(err.Error(), "write ") {
		t.Fatalf("expected write error, got %v", err)
	}
}

func TestInstallOpenCodeMCPInjectionFailureIsNonFatal(t *testing.T) {
	resetSetupSeams(t)
	home := useTestHome(t)
	runtimeGOOS = "linux"
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	injectOpenCodeMCPFn = func() error {
		return errors.New("cannot write config")
	}

	result, err := installOpenCode()
	if err != nil {
		t.Fatalf("expected non-fatal MCP injection failure, got %v", err)
	}
	if result.Files != 1 {
		t.Fatalf("expected only plugin file counted when MCP injection fails, got %d", result.Files)
	}
}

func TestInjectOpenCodeMCPPreservesExistingAndIsIdempotent(t *testing.T) {
	resetSetupSeams(t)
	home := useTestHome(t)
	runtimeGOOS = "linux"
	xdg := filepath.Join(home, "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdg)

	configPath := filepath.Join(xdg, "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	initial := `{"theme":"kanagawa","mcp":{"other":{"type":"local","command":["foo"]}}}`
	if err := os.WriteFile(configPath, []byte(initial), 0644); err != nil {
		t.Fatalf("write initial config: %v", err)
	}

	if err := injectOpenCodeMCP(); err != nil {
		t.Fatalf("injectOpenCodeMCP failed: %v", err)
	}
	if err := injectOpenCodeMCP(); err != nil {
		t.Fatalf("injectOpenCodeMCP should be idempotent: %v", err)
	}

	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read updated config: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse updated config: %v", err)
	}
	mcp, ok := cfg["mcp"].(map[string]any)
	if !ok {
		t.Fatalf("expected mcp object")
	}
	if _, ok := mcp["other"]; !ok {
		t.Fatalf("expected existing mcp entry to be preserved")
	}
	ohara, ok := mcp["ohara"].(map[string]any)
	if !ok {
		t.Fatalf("expected ohara object")
	}
	if ohara["enabled"] != true {
		t.Fatalf("expected ohara.enabled=true")
	}
}

func TestInjectOpenCodeMCPConfigErrors(t *testing.T) {
	t.Run("invalid root json", func(t *testing.T) {
		resetSetupSeams(t)
		home := useTestHome(t)
		runtimeGOOS = "linux"
		xdg := filepath.Join(home, "xdg")
		t.Setenv("XDG_CONFIG_HOME", xdg)

		configPath := filepath.Join(xdg, "opencode", "opencode.json")
		if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
			t.Fatalf("mkdir config dir: %v", err)
		}
		if err := os.WriteFile(configPath, []byte("{"), 0644); err != nil {
			t.Fatalf("write config: %v", err)
		}

		err := injectOpenCodeMCP()
		if err == nil || !strings.Contains(err.Error(), "parse config") {
			t.Fatalf("expected parse config error, got %v", err)
		}
	})

	t.Run("invalid mcp block", func(t *testing.T) {
		resetSetupSeams(t)
		home := useTestHome(t)
		runtimeGOOS = "linux"
		xdg := filepath.Join(home, "xdg")
		t.Setenv("XDG_CONFIG_HOME", xdg)

		configPath := filepath.Join(xdg, "opencode", "opencode.json")
		if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
			t.Fatalf("mkdir config dir: %v", err)
		}
		if err := os.WriteFile(configPath, []byte(`{"mcp":"nope"}`), 0644); err != nil {
			t.Fatalf("write config: %v", err)
		}

		err := injectOpenCodeMCP()
		if err == nil || !strings.Contains(err.Error(), "parse mcp block") {
			t.Fatalf("expected parse mcp block error, got %v", err)
		}
	})

	t.Run("read error", func(t *testing.T) {
		resetSetupSeams(t)
		home := useTestHome(t)
		runtimeGOOS = "linux"
		xdg := filepath.Join(home, "xdg")
		t.Setenv("XDG_CONFIG_HOME", xdg)

		configPath := filepath.Join(xdg, "opencode", "opencode.json")
		if err := os.MkdirAll(configPath, 0755); err != nil {
			t.Fatalf("create directory at config path: %v", err)
		}

		err := injectOpenCodeMCP()
		if err == nil || !strings.Contains(err.Error(), "read config") {
			t.Fatalf("expected read config error, got %v", err)
		}
	})

	t.Run("marshal ohara entry error", func(t *testing.T) {
		resetSetupSeams(t)
		home := useTestHome(t)
		runtimeGOOS = "linux"
		xdg := filepath.Join(home, "xdg")
		t.Setenv("XDG_CONFIG_HOME", xdg)

		jsonMarshalFn = func(any) ([]byte, error) {
			return nil, errors.New("marshal entry boom")
		}

		err := injectOpenCodeMCP()
		if err == nil || !strings.Contains(err.Error(), "marshal ohara entry") {
			t.Fatalf("expected marshal ohara entry error, got %v", err)
		}
	})

	t.Run("marshal mcp block error", func(t *testing.T) {
		resetSetupSeams(t)
		home := useTestHome(t)
		runtimeGOOS = "linux"
		xdg := filepath.Join(home, "xdg")
		t.Setenv("XDG_CONFIG_HOME", xdg)

		calls := 0
		jsonMarshalFn = func(v any) ([]byte, error) {
			calls++
			if calls == 2 {
				return nil, errors.New("marshal mcp boom")
			}
			return json.Marshal(v)
		}

		err := injectOpenCodeMCP()
		if err == nil || !strings.Contains(err.Error(), "marshal mcp block") {
			t.Fatalf("expected marshal mcp block error, got %v", err)
		}
	})

	t.Run("marshal config error", func(t *testing.T) {
		resetSetupSeams(t)
		home := useTestHome(t)
		runtimeGOOS = "linux"
		xdg := filepath.Join(home, "xdg")
		t.Setenv("XDG_CONFIG_HOME", xdg)

		jsonMarshalIndentFn = func(any, string, string) ([]byte, error) {
			return nil, errors.New("marshal config boom")
		}

		err := injectOpenCodeMCP()
		if err == nil || !strings.Contains(err.Error(), "marshal config") {
			t.Fatalf("expected marshal config error, got %v", err)
		}
	})
}

func TestResolveOharaCommand(t *testing.T) {
	t.Run("unix returns absolute path from os.Executable", func(t *testing.T) {
		resetSetupSeams(t)
		runtimeGOOS = "linux"
		osExecutable = func() (string, error) { return "/usr/local/bin/ohara", nil }

		got := resolveOharaCommand()
		// EvalSymlinks on a non-existent path returns an error, so the result
		// is the raw os.Executable() value.
		if got == "ohara" {
			t.Fatalf("expected absolute path on unix, got bare 'ohara'")
		}
		if !strings.Contains(got, "ohara") {
			t.Fatalf("expected ohara in path, got %q", got)
		}
	})

	t.Run("darwin returns absolute path from os.Executable", func(t *testing.T) {
		resetSetupSeams(t)
		runtimeGOOS = "darwin"
		osExecutable = func() (string, error) { return "/opt/homebrew/bin/ohara", nil }

		got := resolveOharaCommand()
		if got == "ohara" {
			t.Fatalf("expected absolute path on darwin, got bare 'ohara'")
		}
		if !strings.Contains(got, "ohara") {
			t.Fatalf("expected ohara in path, got %q", got)
		}
	})

	t.Run("windows returns absolute path", func(t *testing.T) {
		resetSetupSeams(t)
		runtimeGOOS = "windows"
		osExecutable = func() (string, error) { return `C:\Users\user\bin\ohara.exe`, nil }

		got := resolveOharaCommand()
		// EvalSymlinks may change the path on real OS but in tests it should
		// either equal the input or the resolved form — either way not bare "ohara"
		if got == "ohara" {
			t.Fatalf("expected absolute path on windows, got bare 'ohara'")
		}
		if !strings.Contains(got, "ohara") {
			t.Fatalf("expected ohara in path, got %q", got)
		}
	})

	t.Run("executable error falls back to bare name on all platforms", func(t *testing.T) {
		for _, goos := range []string{"linux", "darwin", "windows"} {
			t.Run(goos, func(t *testing.T) {
				resetSetupSeams(t)
				runtimeGOOS = goos
				osExecutable = func() (string, error) { return "", errors.New("no executable") }

				if got := resolveOharaCommand(); got != "ohara" {
					t.Fatalf("expected fallback to bare 'ohara', got %q", got)
				}
			})
		}
	})
}

func TestOpenCodePluginDir(t *testing.T) {
	resetSetupSeams(t)
	userHomeDir = func() (string, error) { return "/home/tester", nil }

	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("APPDATA", "")

	runtimeGOOS = "linux"
	if got := openCodePluginDir(); got != filepath.Join("/home/tester", ".config", "opencode", "plugins") {
		t.Fatalf("unexpected linux openCodePluginDir: %s", got)
	}

	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got := openCodePluginDir(); got != filepath.Join("/xdg", "opencode", "plugins") {
		t.Fatalf("unexpected xdg openCodePluginDir: %s", got)
	}

	runtimeGOOS = "windows"
	t.Setenv("APPDATA", "C:/AppData/Roaming")
	t.Setenv("XDG_CONFIG_HOME", "")
	// OpenCode uses ~/.config/opencode/ on ALL platforms, ignoring %APPDATA%
	if got := openCodePluginDir(); got != filepath.Join("/home/tester", ".config", "opencode", "plugins") {
		t.Fatalf("unexpected windows openCodePluginDir: %s", got)
	}
}

func TestAdditionalOpenCodeHelperBranches(t *testing.T) {
	t.Run("installOpenCode mkdir error", func(t *testing.T) {
		resetSetupSeams(t)
		home := useTestHome(t)
		runtimeGOOS = "linux"

		blocked := filepath.Join(home, "xdg-block")
		if err := os.WriteFile(blocked, []byte("x"), 0644); err != nil {
			t.Fatalf("write blocker file: %v", err)
		}
		t.Setenv("XDG_CONFIG_HOME", blocked)

		_, err := installOpenCode()
		if err == nil || !strings.Contains(err.Error(), "create plugin dir") {
			t.Fatalf("expected create plugin dir error, got %v", err)
		}
	})

	t.Run("injectOpenCodeMCP write error when parent missing", func(t *testing.T) {
		resetSetupSeams(t)
		home := useTestHome(t)
		runtimeGOOS = "linux"
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))

		err := injectOpenCodeMCP()
		if err == nil || !strings.Contains(err.Error(), "write config") {
			t.Fatalf("expected write config error, got %v", err)
		}
	})
}

// ─── Issue #18: opencode.jsonc regression tests ─────────────────────────────

func TestStripJSONC(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"no comments", `{"key":"value"}`, `{"key":"value"}`},
		{"single line comment", "{\n// comment\n\"key\":\"value\"}", "{\n\n\"key\":\"value\"}"},
		{"multi line comment", "{/* block */\"key\":\"value\"}", "{\"key\":\"value\"}"},
		{"comment inside string preserved", `{"key":"val // not a comment"}`, `{"key":"val // not a comment"}`},
		{"escaped quote in string", `{"key":"val\"ue"}`, `{"key":"val\"ue"}`},
		{"trailing single-line comment", "{\"key\":\"value\" // inline\n}", "{\"key\":\"value\" \n}"},
		{"empty input", "", ""},
		{"only comments", "// nothing here\n/* also nothing */", "\n"},
		{"comment at EOF without newline", "{\"a\":1}// trailing", "{\"a\":1}"},
		{"unterminated multi-line comment", "{\"a\":1}/* never closed", "{\"a\":1}"},
		{"block comment with stars", "{/* ** fancy ** */\"a\":1}", "{\"a\":1}"},
		{"multi-line block comment preserves newlines", "{\n/* line1\nline2 */\n\"a\":1}", "{\n\n\"a\":1}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(stripJSONC([]byte(tt.input)))
			if got != tt.want {
				t.Fatalf("stripJSONC(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestOpenCodeConfigPathPrefersJSONC(t *testing.T) {
	resetSetupSeams(t)
	home := useTestHome(t)
	runtimeGOOS = "linux"
	t.Setenv("XDG_CONFIG_HOME", "")

	// When .jsonc exists, return .jsonc path
	statFn = func(name string) (os.FileInfo, error) {
		if strings.HasSuffix(name, "opencode.jsonc") {
			return nil, nil // exists
		}
		return nil, os.ErrNotExist
	}

	got := openCodeConfigPath()
	expected := filepath.Join(home, ".config", "opencode", "opencode.jsonc")
	if got != expected {
		t.Fatalf("expected %s, got %s", expected, got)
	}
}

func TestOpenCodeConfigPathFallsBackToJSON(t *testing.T) {
	resetSetupSeams(t)
	home := useTestHome(t)
	runtimeGOOS = "linux"
	t.Setenv("XDG_CONFIG_HOME", "")

	// When .jsonc does NOT exist, return .json path
	statFn = func(name string) (os.FileInfo, error) {
		return nil, os.ErrNotExist
	}

	got := openCodeConfigPath()
	expected := filepath.Join(home, ".config", "opencode", "opencode.json")
	if got != expected {
		t.Fatalf("expected %s, got %s", expected, got)
	}
}

func TestOpenCodeConfigPathXDGWithJSONC(t *testing.T) {
	resetSetupSeams(t)
	_ = useTestHome(t)
	runtimeGOOS = "linux"
	t.Setenv("XDG_CONFIG_HOME", "/custom/xdg")

	statFn = func(name string) (os.FileInfo, error) {
		if strings.HasSuffix(name, "opencode.jsonc") {
			return nil, nil
		}
		return nil, os.ErrNotExist
	}

	got := openCodeConfigPath()
	expected := filepath.Join("/custom/xdg", "opencode", "opencode.jsonc")
	if got != expected {
		t.Fatalf("expected %s, got %s", expected, got)
	}
}

func TestOpenCodeConfigPathWindowsWithJSONC(t *testing.T) {
	resetSetupSeams(t)
	home := useTestHome(t)
	runtimeGOOS = "windows"
	t.Setenv("APPDATA", "C:/Users/test/AppData/Roaming")
	t.Setenv("XDG_CONFIG_HOME", "")

	statFn = func(name string) (os.FileInfo, error) {
		if strings.HasSuffix(name, "opencode.jsonc") {
			return nil, nil
		}
		return nil, os.ErrNotExist
	}

	got := openCodeConfigPath()
	// OpenCode uses ~/.config/opencode/ on all platforms, not %APPDATA%
	expected := filepath.Join(home, ".config", "opencode", "opencode.jsonc")
	if got != expected {
		t.Fatalf("expected %s, got %s", expected, got)
	}
}

func TestInjectOpenCodeMCPHandlesJSONC(t *testing.T) {
	resetSetupSeams(t)
	home := useTestHome(t)
	runtimeGOOS = "linux"
	t.Setenv("XDG_CONFIG_HOME", "")

	configDir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Create a .jsonc file with comments
	jsoncPath := filepath.Join(configDir, "opencode.jsonc")
	content := `{
  // This is a comment
  "theme": "kanagawa",
  "mcp": {
    /* existing server */
    "other": {"type": "local", "command": ["foo"]}
  }
}`
	if err := os.WriteFile(jsoncPath, []byte(content), 0644); err != nil {
		t.Fatalf("write jsonc: %v", err)
	}

	// statFn should find the .jsonc file
	statFn = os.Stat

	if err := injectOpenCodeMCP(); err != nil {
		t.Fatalf("injectOpenCodeMCP with JSONC failed: %v", err)
	}

	// Verify ohara was added to the .jsonc file
	raw, err := os.ReadFile(jsoncPath)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("result should be valid JSON: %v", err)
	}
	mcp, ok := cfg["mcp"].(map[string]any)
	if !ok {
		t.Fatalf("expected mcp object in result")
	}
	if _, ok := mcp["ohara"]; !ok {
		t.Fatalf("expected ohara to be registered")
	}
	if _, ok := mcp["other"]; !ok {
		t.Fatalf("expected existing 'other' entry to be preserved")
	}
}

// ─── Issue #112: OpenCode MCP absolute-path config ───────────────────────────

func TestInjectOpenCodeMCPUsesResolvedCommand(t *testing.T) {
	for _, tc := range []struct {
		goos string
		exe  string
	}{
		{"windows", `C:\Users\user\bin\ohara.exe`},
		{"linux", "/usr/local/bin/ohara"},
		{"darwin", "/opt/homebrew/bin/ohara"},
	} {
		t.Run(tc.goos+" writes absolute path in command array", func(t *testing.T) {
			resetSetupSeams(t)
			home := useTestHome(t)
			runtimeGOOS = tc.goos
			osExecutable = func() (string, error) { return tc.exe, nil }
			t.Setenv("XDG_CONFIG_HOME", "")

			configDir := filepath.Join(home, ".config", "opencode")
			if err := os.MkdirAll(configDir, 0755); err != nil {
				t.Fatalf("mkdir config dir: %v", err)
			}

			if err := injectOpenCodeMCP(); err != nil {
				t.Fatalf("injectOpenCodeMCP failed: %v", err)
			}

			raw, err := os.ReadFile(filepath.Join(configDir, "opencode.json"))
			if err != nil {
				t.Fatalf("read config: %v", err)
			}
			var cfg map[string]any
			if err := json.Unmarshal(raw, &cfg); err != nil {
				t.Fatalf("parse config: %v", err)
			}
			mcp := cfg["mcp"].(map[string]any)
			oharaEntry := mcp["ohara"].(map[string]any)
			cmd := oharaEntry["command"].([]any)
			if len(cmd) == 0 {
				t.Fatalf("expected non-empty command array")
			}
			first := cmd[0].(string)
			if first == "ohara" {
				t.Fatalf("expected absolute path on %s, got bare 'ohara'", tc.goos)
			}
			if !strings.Contains(first, "ohara") {
				t.Fatalf("expected ohara in command path, got %q", first)
			}
			// Remaining args should be the MCP flags
			if len(cmd) != 3 || cmd[1] != "mcp" || cmd[2] != "--tools=agent" {
				t.Fatalf("expected args [<path> mcp --tools=agent], got %v", cmd)
			}
		})
	}

	t.Run("executable error falls back to bare ohara on all platforms", func(t *testing.T) {
		for _, goos := range []string{"linux", "darwin", "windows"} {
			t.Run(goos, func(t *testing.T) {
				resetSetupSeams(t)
				home := useTestHome(t)
				runtimeGOOS = goos
				osExecutable = func() (string, error) { return "", errors.New("no executable") }

				t.Setenv("XDG_CONFIG_HOME", "")

				configDir := filepath.Join(home, ".config", "opencode")
				if err := os.MkdirAll(configDir, 0755); err != nil {
					t.Fatalf("mkdir config dir: %v", err)
				}

				if err := injectOpenCodeMCP(); err != nil {
					t.Fatalf("injectOpenCodeMCP failed: %v", err)
				}

				raw, err := os.ReadFile(filepath.Join(configDir, "opencode.json"))
				if err != nil {
					t.Fatalf("read config: %v", err)
				}
				var cfg map[string]any
				if err := json.Unmarshal(raw, &cfg); err != nil {
					t.Fatalf("parse config: %v", err)
				}
				mcp := cfg["mcp"].(map[string]any)
				oharaEntry := mcp["ohara"].(map[string]any)
				cmd := oharaEntry["command"].([]any)
				if len(cmd) == 0 {
					t.Fatalf("expected non-empty command array")
				}
				if got := cmd[0].(string); got != "ohara" {
					t.Fatalf("expected fallback to bare 'ohara' when os.Executable fails, got %q", got)
				}
			})
		}
	})
}

func TestInstallOpenCodeWarningUsesResolvedCommand(t *testing.T) {
	for _, tc := range []struct {
		goos string
		exe  string
	}{
		{"windows", `C:\bin\ohara.exe`},
		{"linux", "/nonexistent/bin/ohara"},  // non-existent so EvalSymlinks is a no-op
		{"darwin", "/nonexistent/bin/ohara"}, // non-existent so EvalSymlinks is a no-op
	} {
		t.Run(tc.goos+" warning contains absolute path", func(t *testing.T) {
			resetSetupSeams(t)
			home := useTestHome(t)
			runtimeGOOS = tc.goos
			osExecutable = func() (string, error) { return tc.exe, nil }
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))

			// Force MCP injection to fail so the warning branch is exercised
			injectOpenCodeMCPFn = func() error {
				return errors.New("cannot write config")
			}

			// Capture stderr
			origStderr := os.Stderr
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatalf("pipe: %v", err)
			}
			os.Stderr = w

			_, installErr := installOpenCode()
			w.Close()
			os.Stderr = origStderr

			if installErr != nil {
				t.Fatalf("installOpenCode should not fail when MCP injection is non-fatal: %v", installErr)
			}

			buf := make([]byte, 4096)
			n, _ := r.Read(buf)
			stderr := string(buf[:n])

			// Warning must reference the binary path — not just bare "ohara"
			if !strings.Contains(stderr, "ohara") {
				t.Fatalf("expected ohara path in warning on %s, got:\n%s", tc.goos, stderr)
			}
			// Must NOT be the bare "ohara" unquoted form (since we have an absolute path)
			if strings.Contains(stderr, `["ohara",`) {
				t.Fatalf("expected absolute path (not bare ohara) in warning message, got:\n%s", stderr)
			}
		})
	}
}

// ─── Issue #113: OpenCode plugin OHARA_BIN bake-in ─────────────────────────

func TestPatchOharaBINLine(t *testing.T) {
	const original = `const OHARA_BIN = process.env.OHARA_BIN ?? "ohara"`

	t.Run("bakes in absolute path with Bun.which intermediate fallback", func(t *testing.T) {
		result := string(patchOharaBINLine([]byte(original), "/usr/local/bin/ohara"))

		if strings.Contains(result, `?? "ohara"`) {
			t.Fatalf("original bare-ohara fallback should be replaced, got:\n%s", result)
		}
		if !strings.Contains(result, `process.env.OHARA_BIN`) {
			t.Fatalf("must keep process.env.OHARA_BIN as first option, got:\n%s", result)
		}
		if !strings.Contains(result, `Bun.which("ohara")`) {
			t.Fatalf("must include Bun.which fallback, got:\n%s", result)
		}
		if !strings.Contains(result, `"/usr/local/bin/ohara"`) {
			t.Fatalf("must include baked-in absolute path, got:\n%s", result)
		}
		// Verify precedence order: env var ?? Bun.which ?? absolute path
		envIdx := strings.Index(result, `process.env.OHARA_BIN`)
		whichIdx := strings.Index(result, `Bun.which`)
		absIdx := strings.Index(result, `"/usr/local/bin/ohara"`)
		if !(envIdx < whichIdx && whichIdx < absIdx) {
			t.Fatalf("wrong precedence order (env < which < abs), got:\n%s", result)
		}
	})

	t.Run("Windows path with backslashes is JSON-quoted correctly", func(t *testing.T) {
		result := string(patchOharaBINLine([]byte(original), `C:\Users\user\bin\ohara.exe`))

		if !strings.Contains(result, `Bun.which("ohara")`) {
			t.Fatalf("must include Bun.which fallback, got:\n%s", result)
		}
		if !strings.Contains(result, `ohara.exe`) {
			t.Fatalf("must include Windows binary name, got:\n%s", result)
		}
	})

	t.Run("bare ohara fallback when os.Executable failed", func(t *testing.T) {
		result := string(patchOharaBINLine([]byte(original), "ohara"))

		if !strings.Contains(result, `process.env.OHARA_BIN`) {
			t.Fatalf("must keep process.env.OHARA_BIN, got:\n%s", result)
		}
		if !strings.Contains(result, `Bun.which("ohara")`) {
			t.Fatalf("must include Bun.which fallback, got:\n%s", result)
		}
	})

	t.Run("does not modify source if marker is absent", func(t *testing.T) {
		src := []byte(`// already patched\nconst OHARA_BIN = process.env.OHARA_BIN ?? Bun.which("ohara") ?? "/bin/ohara"`)
		result := patchOharaBINLine(src, "/new/bin/ohara")
		// Marker not found — returns original unchanged
		if string(result) != string(src) {
			t.Fatalf("expected no-op when marker absent, got:\n%s", string(result))
		}
	})

	t.Run("only replaces first occurrence", func(t *testing.T) {
		doubled := original + "\n" + original
		result := string(patchOharaBINLine([]byte(doubled), "/bin/ohara"))
		// One line should be replaced, the other should remain as-is
		if strings.Count(result, `?? "ohara"`) != 1 {
			t.Fatalf("expected exactly one original line to remain, got:\n%s", result)
		}
	})
}

func TestInstallOpenCodeBakesOHARABIN(t *testing.T) {
	t.Run("installed plugin contains absolute path fallback", func(t *testing.T) {
		resetSetupSeams(t)
		home := useTestHome(t)
		runtimeGOOS = "linux"
		osExecutable = func() (string, error) { return "/usr/local/bin/ohara", nil }
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))

		result, err := installOpenCode()
		if err != nil {
			t.Fatalf("installOpenCode failed: %v", err)
		}
		if result.Agent != "opencode" {
			t.Fatalf("unexpected agent: %q", result.Agent)
		}

		pluginPath := filepath.Join(home, "xdg", "opencode", "plugins", "ohara.ts")
		raw, err := os.ReadFile(pluginPath)
		if err != nil {
			t.Fatalf("read installed plugin: %v", err)
		}
		content := string(raw)

		// Must have env var override as first priority
		if !strings.Contains(content, `process.env.OHARA_BIN`) {
			t.Fatalf("installed plugin must keep process.env.OHARA_BIN override")
		}
		// Must have Bun.which intermediate fallback
		if !strings.Contains(content, `Bun.which("ohara")`) {
			t.Fatalf("installed plugin must include Bun.which fallback")
		}
		// Must have the baked-in absolute path
		if !strings.Contains(content, `"/usr/local/bin/ohara"`) {
			t.Fatalf("installed plugin must contain baked-in absolute path, got:\n%s", content)
		}
		// Source plugin file must remain unchanged (no patching of the template)
		srcRaw, err := openCodeReadFile("plugins/opencode/ohara.ts")
		if err != nil {
			t.Fatalf("read embedded plugin: %v", err)
		}
		if !strings.Contains(string(srcRaw), `?? "ohara"`) {
			t.Fatalf("source embedded plugin must remain unpatched")
		}
	})

	t.Run("OHARA_BIN env var still takes precedence at runtime", func(t *testing.T) {
		resetSetupSeams(t)
		home := useTestHome(t)
		runtimeGOOS = "linux"
		osExecutable = func() (string, error) { return "/usr/local/bin/ohara", nil }
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))

		if _, err := installOpenCode(); err != nil {
			t.Fatalf("installOpenCode failed: %v", err)
		}

		pluginPath := filepath.Join(home, "xdg", "opencode", "plugins", "ohara.ts")
		raw, err := os.ReadFile(pluginPath)
		if err != nil {
			t.Fatalf("read installed plugin: %v", err)
		}
		content := string(raw)

		envIdx := strings.Index(content, `process.env.OHARA_BIN`)
		whichIdx := strings.Index(content, `Bun.which("ohara")`)
		absIdx := strings.Index(content, `"/usr/local/bin/ohara"`)
		if envIdx == -1 || whichIdx == -1 || absIdx == -1 {
			t.Fatalf("missing expected tokens in installed plugin:\n%s", content)
		}
		if !(envIdx < whichIdx && whichIdx < absIdx) {
			t.Fatalf("wrong operator precedence in OHARA_BIN line:\n%s", content)
		}
	})

	t.Run("os.Executable fallback: Bun.which added but no double-ohara", func(t *testing.T) {
		resetSetupSeams(t)
		home := useTestHome(t)
		runtimeGOOS = "linux"
		osExecutable = func() (string, error) { return "", errors.New("no executable") }
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))

		if _, err := installOpenCode(); err != nil {
			t.Fatalf("installOpenCode failed: %v", err)
		}

		pluginPath := filepath.Join(home, "xdg", "opencode", "plugins", "ohara.ts")
		raw, err := os.ReadFile(pluginPath)
		if err != nil {
			t.Fatalf("read installed plugin: %v", err)
		}
		content := string(raw)

		if !strings.Contains(content, `Bun.which("ohara")`) {
			t.Fatalf("must still add Bun.which even when os.Executable fails")
		}
	})
}

// ─── Issue #116: Sub-agent session inflation fix ─────────────────────────────

func TestPluginSubAgentFiltering(t *testing.T) {
	resetSetupSeams(t)
	home := useTestHome(t)
	runtimeGOOS = "linux"
	osExecutable = func() (string, error) { return "/usr/local/bin/ohara", nil }
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))

	if _, err := installOpenCode(); err != nil {
		t.Fatalf("installOpenCode failed: %v", err)
	}

	pluginPath := filepath.Join(home, "xdg", "opencode", "plugins", "ohara.ts")
	raw, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read installed plugin: %v", err)
	}
	content := string(raw)

	// a) Session data must be read from event.properties.info
	if !strings.Contains(content, `event.properties as any)?.info`) {
		t.Fatalf("plugin must read session data from event.properties.info, got:\n%s", content)
	}

	// b) parentID check: sub-agents with a parentID must not register sessions
	if !strings.Contains(content, `parentID`) {
		t.Fatalf("plugin must check parentID to detect sub-agent sessions")
	}

	// b) title suffix check: secondary signal for sub-agent detection
	if !strings.Contains(content, `subagent)`) {
		t.Fatalf("plugin must check title suffix ' subagent)' as secondary sub-agent signal")
	}

	// b) isSubAgent gate: must guard ensureSession() call
	if !strings.Contains(content, `isSubAgent`) {
		t.Fatalf("plugin must use isSubAgent flag to gate ensureSession()")
	}

	// c) subAgentSessions set must exist for cross-hook suppression
	if !strings.Contains(content, `subAgentSessions`) {
		t.Fatalf("plugin must define subAgentSessions set for cross-hook suppression")
	}

	// Verify ensureSession itself guards against sub-agent sessions
	if !strings.Contains(content, `subAgentSessions.has(sessionId)`) {
		t.Fatalf("ensureSession must check subAgentSessions before registering")
	}

	// session.deleted must clean up subAgentSessions too
	if !strings.Contains(content, `subAgentSessions.delete(sessionId)`) {
		t.Fatalf("session.deleted handler must clean up subAgentSessions set")
	}
}
