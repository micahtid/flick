// Package main is the Flick conversion worker. It claims jobs from the queue
// one at a time and converts each video to HLS with FFmpeg: 1080p, 720p and
// 360p versions cut into 4-second segments, for adaptive streaming. Run
// several copies to convert several videos at once.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// main runs the worker and exits with an error message if it fails.
func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run starts the worker and keeps it working until it is told to stop. It
// returns errors instead of exiting so cleanup (like closing the database)
// always happens.
func run() error {
	// ctx is cancelled when the program is told to stop: Ctrl+C, or SIGTERM,
	// which is what `docker compose down` and `docker stop` send.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Read the settings from environment variables, which docker-compose.yml sets.
	queueURL := getenv("QUEUE_URL", "http://queue:8081")
	dataDir := getenv("DATA_DIR", "/data") // This is where the API saves uploads.

	db, err := connectDB(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer db.Close()

	w := &worker{
		// Give up on a queue request after 10 seconds, so a stuck queue can't
		// stop the worker for good.
		queue:   &queueClient{url: queueURL, http: &http.Client{Timeout: 10 * time.Second}},
		videos:  &videoStore{db: db},
		dataDir: dataDir,
		convert: convertToHLS,
	}

	log.Printf("worker started, asking %s for jobs", queueURL)
	w.run(ctx)
	log.Print("shutting down")
	return nil
}

// getenv returns an environment variable, or fallback if it isn't set.
func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
