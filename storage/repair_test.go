package main

// This file tests the repair loop in repair.go. The leader runs Raft through
// memory, and the nodes it repairs are real chunk handlers behind test
// servers, so `go test` needs no Docker.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
)

// Only nodes that are up and missing a chunk on disk get a copy, even if the
// map lists them. Only nodes that are up and have it saved are copied from.
func TestPlan(t *testing.T) {
	locs := map[string][]string{
		"full":    {"a", "b", "c"},
		"missing": {"a"},
		"lost":    {"a", "b"}, // b is listed, but its copy is gone.
		"down":    {"a", "c"}, // c is down, so it can't be copied from.
	}
	saved := map[string]map[string]bool{
		"a": {"full": true, "missing": true, "lost": true, "down": true},
		"b": {"full": true},
	}
	jobs := plan(locs, saved)
	slices.SortFunc(jobs, func(x, y copyJob) int { return strings.Compare(x.id, y.id) })

	want := []copyJob{
		{id: "down", from: []string{"a"}, to: []string{"b"}},
		{id: "lost", from: []string{"a"}, to: []string{"b"}},
		{id: "missing", from: []string{"a"}, to: []string{"b"}},
	}
	if !slices.EqualFunc(jobs, want, func(x, y copyJob) bool {
		return x.id == y.id && slices.Equal(x.from, y.from) && slices.Equal(x.to, y.to)
	}) {
		t.Errorf("got %+v, want %+v", jobs, want)
	}
}

// newRepairCluster makes a leader that repairs three real nodes, each with its
// own empty folder and returns the leader and the nodes.
func newRepairCluster(t *testing.T) (*node, []*node) {
	t.Helper()
	leader := leaderOf(newCluster(t, 1))
	var stores []*node
	leader.peers = nil
	for range 3 {
		s := &node{dir: t.TempDir()}
		srv := httptest.NewServer(s.routes())
		t.Cleanup(srv.Close)
		stores = append(stores, s)
		leader.peers = append(leader.peers, peer{addr: srv.URL})
	}
	return leader, stores
}

// A chunk held by one node is copied to the other two, and the map records
// all three copies.
func TestRepair(t *testing.T) {
	leader, stores := newRepairCluster(t)
	first := leader.peers[0].addr

	// Save a chunk on the first node only, and record that in the map.
	id := fingerprint([]byte("hello"))
	if rec := send(stores[0].routes(), http.MethodPut, "/chunks/"+id, "hello"); rec.Code != http.StatusNoContent {
		t.Fatalf("put status = %d", rec.Code)
	}
	if err := leader.apply(change{Op: opAdd, ChunkID: id, Nodes: []string{first}}); err != nil {
		t.Fatal(err)
	}

	leader.repair(context.Background())

	for i, s := range stores {
		if rec := send(s.routes(), http.MethodGet, "/chunks/"+id, ""); rec.Body.String() != "hello" {
			t.Errorf("node %d has %q, want hello", i+1, rec.Body)
		}
	}
	if got := leader.fsm.Holders(id); len(got) != 3 {
		t.Errorf("map lists %v, want all 3 nodes", got)
	}
}

// A copy the map still lists, but that is gone from a node's disk, is copied
// back.
func TestRepairReplacesLostCopy(t *testing.T) {
	leader, stores := newRepairCluster(t)
	id := fingerprint([]byte("hello"))
	var all []string
	for i, s := range stores {
		send(s.routes(), http.MethodPut, "/chunks/"+id, "hello")
		all = append(all, leader.peers[i].addr)
	}
	if err := leader.apply(change{Op: opAdd, ChunkID: id, Nodes: all}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(stores[1].path(id)); err != nil {
		t.Fatal(err)
	}

	leader.repair(context.Background())

	if rec := send(stores[1].routes(), http.MethodGet, "/chunks/"+id, ""); rec.Body.String() != "hello" {
		t.Errorf("node 2 has %q, want hello", rec.Body)
	}
}
