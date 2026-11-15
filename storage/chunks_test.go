package main

// Tests for the handlers in chunks.go. They save chunks in a temporary
// folder, so `go test` needs no Docker.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestNode makes a node with an empty folder. t.TempDir is deleted
// automatically when the test ends.
func newTestNode(t *testing.T) (*node, http.Handler) {
	t.Helper()
	n := &node{dir: t.TempDir()}
	return n, n.routes()
}

// send makes one request straight to the handler, with no real network.
func send(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

// A chunk that matches its ID is saved, with no temporary file left, and
// comes back unchanged.
func TestPutThenGet(t *testing.T) {
	n, h := newTestNode(t)
	id := fingerprint([]byte("hello"))

	if rec := send(h, http.MethodPut, "/chunks/"+id, "hello"); rec.Code != http.StatusNoContent {
		t.Fatalf("put status = %d, body = %q", rec.Code, rec.Body)
	}
	entries, _ := os.ReadDir(n.dir)
	if len(entries) != 1 || entries[0].Name() != id {
		t.Errorf("folder holds %v, want only %s", entries, id)
	}

	rec := send(h, http.MethodGet, "/chunks/"+id, "")
	if rec.Code != http.StatusOK || rec.Body.String() != "hello" {
		t.Errorf("get = %d %q, want 200 hello", rec.Code, rec.Body)
	}
}

// Bad requests are turned away, and nothing is saved.
func TestPutRejects(t *testing.T) {
	tests := []struct {
		name string
		id   string
		body string
		want int
	}{
		{"wrong fingerprint", fingerprint([]byte("hello")), "jello", http.StatusBadRequest},
		{"bad id", "not-a-fingerprint", "hello", http.StatusBadRequest},
		{"too large", fingerprint([]byte("x")), strings.Repeat("x", maxChunkBytes+1), http.StatusRequestEntityTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, h := newTestNode(t)
			if rec := send(h, http.MethodPut, "/chunks/"+tt.id, tt.body); rec.Code != tt.want {
				t.Errorf("status = %d, want %d", rec.Code, tt.want)
			}
			if entries, _ := os.ReadDir(n.dir); len(entries) != 0 {
				t.Errorf("folder holds %v, want nothing", entries)
			}
		})
	}
}

// A missing chunk replies 404, and a chunk damaged on disk replies 500
// rather than sending bad data.
func TestGetMissingAndDamaged(t *testing.T) {
	n, h := newTestNode(t)
	id := fingerprint([]byte("hello"))

	if rec := send(h, http.MethodGet, "/chunks/"+id, ""); rec.Code != http.StatusNotFound {
		t.Errorf("missing chunk status = %d, want 404", rec.Code)
	}

	if err := os.WriteFile(filepath.Join(n.dir, id), []byte("jello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rec := send(h, http.MethodGet, "/chunks/"+id, ""); rec.Code != http.StatusInternalServerError {
		t.Errorf("damaged chunk status = %d, want 500", rec.Code)
	}
}
