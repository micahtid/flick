package main

// This file tests the cluster in cluster.go. Raft runs for real, but its
// messages pass through memory instead of the network, so `go test` needs no
// Docker.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/raft"
)

// newCluster starts size nodes that talk to each other through memory, and
// waits until one is the leader. The nodes are named node1, node2 and so on,
// with addresses such as http://node1:9001.
func newCluster(t *testing.T, size int) []*node {
	t.Helper()
	var peers []peer
	var transports []*raft.InmemTransport
	for i := range size {
		addr, trans := raft.NewInmemTransport("")
		peers = append(peers, peer{
			id:   fmt.Sprintf("node%d", i+1),
			addr: fmt.Sprintf("http://node%d:900%d", i+1, i+1),
			raft: string(addr),
		})
		transports = append(transports, trans)
	}
	// Connect every node to every other node.
	for _, a := range transports {
		for _, b := range transports {
			a.Connect(b.LocalAddr(), b)
		}
	}

	var nodes []*node
	for i, p := range peers {
		cfg := raft.DefaultConfig()
		cfg.LocalID = raft.ServerID(p.id)
		cfg.LogLevel = "ERROR"
		// Shorter timeouts make elections finish quickly.
		cfg.HeartbeatTimeout = 50 * time.Millisecond
		cfg.ElectionTimeout = 50 * time.Millisecond
		cfg.LeaderLeaseTimeout = 50 * time.Millisecond
		cfg.CommitTimeout = 5 * time.Millisecond

		f := newFSM()
		store := raft.NewInmemStore()
		r, err := raft.NewRaft(cfg, f, store, store, raft.NewInmemSnapshotStore(), transports[i])
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { r.Shutdown().Error() })
		if i == 0 {
			if err := r.BootstrapCluster(membership(peers)).Error(); err != nil {
				t.Fatal(err)
			}
		}
		nodes = append(nodes, &node{dir: t.TempDir(), self: p, peers: peers, fsm: f, raft: r, http: http.DefaultClient})
	}

	waitFor(t, "a leader", func() bool { return leaderOf(nodes) != nil })
	return nodes
}

// leaderOf returns the node that is the leader, or nil if there isn't one yet.
func leaderOf(nodes []*node) *node {
	for _, n := range nodes {
		if n.raft.State() == raft.Leader {
			return n
		}
	}
	return nil
}

// followerOf returns a node that isn't the leader.
func followerOf(nodes []*node) *node {
	for _, n := range nodes {
		if n.raft.State() != raft.Leader {
			return n
		}
	}
	return nil
}

// waitFor checks done until it's true, and fails the test after 5 seconds.
func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if done() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// addChange is a valid change that adds node1 as a holder of a chunk.
func addChange(id string) string {
	return fmt.Sprintf(`{"op":"add","chunk_id":%q,"nodes":["http://node1:9001"]}`, id)
}

// Each address becomes a node named after its host, with a Raft port 1000
// above its chunk port.
func TestParsePeers(t *testing.T) {
	peers, err := parsePeers("http://node1:9001,http://node2:9002")
	if err != nil {
		t.Fatal(err)
	}
	want := []peer{
		{id: "node1", addr: "http://node1:9001", port: "9001", raft: "node1:10001"},
		{id: "node2", addr: "http://node2:9002", port: "9002", raft: "node2:10002"},
	}
	if !slices.Equal(peers, want) {
		t.Errorf("got %+v, want %+v", peers, want)
	}

	if _, err := parsePeers("http://node1"); err == nil {
		t.Error("an address with no port was accepted")
	}
}

// A change sent to the leader is committed, and then every node can answer
// for it from its own copy of the map.
func TestMapThroughLeader(t *testing.T) {
	nodes := newCluster(t, 3)
	id := fingerprint([]byte("hello"))

	rec := send(leaderOf(nodes).routes(), http.MethodPost, "/map", addChange(id))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("post status = %d, body = %q", rec.Code, rec.Body)
	}

	for _, n := range nodes {
		waitFor(t, n.self.id+" to apply the change", func() bool {
			return send(n.routes(), http.MethodGet, "/map/"+id, "").Code == http.StatusOK
		})
		var holders []string
		json.NewDecoder(send(n.routes(), http.MethodGet, "/map/"+id, "").Body).Decode(&holders)
		if want := []string{"http://node1:9001"}; !slices.Equal(holders, want) {
			t.Errorf("%s says %v, want %v", n.self.id, holders, want)
		}
	}
}

// A follower sends changes on to the leader with a 307.
func TestFollowerRedirects(t *testing.T) {
	nodes := newCluster(t, 3)
	leader, follower := leaderOf(nodes), followerOf(nodes)
	waitFor(t, "the follower to learn the leader", func() bool {
		_, ok := follower.leader()
		return ok
	})

	rec := send(follower.routes(), http.MethodPost, "/map", addChange(fingerprint([]byte("hello"))))
	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want 307", rec.Code)
	}
	if got, want := rec.Header().Get("Location"), leader.self.addr+"/map"; got != want {
		t.Errorf("Location = %s, want %s", got, want)
	}
}

// Bad changes are turned away, and the map stays empty.
func TestPostMapRejects(t *testing.T) {
	leader := leaderOf(newCluster(t, 1))
	id := fingerprint([]byte("hello"))
	tests := []struct {
		name string
		body string
	}{
		{"not json", "hello"},
		{"unknown op", fmt.Sprintf(`{"op":"move","chunk_id":%q,"nodes":["http://node1:9001"]}`, id)},
		{"bad chunk id", `{"op":"add","chunk_id":"nope","nodes":["http://node1:9001"]}`},
		{"no nodes", fmt.Sprintf(`{"op":"add","chunk_id":%q,"nodes":[]}`, id)},
		{"unknown node", fmt.Sprintf(`{"op":"add","chunk_id":%q,"nodes":["http://elsewhere:9001"]}`, id)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if rec := send(leader.routes(), http.MethodPost, "/map", tt.body); rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	}
	if all := leader.fsm.All(); len(all) != 0 {
		t.Errorf("map holds %v, want nothing", all)
	}
}

// /status reports the node's name, its state, and the leader.
func TestStatus(t *testing.T) {
	leader := leaderOf(newCluster(t, 1))
	body := send(leader.routes(), http.MethodGet, "/status", "").Body.String()
	for _, want := range []string{`"id":"node1"`, `"state":"Leader"`, `"leader":"node1"`} {
		if !strings.Contains(body, want) {
			t.Errorf("status = %s, want it to contain %s", body, want)
		}
	}
}
