// Package chunk stores files on the storage nodes. It cuts each file into
// 4 MB chunks, sends every chunk to the nodes and records which nodes saved
// it in the chunk map. The chunk map is a small table, kept by the nodes, that
// lists the nodes holding each chunk. To read a file back, the package asks
// the map where each chunk is and puts the chunks back together. The API, the
// worker and the ingest service all use it.
//
// The nodes use Raft to keep the map the same on every node. One node, the
// leader, makes every change to the map and copies it to the others, the
// followers. If the leader stops, the others hold an election (a vote) to
// choose a new one.
package chunk

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Size is the largest a chunk can be: 4 MB (4 × 2²⁰ bytes). The last chunk of
// a file is usually smaller.
const Size = 4 << 20

// These set how long Put keeps trying to record a chunk in the map. An
// election usually takes a second or two, so 10 tries is plenty.
const (
	recordTries = 10
	recordWait  = time.Second
)

// mapChange is one update to the chunk map. It matches change in
// storage/fsm.go.
type mapChange struct {
	Op      string   `json:"op"`
	ChunkID string   `json:"chunk_id"`
	Nodes   []string `json:"nodes"`
}

// ID is a chunk's name: the SHA-256 fingerprint of its bytes, written as 64
// hex characters. Because the name is the fingerprint, a node can check that a
// chunk isn't damaged without storing anything else.
func ID(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Client sends chunks to the storage nodes and fetches them back.
type Client struct {
	nodes []string // These are the node addresses, such as http://node1:9001.
	http  *http.Client

	mu     sync.Mutex           // Requests run at the same time, so this guards restAt.
	restAt map[string]time.Time // This is when each node last started resting.
}

// New makes a client for the nodes in a comma-separated list, the way the
// STORAGE_NODES environment variable holds them.
func New(nodes string) *Client {
	return &Client{
		nodes: strings.Split(nodes, ","),
		// Give up on a node after 30 seconds, so a stuck node can't hold an
		// upload open forever.
		http:   &http.Client{Timeout: 30 * time.Second},
		restAt: map[string]time.Time{},
	}
}

// Put cuts r into chunks, sends each one to the nodes, records it in the map,
// and returns the chunk IDs in order. Only one chunk is held in memory at a
// time.
func (c *Client) Put(ctx context.Context, r io.Reader) ([]string, error) {
	var ids []string
	buf := make([]byte, Size)
	for {
		n, err := io.ReadFull(r, buf) // Read up to 4 MB.
		if n > 0 {
			id := ID(buf[:n])
			saved, err := c.writeAll(ctx, id, buf[:n])
			if err != nil {
				return nil, err
			}
			if err := c.record(ctx, id, saved); err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
		// ReadFull reports EOF when nothing was left to read, and
		// ErrUnexpectedEOF when the last chunk was short. Both mean the end.
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return ids, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// writeAll sends one chunk to the nodes and returns the nodes that saved it.
// It succeeds if most of them (2 of 3) save it, so one dead node doesn't stop
// uploads. Resting nodes are skipped, and the repair loop copies the chunk to
// them later. If too few nodes save it, the resting nodes are tried too, in
// case one is back.
func (c *Client) writeAll(ctx context.Context, id string, data []byte) ([]string, error) {
	ready, resting := c.split()
	saved, errs := c.writeTo(ctx, ready, id, data)
	if len(saved) <= len(c.nodes)/2 {
		more, moreErrs := c.writeTo(ctx, resting, id, data)
		saved, errs = append(saved, more...), append(errs, moreErrs...)
	}
	if len(saved) <= len(c.nodes)/2 {
		return nil, fmt.Errorf("chunk %s saved on %d of %d nodes: %w", id, len(saved), len(c.nodes), errors.Join(errs...))
	}
	return saved, nil
}

// writeTo sends one chunk to the given nodes at the same time and returns
// the nodes that saved it and the errors from the others. It waits for every
// node to answer, since they all read from data, which Put reuses for the
// next chunk.
func (c *Client) writeTo(ctx context.Context, nodes []string, id string, data []byte) ([]string, []error) {
	errs := make([]error, len(nodes))
	var wg sync.WaitGroup
	for i, node := range nodes {
		wg.Go(func() { errs[i] = c.write(ctx, node, id, data) })
	}
	wg.Wait()

	var saved []string
	for i, err := range errs {
		if err == nil {
			saved = append(saved, nodes[i])
		}
	}
	return saved, errs
}

// record tells the chunk map which nodes now hold a chunk. Any node takes the
// request: a follower replies 307 with the leader's address, and Go's HTTP
// client follows it by itself. While the nodes elect a new leader, they reply
// 503, so record waits a second and tries again.
func (c *Client) record(ctx context.Context, id string, nodes []string) error {
	body, err := json.Marshal(mapChange{Op: "add", ChunkID: id, Nodes: nodes})
	if err != nil {
		return err
	}
	for range recordTries {
		for _, node := range c.order(nil) {
			if _, err = c.send(ctx, http.MethodPost, node, "/map", body, http.StatusNoContent); err == nil {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(recordWait):
		}
	}
	return fmt.Errorf("record chunk %s: %w", id, err) // This is the last error.
}

// write sends one chunk to one node.
func (c *Client) write(ctx context.Context, node, id string, data []byte) error {
	_, err := c.send(ctx, http.MethodPut, node, "/chunks/"+id, data, http.StatusNoContent)
	return err
}

// Get writes the chunks to w in order. For each chunk, it asks the nodes one
// at a time and uses the first good answer, so the file can be read while any
// one node is down.
func (c *Client) Get(ctx context.Context, w io.Writer, ids []string) error {
	for _, id := range ids {
		data, err := c.readAny(ctx, id)
		if err != nil {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
	}
	return nil
}

// readAny fetches one chunk from the first node that has a good copy. It asks
// the nodes the map lists first. The other nodes are still asked after them,
// in case the map is behind.
func (c *Client) readAny(ctx context.Context, id string) ([]byte, error) {
	var errs []error
	for _, node := range c.order(c.holders(ctx, id)) {
		data, err := c.read(ctx, node, id)
		if err == nil {
			return data, nil
		}
		errs = append(errs, err)
	}
	return nil, fmt.Errorf("chunk %s on no node: %w", id, errors.Join(errs...))
}

// holders asks the chunk map which nodes hold a chunk. Any node can answer
// from its own copy of the map, so it tries them one at a time until one does.
// It returns nil if none answers.
func (c *Client) holders(ctx context.Context, id string) []string {
	for _, node := range c.order(nil) {
		body, err := c.send(ctx, http.MethodGet, node, "/map/"+id, nil, http.StatusOK)
		if err != nil {
			continue
		}
		var holders []string
		if json.Unmarshal(body, &holders) == nil {
			return holders
		}
	}
	return nil
}

// read fetches one chunk from one node and checks its fingerprint in case it
// was damaged on the way.
func (c *Client) read(ctx context.Context, node, id string) ([]byte, error) {
	data, err := c.send(ctx, http.MethodGet, node, "/chunks/"+id, nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	if ID(data) != id {
		return nil, fmt.Errorf("%s sent a damaged chunk", node)
	}
	return data, nil
}

// send makes one request to a node and returns the reply body. Any status
// other than want is an error. If the node can't be reached at all, it rests,
// so later requests try it last.
func (c *Client) send(ctx context.Context, method, node, path string, body []byte, want int) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, node+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() == nil { // A cancelled request isn't the node's fault.
			c.rest(node)
		}
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		return nil, fmt.Errorf("%s replied %s", node+path, resp.Status)
	}
	// Read one byte over the limit, so a too-big chunk fails its fingerprint.
	return io.ReadAll(io.LimitReader(resp.Body, Size+1))
}
