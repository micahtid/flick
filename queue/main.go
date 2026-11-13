// Package main is the Flick job queue. The API adds a job for each upload,
// and workers claim jobs one at a time and mark them done. Jobs are kept in
// Postgres, so restarting the queue loses nothing.
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

// main runs the queue and exits with an error message if it fails.
func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run starts the queue and keeps it running until it is told to stop. It
// returns errors instead of exiting so cleanup (like closing the database)
// always happens.
func run() error {
	// ctx is cancelled when the program is told to stop: Ctrl+C, or SIGTERM,
	// which is what `docker compose down` sends.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Read the settings from environment variables, which docker-compose.yml sets.
	port := getenv("PORT", "8081")

	db, err := connectDB(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer db.Close()

	s := newServer(&jobStore{db: db})
	go s.requeueLoop(ctx) // This runs in the background and stops when ctx is cancelled.

	srv := &http.Server{
		Addr:    ":" + port, // ":8081" means port 8081 on every network interface.
		Handler: s.routes(),
		// Drop clients that take over 10 seconds to send request headers.
		ReadHeaderTimeout: 10 * time.Second,
		// Close connections left idle for 2 minutes.
		IdleTimeout: 2 * time.Minute,
	}

	// Run the server in the background (a goroutine) so this function can also
	// wait for a stop signal. If the server fails, its error arrives on errc.
	errc := make(chan error, 1)
	go func() {
		log.Printf("queue listening on :%s", port)
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
