package store

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func TestAddMemoryEnqueuesDerivedJobsAndRunnerDrains(t *testing.T) {
	s := newTestStore(t)

	memID, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Adopt durable jobs",
		Body:      "Derived indexing should be async and durable.",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	var pending int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM memory_jobs WHERE memory_id = ? AND status = 'pending'`, memID).Scan(&pending); err != nil {
		t.Fatalf("count pending jobs: %v", err)
	}
	if pending != 4 {
		t.Fatalf("expected 4 pending jobs, got %d", pending)
	}

	processed, err := s.RunJobsOnce(10)
	if err != nil {
		t.Fatalf("RunJobsOnce: %v", err)
	}
	if processed == 0 {
		t.Fatal("expected jobs to be processed")
	}

	var done int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM memory_jobs WHERE memory_id = ? AND status = 'done'`, memID).Scan(&done); err != nil {
		t.Fatalf("count done jobs: %v", err)
	}
	if done != 4 {
		t.Fatalf("expected 4 done jobs, got %d", done)
	}
}

func TestAddMemoryRollsBackWhenJobEnqueueFails(t *testing.T) {
	s := newTestStore(t)

	origExec := s.hooks.exec
	defer func() { s.hooks.exec = origExec }()
	s.hooks.exec = func(db execer, query string, args ...any) (sql.Result, error) {
		if strings.Contains(query, "INSERT INTO memory_jobs") {
			return nil, errors.New("forced enqueue failure")
		}
		return origExec(db, query, args...)
	}

	_, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Should rollback",
		Body:      "rollback test",
	})
	if err == nil {
		t.Fatal("expected AddMemory to fail when enqueue fails")
	}

	var count int
	if scanErr := s.db.QueryRow(`SELECT COUNT(*) FROM memory_items WHERE title = 'Should rollback'`).Scan(&count); scanErr != nil {
		t.Fatalf("count memory_items: %v", scanErr)
	}
	if count != 0 {
		t.Fatalf("expected memory insert rollback, found %d rows", count)
	}
}

func TestRunJobsOnceRecordsAttemptsAndLastError(t *testing.T) {
	cfg := mustDefaultConfig(t)
	cfg.DataDir = t.TempDir()
	cfg.RetrievalMode = "hybrid"
	cfg.EmbeddingBackend = "ollama"
	cfg.OllamaURL = "http://127.0.0.1:1"

	s, err := New(cfg)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	memID, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Embedding failure test",
		Body:      "This should enqueue embed job and fail gracefully.",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	if _, err := s.Exec(`UPDATE memory_jobs SET status = 'pending', attempts = 0, available_at = strftime('%Y-%m-%dT%H:%M:%f','now') WHERE memory_id = ? AND job_type = ?`, memID, JobTypeEmbedMemory); err != nil {
		t.Fatalf("prepare embed job: %v", err)
	}
	if _, err := s.Exec(`UPDATE memory_jobs SET status = 'done' WHERE memory_id = ? AND job_type != ?`, memID, JobTypeEmbedMemory); err != nil {
		t.Fatalf("prepare non-embed jobs: %v", err)
	}

	processed, err := s.RunJobsOnce(1)
	if err != nil {
		t.Fatalf("RunJobsOnce: %v", err)
	}
	if processed != 1 {
		t.Fatalf("expected 1 processed job, got %d", processed)
	}

	var status string
	var attempts int
	var lastErr string
	if err := s.db.QueryRow(`SELECT status, attempts, COALESCE(last_error,'') FROM memory_jobs WHERE memory_id = ? AND job_type = ?`, memID, JobTypeEmbedMemory).Scan(&status, &attempts, &lastErr); err != nil {
		t.Fatalf("select job state: %v", err)
	}
	if status != jobStatusRetry {
		t.Fatalf("expected retry status, got %q", status)
	}
	if attempts != 1 {
		t.Fatalf("expected attempts=1, got %d", attempts)
	}
	if strings.TrimSpace(lastErr) == "" {
		t.Fatal("expected last_error to be recorded")
	}
}

func TestPendingJobsSurviveRestartAndDrain(t *testing.T) {
	cfg := mustDefaultConfig(t)
	cfg.DataDir = t.TempDir()

	s1, err := New(cfg)
	if err != nil {
		t.Fatalf("new store s1: %v", err)
	}
	memID, err := s1.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindPattern,
		Title:     "Restart durability",
		Body:      "pending jobs should survive restart.",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	var pendingBefore int
	if err := s1.db.QueryRow(`SELECT COUNT(*) FROM memory_jobs WHERE memory_id = ? AND status = 'pending'`, memID).Scan(&pendingBefore); err != nil {
		t.Fatalf("count pending before close: %v", err)
	}
	if pendingBefore == 0 {
		t.Fatal("expected pending jobs before restart")
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("close s1: %v", err)
	}

	s2, err := New(cfg)
	if err != nil {
		t.Fatalf("new store s2: %v", err)
	}
	defer s2.Close()

	processed, err := s2.RunJobsOnce(20)
	if err != nil {
		t.Fatalf("RunJobsOnce after restart: %v", err)
	}
	if processed == 0 {
		t.Fatal("expected pending jobs to drain after restart")
	}
}
