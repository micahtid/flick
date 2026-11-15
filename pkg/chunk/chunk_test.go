package chunk

// Tests for the Client in chunk.go. Pretend nodes keep chunks in memory, so
// `go test` needs no Docker. Store in files.go only adds Postgres queries, so
// it isn't tested here.

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeNode is a pretend storage node. Set down to make it fail every request.
type fakeNode struct {
	mu     sync.Mutex // Put sends to all nodes at once, so guard the map.
	chunks map[string][]byte
	down   bool
}

func (n *fakeNode) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.down {
		http.Error(w, "down", http.StatusServiceUnavailable)
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

// newTestClient starts three pretend nodes and a client that uses them.
func newTestClient(t *testing.T) (*Client, []*fakeNode) {
	t.Helper()
	var nodes []*fakeNode
	var urls []string
	for range 3 {
		n := &fakeNode{chunks: map[string][]byte{}}
		srv := httptest.NewServer(n)
		t.Cleanup(srv.Close)
		nodes = append(nodes, n)
		urls = append(urls, srv.URL)
	}
	return New(strings.Join(urls, ",")), nodes
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
	var got bytes.Buffer
	if err := c.Get(context.Background(), &got, ids); err != nil {
		t.Fatal(err)
	}
	return got.Bytes()
}

// A file is cut into chunks named by their fingerprints, every node gets a
// copy of each, and the chunks join back into the same file.
func TestPutAndGet(t *testing.T) {
	c, nodes := newTestClient(t)
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

	var got bytes.Buffer
	if err := c.Get(context.Background(), &got, ids); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), data) {
		t.Error("read back different bytes")
	}
}

// With one node down, files can still be saved and read.
func TestOneNodeDown(t *testing.T) {
	c, nodes := newTestClient(t)
	nodes[0].down = true

	data := testFile()
	if got := roundTrip(t, c, data); !bytes.Equal(got, data) {
		t.Error("read back different bytes")
	}
}

// With two of three nodes down, saving fails, since a chunk with only one
// copy could easily be lost.
func TestTwoNodesDown(t *testing.T) {
	c, nodes := newTestClient(t)
	nodes[0].down = true
	nodes[1].down = true

	if _, err := c.Put(context.Background(), strings.NewReader("hello")); err == nil {
		t.Error("Put = nil error, want a failure")
	}
}

// A damaged copy on one node is skipped, and a good copy is read from the next.
func TestGetSkipsDamagedChunk(t *testing.T) {
	c, nodes := newTestClient(t)
	ids, err := c.Put(context.Background(), strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	nodes[0].chunks[ids[0]] = []byte("jello")

	var got bytes.Buffer
	if err := c.Get(context.Background(), &got, ids); err != nil {
		t.Fatal(err)
	}
	if got.String() != "hello" {
		t.Errorf("got %q, want hello", got.String())
	}
}

// An empty file has no chunks, and reads back empty.
func TestEmptyFile(t *testing.T) {
	c, _ := newTestClient(t)
	if got := roundTrip(t, c, nil); len(got) != 0 {
		t.Errorf("got %d bytes, want 0", len(got))
	}
}
