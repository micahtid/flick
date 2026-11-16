package main

// This file holds the repair loop. Every 5 seconds, the leader (the node that
// makes every change to the chunk map) asks every node which chunks it has
// saved, and copies each missing chunk over. This fills in a node that was
// down while chunks were being saved, or one that lost or damaged a copy.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/hashicorp/raft"
)

// These set how often the leader repairs, and how long it waits for a node to
// list its chunks.
const (
	repairEvery = 5 * time.Second
	listTimeout = 5 * time.Second
)

// copyJob is one chunk the leader needs to copy.
type copyJob struct {
	id   string
	from []string // These nodes are up and have the chunk saved.
	to   []string // These nodes are up and missing it.
}

// repairLoop runs a repair every 5 seconds until ctx is cancelled. Every node
// runs the loop, but only the leader does any work. If the leader fails, the
// new leader takes over.
func (n *node) repairLoop(ctx context.Context) {
	t := time.NewTicker(repairEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if n.raft.State() == raft.Leader {
			n.repair(ctx)
		}
	}
}

// repair copies each chunk to the nodes that are up but missing it.
func (n *node) repair(ctx context.Context) {
	for _, job := range plan(n.fsm.All(), n.saved(ctx)) {
		if err := n.copyChunk(ctx, job); err != nil {
			log.Printf("repair chunk %s: %v", job.id, err)
		}
	}
}

// plan lists the chunks in the map that are missing from at least one node
// that's up. saved maps each node that's up to the chunks it has on disk. A
// copy that was lost or deleted counts as missing, even while the map still
// lists it. A chunk with no copy on a node that's up is still listed, so
// copyChunk can report it.
func plan(locs map[string][]string, saved map[string]map[string]bool) []copyJob {
	up := slices.Sorted(maps.Keys(saved))
	var jobs []copyJob
	for id := range locs {
		var from, to []string
		for _, addr := range up {
			if saved[addr][id] {
				from = append(from, addr)
			} else {
				to = append(to, addr)
			}
		}
		if len(to) > 0 {
			jobs = append(jobs, copyJob{id: id, from: from, to: to})
		}
	}
	return jobs
}

// saved asks every node which chunks it has on disk. A node that doesn't
// answer is down, so it's left out.
func (n *node) saved(ctx context.Context) map[string]map[string]bool {
	saved := map[string]map[string]bool{}
	for _, p := range n.peers {
		ids, err := n.list(ctx, p.addr)
		if err != nil {
			continue
		}
		saved[p.addr] = map[string]bool{}
		for _, id := range ids {
			saved[p.addr][id] = true
		}
	}
	return saved
}

// list asks one node for the IDs of the chunks it has saved.
func (n *node) list(ctx context.Context, addr string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	body, err := n.request(ctx, http.MethodGet, addr+"/chunks", nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	var ids []string
	err = json.Unmarshal(body, &ids)
	return ids, err
}

// copyChunk reads a chunk from a node that holds it, saves it on every node
// in job.to, and records the new copies in the map.
func (n *node) copyChunk(ctx context.Context, job copyJob) error {
	data, err := n.fetch(ctx, job)
	if err != nil {
		return err
	}

	var saved []string
	for _, addr := range job.to {
		if _, err := n.request(ctx, http.MethodPut, addr+"/chunks/"+job.id, data, http.StatusNoContent); err != nil {
			log.Printf("copy chunk %s to %s: %v", job.id, addr, err)
			continue
		}
		saved = append(saved, addr)
	}
	if len(saved) == 0 {
		return fmt.Errorf("no node took a copy")
	}
	log.Printf("copied chunk %s to %v", job.id, saved)
	return n.apply(change{Op: opAdd, ChunkID: job.id, Nodes: saved})
}

// fetch reads a chunk from the first node in job.from with a good copy.
func (n *node) fetch(ctx context.Context, job copyJob) ([]byte, error) {
	for _, addr := range job.from {
		data, err := n.request(ctx, http.MethodGet, addr+"/chunks/"+job.id, nil, http.StatusOK)
		if err == nil && fingerprint(data) == job.id {
			return data, nil
		}
	}
	return nil, fmt.Errorf("no node that's up has a good copy")
}

// request makes one request to another node, and returns the reply body. Any
// status other than want is an error. It's the same as send in pkg/chunk.
func (n *node) request(ctx context.Context, method, url string, body []byte, want int) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	resp, err := n.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		return nil, fmt.Errorf("%s replied %s", url, resp.Status)
	}
	// Read one byte over the limit, so a too-big chunk fails its fingerprint.
	return io.ReadAll(io.LimitReader(resp.Body, maxChunkBytes+1))
}
