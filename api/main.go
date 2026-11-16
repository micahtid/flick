// Package main is the Flick API. It saves uploaded videos on the storage
// nodes, records them in Postgres, asks the queue to convert them, and lists
// them. It also runs live chat, passing each message to every viewer through
// Redis, and counts views and watch time for the creator dashboard. Nginx
// forwards every /api request here with the /api part removed.
// The cache also fetches the HLS files from here.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/micahtid/flick/pkg/chunk"
	"github.com/redis/go-redis/v9"
)

// main runs the API and exits with an error message if it fails.
func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run starts the API and keeps it running until it is told to stop. It
// returns errors instead of exiting so cleanup (like closing the database)
// always happens.
func run() error {
	// ctx is cancelled when the program is told to stop: Ctrl+C, or SIGTERM,
	// which is what `docker compose down` sends.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Read the settings from environment variables, which docker-compose.yml sets.
	port := getenv("PORT", "8080")
	queueURL := getenv("QUEUE_URL", "http://queue:8081")
	storageNodes := getenv("STORAGE_NODES", "http://node1:9001,http://node2:9002,http://node3:9003")
	redisOpts, err := redis.ParseURL(getenv("REDIS_URL", "redis://redis:6379"))
	if err != nil {
		return fmt.Errorf("REDIS_URL: %w", err)
	}

	db, err := connectDB(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer db.Close()

	// Creating the Redis client doesn't connect yet. It connects when chat or
	// the view counter first uses it, so the rest of the API works even while
	// Redis is down.
	rdb := redis.NewClient(redisOpts)
	defer rdb.Close()

	// Give up on a queue request after 10 seconds, so a stuck queue can't hold
	// an upload open forever.
	jobs := &queueClient{url: queueURL, http: &http.Client{Timeout: 10 * time.Second}}

	s := newServer(&videoStore{db: db}, jobs, chunk.NewStore(db, chunk.New(storageNodes)), &redisHub{rdb: rdb}, &redisCounter{rdb: rdb})
	go s.flushLoop(ctx) // This runs in the background and stops when ctx is cancelled.

	srv := &http.Server{
		Addr:    ":" + port, // ":8080" means port 8080 on every network interface.
		Handler: s.routes(),
		// Drop clients that take over 10 seconds to send request headers. There
		// is no limit on the whole request, since uploads can take minutes.
		ReadHeaderTimeout: 10 * time.Second,
		// Close connections left idle for 2 minutes.
		IdleTimeout: 2 * time.Minute,
	}

	// Run the server in the background (a goroutine) so this function can also
	// wait for a stop signal. If the server fails, its error arrives on errc.
	errc := make(chan error, 1)
	go func() {
		log.Printf("api listening on :%s", port)
		errc <- srv.ListenAndServe()
	}()

	// Wait for whichever happens first: the server fails, or a stop signal.
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	// Stop accepting new requests, and give ones in progress up to 10 seconds
	// to finish.
	log.Print("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// getenv returns an environment variable, or fallback if it isn't set.
func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
