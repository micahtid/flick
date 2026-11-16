package chunk

// This file tests the Client in chunk.go. Pretend nodes keep chunks and the
// chunk map in memory, so `go test` needs no Docker. Store in files.go only
// adds Postgres queries, so it isn't tested here.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

// fakeMap is a pretend chunk map, shared by every fakeNode the way the real
// nodes share one through Raft. Set electing to make the next few changes
// fail with 503, the way they do during an election.
type fakeMap struct {
	mu       sync.Mutex
	locs     map[string][]string
	electing int
}

// fakeNode is a pretend storage node. Set down to make it fail every request.
type fakeNode struct {
	mu     sync.Mutex // Requests can arrive at the same time, so this guards chunks.
	chunks map[string][]byte
	down   bool
	shared *fakeMap
}

func (n *fakeNode) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.down {
		http.Error(w, "down", http.StatusServiceUnavailable)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/map") {
		n.shared.serveHTTP(w, r)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/chunks/")
	switch r.Method {
	case http.MethodPut:
		data, _ := io.ReadAll(r.Body)
		n.chunks[id] = data
		w.WriteHeader(http.StatusNoContent)
	case http.MethodGet:
		data, ok := n.chunks[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	}
}

// serveHTTP answers POST /map and GET /map/<id>.
func (m *fakeMap) serveHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r.Method == http.MethodPost {
		if m.electing > 0 {
			m.electing--
			http.Error(w, "no leader yet", http.StatusServiceUnavailable)
			return
		}
		var c mapChange
		json.NewDecoder(r.Body).Decode(&c)
		m.locs[c.ChunkID] = append(m.locs[c.ChunkID], c.Nodes...)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	holders, ok := m.locs[strings.TrimPrefix(r.URL.Path, "/map/")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	json.NewEncoder(w).Encode(holders)
}

// newTestClient starts three pretend nodes that share one map, and a client
// that uses them.
func newTestClient(t *testing.T) (*Client, []*fakeNode, *fakeMap) {
	t.Helper()
	shared := &fakeMap{locs: map[string][]string{}}
	var nodes []*fakeNode
	var urls []string
	for range 3 {
		n := &fakeNode{chunks: map[string][]byte{}, shared: shared}
		srv := httptest.NewServer(n)
		t.Cleanup(srv.Close)
		nodes = append(nodes, n)
		urls = append(urls, srv.URL)
	}
	return New(strings.Join(urls, ",")), nodes, shared
}

// testFile is 2.5 chunks long, so it has two full chunks and a short one.
// Each byte depends on its position, so the chunks all differ.
func testFile() []byte {
	data := make([]byte, Size*5/2)
	for i := range data {
		data[i] = byte(i % 251)
	}
	return data
}

// roundTrip puts data and gets it back.
func roundTrip(t *testing.T, c *Client, data []byte) []byte {
	t.Helper()
	ids, err := c.Put(context.Background(), bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return readBack(t, c, ids)
}

// readBack gets the chunks with the given IDs, joined in order.
func readBack(t *testing.T, c *Client, ids []string) []byte {
	t.Helper()
	var got bytes.Buffer
	if err := c.Get(context.Background(), &got, ids); err != nil {
		t.Fatal(err)
	}
	return got.Bytes()
}

// A file is cut into chunks named by their fingerprints, every node gets a
// copy of each, the map lists all three copies, and the chunks join back into
// the same file.
func TestPutAndGet(t *testing.T) {
	c, nodes, shared := newTestClient(t)
	data := testFile()

	ids, err := c.Put(context.Background(), bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 {
		t.Fatalf("got %d chunks, want 3", len(ids))
	}
	if ids[0] != ID(data[:Size]) {
		t.Errorf("first ID = %s, want the fingerprint of the first 4 MB", ids[0])
	}
	for i, n := range nodes {
		if len(n.chunks) != 3 {
			t.Errorf("node %d has %d chunks, want 3", i+1, len(n.chunks))
		}
	}
	for _, id := range ids {
		if got := len(shared.locs[id]); got != 3 {
			t.Errorf("map lists %d nodes for chunk %s, want 3", got, id)
		}
	}

	if got := readBack(t, c, ids); !bytes.Equal(got, data) {
		t.Error("read back different bytes")
	}
}

// With one node down, files can still be saved and read.
func TestOneNodeDown(t *testing.T) {
	c, nodes, _ := newTestClient(t)
	nodes[0].down = true

	data := testFile()
	if got := roundTrip(t, c, data); !bytes.Equal(got, data) {
		t.Error("read back different bytes")
	}
}

// With two of three nodes down, saving fails, since a chunk with only one
// copy could easily be lost.
func TestTwoNodesDown(t *testing.T) {
	c, nodes, _ := newTestClient(t)
	nodes[0].down = true
	nodes[1].down = true

	if _, err := c.Put(context.Background(), strings.NewReader("hello")); err == nil {
		t.Error("Put = nil error, want a failure")
	}
}

// A damaged copy on one node is skipped, and a good copy is read from the next.
func TestGetSkipsDamagedChunk(t *testing.T) {
	c, nodes, _ := newTestClient(t)
	ids, err := c.Put(context.Background(), strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	nodes[0].chunks[ids[0]] = []byte("jello")

	if got := readBack(t, c, ids); string(got) != "hello" {
		t.Errorf("got %q, want hello", got)
	}
}

// An empty file has no chunks, and reads back empty.
func TestEmptyFile(t *testing.T) {
	c, _, _ := newTestClient(t)
	if got := roundTrip(t, c, nil); len(got) != 0 {
		t.Errorf("got %d bytes, want 0", len(got))
	}
}

// A change sent during an election is tried again until a leader takes it.
func TestPutWaitsForLeader(t *testing.T) {
	c, _, shared := newTestClient(t)
	shared.electing = 4 // The first round of 3 nodes fails, then 1 more.

	ids, err := c.Put(context.Background(), strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(shared.locs[ids[0]]); got != 3 {
		t.Errorf("map lists %d nodes, want 3", got)
	}
}

// A chunk missing from the map is still found, by asking every node.
func TestGetWithoutMap(t *testing.T) {
	c, _, shared := newTestClient(t)
	ids, err := c.Put(context.Background(), strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	clear(shared.locs)

	if got := readBack(t, c, ids); string(got) != "hello" {
		t.Errorf("got %q, want hello", got)
	}
}

// A node that can't be reached rests, so the next chunks skip it, and files
// still save and read.
func TestUnreachableNodeRests(t *testing.T) {
	c, _, _ := newTestClient(t)
	gone := "http://127.0.0.1:1" // Nothing listens on port 1.
	c.nodes[0] = gone

	data := testFile()
	if got := roundTrip(t, c, data); !bytes.Equal(got, data) {
		t.Error("read back different bytes")
	}
	if !c.resting(gone) {
		t.Error("the unreachable node isn't resting")
	}
	if ready, _ := c.split(); slices.Contains(ready, gone) {
		t.Error("new chunks are still sent to the resting node")
	}
}

// A resting node that's back is still used when too few other nodes can save
// a chunk.
func TestRestingNodeUsedWhenNeeded(t *testing.T) {
	c, nodes, _ := newTestClient(t)
	c.rest(c.nodes[0]) // The first node is resting, but it's really up.
	nodes[1].down = true

	if got := roundTrip(t, c, []byte("hello")); string(got) != "hello" {
		t.Errorf("got %q, want hello", got)
	}
	if len(nodes[0].chunks) != 1 {
		t.Error("the resting node wasn't used")
	}
}
