package main

// This file talks to Postgres: it connects, and adds, finishes and removes
// streams. The API creates the videos table, so the ingest service only
// changes its rows.

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
// starts the ingest service before Postgres has finished starting, so the
// first few tries are expected to fail.
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

// pingWithRetry checks that Postgres answers, and tries again until it does
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

// CreateLive adds a stream as a video with the status "live", which tells the
// website to play it from its live playlist. ON CONFLICT makes adding the
// same stream twice do nothing, so a retry does no harm.
func (s *videoStore) CreateLive(ctx context.Context, id uuid.UUID, title string) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO videos (id, title, status) VALUES ($1, $2, 'live') ON CONFLICT (id) DO NOTHING`,
		id, title)
	return err
}

// MarkReady sets a video's status to "ready", which tells the website it can
// be played like any other video, and saves its length in seconds. NULLIF
// saves a length of 0, meaning unknown, as empty (NULL).
func (s *videoStore) MarkReady(ctx context.Context, id uuid.UUID, duration float64) error {
	_, err := s.db.Exec(ctx,
		`UPDATE videos SET status = 'ready', duration = NULLIF($2::double precision, 0) WHERE id = $1`,
		id, duration)
	return err
}

// DeleteLive removes every video still marked live, and returns how many it
// removed.
func (s *videoStore) DeleteLive(ctx context.Context) (int64, error) {
	tag, err := s.db.Exec(ctx, `DELETE FROM videos WHERE status = 'live'`)
	return tag.RowsAffected(), err
}
