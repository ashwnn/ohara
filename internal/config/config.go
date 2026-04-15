// Package config provides the runtime configuration for Ohara.
//
// It loads core settings from a JSONC config file (~/.ohara/config.json)
// with environment variable overrides. All settings are optional; sensible
// defaults are provided.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Default config file path, relative to the data directory.
const DefaultConfigFile = "config.json"

// RuntimeConfig holds the core runtime settings for Ohara.
type RuntimeConfig struct {
	// HTTPAddr is the TCP address the server listens on.
	// Default: ":7437".
	HTTPAddr string
	// SocketPath is a unix socket path. When set, the server listens on this
	// socket instead of HTTPAddr.
	SocketPath string
	// DataDir is the Ohara data directory (SQLite DB, snapshots, etc.).
	// Default: ~/.ohara.
	DataDir string
	// SyncDir is the directory used for cloud sync chunks.
	// Default: "", which means .ohara/ relative to cwd.
	SyncDir string
	// SnapshotDir is the directory for database snapshots.
	// Default: {DataDir}/snapshots.
	SnapshotDir string
	// RetainSnapshots is the number of daily snapshots to retain.
	// Default: 7.
	RetainSnapshots int
	// DefaultBudgetTokens is the default token budget for context pack assembly.
	// Default: 400.
	DefaultBudgetTokens int
	// MaxBudgetTokens is the maximum allowed token budget for context pack assembly.
	// Default: 800.
	MaxBudgetTokens int
	// ConflictEnabled controls whether contradiction detection runs on memory add.
	// Default: true.
	ConflictEnabled bool
	// ConflictThreshold is the minimum Jaccard overlap score (0.0-1.0) required to
	// report a conflict. Only applies when ConflictEnabled is true.
	// Default: 0.6.
	ConflictThreshold float64
}

// fileConfig is the JSONC shape of the config file (before env overrides).
type fileConfig struct {
	HTTPAddr            string   `json:"http_addr"`
	SocketPath          string   `json:"socket_path"`
	DataDir             string   `json:"data_dir"`
	SyncDir             string   `json:"sync_dir"`
	SnapshotDir         string   `json:"snapshot_dir"`
	RetainSnapshots     int      `json:"retain_snapshots"`
	DefaultBudgetTokens int      `json:"default_budget_tokens"`
	MaxBudgetTokens     int      `json:"max_budget_tokens"`
	ConflictEnabled     *bool    `json:"conflict_enabled"`
	ConflictThreshold   *float64 `json:"conflict_threshold"`
}

// Default returns a RuntimeConfig with all sensible defaults.
func Default() RuntimeConfig {
	home, _ := os.UserHomeDir()
	dataDir := "~/.ohara"
	if home != "" {
		dataDir = filepath.Join(home, ".ohara")
	}
	return RuntimeConfig{
		HTTPAddr:            ":7437",
		SocketPath:          "",
		DataDir:             dataDir,
		SyncDir:             "",
		SnapshotDir:         filepath.Join(dataDir, "snapshots"),
		RetainSnapshots:     7,
		DefaultBudgetTokens: 400,
		MaxBudgetTokens:     800,
		ConflictEnabled:     true,
		ConflictThreshold:   0.6,
	}
}

// Load reads the config file at path and applies environment-variable overrides.
// If the file does not exist, Load returns Default() with env overrides applied.
// Load is tolerant of JSONC comments (// and /* */) in the config file.
func Load(path string) (RuntimeConfig, error) {
	cfg := Default()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				// Config file is optional — apply env overrides to defaults.
				applyEnvOverrides(&cfg)
				return cfg, nil
			}
			return RuntimeConfig{}, fmt.Errorf("read config %s: %w", path, err)
		}

		clean, err := stripJSONC(string(data))
		if err != nil {
			return RuntimeConfig{}, fmt.Errorf("strip jsonc: %w", err)
		}

		var fc fileConfig
		if err := json.Unmarshal([]byte(clean), &fc); err != nil {
			return RuntimeConfig{}, fmt.Errorf("parse config %s: %w", path, err)
		}

		if fc.HTTPAddr != "" {
			cfg.HTTPAddr = fc.HTTPAddr
		}
		if fc.SocketPath != "" {
			cfg.SocketPath = fc.SocketPath
		}
		if fc.DataDir != "" {
			cfg.DataDir = expandHome(fc.DataDir)
			// SnapshotDir defaults to {DataDir}/snapshots if not set.
			if fc.SnapshotDir == "" {
				cfg.SnapshotDir = filepath.Join(cfg.DataDir, "snapshots")
			}
		}
		if fc.SyncDir != "" {
			cfg.SyncDir = expandHome(fc.SyncDir)
		}
		if fc.SnapshotDir != "" {
			cfg.SnapshotDir = expandHome(fc.SnapshotDir)
		}
		if fc.RetainSnapshots > 0 {
			cfg.RetainSnapshots = fc.RetainSnapshots
		}
		if fc.DefaultBudgetTokens > 0 {
			cfg.DefaultBudgetTokens = fc.DefaultBudgetTokens
		}
		if fc.MaxBudgetTokens > 0 {
			cfg.MaxBudgetTokens = fc.MaxBudgetTokens
		}
		if fc.ConflictEnabled != nil {
			cfg.ConflictEnabled = *fc.ConflictEnabled
		}
		if fc.ConflictThreshold != nil && *fc.ConflictThreshold >= 0.0 && *fc.ConflictThreshold <= 1.0 {
			cfg.ConflictThreshold = *fc.ConflictThreshold
		}
	}

	applyEnvOverrides(&cfg)
	return cfg, nil
}

// applyEnvOverrides applies OHARA_* environment variable overrides.
func applyEnvOverrides(cfg *RuntimeConfig) {
	if v := os.Getenv("OHARA_HTTP_ADDR"); v != "" {
		cfg.HTTPAddr = v
	}
	if v := os.Getenv("OHARA_SOCKET"); v != "" {
		cfg.SocketPath = v
	}
	if v := os.Getenv("OHARA_DATA_DIR"); v != "" {
		cfg.DataDir = v
		cfg.SnapshotDir = filepath.Join(v, "snapshots")
	}
	if v := os.Getenv("OHARA_SYNC_DIR"); v != "" {
		cfg.SyncDir = v
	}
}

// expandHome replaces a leading "~" with the user's home directory.
func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

// HTTPAddrParts splits an HTTPAddr like ":7437" or "127.0.0.1:8080" into
// host and port. Returns ("", 7437) for ":7437".
func HTTPAddrParts(addr string) (host string, port int) {
	if addr == "" {
		addr = ":7437"
	}
	// Remove leading colon if host is empty.
	if strings.HasPrefix(addr, ":") {
		host = ""
		addr = addr[1:]
	} else {
		parts := strings.Split(addr, ":")
		if len(parts) == 2 {
			host = parts[0]
			addr = parts[1]
		}
	}
	port, _ = strconv.Atoi(addr)
	return
}

// stripJSONC removes // and /* */ comments from a JSONC string so it can
// be parsed as valid JSON. It correctly handles line-ending block comments,
// but does not handle block comments inside string values.
func stripJSONC(s string) (string, error) {
	// Single-pass scanner: remove // and /* */ comments.
	var result strings.Builder
	result.Grow(len(s))

	i := 0
	for i < len(s) {
		if i < len(s)-1 && s[i] == '/' && s[i+1] == '/' {
			// Line comment — skip to end of line.
			for i < len(s) && s[i] != '\n' {
				i++
			}
			continue
		}
		if i < len(s)-1 && s[i] == '/' && s[i+1] == '*' {
			// Block comment — skip until */.
			i += 2
			for i < len(s)-1 {
				if s[i] == '*' && s[i+1] == '/' {
					i += 2
					break
				}
				i++
			}
			continue
		}
		result.WriteByte(s[i])
		i++
	}

	raw := result.String()

	// Validate it parses as JSON.
	var js json.RawMessage
	if err := json.Unmarshal([]byte(raw), &js); err != nil {
		return "", fmt.Errorf("not valid JSON (after stripping comments): %w", err)
	}
	return raw, nil
}
