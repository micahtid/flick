# Flick

Flick is a video platform built from the storage up. Uploaded videos are converted in the background. They play in 1080p, 720p or 360p, and switch quality to suit the viewer's connection. Live streams from OBS play less than 10 seconds behind, with chat beside them, and can be replayed once they end. A creator dashboard shows how many people watch each video, and for how long.

Every file is saved on three storage nodes, so any one node can fail and nothing is lost. The nodes share a chunk map, which lists where each piece of a file is kept, and Raft keeps it the same on every node. A cache keeps recently watched segments in memory, so a popular video's segments are fetched once instead of once per viewer. Grafana draws live charts of every service, so you can see which part slowed down or failed, and when.

## Architecture

![How the folders connect](docs/folders.svg)

| Folder | Description | Docs |
| --- | --- | --- |
| `web/` | The website. It uploads, lists and plays videos and live streams. It also shows each stream's chat and the creator dashboard. | [README](web/README.md) |
| `nginx/` | Serves the website. It forwards `/api` requests and chat to the API, and `/hls` requests to the cache. | |
| `cache/` | Keeps recently watched segments in memory and fetches the rest from the API. | [README](cache/README.md) |
| `api/` | Saves uploads, lists videos and asks the queue to convert them. It also serves HLS files to the cache, runs chat through Redis and counts views. | [README](api/README.md) |
| `queue/` | Assigns conversion jobs to workers and reassigns a job if its worker stops renewing it. | [README](queue/README.md) |
| `worker/` | Converts each upload to HLS in up to 3 qualities, then marks it ready. It gives up after 3 failed attempts. | [README](worker/README.md) |
| `ingest/` | Takes live streams from OBS and saves each segment as soon as it's made. When a stream ends, it becomes a normal video. | [README](ingest/README.md) |
| `storage/` | Runs three nodes that each save a copy of every chunk. They share a chunk map through Raft, and the leader repairs missing copies. | [README](storage/README.md) |
| `pkg/chunk/` | Splits files into chunks, sends them to the nodes and joins them back together. The API, the worker and the ingest service use it. | [README](pkg/chunk/README.md) |
| `pkg/metrics/` | Counts and times every request and serves the metrics at `/metrics`. Every Go service uses it. | [README](pkg/metrics/README.md) |
| `monitoring/` | Sets up Prometheus, which collects every service's metrics, and Grafana, which draws them. | [README](monitoring/README.md) |
| `scripts/` | Stops storage nodes at random and checks that no chunk is lost. | [README](scripts/README.md) |

## Running It

```sh
# Requires Docker Desktop.
docker compose up --build
```

| Address | Description |
| --- | --- |
| http://localhost | The website. |
| http://localhost/dashboard | The creator dashboard. A new view shows up within about 90 seconds. |
| http://localhost:3001 | Grafana. Log in with `admin` and `admin`, then open the **Flick** dashboard. |
| http://localhost:9090 | Prometheus. |

## Trying It Out

| Try | How | What Happens |
| --- | --- | --- |
| Two Workers | Add `--scale worker=2` to `docker compose up`. | Two videos convert at once. |
| A Node Failing | Run `docker compose stop node2`, then `docker compose start node2`. | Videos still play and upload while it's down. Within seconds of restarting, it gets a copy of everything it missed. |
| Raft | Find the leader with `curl localhost:9001/status`, then stop that node. | The other two elect a new leader within a few seconds, and uploads keep working. |
| The Cache | Watch a video twice and run `curl localhost:8082/stats` after each viewing. | Almost every segment is a hit the second time. |
| Going Live | In OBS, open **Settings**, then **Stream**. Set the service to **Custom**, the server to `rtmp://localhost:1935/live` and the stream key to `stream`, then click **Start Streaming**. | A video called "Live Stream" appears within a few seconds. Open it in two windows to chat. Once you stop, it becomes a normal video. |
| Chaos | Run `sh scripts/chaos.sh` and watch Grafana. | Nodes go down, the leader changes and everything recovers. |
