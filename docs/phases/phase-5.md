# Phase 5: Consensus With Raft

## The Problem

At the end of Phase 4 you found a hole. A node that was down misses some writes, and nobody notices. Over time, chunks quietly lose their copies.

To fix this, the system needs one **chunk map** that it can trust. The map is a record of which nodes hold each chunk. The map itself must not live on just one node, or that node becomes the new weak spot. So all three nodes keep a copy, and the copies must always match, even while nodes crash and restart.

Getting several computers to agree on the same data is called **consensus**. **Raft** is the most popular way to do it. This is the heart of the project and the longest phase.

## How Raft Works

- One node is the **leader**. The others are **followers**.
- Every change goes to the leader first.
- The leader adds the change to its **log**, which is a numbered list of every change ever made. Then it sends the change to the followers.
- When a **majority** of nodes (2 out of 3) have saved the change, it's **committed**. That means it's final, and every node applies it.
- The leader sends a small "I am alive" message, called a **heartbeat**, many times a second. If the followers stop hearing it, they hold an **election** and vote for a new leader.
- A node that was down and comes back gets every change it missed from the leader's log.

Why a majority? Two different majorities of 3 always share at least one node. So two leaders can never both commit changes that disagree.

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

- `PORT` (9001, 9002, 9003) for chunk requests, as before.
- `RAFT_PORT` (10001, 10002, 10003) for Raft messages between the nodes.

## Step 2: Write The State Machine

Raft only copies **commands**, which are small messages that describe a change. You write the code that applies each command to the chunk map. Raft calls this the **FSM** (finite state machine). The name just means "the thing that changes when a command is applied".

There are two commands. `add` means "these nodes now hold this chunk". `remove` means "these nodes no longer hold it".

```go
type Command struct {
    Op      string   `json:"op"` // "add" or "remove"
    ChunkID string   `json:"chunk_id"`
    Nodes   []string `json:"nodes"`
}

type FSM struct {
    mu   sync.RWMutex
    locs map[string][]string // chunk ID -> the nodes that hold it
}

// Raft calls Apply on every node, in the same order, for every committed command.
func (f *FSM) Apply(l *raft.Log) any {
    var c Command
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

The FSM also needs two more methods:

- `Snapshot()` saves the whole map as JSON.
- `Restore()` loads it back.

Without snapshots, the log would grow forever. With them, Raft saves the map now and then and throws away the old log entries.

## Step 3: Start Raft On Each Node

```go
cfg := raft.DefaultConfig()
cfg.LocalID = raft.ServerID(nodeID) // "node1", "node2" or "node3"

store, _ := raftboltdb.NewBoltStore("/chunks/raft.db")        // the log and the votes
snaps, _ := raft.NewFileSnapshotStore("/chunks", 2, os.Stderr) // keep the last 2 snapshots
addr := nodeID + ":" + raftPort
trans, _ := raft.NewTCPTransport(addr, nil, 3, 10*time.Second, os.Stderr) // the network

r, _ := raft.NewRaft(cfg, fsm, store, store, snaps, trans)
```

The very first time the cluster starts, someone has to tell it who its members are. This is called **bootstrapping**. Do it on node1 only:

```go
if os.Getenv("BOOTSTRAP") == "true" {
    r.BootstrapCluster(raft.Configuration{Servers: []raft.Server{
        {ID: "node1", Address: "node1:10001"},
        {ID: "node2", Address: "node2:10002"},
        {ID: "node3", Address: "node3:10003"},
    }})
}
```

In Compose, set `BOOTSTRAP: "true"` on node1 only. Running it again on later starts does no harm. Raft sees that a cluster already exists and returns an error you can ignore.

## Step 4: Send Every Change Through The Leader

The chunk **bytes** still go straight to the nodes, just like in Phase 4. Only the **map** changes go through Raft.

Add a new endpoint to each node, `POST /map`, which takes a `Command`. Only the leader can apply changes. If a follower gets the request, it replies with the leader's address:

```go
if r.State() != raft.Leader {
    _, leaderID := r.LeaderWithID()
    w.Header().Set("Location", httpAddr(leaderID)) // for example http://node2:9002/map
    w.WriteHeader(http.StatusTemporaryRedirect)   // 307: "ask over there instead"
    return
}
f := r.Apply(body, 5*time.Second) // returns once a majority has saved it
if err := f.Error(); err != nil { http.Error(w, err.Error(), 503); return }
```

`httpAddr` is a small helper you write. It turns `node2` into `http://node2:9002/map`.

Add `GET /map/{id}` too. Any node can answer it from its own copy of the map.

Then update `pkg/chunk`:

- **Put.** Send the bytes to the nodes as before. Then call `POST /map` with `add`, the chunk ID, and the list of nodes that saved it. Go's HTTP client follows the `307` reply by itself. If the reply is `503`, an election is happening, so wait a second and try again.
- **Get.** First call `GET /map/{id}` on any node to learn which nodes hold the chunk. Then read it from one of them.

## Step 5: Add A Repair Loop

This loop is what fixes the Phase 4 problem. It runs only on the leader, every 5 seconds:

1. Call `GET /health` on each node to see which ones are alive.
2. Look through the map for any chunk held by fewer than 3 healthy nodes.
3. Copy that chunk from a node that has it to a healthy node that doesn't.
4. Apply an `add` command for the new copy.

So a node that comes back after missing writes gets filled in within a few seconds.

## Step 6: Write A Chaos Script

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

While it runs, keep uploading videos in another terminal. A loop of `curl -F file=@test.mp4 -F title=t localhost/api/videos` works.

When it finishes, check every chunk listed in the `files` table. Each one should be readable and held by 3 nodes.

## Done When

- Stop the leader. Within a few seconds another node becomes leader, and uploads keep working. To find the leader, add `GET /status` to each node so it reports `r.State()`.
- A node that restarts catches up on everything it missed.
- After the chaos script runs for 10 minutes, no chunk is lost.
