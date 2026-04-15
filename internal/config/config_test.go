package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefault(t *testing.T) {
	cfg := Default()
	if cfg.HTTPAddr != "127.0.0.1:7331" {
		t.Errorf("HTTPAddr: got %q, want 127.0.0.1:7331", cfg.HTTPAddr)
	}
	if cfg.SocketPath == "" {
		t.Error("SocketPath: got empty, want non-empty")
	}
	if cfg.DataDir == "" {
		t.Error("DataDir: got empty, want non-empty")
	}
	if cfg.RetainSnapshots != 7 {
		t.Errorf("RetainSnapshots: got %d, want 7", cfg.RetainSnapshots)
	}
	if cfg.DefaultBudgetTokens != 400 {
		t.Errorf("DefaultBudgetTokens: got %d, want 400", cfg.DefaultBudgetTokens)
	}
	if cfg.MaxBudgetTokens != 800 {
		t.Errorf("MaxBudgetTokens: got %d, want 800", cfg.MaxBudgetTokens)
	}
	if !cfg.ConflictEnabled {
		t.Error("ConflictEnabled: got false, want true")
	}
	if cfg.ConflictThreshold != 0.6 {
		t.Errorf("ConflictThreshold: got %f, want 0.6", cfg.ConflictThreshold)
	}
}

func TestLoadMissingFile(t *testing.T) {
	cfg, err := Load("/nonexistent/path/config.json")
	if err != nil {
		t.Fatalf("Load missing file: unexpected error: %v", err)
	}
	// Should return defaults.
	if cfg.HTTPAddr != "127.0.0.1:7331" {
		t.Errorf("HTTPAddr: got %q, want default", cfg.HTTPAddr)
	}
}

func TestLoadEmptyFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.json")

	if err := os.WriteFile(path, []byte("{ }"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load empty object: unexpected error: %v", err)
	}
	if cfg.HTTPAddr != "127.0.0.1:7331" {
		t.Errorf("HTTPAddr: got %q, want default", cfg.HTTPAddr)
	}
}

func TestLoadJSONCComments(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.json")

	content := `{
		// This is a comment
		"http_addr": ":9000",
		/* block comment */
		"socket_path": "/tmp/ohara.sock"
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load JSONC: unexpected error: %v", err)
	}
	if cfg.HTTPAddr != ":9000" {
		t.Errorf("HTTPAddr: got %q, want :9000", cfg.HTTPAddr)
	}
	if cfg.SocketPath != "/tmp/ohara.sock" {
		t.Errorf("SocketPath: got %q, want /tmp/ohara.sock", cfg.SocketPath)
	}
}

func TestLoadEnvOverrides(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.json")

	// File says :9000 and /tmp/ohara.sock, but env wins.
	if err := os.WriteFile(path, []byte(`{"http_addr":":9000","socket_path":"/tmp/ohara.sock"}`), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	t.Setenv("OHARA_HTTP_ADDR", ":9999")
	// Note: OHARA_SOCKET is intentionally NOT set here to verify the config file
	// value is preserved (we cannot distinguish "unset" from "set to empty" with
	// os.Getenv, so empty-string env override is not supported).
	t.Setenv("OHARA_DATA_DIR", "/custom/data")
	t.Setenv("OHARA_SYNC_DIR", "/custom/sync")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load with env: unexpected error: %v", err)
	}
	if cfg.HTTPAddr != ":9999" {
		t.Errorf("HTTPAddr: got %q, want env :9999", cfg.HTTPAddr)
	}
	// SocketPath from config file is preserved because OHARA_SOCKET is not set.
	if cfg.SocketPath != "/tmp/ohara.sock" {
		t.Errorf("SocketPath: got %q, want /tmp/ohara.sock (config file value)", cfg.SocketPath)
	}
	if cfg.DataDir != "/custom/data" {
		t.Errorf("DataDir: got %q, want /custom/data", cfg.DataDir)
	}
	if cfg.SyncDir != "/custom/sync" {
		t.Errorf("SyncDir: got %q, want /custom/sync", cfg.SyncDir)
	}
	// SnapshotDir should default to {DataDir}/snapshots.
	if cfg.SnapshotDir != "/custom/data/snapshots" {
		t.Errorf("SnapshotDir: got %q, want /custom/data/snapshots", cfg.SnapshotDir)
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.json")

	if err := os.WriteFile(path, []byte("{invalid}"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Error("Load invalid JSON: expected error, got nil")
	}
}

func TestLoadSyncDir(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.json")

	if err := os.WriteFile(path, []byte(`{"sync_dir":".ohara"}`), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load sync_dir: unexpected error: %v", err)
	}
	if cfg.SyncDir != ".ohara" {
		t.Errorf("SyncDir: got %q, want .ohara", cfg.SyncDir)
	}
}

func TestLoadConflictConfig(t *testing.T) {
	tmp := t.TempDir()

	t.Run("conflict_enabled false", func(t *testing.T) {
		path := filepath.Join(tmp, "conflict-disabled.json")
		if err := os.WriteFile(path, []byte(`{"conflict_enabled":false}`), 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("Load conflict_enabled=false: unexpected error: %v", err)
		}
		if cfg.ConflictEnabled {
			t.Error("ConflictEnabled: got true, want false")
		}
	})

	t.Run("conflict_threshold out of range ignored", func(t *testing.T) {
		path := filepath.Join(tmp, "conflict-badrange.json")
		if err := os.WriteFile(path, []byte(`{"conflict_threshold":1.5}`), 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("Load out-of-range threshold: unexpected error: %v", err)
		}
		// Should keep the default (0.6) when threshold is out of range.
		if cfg.ConflictThreshold != 0.6 {
			t.Errorf("ConflictThreshold: got %f, want 0.6 (default, out-of-range ignored)", cfg.ConflictThreshold)
		}
	})

	t.Run("conflict_threshold boundary 0.0 and 1.0", func(t *testing.T) {
		path := filepath.Join(tmp, "conflict-boundary.json")
		if err := os.WriteFile(path, []byte(`{"conflict_threshold":0.0}`), 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("Load threshold=0.0: unexpected error: %v", err)
		}
		if cfg.ConflictThreshold != 0.0 {
			t.Errorf("ConflictThreshold: got %f, want 0.0", cfg.ConflictThreshold)
		}
	})
}

func TestLoadAllFields(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.json")

	if err := os.WriteFile(path, []byte(`{
		"http_addr": "127.0.0.1:8080",
		"socket_path": "/var/run/ohara.sock",
		"data_dir": "/opt/ohara",
		"sync_dir": "/opt/ohara/sync",
		"snapshot_dir": "/opt/ohara/snapshots",
		"retain_snapshots": 14,
		"default_budget_tokens": 500,
		"max_budget_tokens": 1000
	}`), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load all fields: unexpected error: %v", err)
	}
	if cfg.HTTPAddr != "127.0.0.1:8080" {
		t.Errorf("HTTPAddr: got %q, want 127.0.0.1:8080", cfg.HTTPAddr)
	}
	if cfg.SocketPath != "/var/run/ohara.sock" {
		t.Errorf("SocketPath: got %q, want /var/run/ohara.sock", cfg.SocketPath)
	}
	if cfg.DataDir != "/opt/ohara" {
		t.Errorf("DataDir: got %q, want /opt/ohara", cfg.DataDir)
	}
	if cfg.SyncDir != "/opt/ohara/sync" {
		t.Errorf("SyncDir: got %q, want /opt/ohara/sync", cfg.SyncDir)
	}
	if cfg.SnapshotDir != "/opt/ohara/snapshots" {
		t.Errorf("SnapshotDir: got %q, want /opt/ohara/snapshots", cfg.SnapshotDir)
	}
	if cfg.RetainSnapshots != 14 {
		t.Errorf("RetainSnapshots: got %d, want 14", cfg.RetainSnapshots)
	}
	if cfg.DefaultBudgetTokens != 500 {
		t.Errorf("DefaultBudgetTokens: got %d, want 500", cfg.DefaultBudgetTokens)
	}
	if cfg.MaxBudgetTokens != 1000 {
		t.Errorf("MaxBudgetTokens: got %d, want 1000", cfg.MaxBudgetTokens)
	}
}

func TestHTTPAddrParts(t *testing.T) {
	tests := []struct {
		addr string
		host string
		port int
	}{
		{":7331", "", 7331},
		{"127.0.0.1:8080", "127.0.0.1", 8080},
		{"localhost:3000", "localhost", 3000},
		{"", "", 7331}, // empty defaults to :7331
		{":8080", "", 8080},
	}

	for _, tc := range tests {
		t.Run(tc.addr, func(t *testing.T) {
			host, port := HTTPAddrParts(tc.addr)
			if host != tc.host {
				t.Errorf("host: got %q, want %q", host, tc.host)
			}
			if port != tc.port {
				t.Errorf("port: got %d, want %d", port, tc.port)
			}
		})
	}
}

func TestExpandHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	if home == "" {
		t.Skip("no home directory")
	}

	result := expandHome("~/test")
	expected := filepath.Join(home, "test")
	if result != expected {
		t.Errorf("expandHome: got %q, want %q", result, expected)
	}

	result2 := expandHome("/absolute/path")
	if result2 != "/absolute/path" {
		t.Errorf("expandHome absolute: got %q, want /absolute/path", result2)
	}
}
