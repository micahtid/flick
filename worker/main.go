// Package main is the Flick conversion worker. It claims jobs from the queue
// one at a time and converts each video to HLS with FFmpeg: up to 3 qualities
// (1080p, 720p and 360p), cut into 4-second segments, so the player can switch
// quality to suit the connection. Uploads are read from the storage nodes, and
// the HLS files are saved back to them. Run several copies to convert several
// videos at once.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/micahtid/flick/pkg/chunk"
	"github.com/micahtid/flick/pkg/metrics"
)

// main runs the worker and exits with an error message if it fails.
func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run starts the worker and keeps it running until it's told to stop. It
// returns errors instead of exiting, so cleanup such as closing the database
// always happens.
func run() error {
	// ctx is cancelled when the program is told to stop, by Ctrl+C or by
	// SIGTERM. Docker sends SIGTERM when it stops a container.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Read the settings from environment variables set in docker-compose.yml.
	queueURL := getenv("QUEUE_URL", "http://queue:8081")
	storageNodes := getenv("STORAGE_NODES", "http://node1:9001,http://node2:9002,http://node3:9003")
	metricsPort := getenv("METRICS_PORT", "8090")

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
		files:   chunk.NewStore(db, chunk.New(storageNodes)),
		convert: convertToHLS,
	}

	// The worker has no server of its own, so it runs a small one in the
	// background for Prometheus. A failure is only logged, since the work
	// matters more than the metrics.
	go func() {
		if err := metrics.Serve(ctx, metricsPort); err != nil {
			log.Printf("metrics server: %v", err)
		}
	}()

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
