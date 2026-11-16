# Flick

Flick is a video platform built from the storage up. Uploads are converted in the background, then play with adaptive streaming, which switches between 1080p, 720p and 360p to suit the viewer's connection. Live streams from OBS play less than 10 seconds behind, with chat beside them, and can be replayed once they end. Every file is kept on three storage nodes that agree through Raft, so any one node can fail and nothing is lost. A cache keeps popular segments in memory, Grafana shows how every service is doing, and a creator dashboard shows who's watching.

## Architecture

![How the folders connect](docs/folders.svg)

| Folder | Description | Docs |
| --- | --- | --- |
| `web/` | The website. Uploads, lists and plays videos and live streams, shows each stream's chat, and shows the creator dashboard. | [README](web/README.md) |
| `nginx/` | Serves the website, forwards `/api` requests and chat to the API, and forwards `/hls` requests to the cache. | |
| `cache/` | Keeps recently watched segments in memory, and fetches the rest from the API. | [README](cache/README.md) |
| `api/` | Saves uploads, lists videos and asks the queue to convert them. It also sends HLS files to the cache, runs chat through Redis, and counts views. | [README](api/README.md) |
| `queue/` | Hands out one conversion job at a time, and hands a job out again if its worker stops renewing it. | [README](queue/README.md) |
| `worker/` | Converts each upload to HLS in up to 3 qualities, then marks it ready. It gives up after 3 failed tries. | [README](worker/README.md) |
| `ingest/` | Takes live streams from OBS, and saves each segment as soon as it's made. When a stream ends, it becomes a normal video. | [README](ingest/README.md) |
| `storage/` | Three nodes, each saving a copy of every chunk. They share a chunk map through Raft, and the leader repairs missing copies. | [README](storage/README.md) |
| `pkg/chunk/` | Cuts files into chunks, sends them to the nodes, and joins them back together. The API, the worker and the ingest service use it. | [README](pkg/chunk/README.md) |
| `pkg/metrics/` | Counts and times every request, and serves the numbers at `/metrics`. Every Go service uses it. | [README](pkg/metrics/README.md) |
| `monitoring/` | Sets up Prometheus, which collects every service's numbers, and Grafana, which draws them. | [README](monitoring/README.md) |
| `scripts/` | Stops storage nodes at random, and checks that no chunk is lost. | [README](scripts/README.md) |
| `docs/` | The architecture overview and the next steps. | [Architecture](docs/architecture.svg), [Next Steps](docs/next-steps.md) |

## Running It

```sh
# Needs Docker Desktop.
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
| A Node Failing | Run `docker compose stop node2`, then `docker compose start node2`. | Videos still play and upload while it's down. Within seconds of starting, it has a copy of everything it missed. |
| Raft | Find the leader with `curl localhost:9001/status`, then stop that node. | The other two elect a new leader within a few seconds, and uploads keep working. |
| The Cache | Watch a video twice, and run `curl localhost:8082/stats` after each time. | Almost every segment is a hit the second time. |
| Going Live | In OBS, open **Settings**, then **Stream**. Set the service to **Custom**, the server to `rtmp://localhost:1935/live` and the stream key to `stream`, then click **Start Streaming**. | A video called "Live Stream" appears within a few seconds. Open it in two windows to chat. Once you stop, it becomes a normal video. |
| Chaos | Run `sh scripts/chaos.sh`, and watch Grafana. | Nodes go down, the leader changes, and everything recovers. |
