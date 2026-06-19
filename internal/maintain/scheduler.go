// Package maintain implements the maintenance lifecycle for Ohara's SQLite store.
// scheduler.go provides an opt-in background scheduler that runs maintenance
// at configurable intervals.
package maintain

import (
	"log"
	"sync"
	"time"
)

// Scheduler runs periodic maintenance operations on a database.
// It is opt-in: create one, call Start(), and stop with Stop().
type Scheduler struct {
	opts     Options
	db       DB
	interval time.Duration
	stopCh   chan struct{}
	wg       sync.WaitGroup
	mu       sync.Mutex
	running  bool
}

// SchedulerConfig holds the configuration for creating a new Scheduler.
type SchedulerConfig struct {
	// DB is the database to run maintenance on.
	DB DB
	// Options are the maintenance options to use on each tick.
	Options Options
	// Interval is the time between maintenance runs.
	Interval time.Duration
}

// DefaultSchedulerInterval is the default interval between maintenance runs.
const DefaultSchedulerInterval = 60 * time.Minute

// NewScheduler creates a new Scheduler. It does not start until Start() is called.
func NewScheduler(cfg SchedulerConfig) *Scheduler {
	interval := cfg.Interval
	if interval <= 0 {
		interval = DefaultSchedulerInterval
	}
	return &Scheduler{
		opts:     cfg.Options,
		db:       cfg.DB,
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

// Start begins running maintenance on a background goroutine at the configured
// interval. It is safe to call multiple times — subsequent calls are no-ops.
func (s *Scheduler) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return
	}
	s.running = true
	s.wg.Add(1)
	go s.loop()
	log.Printf("[maintain] scheduler started (interval=%v)", s.interval)
}

// Stop signals the scheduler to stop after the current maintenance run completes.
// It waits for the goroutine to exit. Safe to call multiple times.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	s.mu.Unlock()

	close(s.stopCh)
	s.wg.Wait()
	log.Printf("[maintain] scheduler stopped")
}

// Running returns true if the scheduler is currently running.
func (s *Scheduler) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// RunNow triggers an immediate maintenance run on the calling goroutine.
// It is safe to call regardless of whether the scheduler is running.
func (s *Scheduler) RunNow() {
	s.runOnce()
}

func (s *Scheduler) loop() {
	defer s.wg.Done()

	// Run once immediately on start.
	s.runOnce()

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.runOnce()
		case <-s.stopCh:
			return
		}
	}
}

func (s *Scheduler) runOnce() {
	stats, err := Run(s.db, s.opts)
	if err != nil {
		log.Printf("[maintain] scheduler run completed with errors: %v", err)
	} else {
		log.Printf("[maintain] scheduler run ok: archived=%d decayed=%d stale=%d candidates=%d lowutil=%d integrity=%v fts=%d",
			stats.Archived, stats.Decayed, stats.Stale, stats.StaleCandidates, stats.LowUtilityArchived, stats.IntegrityOK, stats.FTSSOptimized)
	}
}
