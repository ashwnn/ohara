package store

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math"
	"strings"
	"time"
)

const (
	JobTypeEmbedMemory   = "embed_memory"
	JobTypeExtractEntity = "extract_entities"
	JobTypeLinkRelated   = "link_related"
	JobTypeScoreUtility  = "score_utility"
	JobTypeBackfillVec0  = "backfill_vec0"

	jobStatusPending = "pending"
	jobStatusRunning = "running"
	jobStatusRetry   = "retry"
	jobStatusDone    = "done"
	jobStatusFailed  = "failed"

	defaultJobRunLimit = 20
	maxJobAttempts     = 6

	// backfillBatchSize is the number of embedding rows per backfill job.
	backfillBatchSize = 500
)

// MemoryJob models a durable post-write derived processing task.
type MemoryJob struct {
	ID          int64
	MemoryID    int64
	JobType     string
	Status      string
	Attempts    int
	LastError   *string
	AvailableAt string
	CreatedAt   string
	UpdatedAt   string
}

func (s *Store) enqueueDerivedJobsTx(tx *sql.Tx, memoryID int64) error {
	jobTypes := []string{
		JobTypeEmbedMemory,
		JobTypeExtractEntity,
		JobTypeLinkRelated,
		JobTypeScoreUtility,
	}
	for _, jobType := range jobTypes {
		if _, err := s.execHook(tx,
			`INSERT INTO memory_jobs (memory_id, job_type, status)
			 VALUES (?, ?, ?)
			 ON CONFLICT(memory_id, job_type) DO UPDATE SET
			   status = CASE
			     WHEN memory_jobs.status IN ('done', 'failed') THEN excluded.status
			     ELSE memory_jobs.status
			   END,
			   attempts = CASE
			     WHEN memory_jobs.status IN ('done', 'failed') THEN 0
			     ELSE memory_jobs.attempts
			   END,
			   last_error = CASE
			     WHEN memory_jobs.status IN ('done', 'failed') THEN NULL
			     ELSE memory_jobs.last_error
			   END,
			   available_at = CASE
			     WHEN memory_jobs.status IN ('done', 'failed')
			       THEN strftime('%Y-%m-%dT%H:%M:%f','now')
			     ELSE memory_jobs.available_at
			   END,
			   updated_at = strftime('%Y-%m-%dT%H:%M:%f','now')`,
			memoryID, jobType, jobStatusPending,
		); err != nil {
			return err
		}
	}
	return nil
}

// enqueueBackfillBatch enqueues backfill_vec0 jobs for a batch of memory IDs.
// Used by migration 030 to backfill existing obs_embeddings into vec0.
func (s *Store) enqueueBackfillBatch(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	// Enqueue one job per memory ID within a transaction.
	return s.withTx(func(tx *sql.Tx) error {
		for _, id := range ids {
			if _, err := s.execHook(tx,
				`INSERT OR IGNORE INTO memory_jobs (memory_id, job_type, status)
				 VALUES (?, ?, ?)`,
				id, JobTypeBackfillVec0, jobStatusPending,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) startJobWorker() {
	if s.jobsStop != nil {
		return
	}
	s.jobsStop = make(chan struct{})
	s.jobsDone = make(chan struct{})
	stopCh := s.jobsStop
	go func() {
		defer close(s.jobsDone)
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				_, _ = s.RunJobsOnce(defaultJobRunLimit)
			}
		}
	}()
}

// RunJobsOnce drains available derived jobs synchronously.
func (s *Store) RunJobsOnce(limit int) (int, error) {
	if limit <= 0 {
		limit = defaultJobRunLimit
	}

	processed := 0
	for processed < limit {
		job, err := s.claimNextJob()
		if err != nil {
			return processed, err
		}
		if job == nil {
			break
		}

		err = s.executeMemoryJob(job)
		if err != nil {
			if markErr := s.markJobFailure(job, err); markErr != nil {
				return processed, markErr
			}
		} else {
			if markErr := s.markJobDone(job.ID); markErr != nil {
				return processed, markErr
			}
		}

		processed++
	}

	return processed, nil
}

func (s *Store) claimNextJob() (*MemoryJob, error) {
	var claimed *MemoryJob
	err := s.withTx(func(tx *sql.Tx) error {
		var j MemoryJob
		row := tx.QueryRow(
			`SELECT id, memory_id, job_type, status, attempts, last_error, available_at, created_at, updated_at
			 FROM memory_jobs
			 WHERE status IN (?, ?)
			   AND available_at <= strftime('%Y-%m-%dT%H:%M:%f','now')
			 ORDER BY available_at ASC, id ASC
			 LIMIT 1`,
			jobStatusPending, jobStatusRetry,
		)
		if err := row.Scan(&j.ID, &j.MemoryID, &j.JobType, &j.Status, &j.Attempts, &j.LastError, &j.AvailableAt, &j.CreatedAt, &j.UpdatedAt); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}

		res, err := s.execHook(tx,
			`UPDATE memory_jobs
			 SET status = ?, attempts = attempts + 1, updated_at = strftime('%Y-%m-%dT%H:%M:%f','now')
			 WHERE id = ? AND status IN (?, ?)`,
			jobStatusRunning, j.ID, jobStatusPending, jobStatusRetry,
		)
		if err != nil {
			return err
		}

		rows, _ := res.RowsAffected()
		if rows == 0 {
			return nil
		}

		j.Status = jobStatusRunning
		j.Attempts++
		claimed = &j
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

func (s *Store) executeMemoryJob(job *MemoryJob) error {
	switch job.JobType {
	case JobTypeEmbedMemory:
		if !s.hybridEnabled() {
			return nil
		}
		mem, err := s.GetMemory(job.MemoryID)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "not found") {
				return nil
			}
			return err
		}
		return s.indexMemoryEmbedding(mem.ID, mem.Title+"\n"+mem.Body)
	case JobTypeExtractEntity:
		mem, err := s.GetMemory(job.MemoryID)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "not found") {
				return nil
			}
			return err
		}
		names := ExtractEntitiesHeuristic(mem.Title + "\n" + mem.Body)
		if len(names) == 0 {
			return nil
		}
		_, err = s.AttachExtractedEntities(mem.ID, mem.ProjectID, names)
		return err
	case JobTypeLinkRelated:
		return nil
	case JobTypeScoreUtility:
		return nil
	case JobTypeBackfillVec0:
		// Backfill an existing obs_embeddings BLOB into the vec0 virtual table.
		// This is a one-shot job enqueued by migration 030 for existing embeddings.
		// The embedding dimension must match vec0Dim (768) to be inserted.
		var embeddingBlob []byte
		if err := s.db.QueryRow(
			`SELECT embedding FROM obs_embeddings WHERE obs_id = ?`, job.MemoryID,
		).Scan(&embeddingBlob); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil // embedding already deleted, nothing to backfill
			}
			return fmt.Errorf("backfill_vec0 read obs_embeddings: %w", err)
		}
		vec, err := bytesToFloats(embeddingBlob)
		if err != nil {
			log.Printf("[ohara] warning: backfill_vec0 invalid embedding blob for memory %d: %v", job.MemoryID, err)
			return nil // skip invalid blobs
		}
		if len(vec) != vec0Dim {
			// Non-768d embeddings (e.g. from test embedders) aren't stored in vec0.
			return nil
		}
		if _, err := s.execHook(s.db,
			`INSERT OR REPLACE INTO observation_embeddings_vec(rowid, embedding) VALUES (?, ?)`,
			job.MemoryID, embeddingBlob,
		); err != nil {
			return fmt.Errorf("backfill_vec0 insert: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unknown memory job type %q", job.JobType)
	}
}

func (s *Store) markJobDone(jobID int64) error {
	_, err := s.execHook(s.db,
		`UPDATE memory_jobs
		 SET status = ?, last_error = NULL, updated_at = strftime('%Y-%m-%dT%H:%M:%f','now')
		 WHERE id = ?`,
		jobStatusDone, jobID,
	)
	return err
}

func (s *Store) markJobFailure(job *MemoryJob, runErr error) error {
	nextStatus := jobStatusRetry
	if job.Attempts >= maxJobAttempts {
		nextStatus = jobStatusFailed
	}

	backoffSeconds := int(math.Pow(2, float64(max(0, job.Attempts-1)))) * 5
	if backoffSeconds > 900 {
		backoffSeconds = 900
	}
	availableAt := time.Now().UTC().Add(time.Duration(backoffSeconds) * time.Second).Format("2006-01-02T15:04:05.000")
	lastError := runErr.Error()
	if len(lastError) > 1500 {
		lastError = lastError[:1500] + "..."
	}

	_, err := s.execHook(s.db,
		`UPDATE memory_jobs
		 SET status = ?, last_error = ?, available_at = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%f','now')
		 WHERE id = ?`,
		nextStatus, lastError, availableAt, job.ID,
	)
	return err
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
