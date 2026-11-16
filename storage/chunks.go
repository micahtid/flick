package main

// This file handles the chunk requests. It saves chunks, sends them back,
// lists them, answers /health, and counts the chunks for Prometheus.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"github.com/hashicorp/raft"
	"github.com/micahtid/flick/pkg/metrics"
)

// maxChunkBytes is the largest chunk the node accepts. It matches chunk.Size
// in pkg/chunk.
const maxChunkBytes = 4 << 20

// validID matches a chunk ID: a SHA-256 fingerprint written as 64 hex
// characters. Checking it also stops IDs such as ".." from reaching the disk.
var validID = regexp.MustCompile(`^[0-9a-f]{64}$`)

// node is one storage node. It saves chunks as files in dir, each named by
// its ID, and keeps its copy of the chunk map in fsm.
type node struct {
	dir   string
	self  peer         // This is the node itself.
	peers []peer       // These are all the nodes, this one included.
	fsm   *fsm         // This is the chunk map.
	raft  *raft.Raft   // This keeps the chunk map the same on every node.
	http  *http.Client // This talks to the other nodes.
}

// routes connects each request type and path to its handler. metrics.Wrap
// counts and times every request, and Prometheus reads the numbers from
// /metrics.
func (n *node) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", n.health)
	mux.HandleFunc("PUT /chunks/{id}", n.putChunk)
	mux.HandleFunc("GET /chunks/{id}", n.getChunk)
	mux.HandleFunc("GET /chunks", n.listChunks)
	mux.HandleFunc("POST /map", n.postMap)
	mux.HandleFunc("GET /map/{id}", n.getMap)
	mux.HandleFunc("GET /status", n.status)
	mux.Handle("GET /metrics", metrics.Handler())
	return metrics.Wrap(mux)
}

// health replies "ok". It's a quick way to check the node is running.
func (n *node) health(w http.ResponseWriter, _ *http.Request) {
	io.WriteString(w, "ok")
}

// putChunk saves the request body as a chunk. The body's fingerprint must
// match the ID, so a chunk damaged on the way is turned away.
func (n *node) putChunk(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validID.MatchString(id) {
		http.Error(w, "bad chunk id", http.StatusBadRequest)
		return
	}

	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxChunkBytes))
	if err != nil {
		http.Error(w, "chunk too large", http.StatusRequestEntityTooLarge)
		return
	}
	if fingerprint(data) != id {
		http.Error(w, "chunk does not match its id", http.StatusBadRequest)
		return
	}

	if err := writeFileAtomic(n.path(id), data); err != nil {
		log.Printf("save chunk %s: %v", id, err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent) // 204 means saved, with nothing to send back.
}

// getChunk sends a chunk back, after checking it hasn't been damaged on disk.
// A damaged copy is deleted, so the repair loop copies a good one back.
func (n *node) getChunk(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validID.MatchString(id) {
		http.Error(w, "bad chunk id", http.StatusBadRequest)
		return
	}

	data, err := os.ReadFile(n.path(id))
	if errors.Is(err, fs.ErrNotExist) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		log.Printf("read chunk %s: %v", id, err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if fingerprint(data) != id {
		// Never send damaged data. The client asks another node instead.
		log.Printf("chunk %s is damaged, so it's deleted", id)
		if err := os.Remove(n.path(id)); err != nil {
			log.Printf("delete chunk %s: %v", id, err)
		}
		http.Error(w, "corrupt chunk", http.StatusInternalServerError)
		return
	}
	w.Write(data)
}

// listChunks replies with the ID of every chunk saved on this node, such as
// ["3f2a...", "9b1c..."]. The leader's repair loop uses it to find the copies
// a node is missing.
func (n *node) listChunks(w http.ResponseWriter, _ *http.Request) {
	ids, err := n.chunkIDs()
	if err != nil {
		log.Printf("list chunks: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, ids)
}

// chunkCount counts the chunks saved in dir. Prometheus reads it as
// chunks_stored each time it collects the numbers.
func (n *node) chunkCount() float64 {
	ids, err := n.chunkIDs()
	if err != nil {
		log.Printf("count chunks: %v", err)
		return 0
	}
	return float64(len(ids))
}

// chunkIDs lists the chunks saved in dir. Raft's folder and stray .tmp files
// aren't chunks, so they're skipped. It starts with an empty list rather than
// nil, so no chunks is sent as [] instead of null.
func (n *node) chunkIDs() ([]string, error) {
	entries, err := os.ReadDir(n.dir)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, e := range entries {
		if validID.MatchString(e.Name()) {
			ids = append(ids, e.Name())
		}
	}
	return ids, nil
}

// path is where a chunk is saved, such as /chunks/<id>.
func (n *node) path(id string) string {
	return filepath.Join(n.dir, id)
}

// fingerprint is the SHA-256 of data as 64 hex characters, the same as
// chunk.ID in pkg/chunk.
func fingerprint(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// writeFileAtomic writes data to a temporary file, then renames it to path.
// A rename happens all at once, so a crash mid-save leaves a stray .tmp file,
// never half a chunk. Each save gets its own temporary file, so two saves of
// the same chunk can't mix.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // This does nothing once the rename has worked.

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	// Sync waits until the bytes are really on the disk, not just in memory.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
