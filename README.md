# Flick

Flick is a video platform built from the storage up. Upload a video, and workers convert it in the background. It then plays with adaptive streaming, switching between 1080p, 720p and 360p as your connection speeds up or slows down. Every file is kept on three storage nodes, so any one node can fail and nothing is lost.

## Architecture

![How the folders connect](docs/folders.svg)

| Folder | Description | Docs |
| --- | --- | --- |
| `web/` | Shows the upload form and video grid, and plays videos with `hls.js`. | [README](web/README.md) |
| `nginx/` | Serves the site, and forwards `/api` and `/hls` requests to the API. | |
| `api/` | Saves uploads, lists videos, asks the queue to convert them, and sends HLS files to the browser. | [README](api/README.md) |
| `queue/` | Hands out one job per worker, and retries jobs stuck for 5 minutes. | [README](queue/README.md) |
| `worker/` | Converts each video to HLS in 1080p, 720p and 360p, then marks it ready. | [README](worker/README.md) |
| `storage/` | Saves chunks on its own disk. Three nodes run at once, each with a copy of every chunk. | [README](storage/README.md) |
| `pkg/chunk/` | Cuts files into chunks, sends them to the nodes, and joins them back into files. The API and the worker share it. | [README](pkg/chunk/README.md) |

`cache/`, `ingest/` and `monitoring/` are placeholders for later phases. See the [roadmap](docs/phases/README.md) and the [full planned architecture](docs/architecture.svg).

## Running It

Requires Docker Desktop.

```sh
docker compose up --build
```

Open http://localhost. Add `--scale worker=2` to run two workers.

To see the copies at work, run `docker compose stop node2`. Videos still play and uploads still work. Run `docker compose start node2` to bring it back.

## Upgrading From Phase 3

Videos uploaded before Phase 4 are in the old `uploads` volume, which nothing reads now, so they won't play. Run this once to remove them, then upload them again:

```sh
docker compose exec -T db psql -U postgres -c \
  "DELETE FROM videos WHERE NOT EXISTS (SELECT 1 FROM files WHERE path = 'uploads/' || videos.id || '.mp4')"
docker volume rm flick_uploads
```
