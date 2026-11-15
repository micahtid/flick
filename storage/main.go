// Package main is a Flick storage node. It saves chunks of files on its own
// disk and hands them back. Three nodes run at once, and the API and workers
// send every chunk to all of them, so losing one node loses nothing.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// main runs the node and exits with an error message if it fails.
func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run starts the node and keeps it running until it is told to stop.
func run() error {
	// ctx is cancelled when the program is told to stop: Ctrl+C, or SIGTERM,
	// which is what `docker compose stop` sends.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Read the settings from environment variables, which docker-compose.yml sets.
	nodeID := getenv("NODE_ID", "node1")
	port := getenv("PORT", "9001")
	dir := getenv("CHUNK_DIR", "/chunks") // This is where chunks are saved.

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           (&node{dir: dir}).routes(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	// Run the server in the background, and wait for it to fail or for a
	// stop signal, whichever comes first.
	errc := make(chan error, 1)
	go func() {
		log.Printf("%s listening on :%s", nodeID, port)
		errc <- srv.ListenAndServe()
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	// Give requests in progress up to 10 seconds to finish.
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
