# Flick

Flick is a video platform built from the storage up. Upload a video, and workers convert it in the background. It then plays with adaptive streaming, switching between 1080p, 720p and 360p as your connection speeds up or slows down. Every file is kept on three storage nodes, so any one node can fail and nothing is lost. The nodes use Raft to agree on which node holds each chunk, and copy chunks to any node that missed them.

## Architecture

![How the folders connect](docs/folders.svg)

| Folder | Description | Docs |
| --- | --- | --- |
| `web/` | Shows the upload form and video grid, and plays videos with `hls.js`. | [README](web/README.md) |
| `nginx/` | Serves the site, and forwards `/api` and `/hls` requests to the API. | |
| `api/` | Saves uploads, lists videos, asks the queue to convert them, and sends HLS files to the browser. | [README](api/README.md) |
| `queue/` | Hands out one job per worker, and retries jobs stuck for 5 minutes. | [README](queue/README.md) |
| `worker/` | Converts each video to HLS in 1080p, 720p and 360p, then marks it ready. | [README](worker/README.md) |
| `storage/` | Saves chunks on its own disk. Three nodes run at once, each with a copy of every chunk. They share a chunk map through Raft, and the leader repairs missing copies. | [README](storage/README.md) |
| `pkg/chunk/` | Cuts files into chunks, sends them to the nodes, and joins them back into files. The API and the worker share it. | [README](pkg/chunk/README.md) |
| `scripts/` | Tests the storage nodes by stopping them at random, checks that no chunk is lost, and adds old chunks to the chunk map. | [README](scripts/README.md) |

`cache/`, `ingest/` and `monitoring/` are placeholders for later phases. See the [roadmap](docs/phases/README.md) and the [full planned architecture](docs/architecture.svg).

## Running It

Requires Docker Desktop.

```sh
docker compose up --build
```

Open http://localhost. Add `--scale worker=2` to run two workers.

To see the copies at work, run `docker compose stop node2`. Videos still play and uploads still work. Run `docker compose start node2` to bring it back, and within a few seconds it has a copy of everything it missed.

To see Raft at work, find the leader with `curl localhost:9001/status`, then stop that node. Within a few seconds, the other two elect a new leader, and uploads keep working.

## Upgrading From Phase 3

Videos uploaded before Phase 4 are in the old `uploads` volume, which nothing reads now, so they won't play. Run this once to remove them, then upload them again:

```sh
docker compose exec -T db psql -U postgres -c \
  "DELETE FROM videos WHERE NOT EXISTS (SELECT 1 FROM files WHERE path = 'uploads/' || videos.id || '.mp4')"
docker volume rm flick_uploads
```

## Upgrading From Phase 4

Chunks saved before Phase 5 aren't in the chunk map, so the repair loop can't see them. They still play, since a chunk missing from the map is looked for on every node. Run this once to add them to the map, and the repair loop fills in any missing copies:

```sh
sh scripts/backfill.sh
```
