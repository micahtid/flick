package main

// This file holds the chunk map: a record of which nodes hold each chunk.
// Every node keeps its own copy, and Raft makes sure all the copies match.

import (
	"encoding/json"
	"io"
	"maps"
	"slices"
	"sync"

	"github.com/hashicorp/raft"
)

// These are the two kinds of change to the chunk map.
const (
	opAdd    = "add"    // The nodes now hold the chunk.
	opRemove = "remove" // The nodes no longer hold the chunk.
)

// change is one update to the chunk map. Raft copies changes to every node,
// and each node applies them in the same order. It matches mapChange in
// pkg/chunk.
type change struct {
	Op      string   `json:"op"`
	ChunkID string   `json:"chunk_id"`
	Nodes   []string `json:"nodes"` // These are node addresses, such as http://node1:9001.
}

// fsm is the chunk map. Raft calls it a finite state machine, which just
// means "the thing that Raft's changes are applied to".
type fsm struct {
	mu   sync.RWMutex        // Raft applies changes while handlers read the map.
	locs map[string][]string // This maps each chunk ID to the nodes that hold it.
}

func newFSM() *fsm {
	return &fsm{locs: map[string][]string{}}
}

// Apply makes one committed change to the map. Raft calls it on every node, in
// the same order, so every copy of the map ends up the same.
func (f *fsm) Apply(l *raft.Log) any {
	var c change
	if err := json.Unmarshal(l.Data, &c); err != nil {
		return err // Handlers check changes before applying them, so this never happens.
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch c.Op {
	case opAdd:
		f.locs[c.ChunkID] = union(f.locs[c.ChunkID], c.Nodes)
	case opRemove:
		f.locs[c.ChunkID] = minus(f.locs[c.ChunkID], c.Nodes)
		if len(f.locs[c.ChunkID]) == 0 {
			delete(f.locs, c.ChunkID)
		}
	}
	return nil
}

// Holders returns the nodes that hold a chunk.
func (f *fsm) Holders(id string) []string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return slices.Clone(f.locs[id])
}

// All returns a copy of the whole map, so the caller can read it while Raft
// keeps applying changes.
func (f *fsm) All() map[string][]string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	all := make(map[string][]string, len(f.locs))
	for id, nodes := range f.locs {
		all[id] = slices.Clone(nodes)
	}
	return all
}

// Snapshot saves the whole map. Raft calls it now and then, so it can throw
// away the log entries the snapshot already covers.
func (f *fsm) Snapshot() (raft.FSMSnapshot, error) {
	return snapshot(f.All()), nil
}

// Restore replaces the map with one saved by Snapshot. Raft calls it when a
// node starts, or when a node is so far behind that the leader sends it a
// snapshot instead of the log entries it missed.
func (f *fsm) Restore(r io.ReadCloser) error {
	defer r.Close()
	locs := map[string][]string{}
	if err := json.NewDecoder(r).Decode(&locs); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.locs = locs
	return nil
}

// snapshot is a copy of the map, taken by Snapshot.
type snapshot map[string][]string

// Persist writes the map to Raft's snapshot file as JSON.
func (s snapshot) Persist(sink raft.SnapshotSink) error {
	if err := json.NewEncoder(sink).Encode(s); err != nil {
		sink.Cancel()
		return err
	}
	return sink.Close()
}

// Release is called when Raft is done with the snapshot. There's nothing to
// clean up.
func (s snapshot) Release() {}

// union joins two lists of nodes, leaving out repeats. The result is sorted,
// so every node stores the list in the same order.
func union(a, b []string) []string {
	set := map[string]bool{}
	for _, n := range slices.Concat(a, b) {
		set[n] = true
	}
	return slices.Sorted(maps.Keys(set))
}

// minus returns the nodes in a that aren't in b.
func minus(a, b []string) []string {
	return slices.DeleteFunc(slices.Clone(a), func(n string) bool {
		return slices.Contains(b, n)
	})
}

// intersect returns the nodes in a that are also in b.
func intersect(a, b []string) []string {
	return slices.DeleteFunc(slices.Clone(a), func(n string) bool {
		return !slices.Contains(b, n)
	})
}
