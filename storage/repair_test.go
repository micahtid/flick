package main

// Tests for the repair loop in repair.go. The leader runs Raft through memory,
// and the nodes it repairs are real chunk handlers behind test servers, so
// `go test` needs no Docker.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// Only nodes that are up and missing a chunk get a copy, and only holders that
// are up are copied from.
func TestPlan(t *testing.T) {
	locs := map[string][]string{
		"full":    {"a", "b", "c"},
		"missing": {"a"},
		"down":    {"a", "c"}, // c is down, so it can't be copied from.
	}
	jobs := plan(locs, []string{"a", "b"})
	slices.SortFunc(jobs, func(x, y copyJob) int { return strings.Compare(x.id, y.id) })

	want := []copyJob{
		{id: "down", from: []string{"a"}, to: []string{"b"}},
		{id: "missing", from: []string{"a"}, to: []string{"b"}},
	}
	if !slices.EqualFunc(jobs, want, func(x, y copyJob) bool {
		return x.id == y.id && slices.Equal(x.from, y.from) && slices.Equal(x.to, y.to)
	}) {
		t.Errorf("got %+v, want %+v", jobs, want)
	}
}

// A chunk held by one node is copied to the other two, and the map records
// all three copies.
func TestRepair(t *testing.T) {
	leader := leaderOf(newCluster(t, 1))

	// Point the leader at three real nodes, each with its own empty folder.
	var stores []*node
	leader.peers = nil
	for range 3 {
		s := &node{dir: t.TempDir()}
		srv := httptest.NewServer(s.routes())
		t.Cleanup(srv.Close)
		stores = append(stores, s)
		leader.peers = append(leader.peers, peer{addr: srv.URL})
	}
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
