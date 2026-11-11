package main

// Everything that talks to Postgres: connecting, creating the table, and the
// two queries the API uses.

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// schema creates the videos table. IF NOT EXISTS makes it safe to run on every
// start. Each row is one video, and status is always "ready".
const schema = `
CREATE TABLE IF NOT EXISTS videos (
  id         UUID PRIMARY KEY,
  title      TEXT NOT NULL,
  status     TEXT NOT NULL DEFAULT 'ready',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`

// At startup, try to reach Postgres up to 10 times, 1 second apart.
const (
	connectAttempts = 10
	connectDelay    = time.Second
)

// connectDB connects to Postgres, waits until it's ready, and creates the
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

// Video is one row of the videos table. The `json:"..."` tags name each field
// in the JSON sent to the website.
type Video struct {
	ID        uuid.UUID `json:"id"`
	Title     string    `json:"title"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// videoStore runs the queries on the videos table.
type videoStore struct {
	db *pgxpool.Pool
}

// Create adds a video and returns it, including the status and upload time
// that Postgres fills in.
func (s *videoStore) Create(ctx context.Context, id uuid.UUID, title string) (Video, error) {
	v := Video{ID: id, Title: title}
	// $1 and $2 are placeholders that Postgres fills with id and title. Passing
	// values this way, never pasting them into the SQL text, prevents SQL
	// injection. RETURNING sends back the columns Postgres filled in, and Scan
	// copies them into v.
	err := s.db.QueryRow(ctx,
		`INSERT INTO videos (id, title) VALUES ($1, $2) RETURNING status, created_at`,
		id, title,
	).Scan(&v.Status, &v.CreatedAt)
	return v, err
}

// List returns every video, newest first.
func (s *videoStore) List(ctx context.Context) ([]Video, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, title, status, created_at FROM videos ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// An empty list rather than nil, so no videos is sent as [] instead of null.
	videos := []Video{}
	for rows.Next() {
		var v Video
		if err := rows.Scan(&v.ID, &v.Title, &v.Status, &v.CreatedAt); err != nil {
			return nil, err
		}
		videos = append(videos, v)
	}
	return videos, rows.Err() // rows.Err reports any error that stopped the loop early.
}
