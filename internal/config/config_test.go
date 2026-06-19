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
	if cfg.RetrievalAutoMode != "auto" {
		t.Errorf("RetrievalAutoMode: got %q, want auto", cfg.RetrievalAutoMode)
	}
	if cfg.RetrievalMaxResults != 20 {
		t.Errorf("RetrievalMaxResults: got %d, want 20", cfg.RetrievalMaxResults)
	}
	if cfg.RetrievalMinScore != 0.0 {
		t.Errorf("RetrievalMinScore: got %f, want 0.0", cfg.RetrievalMinScore)
	}
	if cfg.SummarizerEnabled {
		t.Error("SummarizerEnabled: got true, want false")
	}
	if cfg.SummarizerBackend != "ollama" {
		t.Errorf("SummarizerBackend: got %q, want ollama", cfg.SummarizerBackend)
	}
	if cfg.SummarizerMaxTokens != 500 {
		t.Errorf("SummarizerMaxTokens: got %d, want 500", cfg.SummarizerMaxTokens)
	}
	if cfg.MaintenanceEnabled {
		t.Error("MaintenanceEnabled: got true, want false")
	}
	if cfg.MaintenanceIntervalMinutes != 60 {
		t.Errorf("MaintenanceIntervalMinutes: got %d, want 60", cfg.MaintenanceIntervalMinutes)
	}
	if cfg.MaintenanceArchiveDays != 90 {
		t.Errorf("MaintenanceArchiveDays: got %d, want 90", cfg.MaintenanceArchiveDays)
	}
	if cfg.MaintenanceBackupEnabled {
		t.Error("MaintenanceBackupEnabled: got true, want false")
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

func TestLoadNewConfigFields(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.json")

	if err := os.WriteFile(path, []byte(`{
		"retrieval_auto_mode": "hybrid",
		"retrieval_max_results": 50,
		"retrieval_min_score": 0.3,
		"summarizer_enabled": true,
		"summarizer_backend": "ollama",
		"summarizer_model": "qwen3-0.6b",
		"summarizer_max_tokens": 1000,
		"maintenance_enabled": true,
		"maintenance_interval_minutes": 120,
		"maintenance_archive_days": 30,
		"maintenance_backup_enabled": true
	}`), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load new config fields: unexpected error: %v", err)
	}
	if cfg.RetrievalAutoMode != "hybrid" {
		t.Errorf("RetrievalAutoMode: got %q, want hybrid", cfg.RetrievalAutoMode)
	}
	if cfg.RetrievalMaxResults != 50 {
		t.Errorf("RetrievalMaxResults: got %d, want 50", cfg.RetrievalMaxResults)
	}
	if cfg.RetrievalMinScore != 0.3 {
		t.Errorf("RetrievalMinScore: got %f, want 0.3", cfg.RetrievalMinScore)
	}
	if !cfg.SummarizerEnabled {
		t.Error("SummarizerEnabled: got false, want true")
	}
	if cfg.SummarizerBackend != "ollama" {
		t.Errorf("SummarizerBackend: got %q, want ollama", cfg.SummarizerBackend)
	}
	if cfg.SummarizerModel != "qwen3-0.6b" {
		t.Errorf("SummarizerModel: got %q, want qwen3-0.6b", cfg.SummarizerModel)
	}
	if cfg.SummarizerMaxTokens != 1000 {
		t.Errorf("SummarizerMaxTokens: got %d, want 1000", cfg.SummarizerMaxTokens)
	}
	if !cfg.MaintenanceEnabled {
		t.Error("MaintenanceEnabled: got false, want true")
	}
	if cfg.MaintenanceIntervalMinutes != 120 {
		t.Errorf("MaintenanceIntervalMinutes: got %d, want 120", cfg.MaintenanceIntervalMinutes)
	}
	if cfg.MaintenanceArchiveDays != 30 {
		t.Errorf("MaintenanceArchiveDays: got %d, want 30", cfg.MaintenanceArchiveDays)
	}
	if !cfg.MaintenanceBackupEnabled {
		t.Error("MaintenanceBackupEnabled: got false, want true")
	}
}

func TestLoadNewFieldsEnvOverrides(t *testing.T) {
	t.Setenv("OHARA_RETRIEVAL_AUTO_MODE", "embedding")
	t.Setenv("OHARA_RETRIEVAL_MAX_RESULTS", "100")
	t.Setenv("OHARA_RETRIEVAL_MIN_SCORE", "0.5")
	t.Setenv("OHARA_SUMMARIZER_ENABLED", "true")
	t.Setenv("OHARA_SUMMARIZER_BACKEND", "ollama")
	t.Setenv("OHARA_SUMMARIZER_MODEL", "test-model")
	t.Setenv("OHARA_SUMMARIZER_MAX_TOKENS", "800")
	t.Setenv("OHARA_MAINTENANCE_ENABLED", "true")
	t.Setenv("OHARA_MAINTENANCE_INTERVAL", "30")
	t.Setenv("OHARA_MAINTENANCE_ARCHIVE_DAYS", "14")
	t.Setenv("OHARA_MAINTENANCE_BACKUP_ENABLED", "1")

	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.json")
	if err := os.WriteFile(path, []byte(`{}`), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load new fields env: unexpected error: %v", err)
	}
	if cfg.RetrievalAutoMode != "embedding" {
		t.Errorf("RetrievalAutoMode: got %q, want embedding", cfg.RetrievalAutoMode)
	}
	if cfg.RetrievalMaxResults != 100 {
		t.Errorf("RetrievalMaxResults: got %d, want 100", cfg.RetrievalMaxResults)
	}
	if cfg.RetrievalMinScore != 0.5 {
		t.Errorf("RetrievalMinScore: got %f, want 0.5", cfg.RetrievalMinScore)
	}
	if !cfg.SummarizerEnabled {
		t.Error("SummarizerEnabled: got false, want true")
	}
	if cfg.SummarizerBackend != "ollama" {
		t.Errorf("SummarizerBackend: got %q, want ollama", cfg.SummarizerBackend)
	}
	if cfg.SummarizerModel != "test-model" {
		t.Errorf("SummarizerModel: got %q, want test-model", cfg.SummarizerModel)
	}
	if cfg.SummarizerMaxTokens != 800 {
		t.Errorf("SummarizerMaxTokens: got %d, want 800", cfg.SummarizerMaxTokens)
	}
	if !cfg.MaintenanceEnabled {
		t.Error("MaintenanceEnabled: got false, want true")
	}
	if cfg.MaintenanceIntervalMinutes != 30 {
		t.Errorf("MaintenanceIntervalMinutes: got %d, want 30", cfg.MaintenanceIntervalMinutes)
	}
	if cfg.MaintenanceArchiveDays != 14 {
		t.Errorf("MaintenanceArchiveDays: got %d, want 14", cfg.MaintenanceArchiveDays)
	}
	if !cfg.MaintenanceBackupEnabled {
		t.Error("MaintenanceBackupEnabled: got false, want true")
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

func TestDefaultRemoteMCPConfig(t *testing.T) {
	cfg := Default()
	if cfg.MCPRemoteEnable {
		t.Fatal("MCPRemoteEnable should default to false")
	}
	if cfg.MCPTransport != "streamable-http" {
		t.Fatalf("unexpected default MCPTransport: %q", cfg.MCPTransport)
	}
	if !cfg.MCPRequireAuth {
		t.Fatal("MCPRequireAuth should default to true")
	}
	if cfg.MCPAccessMode != "readonly" {
		t.Fatalf("unexpected default MCPAccessMode: %q", cfg.MCPAccessMode)
	}
	if cfg.MCPTrustLevel != "low" {
		t.Fatalf("unexpected default MCPTrustLevel: %q", cfg.MCPTrustLevel)
	}
}

func TestLoadRemoteMCPEnvOverrides(t *testing.T) {
	t.Setenv("OHARA_MCP_REMOTE_ENABLE", "1")
	t.Setenv("OHARA_MCP_TRANSPORT", "sse")
	t.Setenv("OHARA_MCP_BIND_ADDR", "0.0.0.0:7331")
	t.Setenv("OHARA_MCP_AUTH_MODE", "bearer")
	t.Setenv("OHARA_MCP_REQUIRE_AUTH", "1")
	t.Setenv("OHARA_MCP_ACCESS_MODE", "full")
	t.Setenv("OHARA_MCP_BEARER_TOKEN", "abc")
	t.Setenv("OHARA_MCP_ALLOWED_ORIGINS", "https://chatgpt.com, https://example.com")
	t.Setenv("OHARA_MCP_TRUST_LEVEL", "trusted")

	cfg, err := Load("/nonexistent/config.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.MCPRemoteEnable {
		t.Fatal("expected remote enable from env")
	}
	if cfg.MCPTransport != "sse" {
		t.Fatalf("unexpected transport: %q", cfg.MCPTransport)
	}
	if cfg.MCPBindAddr != "0.0.0.0:7331" {
		t.Fatalf("unexpected bind addr: %q", cfg.MCPBindAddr)
	}
	if !cfg.MCPRequireAuth {
		t.Fatal("expected MCPRequireAuth=true from env")
	}
	if cfg.MCPAccessMode != "full" {
		t.Fatalf("unexpected access mode: %q", cfg.MCPAccessMode)
	}
	if cfg.MCPBearerToken != "abc" {
		t.Fatalf("unexpected bearer token override")
	}
	if cfg.MCPTrustLevel != "trusted" {
		t.Fatalf("unexpected trust level: %q", cfg.MCPTrustLevel)
	}
}

func TestLegacyMCPHTTPEnablesRemote(t *testing.T) {
	t.Setenv("OHARA_MCP_HTTP", "true")
	cfg, err := Load("/nonexistent/config.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.MCPRemoteEnable {
		t.Fatal("legacy OHARA_MCP_HTTP should imply MCPRemoteEnable")
	}
}
