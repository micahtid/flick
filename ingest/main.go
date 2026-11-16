// Package main is the Flick live ingest service. FFmpeg waits for OBS to send
// a stream over RTMP (the protocol streaming apps use) and turns it into HLS
// (HTTP Live Streaming) as it comes in: one quality, cut into 2-second
// segments. Each segment is saved to the storage nodes as soon as it's
// finished, so viewers watch less than 10 seconds behind. When the stream
// ends, it becomes a normal video that anyone can replay.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/micahtid/flick/pkg/chunk"
	"github.com/micahtid/flick/pkg/metrics"
)

// main runs the ingest service and exits with an error message if it fails.
func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run starts the ingest service and keeps it running until it's told to
// stop. It returns errors instead of exiting, so cleanup such as closing the
// database always happens.
func run() error {
	// ctx is cancelled when the program is told to stop, by Ctrl+C or by
	// SIGTERM. Docker sends SIGTERM when it stops a container.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Read the settings from environment variables set in docker-compose.yml.
	storageNodes := getenv("STORAGE_NODES", "http://node1:9001,http://node2:9002,http://node3:9003")
	metricsPort := getenv("METRICS_PORT", "8083")

	db, err := connectDB(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer db.Close()

	videos := &videoStore{db: db}
	removeCutOffStreams(ctx, videos)

	in := &ingester{
		videos: videos,
		files:  chunk.NewStore(db, chunk.New(storageNodes)),
		record: record,
		poster: makePoster,
	}

	// The ingest service only speaks RTMP, so it runs a small HTTP server in
	// the background for Prometheus. A failure is only logged, since streams
	// matter more than the metrics.
	go func() {
		if err := metrics.Serve(ctx, metricsPort); err != nil {
			log.Printf("metrics server: %v", err)
		}
	}()

	log.Printf("ingest waiting for OBS at %s", rtmpURL)
	in.run(ctx)
	log.Print("shutting down")
	return nil
}

// removeCutOffStreams removes any stream still marked live. Such a stream was
// cut off when the service crashed, and its list of segments went with the
// temporary folder, so it can't be finished. A failure is only logged, since
// it shouldn't stop new streams.
func removeCutOffStreams(ctx context.Context, videos *videoStore) {
	n, err := videos.DeleteLive(ctx)
	if err != nil {
		log.Printf("remove cut-off streams: %v", err)
		return
	}
	if n > 0 {
		log.Printf("removed %d cut-off streams", n)
	}
}

// getenv returns an environment variable, or fallback if it isn't set.
func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
