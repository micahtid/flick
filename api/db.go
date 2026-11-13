package main

// Everything that talks to Postgres: connecting, creating the table, and the
// queries the API uses.

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// schema creates the videos table, one statement at a time. IF NOT EXISTS
// makes each one safe to run on every start. Each row is one video. Its status
// is "processing" until a worker has converted the file, then "ready". The
// worker also fills in duration, the length in seconds.
//
// The ALTER adds duration to a table made before Phase 3. Videos converted
// before then have no duration, so it stays empty (NULL) for them.
var schema = []string{
	`CREATE TABLE IF NOT EXISTS videos (
  id         UUID PRIMARY KEY,
  title      TEXT NOT NULL,
  status     TEXT NOT NULL DEFAULT 'processing',
  duration   DOUBLE PRECISION,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`,
	`ALTER TABLE videos ADD COLUMN IF NOT EXISTS duration DOUBLE PRECISION`,
}

// At startup, try to reach Postgres up to 10 times, 1 second apart.
const (
	connectAttempts = 10
	connectDelay    = time.Second
)

// connectDB connects to Postgres, waits until it's ready, and sets up the
// table. Docker often starts the API before Postgres has finished starting,
// so the first few tries are expected to fail.
func connectDB(ctx context.Context, url string) (*pgxpool.Pool, error) {
	// A pool keeps several connections open and shares them between requests.
	// Creating it doesn't connect yet. The ping below does.
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err) // %w keeps the original error inside the new one.
	}

	if err := pingWithRetry(ctx, db); err != nil {
		db.Close()
		return nil, err
	}

	for _, stmt := range schema {
		if _, err := db.Exec(ctx, stmt); err != nil {
			db.Close()
			return nil, fmt.Errorf("create schema: %w", err)
		}
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

// Video is one row of the videos table. The `json:"..."` tags name each field
// in the JSON sent to the website. Duration is a pointer so it can be nil,
// sent as null, until a worker has measured the video.
type Video struct {
	ID        uuid.UUID `json:"id"`
	Title     string    `json:"title"`
	Status    string    `json:"status"`
	Duration  *float64  `json:"duration"`
	CreatedAt time.Time `json:"created_at"`
}

// videoStore runs the queries on the videos table.
type videoStore struct {
	db *pgxpool.Pool
}

// Create adds a video with the status "processing" and returns it, including
// the upload time that Postgres fills in.
func (s *videoStore) Create(ctx context.Context, id uuid.UUID, title string) (Video, error) {
	v := Video{ID: id, Title: title}
	// $1 and $2 are placeholders that Postgres fills with id and title. Passing
	// values this way, never pasting them into the SQL text, prevents SQL
	// injection. RETURNING sends back the columns Postgres filled in, and Scan
	// copies them into v.
	//
	// The status is set here, not left to the table's default, because a table
	// made in Phase 1 still has the old default of "ready".
	err := s.db.QueryRow(ctx,
		`INSERT INTO videos (id, title, status) VALUES ($1, $2, 'processing') RETURNING status, created_at`,
		id, title,
	).Scan(&v.Status, &v.CreatedAt)
	return v, err
}

// Delete removes a video's row. It's used to undo an upload that couldn't be
// queued for converting.
func (s *videoStore) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM videos WHERE id = $1`, id)
	return err
}

// List returns every video, newest first.
func (s *videoStore) List(ctx context.Context) ([]Video, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, title, status, duration, created_at FROM videos ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// An empty list rather than nil, so no videos is sent as [] instead of null.
	videos := []Video{}
	for rows.Next() {
		var v Video
		if err := rows.Scan(&v.ID, &v.Title, &v.Status, &v.Duration, &v.CreatedAt); err != nil {
			return nil, err
		}
		videos = append(videos, v)
	}
	return videos, rows.Err() // rows.Err reports any error that stopped the loop early.
}
