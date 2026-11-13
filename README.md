# Flick

A video platform built from the storage up. Upload a video, and workers convert it in the background while you wait. It then plays with adaptive streaming, switching between 1080p, 720p and 360p as your connection speeds up or slows down.

## Architecture

![How the folders connect](docs/folders.svg)

| Folder | Description | Docs |
| --- | --- | --- |
| `web/` | The React site: upload form, video grid, and a watch page that plays HLS with `hls.js`. | [README](web/README.md) |
| `nginx/` | Serves the site and the workers' HLS files (`/hls`), and forwards `/api` requests to the API. | |
| `api/` | Saves uploads, lists videos, and asks the queue to convert them. | [README](api/README.md) |
| `queue/` | Hands out one job per worker, and retries jobs stuck for 5 minutes. | [README](queue/README.md) |
| `worker/` | Converts each video to HLS in 1080p, 720p and 360p, then marks it ready. | [README](worker/README.md) |

`storage/`, `cache/`, `ingest/` and `monitoring/` are placeholders for later phases. See the [roadmap](docs/phases/README.md) and the [full planned architecture](docs/architecture.svg).

## Running It

Requires Docker Desktop.

```sh
docker compose up --build
```

Open http://localhost. Add `--scale worker=2` to run two workers.

## Upgrading From Phase 2

Videos converted before Phase 3 only have a 720p MP4, so they won't play. Run this once to convert them again. It marks every video with no saved length as `processing` and adds a job for each:

```sh
docker compose exec -T db psql -U postgres -qAt \
  -c "UPDATE videos SET status = 'processing' WHERE duration IS NULL RETURNING id" |
while read -r id; do
  curl -s -X POST localhost:8081/jobs -H 'Content-Type: application/json' -d "{\"video_id\": \"$id\"}"
done
```

The old `/data/<id>_720p.mp4` files are no longer used and can be deleted.
