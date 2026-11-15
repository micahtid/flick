package main

// Tests for the chunk map in fsm.go. They call Apply, Snapshot and Restore
// directly, the way Raft would, so `go test` needs no cluster or Docker.

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"
	"testing"

	"github.com/hashicorp/raft"
)

// applyChange applies one change to f, the way Raft does once it's committed.
func applyChange(t *testing.T, f *fsm, c change) {
	t.Helper()
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Apply(&raft.Log{Data: data}); err != nil {
		t.Fatal(err)
	}
}

// fakeSink is a pretend snapshot file that keeps the bytes in memory.
type fakeSink struct{ bytes.Buffer }

func (s *fakeSink) ID() string    { return "test" }
func (s *fakeSink) Cancel() error { return nil }
func (s *fakeSink) Close() error  { return nil }

// add joins nodes without repeats, and remove takes them away. A chunk with
// no nodes left is dropped from the map.
func TestApply(t *testing.T) {
	f := newFSM()
	applyChange(t, f, change{Op: opAdd, ChunkID: "c1", Nodes: []string{"b", "a"}})
	applyChange(t, f, change{Op: opAdd, ChunkID: "c1", Nodes: []string{"a", "c"}})
	if got, want := f.Holders("c1"), []string{"a", "b", "c"}; !slices.Equal(got, want) {
		t.Errorf("after add, holders = %v, want %v", got, want)
	}

	applyChange(t, f, change{Op: opRemove, ChunkID: "c1", Nodes: []string{"b"}})
	if got, want := f.Holders("c1"), []string{"a", "c"}; !slices.Equal(got, want) {
		t.Errorf("after remove, holders = %v, want %v", got, want)
	}

	applyChange(t, f, change{Op: opRemove, ChunkID: "c1", Nodes: []string{"a", "c"}})
	if _, ok := f.All()["c1"]; ok {
		t.Error("chunk with no holders is still in the map")
	}
}

// A snapshot restored into an empty map gives back the same map.
func TestSnapshotAndRestore(t *testing.T) {
	f := newFSM()
	applyChange(t, f, change{Op: opAdd, ChunkID: "c1", Nodes: []string{"a", "b"}})
	applyChange(t, f, change{Op: opAdd, ChunkID: "c2", Nodes: []string{"c"}})

	snap, err := f.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	var sink fakeSink
	if err := snap.Persist(&sink); err != nil {
		t.Fatal(err)
	}

	restored := newFSM()
	if err := restored.Restore(io.NopCloser(&sink)); err != nil {
		t.Fatal(err)
	}
	if got, want := restored.Holders("c1"), []string{"a", "b"}; !slices.Equal(got, want) {
		t.Errorf("c1 holders = %v, want %v", got, want)
	}
	if got, want := restored.Holders("c2"), []string{"c"}; !slices.Equal(got, want) {
		t.Errorf("c2 holders = %v, want %v", got, want)
	}
}
