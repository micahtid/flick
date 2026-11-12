package main

// Everything that talks to Postgres: connecting, creating the table, and the
// queries the queue uses.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// schema creates the jobs table. IF NOT EXISTS makes it safe to run on every
// start. Each row is one job, and state is "waiting", "running" or "done".
// claimed_at stays empty (NULL) until a worker takes the job.
const schema = `
CREATE TABLE IF NOT EXISTS jobs (
  id         UUID PRIMARY KEY,
  video_id   UUID NOT NULL,
  state      TEXT NOT NULL DEFAULT 'waiting',
  claimed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`

// At startup, try to reach Postgres up to 10 times, 1 second apart.
const (
	connectAttempts = 10
	connectDelay    = time.Second
)

// connectDB connects to Postgres, waits until it's ready, and creates the
// table. Docker often starts the queue before Postgres has finished starting,
// so the first few tries are expected to fail.
func connectDB(ctx context.Context, url string) (*pgxpool.Pool, error) {
	// A pool keeps several connections open and shares them between requests.
	// Creating it doesn't connect yet. The ping below does.
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if err := pingWithRetry(ctx, db); err != nil {
		db.Close()
		return nil, err
	}

	if _, err := db.Exec(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	return db, nil
}

// pingWithRetry checks that Postgres answers, retrying until it does or the
// attempts run out.
func pingWithRetry(ctx context.Context, db *pgxpool.Pool) error {
	var err error
	for attempt := 1; attempt <= connectAttempts; attempt++ {
		if err = db.Ping(ctx); err == nil {
			return nil
		}
		log.Printf("database not ready (attempt %d/%d): %v", attempt, connectAttempts, err)

		// Wait before the next try, but give up straight away if the program
		// is told to stop.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(connectDelay):
		}
	}
	return fmt.Errorf("database unreachable after %d attempts: %w", connectAttempts, err)
}

// Job is one row of the jobs table. ClaimedAt is a pointer so it can be nil
// while no worker has taken the job, and omitempty leaves it out of the JSON
// then.
type Job struct {
	ID        uuid.UUID  `json:"id"`
	VideoID   uuid.UUID  `json:"video_id"`
	State     string     `json:"state"`
	ClaimedAt *time.Time `json:"claimed_at,omitempty"`
}

// jobStore runs the queries on the jobs table.
type jobStore struct {
	db *pgxpool.Pool
}

// Add puts a new waiting job at the back of the line and returns it.
func (s *jobStore) Add(ctx context.Context, videoID uuid.UUID) (Job, error) {
	j := Job{ID: uuid.New(), VideoID: videoID}
	err := s.db.QueryRow(ctx,
		`INSERT INTO jobs (id, video_id) VALUES ($1, $2) RETURNING state`,
		j.ID, videoID,
	).Scan(&j.State)
	return j, err
}

// Waiting counts the jobs that no worker has taken yet.
func (s *jobStore) Waiting(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE state = 'waiting'`).Scan(&n)
	return n, err
}

// Claim hands the oldest waiting job to the caller and marks it running. ok is
// false if no job is waiting.
func (s *jobStore) Claim(ctx context.Context) (job Job, ok bool, err error) {
	// The inner SELECT picks the oldest waiting job. FOR UPDATE locks that row,
	// and SKIP LOCKED makes a second worker asking at the same moment skip it
	// and take the next one, so two workers never get the same job.
	err = s.db.QueryRow(ctx, `
		UPDATE jobs SET state = 'running', claimed_at = now()
		WHERE id = (
		  SELECT id FROM jobs WHERE state = 'waiting'
		  ORDER BY created_at LIMIT 1
		  FOR UPDATE SKIP LOCKED
		)
		RETURNING id, video_id, state, claimed_at`,
	).Scan(&job.ID, &job.VideoID, &job.State, &job.ClaimedAt)
	if errors.Is(err, pgx.ErrNoRows) { // No row matched, so nothing is waiting.
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, err
	}
	return job, true, nil
}

// Done marks a job done. ok is false if no job has that ID.
func (s *jobStore) Done(ctx context.Context, id uuid.UUID) (ok bool, err error) {
	tag, err := s.db.Exec(ctx, `UPDATE jobs SET state = 'done' WHERE id = $1`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// Requeue puts every job that has been running for longer than timeout back
// in line, and returns how many it moved. Those jobs most likely belong to a
// worker that crashed.
func (s *jobStore) Requeue(ctx context.Context, timeout time.Duration) (int64, error) {
	// $1 * interval '1 second' turns a number of seconds into a Postgres
	// length of time.
	tag, err := s.db.Exec(ctx, `
		UPDATE jobs SET state = 'waiting'
		WHERE state = 'running' AND claimed_at < now() - $1 * interval '1 second'`,
		timeout.Seconds(),
	)
	return tag.RowsAffected(), err
}
