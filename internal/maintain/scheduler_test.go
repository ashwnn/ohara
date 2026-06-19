package maintain

import (
	"testing"
	"time"

	"github.com/ashwnn/ohara/internal/store"
)

func TestNewSchedulerDefaults(t *testing.T) {
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
	sched := NewScheduler(SchedulerConfig{
		DB:      s,
		Options: opts,
	})
	if sched == nil {
		t.Fatal("expected non-nil scheduler")
	}
	if sched.interval != DefaultSchedulerInterval {
		t.Fatalf("expected default interval %v, got %v", DefaultSchedulerInterval, sched.interval)
	}
	if sched.Running() {
		t.Fatal("scheduler should not be running before Start()")
	}
}

func TestSchedulerStartStop(t *testing.T) {
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
		Integrity:      true,
		Lifecycle:      true,
	}
	sched := NewScheduler(SchedulerConfig{
		DB:       s,
		Options:  opts,
		Interval: 100 * time.Millisecond,
	})

	sched.Start()
	if !sched.Running() {
		t.Fatal("expected scheduler to be running after Start()")
	}

	// Start again should be a no-op.
	sched.Start()

	// Allow one tick.
	time.Sleep(200 * time.Millisecond)

	sched.Stop()
	if sched.Running() {
		t.Fatal("expected scheduler to be stopped after Stop()")
	}

	// Stop again should be a no-op.
	sched.Stop()
}

func TestSchedulerRunNow(t *testing.T) {
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
	sched := NewScheduler(SchedulerConfig{
		DB:       s,
		Options:  opts,
		Interval: time.Hour, // Long interval so RunNow is the only run
	})

	// RunNow before Start should work.
	sched.RunNow()

	sched.Start()
	// RunNow while running should also work.
	sched.RunNow()
	sched.Stop()
}

func TestSchedulerWithCustomInterval(t *testing.T) {
	cfg := SchedulerConfig{
		Interval: 30 * time.Minute,
	}
	sched := NewScheduler(cfg)
	if sched.interval != 30*time.Minute {
		t.Fatalf("expected 30m interval, got %v", sched.interval)
	}

	// Zero interval defaults.
	cfg.Interval = 0
	sched = NewScheduler(cfg)
	if sched.interval != DefaultSchedulerInterval {
		t.Fatalf("expected default interval for zero value, got %v", sched.interval)
	}
}

func TestSchedulerLifecycleRunOnce(t *testing.T) {
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

	// Add a memory that will be decayed.
	id, err := s.AddMemory(store.AddMemoryParams{
		ProjectID:      "ohara",
		Kind:           "pattern",
		Title:          "Old decayable memory",
		Body:           "Body",
		UtilityWeight:  1.0,
		Classification: "tactical",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}
	_, err = s.Exec(`UPDATE memory_items SET updated_at = datetime('now', '-100 days'), last_accessed = NULL WHERE id = ?`, id)
	if err != nil {
		t.Fatalf("age memory: %v", err)
	}

	opts := Options{
		ArchiveExpired:          true,
		Integrity:               true,
		OptimizeFTS:             false,
		Backup:                  false,
		Lifecycle:               true,
		LifecycleDecayAgeDays:   60,
		LifecycleDecayFactor:    0.5,
		LifecycleMinUtilityWeight: 0.1,
	}

	stats, err := Run(s, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Decayed != 1 {
		t.Fatalf("expected 1 decayed, got %d", stats.Decayed)
	}
	if !stats.IntegrityOK {
		t.Fatal("expected integrity ok")
	}
}
