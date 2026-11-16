package main

// This file talks to Postgres: it connects and marks videos ready or failed.
// The API creates the videos table, so the worker only updates it.

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These make startup try to reach Postgres up to 10 times, 1 second apart.
const (
	connectAttempts = 10
	connectDelay    = time.Second
)

// connectDB connects to Postgres and waits until it's ready. Docker often
// starts the worker before Postgres has finished starting, so the first few
// tries are expected to fail.
func connectDB(ctx context.Context, url string) (*pgxpool.Pool, error) {
	// A pool keeps several connections open and shares them. Creating it
	// doesn't connect yet. The ping below does.
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if err := pingWithRetry(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// pingWithRetry checks that Postgres answers and tries again until it does
// or the tries run out.
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

// videoStore runs the queries on the videos table.
type videoStore struct {
	db *pgxpool.Pool
}

// MarkReady sets a video's status to "ready", which tells the website it can
// be played and saves its length in seconds. NULLIF saves a length of 0,
// meaning unknown, as empty (NULL).
func (s *videoStore) MarkReady(ctx context.Context, id uuid.UUID, duration float64) error {
	_, err := s.db.Exec(ctx,
		`UPDATE videos SET status = 'ready', duration = NULLIF($2::double precision, 0) WHERE id = $1`,
		id, duration)
	return err
}

// MarkFailed sets a video's status to "failed", which tells the website it
// couldn't be converted.
func (s *videoStore) MarkFailed(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE videos SET status = 'failed' WHERE id = $1`, id)
	return err
}
