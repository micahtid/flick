// Package main is a Flick storage node. It saves chunks of files on its own
// disk and hands them back. Three nodes run at once, and every chunk ends up
// on all of them, so losing one node loses nothing. The nodes also share a
// chunk map, which lists the nodes that hold each chunk. Raft, a way for
// several machines to agree on the same data, keeps the map the same on every
// node. One node, the leader, makes every change to the map. It also copies
// chunks to any node that missed them.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/hashicorp/raft"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// main runs the node and exits with an error message if it fails.
func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run starts the node and keeps it running until it's told to stop.
func run() error {
	// ctx is cancelled when the program is told to stop, by Ctrl+C or by
	// SIGTERM. Docker sends SIGTERM when it stops a container.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Read the settings from environment variables set in docker-compose.yml.
	nodeID := getenv("NODE_ID", "node1")
	storageNodes := getenv("STORAGE_NODES", "http://node1:9001,http://node2:9002,http://node3:9003")
	bootstrap := os.Getenv("BOOTSTRAP") == "true" // Only node1 sets this.
	dir := getenv("CHUNK_DIR", "/chunks")         // This is where chunks are saved.

	peers, err := parsePeers(storageNodes)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(peers, func(p peer) bool { return p.id == nodeID })
	if i < 0 {
		return fmt.Errorf("%s is not in STORAGE_NODES", nodeID)
	}
	self := peers[i]

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f := newFSM()
	r, err := startRaft(self, peers, filepath.Join(dir, "raft"), bootstrap, f)
	if err != nil {
		return err
	}
	// Stop Raft on the way out. Deferred calls run in reverse order, so this
	// runs after the server below has stopped.
	defer func() { r.Shutdown().Error() }()

	n := &node{
		dir:   dir,
		self:  self,
		peers: peers,
		fsm:   f,
		raft:  r,
		// Give up on a node after 30 seconds, so a stuck node can't hold up a
		// repair forever.
		http: &http.Client{Timeout: 30 * time.Second},
	}
	go n.repairLoop(ctx)

	// Prometheus calls these functions each time it reads /metrics.
	promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "raft_is_leader",
		Help: "1 if this node is the leader, 0 if not.",
	}, func() float64 {
		if r.State() == raft.Leader {
			return 1
		}
		return 0
	})
	promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "chunks_stored",
		Help: "Chunks saved on this node.",
	}, n.chunkCount)

	srv := &http.Server{
		Addr:              ":" + self.port,
		Handler:           n.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	// Run the server in the background (a goroutine) so this function can also
	// wait for a stop signal. If the server fails, its error arrives on errc.
	errc := make(chan error, 1)
	go func() {
		log.Printf("%s listening on :%s", nodeID, self.port)
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
