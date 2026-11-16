// Package main is the Flick edge cache. Nginx forwards every /hls request
// here. It keeps the most recently watched segments (short pieces of video)
// in memory, so a popular video's segments are fetched from the API once
// instead of once per viewer.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// main runs the cache and exits with an error message if it fails.
func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run starts the cache and keeps it running until it's told to stop.
func run() error {
	// ctx is cancelled when the program is told to stop, by Ctrl+C or by
	// SIGTERM. Docker sends SIGTERM when it stops a container.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Read the settings from environment variables set in docker-compose.yml.
	port := getenv("PORT", "8082")
	apiURL := getenv("API_URL", "http://api:8080")
	maxMB, err := strconv.Atoi(getenv("CACHE_MAX_MB", "500"))
	if err != nil || maxMB <= 0 {
		return fmt.Errorf("CACHE_MAX_MB must be a whole number above 0, not %q", os.Getenv("CACHE_MAX_MB"))
	}

	// Give up on the API after 30 seconds, so a stuck request can't hold a
	// viewer forever. maxMB<<20 turns megabytes into bytes.
	s, err := newServer(apiURL, newLRU(maxMB<<20), &http.Client{Timeout: 30 * time.Second})
	if err != nil {
		return err
	}

	// Prometheus reads the hit and miss counts each time it reads /metrics.
	promauto.NewCounterFunc(prometheus.CounterOpts{
		Name: "cache_hits_total",
		Help: "Segments sent from memory.",
	}, func() float64 { return float64(s.hits.Load()) })
	promauto.NewCounterFunc(prometheus.CounterOpts{
		Name: "cache_misses_total",
		Help: "Segments fetched from the API.",
	}, func() float64 { return float64(s.misses.Load()) })

	srv := &http.Server{
		Addr:    ":" + port, // ":8082" means port 8082 on every network interface.
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
		log.Printf("cache listening on :%s, holding up to %d MB", port, maxMB)
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
