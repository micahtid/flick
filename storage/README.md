# Storage

Saves chunks of files on its own disk and hands them back. Three nodes run at once, and every chunk ends up on all of them, so any one node can fail and nothing is lost. The nodes also share a chunk map, which records which nodes hold each chunk. Raft keeps the map the same on every node, and the leader uses it to copy chunks to any node that missed them.

## Startup

![How a storage node starts](diagrams/startup.svg)

## Requests

![How a storage node handles each request](diagrams/requests.svg)

| Request | Reply |
| --- | --- |
| `PUT /chunks/<id>` | `204` once the chunk is saved. `400` if the chunk's bytes don't match its ID, and `413` if it's over 4 MB. |
| `GET /chunks/<id>` | `200` with the chunk. `404` if the node doesn't have it, and `500` if its copy is damaged. |
| `POST /map` | `204` once most of the nodes (2 of 3) have saved the change. A follower replies `307` with the leader's address. `400` if the change is bad, and `503` while the nodes elect a leader. |
| `GET /status` | `200` with the node's name, its Raft state and the leader, such as `{"id":"node2","leader":"node1","state":"Follower"}`. |
| `GET /map/<id>` | `200` with the nodes that hold the chunk, such as `["http://node1:9001","http://node3:9003"]`. `404` if the map doesn't list the chunk. |

## Repairs

![How the leader repairs missing copies](diagrams/repair.svg)

## Map Changes

`POST /map` takes one change as JSON, such as `{"op":"add","chunk_id":"3da6...","nodes":["http://node1:9001"]}`.

| Field | Description |
| --- | --- |
| `op` | `add` if the nodes now hold the chunk, or `remove` if they no longer do. |
| `chunk_id` | The chunk's ID. |
| `nodes` | The addresses of the nodes, such as `http://node1:9001`. |

## Special Cases

| Case | Description |
| --- | --- |
| The Same Chunk Is Saved Twice | The second save replaces the first. The bytes are the same, so nothing changes. |
| The Node Crashes Mid-Save | Each save writes a `.tmp` file first, then renames it. A crash leaves a stray `.tmp` file, never half a chunk. |
| A Chunk Is Damaged on Disk | The node replies `500` instead of sending bad data, and the client asks the next node. |
| The Node Was Down During an Upload | The node never gets that upload's chunks. Once it's back, the leader's repair loop, which runs every 5 seconds, copies them over. Every node runs the loop, but only the leader does any work. |
| The Leader Fails | The others stop hearing heartbeats, and elect a new leader within a few seconds. Changes sent in the meantime get `503`, and the client tries again. |
| A Node Restarts | It reloads its map from its last snapshot and log, then gets every change it missed from the leader. |
| Two Nodes Are Down | No majority is left, so no change can be committed and no leader can be elected. Uploads fail until a second node is back. |

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `NODE_ID` | `node1` | The node's name. It must match a host in `STORAGE_NODES`. |
| `STORAGE_NODES` | `http://node1:9001,http://node2:9002,http://node3:9003` | Where the storage nodes are, this one included, separated by commas. Each node takes Raft messages on its port plus 1000, such as `10001`. |
| `BOOTSTRAP` | `false` | Set to `true` on one node only, so it sets up the cluster on its first start. Compose sets it on `node1`. |
| `CHUNK_DIR` | `/chunks` | The folder chunks are saved to. Each node has its own Docker volume here. |

## Development

| Command | Description |
| --- | --- |
| `go doc -cmd -u -all .` | Shows every function and type with its documentation. |
| `go test ./...` | Runs the tests. Raft runs for real, but through memory, so they need no Docker. |

## Additional Information: The Chunk Map

The chunk map records which nodes hold each chunk. Every node keeps its own copy, and Raft keeps the copies the same.

| Topic | Description |
| --- | --- |
| What It Holds | Each chunk ID and the nodes that hold it, such as `3da6...` → `["http://node1:9001","http://node3:9003"]`. |
| Where It Lives | A Go map in each node's memory (`locs` in `fsm.go`). It's lost when the node stops. |
| How It Changes | Only the leader takes changes (`POST /map`). Raft copies each one to the other nodes, and once 2 of 3 have saved it, every node applies it with `Apply()`. |
| How It's Read | Any node answers `GET /map/<id>` from its own copy. The client reads it before fetching a chunk, and the leader reads it to find missing copies. |
| What's on Disk | Not the map itself, but Raft's log of changes and its snapshots of the whole map, in `/chunks/raft/`. |
| Startup, Step 1 | Raft loads the newest snapshot, and `Restore()` fills the map with it. |
| Startup, Step 2 | Raft replays the changes in its log that came after the snapshot, through `Apply()`. The map is now as it was when the node stopped. |
| Startup, Step 3 | The node rejoins the cluster, and the leader sends every change it missed while it was down. If the leader no longer has those changes, it sends its own snapshot instead. |

## Additional Information: Raft Terms

| Term | Description |
| --- | --- |
| Leader | The one node that can change the map. It sends every change to the other nodes, and sends a heartbeat many times a second. |
| Follower | Any other node. It saves the changes the leader sends. If the heartbeats stop for about a second, it starts an election. |
| Committed | A change that most of the nodes (2 of 3) have saved. It's final, and every node applies it to its map. |
| Snapshot | A copy of the whole map, saved now and then, so Raft can throw away the older changes in its log. |
| Bootstrap | Telling Raft who the members are. Only `node1` does this, and only the very first time the cluster starts. |
| Log | Every change, in order. Raft saves it in `/chunks/raft/`, along with its votes and snapshots, so they survive a restart. |
