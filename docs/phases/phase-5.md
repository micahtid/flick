# Phase 5: Consensus With Raft

## The Problem

At the end of Phase 4 you found a hole. A node that was down misses some writes, and nobody notices. Over time, chunks quietly lose their copies.

To fix this, the system needs one **chunk map** that it can trust. The map is a record of which nodes hold each chunk. The map itself must not live on just one node, or that node becomes the new weak spot. So all three nodes keep a copy, and the copies must always match, even while nodes crash and restart.

Getting several computers to agree on the same data is called **consensus**. **Raft** is the most popular way to do it. This is the heart of the project and the longest phase.

## The Fix

Keep a copy of the map on every node, and use **Raft** to keep the copies the same. This is how Raft works:

- One node is the **leader**. The others are **followers**.
- Every change goes to the leader first.
- The leader adds the change to its **log**, which is a numbered list of every change ever made. Then it sends the change to the followers.
- When a **majority** of nodes (2 out of 3) have saved the change, it's **committed**. That means it's final, and every node applies it.
- The leader sends a small "I am alive" message, called a **heartbeat**, many times a second. If the followers stop hearing it, they hold an **election** and vote for a new leader.
- A node that was down and comes back gets every change it missed from the leader's log.

Why a majority? Two different majorities of 3 always share at least one node. So two leaders can never both commit changes that disagree.

## The Parts In This Phase

| Part | Where | Job |
| --- | --- | --- |
| Chunk map | `storage/fsm.go` (new) | Records which nodes hold each chunk. Every node keeps a copy. |
| Raft | `storage/cluster.go` (new), ports 10001 to 10003 | Keeps every node's copy of the map the same, and picks a leader. |
| Repair loop | `storage/repair.go` (new) | Runs on the leader and copies chunks to any node that missed them. |
| Scripts | `scripts/` (new) | Break the nodes on purpose, check that no chunk is lost, and add old chunks to the map. |

## Step 0: Learn Raft First

Don't write any code until you understand the ideas above.

1. Watch the animated guide at thesecretlivesofdata.com/raft. It takes about 10 minutes.
2. Read the Raft paper, "In Search of an Understandable Consensus Algorithm". It was written to be easy to follow.

## Step 1: Add The Raft Library

Writing Raft yourself is a great challenge for later. MIT course 6.5840 has labs that walk you through it. For now, use a proven library so you can focus on the system around it.

```sh
cd storage
go get github.com/hashicorp/raft github.com/hashicorp/raft-boltdb/v2
```

`raft-boltdb` saves Raft's log to a file on disk, so it survives restarts.

Each node now listens on two ports:

- 9001, 9002 or 9003 for chunk requests, as before.
- 10001, 10002 or 10003 for Raft messages between the nodes.

Every node needs to know where the others are, so give the nodes the same `STORAGE_NODES` list as the API and the workers. Each node finds its own entry by its `NODE_ID`, and its Raft port is its chunk port plus 1000. That way, the list of nodes is written in one place.

## Step 2: Write The State Machine

Raft only copies **changes**, which are small messages that describe one update to the map. You write the code that applies each change to the chunk map. Raft calls this the **FSM** (finite state machine). The name just means "the thing that Raft's changes are applied to".

There are two kinds of change. `add` means "these nodes now hold this chunk". `remove` means "these nodes no longer hold it".

```go
type change struct {
    Op      string   `json:"op"` // "add" or "remove"
    ChunkID string   `json:"chunk_id"`
    Nodes   []string `json:"nodes"`
}

type fsm struct {
    mu   sync.RWMutex
    locs map[string][]string // chunk ID -> the nodes that hold it
}

// Raft calls Apply on every node, in the same order, for every committed change.
func (f *fsm) Apply(l *raft.Log) any {
    var c change
    json.Unmarshal(l.Data, &c)
    f.mu.Lock(); defer f.mu.Unlock()
    switch c.Op {
    case "add":    f.locs[c.ChunkID] = union(f.locs[c.ChunkID], c.Nodes)
    case "remove": f.locs[c.ChunkID] = minus(f.locs[c.ChunkID], c.Nodes)
    }
    return nil
}
```

`union` and `minus` are small helpers that you write. `union` joins two lists without repeats. `minus` removes items from a list.

The `fsm` also needs two more methods:

- `Snapshot()` saves the whole map as JSON.
- `Restore()` loads it back.

Without snapshots, the log would grow forever. With them, Raft saves the map now and then and throws away the old log entries.

## Step 3: Start Raft On Each Node

```go
cfg := raft.DefaultConfig()
cfg.LocalID = raft.ServerID(self.id) // "node1", "node2" or "node3"

store, _ := raftboltdb.NewBoltStore("/chunks/raft/raft.db")        // the log and the votes
snaps, _ := raft.NewFileSnapshotStore("/chunks/raft", 2, os.Stderr) // keep the last 2 snapshots
trans, _ := raft.NewTCPTransport(self.raft, nil, 3, 10*time.Second, os.Stderr) // the network, such as node1:10001

r, _ := raft.NewRaft(cfg, f, store, store, snaps, trans)
```

`self` is this node's entry in a list of `peer`s that you read from `STORAGE_NODES`. Each `peer` has the node's name (`node1`), its address (`http://node1:9001`) and its Raft address (`node1:10001`).

Raft's files go in their own folder, `/chunks/raft/`, so they don't sit among the chunks. It's on the node's volume, so they survive restarts.

The very first time the cluster starts, someone has to tell it who its members are. This is called **bootstrapping**. Do it on node1 only:

```go
if os.Getenv("BOOTSTRAP") == "true" {
    var cfg raft.Configuration
    for _, p := range peers { // one for each node in STORAGE_NODES
        cfg.Servers = append(cfg.Servers, raft.Server{ID: raft.ServerID(p.id), Address: raft.ServerAddress(p.raft)})
    }
    r.BootstrapCluster(cfg)
}
```

Set `BOOTSTRAP` on node1 only (see Step 6). Running it again on later starts does no harm. Raft sees that a cluster already exists and returns an error you can ignore.

## Step 4: Send Every Change Through The Leader

The chunk **bytes** still go straight to the nodes, just like in Phase 4. Only the **map** changes go through Raft.

Add three endpoints to each node:

| Method | Path | What it does |
| --- | --- | --- |
| `POST` | `/map` | Applies one `change` through Raft. Replies `204` once a majority has saved it. |
| `GET` | `/map/{id}` | Replies with the nodes that hold the chunk. Any node can answer from its own copy of the map. |
| `GET` | `/status` | Replies with this node's name, its Raft state and the leader. It shows which node is the leader. |

Only the leader can apply changes. If a follower gets a `POST /map`, it replies with the leader's address. If no leader has been elected yet, it replies `503`:

```go
if n.raft.State() != raft.Leader {
    leader, ok := n.leader() // looks up the leader's peer, such as http://node2:9002
    if !ok { http.Error(w, "no leader yet", 503); return }
    http.Redirect(w, r, leader.addr+"/map", http.StatusTemporaryRedirect) // 307: "ask over there instead"
    return
}
f := n.raft.Apply(body, 5*time.Second) // returns once a majority has saved it
if err := f.Error(); err != nil { http.Error(w, err.Error(), 503); return }
```

Then update `pkg/chunk`:

- **Put.** Send the bytes to the nodes as before. Then call `POST /map` with `add`, the chunk ID, and the list of nodes that saved it. Go's HTTP client follows the `307` reply by itself. If the reply is `503`, an election is happening, so wait a second and try again.
- **Get.** First call `GET /map/{id}` on any node to learn which nodes hold the chunk. Ask those nodes first, then the others, in case the map is behind.

Chunks saved in Phase 4 aren't in the map yet. Write a script, `scripts/backfill.sh`, that adds each chunk in the `files` table to the map for every node that has it. Run it once.

## Step 5: Add A Repair Loop

This loop is what fixes the Phase 4 problem. It runs only on the leader, every 5 seconds:

1. Call `GET /health` on each node to see which ones are alive.
2. Look through the map for any chunk held by fewer than 3 nodes that are up.
3. Copy that chunk from a node that has it to a node that's up and doesn't have it.
4. Apply an `add` change for the new copy.

So a node that comes back after missing writes gets filled in within a few seconds.

## Step 6: Update Compose

- Pass `STORAGE_NODES` to the nodes too. Write the list once at the top of the file with a YAML anchor, such as `x-storage-nodes: &storage-nodes http://node1:9001,...`, and use `*storage-nodes` everywhere.
- Set `BOOTSTRAP: "true"` on node1 only.
- Remove `PORT` from the nodes. Each node finds its port in `STORAGE_NODES`.

## Step 7: Write A Chaos Script

To trust the system, you have to break it on purpose. Save this as `scripts/chaos.sh`. It stops and starts random nodes for 10 minutes:

```sh
#!/bin/sh
end=$(( $(date +%s) + 600 ))   # 600 seconds = 10 minutes
while [ $(date +%s) -lt $end ]; do
  n=node$(shuf -i 1-3 -n 1)    # pick node1, node2 or node3
  docker compose stop $n;  sleep $(shuf -i 5-20 -n 1)
  docker compose start $n; sleep $(shuf -i 5-20 -n 1)
done
```

While it runs, keep uploading videos in another terminal, such as with `while true; do curl -F file=@test.mp4 -F title=Test localhost/api/videos; sleep 8; done`.

When it finishes, check every chunk listed in the `files` table. Each one should be readable and held by 3 nodes. Save this check as `scripts/check.sh`, so you can run it again after any change.

## Done When

- Stop the leader. Within a few seconds another node becomes leader, and uploads keep working. To find the leader, call `GET /status` on any node.
- A node that restarts catches up on everything it missed.
- After the chaos script runs for 10 minutes, no chunk is lost.
