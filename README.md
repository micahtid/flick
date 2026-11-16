# Flick

Flick is a video platform built from the storage up. Upload a video, and workers convert it in the background. It then plays with adaptive streaming, switching between 1080p, 720p and 360p as your connection speeds up or slows down. Or go live from OBS, and viewers watch less than 10 seconds behind while they chat. When the stream ends, anyone can replay it. A cache keeps recently watched segments in memory, so a popular video is read from storage once instead of once per viewer. Every file is kept on three storage nodes, so any one node can fail and nothing is lost. The nodes use Raft to agree on which node holds each chunk, and copy chunks to any node that missed them. Every service reports numbers about itself, which Prometheus collects and Grafana draws on a live dashboard, and a creator dashboard shows how many people watch each video.

## Architecture

![How the folders connect](docs/folders.svg)

| Folder | Description | Docs |
| --- | --- | --- |
| `web/` | Shows the upload form and video grid, plays videos and streams with `hls.js`, shows each stream's chat, and shows the creator dashboard. | [README](web/README.md) |
| `nginx/` | Serves the site, forwards `/api` requests and chat to the API, and forwards `/hls` requests to the cache. | |
| `cache/` | Keeps recently watched segments in memory, and fetches the rest from the API. When it's full, it evicts the least recently used segment. | [README](cache/README.md) |
| `api/` | Saves uploads, lists videos, asks the queue to convert them, and sends HLS files to the cache. It also passes chat messages between viewers through Redis, and counts views and watch time. | [README](api/README.md) |
| `queue/` | Hands out one job per worker, and retries jobs stuck for 5 minutes. | [README](queue/README.md) |
| `worker/` | Converts each video to HLS in 1080p, 720p and 360p, then marks it ready. | [README](worker/README.md) |
| `ingest/` | Takes live streams from OBS, converts them to HLS as they come in, and saves each segment as soon as it's finished. When a stream ends, it becomes a normal video. | [README](ingest/README.md) |
| `storage/` | Saves chunks on its own disk. Three nodes run at once, each with a copy of every chunk. They share a chunk map through Raft, and the leader repairs missing copies. | [README](storage/README.md) |
| `pkg/chunk/` | Cuts files into chunks, sends them to the nodes, and joins them back into files. The API, the worker and the ingest service share it. | [README](pkg/chunk/README.md) |
| `pkg/metrics/` | Counts and times every request, and serves the numbers at `/metrics`. Every Go service shares it. | [README](pkg/metrics/README.md) |
| `monitoring/` | Sets up Prometheus, which collects every service's numbers, and Grafana, which draws them on a dashboard. | [README](monitoring/README.md) |
| `scripts/` | Tests the storage nodes by stopping them at random, checks that no chunk is lost, and adds old chunks to the chunk map. | [README](scripts/README.md) |

See the [roadmap](docs/phases/README.md) and the [full planned architecture](docs/architecture.svg).

## Running It

Requires Docker Desktop.

```sh
docker compose up --build
```

Open http://localhost. Add `--scale worker=2` to run two workers.

To see the copies at work, run `docker compose stop node2`. Videos still play and uploads still work. Run `docker compose start node2` to bring it back, and within a few seconds it has a copy of everything it missed.

To see Raft at work, find the leader with `curl localhost:9001/status`, then stop that node. Within a few seconds, the other two elect a new leader, and uploads keep working.

To see the cache at work, watch a video, then run `curl localhost:8082/stats`. Watch it again and check once more. Almost every segment the second time is a hit.

To go live, open OBS, then **Settings**, then **Stream**. Set the service to **Custom**, the server to `rtmp://localhost:1935/live` and the stream key to `stream`, then click **Start Streaming**. Within a few seconds, a video called "Live Stream" appears on the site. Open it in two windows to chat between them. When you stop streaming, it turns into a normal video.

To see the numbers, open Grafana at http://localhost:3001, log in with `admin` and `admin`, and open the **Flick** dashboard. Run `sh scripts/chaos.sh` to watch nodes go down, the leader change, and everything recover. Prometheus is at http://localhost:9090.

To see who's watching, click **Creator Dashboard** on the home page, or open http://localhost/dashboard. New views show up within a minute.

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
