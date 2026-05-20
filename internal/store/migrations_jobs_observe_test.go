package store

import "testing"

func TestLatestMigrationsCreateObservationAndJobTables(t *testing.T) {
	s := newTestStore(t)

	var obsTableCount int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='session_observations'").Scan(&obsTableCount); err != nil {
		t.Fatalf("check session_observations table: %v", err)
	}
	if obsTableCount != 1 {
		t.Fatal("expected session_observations table to exist")
	}

	var jobsTableCount int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='memory_jobs'").Scan(&jobsTableCount); err != nil {
		t.Fatalf("check memory_jobs table: %v", err)
	}
	if jobsTableCount != 1 {
		t.Fatal("expected memory_jobs table to exist")
	}

	if got := s.SchemaVersion(); got < 27 {
		t.Fatalf("expected schema version >= 27, got %d", got)
	}
}
