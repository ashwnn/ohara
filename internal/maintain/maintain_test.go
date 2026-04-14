package maintain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ashwnn/ohara/internal/store"
)

// newTestDB creates a temporary store for testing.
func newTestDB(t *testing.T) (*store.Store, func()) {
	t.Helper()
	cfg, err := store.DefaultConfig()
	if err != nil {
		t.Fatalf("DefaultConfig: %v", err)
	}
	cfg.DataDir = t.TempDir()
	s, err := store.New(cfg)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	return s, func() { s.Close() }
}

func TestArchiveExpiredNone(t *testing.T) {
	s, cleanup := newTestDB(t)
	defer cleanup()

	n, err := ArchiveExpired(s, false)
	if err != nil {
		t.Fatalf("ArchiveExpired(false): %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 archived, got %d", n)
	}
}

func TestArchiveExpiredDryRun(t *testing.T) {
	s, cleanup := newTestDB(t)
	defer cleanup()

	// Insert a memory item with a past expires_at.
	id, err := s.AddMemory(store.AddMemoryParams{
		ProjectID: "test-proj",
		Kind:      "discovery",
		Scope:     "project",
		Title:     "Test expired",
		Body:      "This should expire",
		Tags:      []string{},
		Source:    "agent",
		ActorID:   "agent",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	// Manually set expires_at to yesterday.
	_, err = s.Exec(
		`UPDATE memory_items SET expires_at = datetime('now', '-1 day') WHERE id = ?`, id)
	if err != nil {
		t.Fatalf("set expires_at: %v", err)
	}

	// Dry run — count what would be archived.
	n, err := ArchiveExpired(s, true)
	if err != nil {
		t.Fatalf("ArchiveExpired(true): %v", err)
	}
	if n != 1 {
		t.Fatalf("dry-run: expected 1, got %d", n)
	}

	// Verify status is still active (dry run makes no changes).
	mem, err := s.GetMemory(id)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if mem.Status != "active" {
		t.Fatalf("dry-run: expected status=active, got %q", mem.Status)
	}
}

func TestArchiveExpiredActive(t *testing.T) {
	s, cleanup := newTestDB(t)
	defer cleanup()

	// Insert a memory item with no expires_at.
	_, err := s.AddMemory(store.AddMemoryParams{
		ProjectID: "test-proj",
		Kind:      "decision",
		Scope:     "project",
		Title:     "No expiry",
		Body:      "This should not expire",
		Tags:      []string{},
		Source:    "agent",
		ActorID:   "agent",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	n, err := ArchiveExpired(s, false)
	if err != nil {
		t.Fatalf("ArchiveExpired(false): %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 archived (no expires_at), got %d", n)
	}
}

func TestArchiveExpiredMultiple(t *testing.T) {
	s, cleanup := newTestDB(t)
	defer cleanup()

	// Insert 3 expired items.
	expiredIDs := make([]int64, 3)
	for i := 0; i < 3; i++ {
		id, err := s.AddMemory(store.AddMemoryParams{
			ProjectID: "test-proj",
			Kind:      "discovery",
			Scope:     "project",
			Title:     "Expired item",
			Body:      "Body",
			Tags:      []string{},
			Source:    "agent",
			ActorID:   "agent",
		})
		if err != nil {
			t.Fatalf("AddMemory: %v", err)
		}
		expiredIDs[i] = id
		_, _ = s.Exec(
			`UPDATE memory_items SET expires_at = datetime('now', '-1 day') WHERE id = ?`, id)
	}

	// Insert 1 active (non-expired) item.
	activeID, err := s.AddMemory(store.AddMemoryParams{
		ProjectID: "test-proj",
		Kind:      "decision",
		Scope:     "project",
		Title:     "Active item",
		Body:      "Body",
		Tags:      []string{},
		Source:    "agent",
		ActorID:   "agent",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	n, err := ArchiveExpired(s, false)
	if err != nil {
		t.Fatalf("ArchiveExpired: %v", err)
	}
	if n != 3 {
		t.Fatalf("expected 3 archived, got %d", n)
	}

	// Verify expired ones are archived.
	for _, id := range expiredIDs {
		mem, err := s.GetMemory(id)
		if err != nil {
			t.Fatalf("GetMemory(%d): %v", id, err)
		}
		if mem.Status != "archived" {
			t.Fatalf("id %d: expected status=archived, got %q", id, mem.Status)
		}
	}

	// Verify active one is still active.
	mem, err := s.GetMemory(activeID)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if mem.Status != "active" {
		t.Fatalf("active item: expected status=active, got %q", mem.Status)
	}
}

func TestIntegrityCheckOK(t *testing.T) {
	s, cleanup := newTestDB(t)
	defer cleanup()

	ok, result, err := IntegrityCheck(s)
	if err != nil {
		t.Fatalf("IntegrityCheck: %v", err)
	}
	if !ok {
		t.Fatalf("expected ok=true, got ok=false result=%q", result)
	}
	if result != "ok" {
		t.Fatalf("expected result='ok', got %q", result)
	}
}

func TestOptimizeFTS(t *testing.T) {
	s, cleanup := newTestDB(t)
	defer cleanup()

	// Add some data so FTS tables are populated.
	_, _ = s.AddMemory(store.AddMemoryParams{
		ProjectID: "test-proj",
		Kind:      "decision",
		Scope:     "project",
		Title:     "Test decision",
		Body:      "Test body content",
		Tags:      []string{"test"},
		Source:    "agent",
		ActorID:   "agent",
	})

	n, err := OptimizeFTS(s)
	if err != nil {
		t.Fatalf("OptimizeFTS: %v", err)
	}
	// Should optimize at least memory_items_fts.
	if n < 1 {
		t.Fatalf("expected at least 1 FTS table optimized, got %d", n)
	}

	// Verify store still works after optimize.
	stats, err := s.Stats()
	if err != nil {
		t.Fatalf("Stats after optimize: %v", err)
	}
	if stats.TotalSessions < 0 {
		t.Fatalf("Stats broken after optimize")
	}
}

func TestBackupCreatesFile(t *testing.T) {
	s, cleanup := newTestDB(t)
	defer cleanup()

	snapshotDir := t.TempDir()

	path, err := Backup(s, snapshotDir)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}

	// Verify file exists and is non-empty.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat backup file: %v", err)
	}
	if info.Size() == 0 {
		t.Fatalf("backup file is empty")
	}

	// Verify it's a gzip file.
	if !strings.HasSuffix(path, ".db.gz") {
		t.Fatalf("expected .db.gz suffix, got %q", path)
	}
}

func TestBackupIdempotentPerDay(t *testing.T) {
	s, cleanup := newTestDB(t)
	defer cleanup()

	snapshotDir := t.TempDir()

	path1, err := Backup(s, snapshotDir)
	if err != nil {
		t.Fatalf("Backup 1: %v", err)
	}
	path2, err := Backup(s, snapshotDir)
	if err != nil {
		t.Fatalf("Backup 2: %v", err)
	}

	// Same-day backup should overwrite (same filename).
	if path1 != path2 {
		t.Fatalf("expected same path on same day, got %q and %q", path1, path2)
	}

	// File should still exist (was overwritten, not duplicated).
	if _, err := os.Stat(path1); err != nil {
		t.Fatalf("backup file missing after overwrite: %v", err)
	}
}

func TestBackupRejectsEmptyDir(t *testing.T) {
	s, cleanup := newTestDB(t)
	defer cleanup()

	_, err := Backup(s, "")
	if err == nil {
		t.Fatalf("expected error for empty snapshot dir")
	}
}

func TestPruneOldSnapshots(t *testing.T) {
	snapshotDir := t.TempDir()

	// Create 10 fake snapshot files.
	// For entries 0-6 (oldest), set old mod times.
	// For entries 7-9 (newest), leave as recent.
	now := time.Now()
	for i := 0; i < 10; i++ {
		name := fmt.Sprintf("ohara-2026-01-%02d.db.gz", i+1)
		f, err := os.Create(filepath.Join(snapshotDir, name))
		if err != nil {
			t.Fatalf("create snapshot: %v", err)
		}
		f.Close()
		// Set older mod time for oldest 7 entries.
		if i < 7 {
			oldTime := now.AddDate(0, 0, -(10 - i))
			os.Chtimes(f.Name(), oldTime, oldTime)
		}
	}

	err := pruneOldSnapshots(snapshotDir, 7)
	if err != nil {
		t.Fatalf("pruneOldSnapshots: %v", err)
	}

	entries, _ := os.ReadDir(snapshotDir)
	if len(entries) > 7 {
		t.Fatalf("expected at most 7 entries after prune, got %d", len(entries))
	}
}

func TestRunDefaultOptions(t *testing.T) {
	cfg, err := store.DefaultConfig()
	if err != nil {
		t.Fatalf("DefaultConfig: %v", err)
	}
	cfg.DataDir = t.TempDir()
	s, err := store.New(cfg)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	opts := DefaultOptions(cfg.DataDir)
	if !opts.ArchiveExpired || !opts.Integrity || !opts.Backup || !opts.OptimizeFTS {
		t.Fatalf("DefaultOptions: expected all ops enabled")
	}
	if opts.RetainDays != 7 {
		t.Fatalf("DefaultOptions RetainDays: expected 7, got %d", opts.RetainDays)
	}
	if opts.DryRun {
		t.Fatalf("DefaultOptions DryRun: expected false")
	}

	stats, err := Run(s, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !stats.IntegrityOK {
		t.Fatalf("Run: integrity should be ok")
	}
	if stats.FTSSOptimized < 1 {
		t.Fatalf("Run: expected at least 1 FTS table optimized")
	}
	if stats.BackupPath == "" {
		t.Fatalf("Run: expected backup path")
	}
}

func TestRunDryRunOnly(t *testing.T) {
	cfg, err := store.DefaultConfig()
	if err != nil {
		t.Fatalf("DefaultConfig: %v", err)
	}
	cfg.DataDir = t.TempDir()
	s, err := store.New(cfg)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	opts := Options{
		ArchiveExpired: true,
		DryRun:         true,
	}

	stats, err := Run(s, opts)
	if err != nil {
		t.Fatalf("Run(dry-run): %v", err)
	}
	if stats.Archived != 0 {
		t.Fatalf("dry-run: expected 0 archived, got %d", stats.Archived)
	}
	if stats.BackupPath != "" {
		t.Fatalf("dry-run: expected no backup, got %q", stats.BackupPath)
	}
}

func TestRunSelective(t *testing.T) {
	cfg, err := store.DefaultConfig()
	if err != nil {
		t.Fatalf("DefaultConfig: %v", err)
	}
	cfg.DataDir = t.TempDir()
	s, err := store.New(cfg)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	opts := Options{
		Integrity:   true,
		OptimizeFTS: true,
		// ArchiveExpired=false, Backup=false
	}

	stats, err := Run(s, opts)
	if err != nil {
		t.Fatalf("Run(selective): %v", err)
	}
	if !stats.IntegrityOK {
		t.Fatalf("selective: integrity should be ok")
	}
	if stats.FTSSOptimized < 1 {
		t.Fatalf("selective: expected FTS optimized")
	}
	if stats.BackupPath != "" {
		t.Fatalf("selective: expected no backup")
	}
	if stats.Archived != 0 {
		t.Fatalf("selective: expected no archive")
	}
}
