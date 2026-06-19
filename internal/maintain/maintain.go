// Package maintain implements the maintenance lifecycle for Ohara's SQLite store.
// Follows the Ohara v2 spec section 5.3: archive expired, integrity check,
// backup, and FTS5 optimize.
package maintain

import (
	"compress/gzip"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultSnapshotDir is the default directory for database snapshots.
var DefaultSnapshotDir = "snapshots"

// DefaultRetainDays is the default number of daily snapshots to retain.
var DefaultRetainDays = 7

// DB is the interface subset of *sql.DB used by maintenance operations.
type DB interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// Stats holds the outcome of a maintenance run.
type Stats struct {
	Archived           int      `json:"archived"`
	IntegrityOK        bool     `json:"integrity_ok"`
	IntegrityResult    string   `json:"integrity_result,omitempty"`
	BackupPath         string   `json:"backup_path,omitempty"`
	FTSSOptimized      int      `json:"fts_optimized"`
	Decayed            int      `json:"decayed,omitempty"`
	Stale              int      `json:"stale,omitempty"`
	StaleCandidates    int      `json:"stale_candidates_archived,omitempty"`
	LowUtilityArchived int      `json:"low_utility_archived,omitempty"`
	Errors             []string `json:"errors,omitempty"`
}

// Options controls which maintenance operations run.
type Options struct {
	// ArchiveExpired runs the expired-item archival pass.
	ArchiveExpired bool
	// Integrity runs PRAGMA integrity_check.
	Integrity bool
	// Backup writes a gzipped SQLite snapshot.
	Backup bool
	// OptimizeFTS runs FTS5 optimize on all FTS tables.
	OptimizeFTS bool
	// Lifecycle runs decay, stale candidate archive, and low-utility archive.
	Lifecycle bool
	// LifecycleDecayAgeDays: max age in days before decay applies (default 90).
	LifecycleDecayAgeDays int
	// LifecycleDecayFactor: multiplier per pass (default 0.9).
	LifecycleDecayFactor float64
	// LifecycleStaleCandidateDays: max age in days for candidates (default 30).
	LifecycleStaleCandidateDays int
	// LifecycleMinUtilityWeight: floor below which memories are archived (default 0.05).
	LifecycleMinUtilityWeight float64
	// SnapshotDir is the directory to write snapshots into.
	// Defaults to "{dataDir}/snapshots".
	SnapshotDir string
	// RetainDays controls how many daily snapshots to keep.
	// Defaults to 7.
	RetainDays int
	// DataDir is the ohara data directory (used to build SnapshotDir).
	DataDir string
	// DryRun shows what would be archived without making changes.
	DryRun bool
}

// DefaultOptions returns an Options with all operations enabled.
func DefaultOptions(dataDir string) Options {
	return Options{
		ArchiveExpired:              true,
		Integrity:                   true,
		Backup:                      true,
		OptimizeFTS:                 true,
		Lifecycle:                   true,
		LifecycleDecayAgeDays:       90,
		LifecycleDecayFactor:        0.9,
		LifecycleStaleCandidateDays: 30,
		LifecycleMinUtilityWeight:   0.05,
		DataDir:                     dataDir,
		SnapshotDir:                 filepath.Join(dataDir, DefaultSnapshotDir),
		RetainDays:                  DefaultRetainDays,
		DryRun:                      false,
	}
}

// Run executes the configured maintenance operations on the given database.
func Run(db DB, opts Options) (*Stats, error) {
	stats := &Stats{}

	if opts.ArchiveExpired {
		n, err := ArchiveExpired(db, opts.DryRun)
		if err != nil {
			stats.Errors = append(stats.Errors, fmt.Sprintf("archive: %v", err))
		} else {
			stats.Archived = n
		}
	}

	if opts.Integrity {
		ok, result, err := IntegrityCheck(db)
		if err != nil {
			stats.Errors = append(stats.Errors, fmt.Sprintf("integrity: %v", err))
		} else {
			stats.IntegrityOK = ok
			stats.IntegrityResult = result
		}
	}

	if opts.OptimizeFTS {
		n, err := OptimizeFTS(db)
		if err != nil {
			stats.Errors = append(stats.Errors, fmt.Sprintf("fts optimize: %v", err))
		} else {
			stats.FTSSOptimized = n
		}
	}

	if opts.Lifecycle {
		decayed, stale, err := RunLifecycle(db, opts)
		if err != nil {
			stats.Errors = append(stats.Errors, fmt.Sprintf("lifecycle: %v", err))
		} else {
			stats.Decayed = decayed
			stats.Stale = stale
		}

		// Archive stale candidates (unreviewed candidate memories past their shelf life).
		if n, err := ArchiveStaleCandidates(db, opts.LifecycleStaleCandidateDays, opts.DryRun); err != nil {
			stats.Errors = append(stats.Errors, fmt.Sprintf("stale candidates: %v", err))
		} else {
			stats.StaleCandidates = n
		}

		// Archive low-utility memories (decayed below threshold).
		if n, err := ArchiveLowUtilityMemories(db, opts.LifecycleMinUtilityWeight, opts.DryRun); err != nil {
			stats.Errors = append(stats.Errors, fmt.Sprintf("low utility: %v", err))
		} else {
			stats.LowUtilityArchived = n
		}
	}

	if opts.Backup {
		snapshotDir := opts.SnapshotDir
		if snapshotDir == "" && opts.DataDir != "" {
			snapshotDir = filepath.Join(opts.DataDir, DefaultSnapshotDir)
		}
		path, err := Backup(db, snapshotDir)
		if err != nil {
			stats.Errors = append(stats.Errors, fmt.Sprintf("backup: %v", err))
		} else {
			stats.BackupPath = path
		}

		// Prune old snapshots after successful backup.
		if err == nil && opts.RetainDays > 0 {
			_ = pruneOldSnapshots(snapshotDir, opts.RetainDays)
		}
	}

	if len(stats.Errors) > 0 {
		return stats, fmt.Errorf("maintenance completed with errors")
	}
	return stats, nil
}

// ArchiveExpired marks memory items past their expires_at as 'archived'.
// If dryRun is true, returns the count without making changes.
func ArchiveExpired(db DB, dryRun bool) (int, error) {
	if dryRun {
		var count int
		err := db.QueryRow(
			`SELECT COUNT(*) FROM memory_items
			 WHERE status = 'active'
			   AND expires_at IS NOT NULL
			   AND expires_at != ''
			   AND datetime(expires_at) < datetime('now')
			   AND classification != 'foundational'`,
		).Scan(&count)
		return count, err
	}

	result, err := db.Exec(
		`UPDATE memory_items
		 SET status = 'archived',
		     updated_at = strftime('%Y-%m-%dT%H:%M:%f','now')
		 WHERE status = 'active'
		   AND expires_at IS NOT NULL
		   AND expires_at != ''
		   AND datetime(expires_at) < datetime('now')
		   AND classification != 'foundational'`,
	)
	if err != nil {
		return 0, fmt.Errorf("archive expired memories: %w", err)
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}

// IntegrityCheck runs SQLite's PRAGMA integrity_check and returns the result.
func IntegrityCheck(db DB) (ok bool, result string, err error) {
	err = db.QueryRow(`PRAGMA integrity_check`).Scan(&result)
	if err != nil {
		return false, "", fmt.Errorf("integrity check: %w", err)
	}
	return strings.TrimSpace(result) == "ok", result, nil
}

// OptimizeFTS runs the FTS5 'optimize' command on all FTS virtual tables.
// Returns the number of FTS tables optimized.
func OptimizeFTS(db DB) (int, error) {
	// Discover all FTS5 virtual tables.
	rows, err := db.Query(
		`SELECT name FROM sqlite_master WHERE type='table' AND sql LIKE '%fts5%'`,
	)
	if err != nil {
		return 0, fmt.Errorf("discover fts tables: %w", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return 0, fmt.Errorf("scan fts name: %w", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("rows iteration: %w", err)
	}

	optimized := 0
	for _, table := range tables {
		// FTS5 'optimize' command merges b-tree segments.
		// We use INSERT INTO ... VALUES('optimize') which is the portable way.
		_, err := db.Exec(fmt.Sprintf(`INSERT INTO %s(%s) VALUES('optimize')`, table, table))
		if err != nil {
			// Log but don't fail — some FTS tables may not support optimize.
			// FTS5 optimize returns no rows and is idempotent.
			continue
		}
		optimized++
	}

	return optimized, nil
}

// Backup creates a gzipped SQLite snapshot in snapshotDir.
// Returns the path to the created snapshot file.
func Backup(db DB, snapshotDir string) (string, error) {
	if snapshotDir == "" {
		return "", fmt.Errorf("snapshot dir is required")
	}

	if err := os.MkdirAll(snapshotDir, 0755); err != nil {
		return "", fmt.Errorf("create snapshot dir: %w", err)
	}

	date := time.Now().UTC().Format("2006-01-02")
	snapshotName := fmt.Sprintf("ohara-%s.db.gz", date)
	snapshotPath := filepath.Join(snapshotDir, snapshotName)

	// SQLite VACUUM INTO writes a consistent snapshot without requiring
	// a second connection. It also compacts the main database.
	// We write to a temp file first, then gzip it.
	tempPath := snapshotPath + ".tmp"
	if _, err := db.Exec(fmt.Sprintf(`VACUUM INTO '%s'`, tempPath)); err != nil {
		return "", fmt.Errorf("vacuum into: %w", err)
	}

	// Gzip the snapshot.
	outFile, err := os.Create(snapshotPath)
	if err != nil {
		os.Remove(tempPath)
		return "", fmt.Errorf("create snapshot file: %w", err)
	}
	defer outFile.Close()

	gzWriter := gzip.NewWriter(outFile)
	gzWriter.Name = filepath.Base(tempPath)

	inFile, err := os.Open(tempPath)
	if err != nil {
		os.Remove(tempPath)
		outFile.Close()
		os.Remove(snapshotPath)
		return "", fmt.Errorf("open temp snapshot: %w", err)
	}

	if _, err := io.Copy(gzWriter, inFile); err != nil {
		inFile.Close()
		os.Remove(tempPath)
		outFile.Close()
		os.Remove(snapshotPath)
		return "", fmt.Errorf("gzip snapshot: %w", err)
	}

	if err := inFile.Close(); err != nil {
		os.Remove(tempPath)
		outFile.Close()
		os.Remove(snapshotPath)
		return "", fmt.Errorf("close temp snapshot: %w", err)
	}

	if err := gzWriter.Close(); err != nil {
		os.Remove(snapshotPath)
		return "", fmt.Errorf("close gzip: %w", err)
	}

	if err := outFile.Close(); err != nil {
		os.Remove(snapshotPath)
		return "", fmt.Errorf("close snapshot: %w", err)
	}

	os.Remove(tempPath)
	return snapshotPath, nil
}

// lifecycleDB is the interface subset needed by lifecycle operations.
// It extends DB with QueryRowContext capability.
type lifecycleDB interface {
	DB
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// RunLifecycle runs the memory lifecycle pass: utility_weight decay.
// It requires the underlying DB to have a DecayMemories-compatible schema
// (memory_items table with utility_weight and classification columns).
// Returns (decayedCount, staleCount).
func RunLifecycle(db lifecycleDB, opts Options) (int, int, error) {
	if opts.LifecycleDecayAgeDays <= 0 {
		opts.LifecycleDecayAgeDays = 90
	}
	if opts.LifecycleDecayFactor <= 0 || opts.LifecycleDecayFactor > 1 {
		opts.LifecycleDecayFactor = 0.9
	}

	if opts.DryRun {
		// Count decayable using most recent of updated_at or last_accessed.
		var decayable int
		err := db.QueryRow(
			`SELECT COUNT(*) FROM memory_items
			 WHERE status = 'active'
			   AND classification != 'foundational'
			   AND utility_weight > 0
			   AND datetime(COALESCE(NULLIF(last_accessed, ''), updated_at)) < datetime('now', ?)`,
			fmt.Sprintf("-%d days", opts.LifecycleDecayAgeDays),
		).Scan(&decayable)
		if err != nil {
			return 0, 0, fmt.Errorf("lifecycle dry-run count: %w", err)
		}

		// Count stale after decay.
		minWeight := opts.LifecycleMinUtilityWeight
		if minWeight <= 0 {
			minWeight = 0.1
		}
		var stale int
		err = db.QueryRow(
			`SELECT COUNT(*) FROM memory_items
			 WHERE status = 'active'
			   AND classification != 'foundational'
			   AND utility_weight > 0
			   AND utility_weight * ? < ?
			   AND datetime(COALESCE(NULLIF(last_accessed, ''), updated_at)) < datetime('now', ?)`,
			opts.LifecycleDecayFactor, minWeight,
			fmt.Sprintf("-%d days", opts.LifecycleDecayAgeDays),
		).Scan(&stale)
		if err != nil {
			return 0, 0, fmt.Errorf("lifecycle stale dry-run: %w", err)
		}
		return decayable, stale, nil
	}

	// Apply decay using most recent of updated_at or last_accessed.
	res, err := db.Exec(
		`UPDATE memory_items
		 SET utility_weight = ROUND(utility_weight * ?, 4),
		     updated_at = strftime('%Y-%m-%dT%H:%M:%f','now')
		 WHERE status = 'active'
		   AND classification != 'foundational'
		   AND utility_weight > 0
		   AND datetime(COALESCE(NULLIF(last_accessed, ''), updated_at)) < datetime('now', ?)`,
		opts.LifecycleDecayFactor,
		fmt.Sprintf("-%d days", opts.LifecycleDecayAgeDays),
	)
	if err != nil {
		return 0, 0, fmt.Errorf("lifecycle decay: %w", err)
	}
	decayed, _ := res.RowsAffected()

	// Count stale.
	minWeight := opts.LifecycleMinUtilityWeight
	if minWeight <= 0 {
		minWeight = 0.1
	}
	var stale int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM memory_items
		 WHERE status = 'active'
		   AND classification != 'foundational'
		   AND utility_weight > 0
		   AND utility_weight < ?`,
		minWeight,
	).Scan(&stale)
	if err != nil {
		return int(decayed), 0, fmt.Errorf("lifecycle stale count: %w", err)
	}

	return int(decayed), stale, nil
}

// ArchiveStaleCandidates archives candidate-status memories older than maxAgeDays.
func ArchiveStaleCandidates(db lifecycleDB, maxAgeDays int, dryRun bool) (int, error) {
	if maxAgeDays <= 0 {
		maxAgeDays = 30
	}
	ageExpr := fmt.Sprintf("-%d days", maxAgeDays)

	if dryRun {
		var count int
		err := db.QueryRow(
			`SELECT COUNT(*) FROM memory_items
			 WHERE status = 'candidate'
			   AND datetime(COALESCE(NULLIF(last_accessed, ''), updated_at)) < datetime('now', ?)`,
			ageExpr,
		).Scan(&count)
		return count, err
	}

	res, err := db.Exec(
		`UPDATE memory_items
		 SET status = 'archived',
		     updated_at = strftime('%Y-%m-%dT%H:%M:%f','now')
		 WHERE status = 'candidate'
		   AND datetime(COALESCE(NULLIF(last_accessed, ''), updated_at)) < datetime('now', ?)`,
		ageExpr,
	)
	if err != nil {
		return 0, fmt.Errorf("archive stale candidates: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ArchiveLowUtilityMemories archives active, non-foundational memories below minWeight.
func ArchiveLowUtilityMemories(db lifecycleDB, minWeight float64, dryRun bool) (int, error) {
	if minWeight <= 0 {
		minWeight = 0.05
	}

	if dryRun {
		var count int
		err := db.QueryRow(
			`SELECT COUNT(*) FROM memory_items
			 WHERE status = 'active'
			   AND classification != 'foundational'
			   AND utility_weight > 0
			   AND utility_weight < ?`,
			minWeight,
		).Scan(&count)
		return count, err
	}

	res, err := db.Exec(
		`UPDATE memory_items
		 SET status = 'archived',
		     updated_at = strftime('%Y-%m-%dT%H:%M:%f','now')
		 WHERE status = 'active'
		   AND classification != 'foundational'
		   AND utility_weight > 0
		   AND utility_weight < ?`,
		minWeight,
	)
	if err != nil {
		return 0, fmt.Errorf("archive low utility: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// pruneOldSnapshots removes gzipped snapshots older than retainDays.
func pruneOldSnapshots(dir string, retainDays int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read snapshot dir: %w", err)
	}

	var snapshots []os.FileInfo
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasPrefix(entry.Name(), "ohara-") || !strings.HasSuffix(entry.Name(), ".db.gz") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		snapshots = append(snapshots, info)
	}

	if len(snapshots) <= retainDays {
		return nil
	}

	// Sort by mod time ascending (oldest first).
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].ModTime().Before(snapshots[j].ModTime())
	})

	// Remove all but the newest retainDays.
	toRemove := len(snapshots) - retainDays
	for i := 0; i < toRemove; i++ {
		path := filepath.Join(dir, snapshots[i].Name())
		if err := os.Remove(path); err != nil {
			// Log but continue pruning other snapshots.
			continue
		}
	}
	return nil
}
