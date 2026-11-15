package main

// This file connects the node to the other nodes with Raft. It also handles
// the requests that read and change the chunk map, and answers /status.

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb/v2"
)

// These are the cluster's settings.
const (
	// raftPortOffset is added to a node's chunk port to get its Raft port, so
	// node1 takes chunk requests on 9001 and Raft messages on 10001.
	raftPortOffset = 1000
	// applyTimeout is how long a change may take to reach most of the nodes.
	applyTimeout = 5 * time.Second
	// maxChangeBytes is the largest change the node accepts. A real one is
	// well under 1 KB.
	maxChangeBytes = 64 << 10
)

// peer is one node in the cluster.
type peer struct {
	id   string // This is the node's name, such as node1.
	addr string // This is where chunk requests go, such as http://node1:9001.
	port string // This is the port in addr, such as 9001.
	raft string // This is where Raft messages go, such as node1:10001.
}

// parsePeers reads the nodes from a comma-separated list of addresses, the way
// the STORAGE_NODES environment variable holds them. Each node is named after
// its host, so http://node1:9001 is node1.
func parsePeers(list string) ([]peer, error) {
	var peers []peer
	for _, addr := range strings.Split(list, ",") {
		u, err := url.Parse(addr)
		if err != nil {
			return nil, err
		}
		port, err := strconv.Atoi(u.Port())
		if err != nil {
			return nil, fmt.Errorf("%s has no port", addr)
		}
		peers = append(peers, peer{
			id:   u.Hostname(),
			addr: addr,
			port: u.Port(),
			raft: net.JoinHostPort(u.Hostname(), strconv.Itoa(port+raftPortOffset)),
		})
	}
	return peers, nil
}

// startRaft starts Raft for this node. Raft saves its log and votes in
// dir/raft.db, and its snapshots in dir, so all of them survive a restart.
// Only the very first start of the cluster needs bootstrap, which tells Raft
// who the members are.
func startRaft(self peer, peers []peer, dir string, bootstrap bool, f *fsm) (*raft.Raft, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	cfg := raft.DefaultConfig()
	cfg.LocalID = raft.ServerID(self.id)
	cfg.LogLevel = "INFO" // The default, DEBUG, logs every heartbeat.

	store, err := raftboltdb.NewBoltStore(filepath.Join(dir, "raft.db"))
	if err != nil {
		return nil, err
	}
	snaps, err := raft.NewFileSnapshotStore(dir, 2, os.Stderr) // Keep the last 2 snapshots.
	if err != nil {
		return nil, err
	}
	trans, err := raft.NewTCPTransport(self.raft, nil, 3, 10*time.Second, os.Stderr)
	if err != nil {
		return nil, err
	}

	r, err := raft.NewRaft(cfg, f, store, store, snaps, trans)
	if err != nil {
		return nil, err
	}
	if bootstrap {
		err := r.BootstrapCluster(membership(peers)).Error()
		// The cluster already exists after the first start, so this error is
		// expected and harmless.
		if err != nil && !errors.Is(err, raft.ErrCantBootstrap) {
			return nil, err
		}
	}
	return r, nil
}

// membership lists the nodes in the form Raft needs to bootstrap.
func membership(peers []peer) raft.Configuration {
	var cfg raft.Configuration
	for _, p := range peers {
		cfg.Servers = append(cfg.Servers, raft.Server{
			ID:      raft.ServerID(p.id),
			Address: raft.ServerAddress(p.raft),
		})
	}
	return cfg
}

// postMap makes one change to the chunk map. Only the leader can do this, so a
// follower replies 307 with the leader's address, and the client asks there
// instead.
func (n *node) postMap(w http.ResponseWriter, r *http.Request) {
	if n.raft.State() != raft.Leader {
		leader, ok := n.leader()
		if !ok {
			http.Error(w, "no leader yet", http.StatusServiceUnavailable)
			return
		}
		http.Redirect(w, r, leader.addr+"/map", http.StatusTemporaryRedirect)
		return
	}

	var c change
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxChangeBytes)).Decode(&c); err != nil {
		http.Error(w, "bad change", http.StatusBadRequest)
		return
	}
	if err := n.check(c); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// This fails while the nodes elect a new leader. The client waits a second
	// and tries again.
	if err := n.apply(c); err != nil {
		log.Printf("apply %s %s: %v", c.Op, c.ChunkID, err)
		http.Error(w, "change not saved", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent) // 204 means saved, with nothing to send back.
}

// getMap replies with the nodes that hold a chunk, such as
// ["http://node1:9001","http://node3:9003"]. Any node can answer from its own
// copy of the map.
func (n *node) getMap(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validID.MatchString(id) {
		http.Error(w, "bad chunk id", http.StatusBadRequest)
		return
	}
	holders := n.fsm.Holders(id)
	if len(holders) == 0 {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, holders)
}

// status replies with this node's name, its Raft state (Leader, Follower or
// Candidate) and the leader's name. It shows which node is the leader.
func (n *node) status(w http.ResponseWriter, _ *http.Request) {
	leader, _ := n.leader()
	writeJSON(w, map[string]string{
		"id":     n.self.id,
		"state":  n.raft.State().String(),
		"leader": leader.id,
	})
}

// check makes sure a change is one the map can apply.
func (n *node) check(c change) error {
	if c.Op != opAdd && c.Op != opRemove {
		return fmt.Errorf("unknown op %q", c.Op)
	}
	if !validID.MatchString(c.ChunkID) {
		return errors.New("bad chunk id")
	}
	if len(c.Nodes) == 0 {
		return errors.New("no nodes")
	}
	for _, addr := range c.Nodes {
		if !slices.ContainsFunc(n.peers, func(p peer) bool { return p.addr == addr }) {
			return fmt.Errorf("unknown node %s", addr)
		}
	}
	return nil
}

// apply sends a change through Raft. It returns once most of the nodes (2 of
// 3) have saved it, which is when the change is committed.
func (n *node) apply(c change) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return n.raft.Apply(data, applyTimeout).Error()
}

// leader returns the current leader, or false while the nodes are still
// electing one.
func (n *node) leader() (peer, bool) {
	_, id := n.raft.LeaderWithID()
	i := slices.IndexFunc(n.peers, func(p peer) bool { return p.id == string(id) })
	if i < 0 {
		return peer{}, false
	}
	return n.peers[i], true
}

// writeJSON sends v as a JSON reply.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write reply: %v", err)
	}
}
