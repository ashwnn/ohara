package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefault(t *testing.T) {
	cfg := Default()
	if cfg.HTTPAddr != ":7437" {
		t.Errorf("HTTPAddr: got %q, want :7437", cfg.HTTPAddr)
	}
	if cfg.SocketPath != "" {
		t.Errorf("SocketPath: got %q, want empty", cfg.SocketPath)
	}
	if cfg.DataDir == "" {
		t.Error("DataDir: got empty, want non-empty")
	}
	if cfg.RetainSnapshots != 7 {
		t.Errorf("RetainSnapshots: got %d, want 7", cfg.RetainSnapshots)
	}
}

func TestLoadMissingFile(t *testing.T) {
	cfg, err := Load("/nonexistent/path/config.json")
	if err != nil {
		t.Fatalf("Load missing file: unexpected error: %v", err)
	}
	// Should return defaults.
	if cfg.HTTPAddr != ":7437" {
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
	if cfg.HTTPAddr != ":7437" {
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

func TestLoadAllFields(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.json")

	if err := os.WriteFile(path, []byte(`{
		"http_addr": "127.0.0.1:8080",
		"socket_path": "/var/run/ohara.sock",
		"data_dir": "/opt/ohara",
		"sync_dir": "/opt/ohara/sync",
		"snapshot_dir": "/opt/ohara/snapshots",
		"retain_snapshots": 14
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
}

func TestHTTPAddrParts(t *testing.T) {
	tests := []struct {
		addr string
		host string
		port int
	}{
		{":7437", "", 7437},
		{"127.0.0.1:8080", "127.0.0.1", 8080},
		{"localhost:3000", "localhost", 3000},
		{"", "", 7437}, // empty defaults to :7437
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
