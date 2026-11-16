# API

Saves uploads on the storage nodes, records them in Postgres and asks the queue to convert them. It also serves HLS files to the cache and runs chat through Redis. It counts views and watch time for the creator dashboard.

## Startup

![How the API starts](diagrams/startup.svg)

## Requests

![How the API handles each request](diagrams/requests.svg)

| Request | Response |
| --- | --- |
| `POST /videos` | `201` with the new video. `400` for a bad form, such as a title over 200 characters. `413` for a file over 2 GB. `503` if the queue is full. |
| `GET /videos` | `200` with every video, newest first. |
| `GET /files/hls/<id>/<file>` | `200` with the HLS file. Only the cache uses this. `404` if there's no such file. |
| `GET /chat/<id>` | `101` and a WebSocket to the stream's chat. `400` if the ID isn't a UUID. `403` if the request comes from another site. `500` if Redis can't be reached. |
| `POST /videos/<id>/view` | `204` once the view is counted. `400` if the ID isn't a UUID. `500` if Redis can't be reached. |
| `POST /videos/<id>/watch` | `204` once the watch time is counted, from a body such as `{"seconds":30}`. `400` if the ID isn't a UUID or the seconds aren't 1 to 60. `500` if Redis can't be reached. |
| `GET /stats` | `200` with every video's `title`, `views`, `watch_seconds` and `created_at`, newest first. |
| `GET /metrics` | `200` with the API's metrics for Prometheus, such as `chat_connections`. |

## Chat Messages

| Field | Description |
| --- | --- |
| `name` | The sender's name, such as `Guest 4821`. It must be 1 to 50 characters after leading and trailing spaces are trimmed. |
| `text` | The message, such as `Hi!`. It must be 1 to 500 characters after leading and trailing spaces are trimmed. |
| Any Other Field | Removed before the message is broadcast. A message with an invalid `name` or `text` is dropped. |

## Counting Views

| Step | Description |
| --- | --- |
| A Viewer Watches | The player reports a view when playback starts, and watch time every 30 seconds. |
| The API Counts | Each report increments `views:<id>` or `watch:<id>` in Redis and adds the ID to the `counted` set. This is much faster than writing to Postgres every time. |
| Every Minute | `flushLoop()` reads and deletes each video's counters with `GETDEL`, then adds them to the `views` and `watch_seconds` columns of `videos` in one transaction. It also runs once more when the API stops, and the API waits for it to finish before exiting. |

## Edge Cases

| Case | Description |
| --- | --- |
| Postgres Fails During a Flush | The counts go back into Redis for the next flush. None are lost. |
| A View Is Counted During a Flush | Each video's counters are read and deleted in one step, so the view is in this flush or the next, never both. |
| Several Copies of the API Flush at Once | A video's counters can only be taken once, so the other copies find nothing to add. |
| The Video Doesn't Exist | The view is counted anyway, since checking Postgres would make every report slower. The flush matches no row, so it's dropped. |
| Redis Is Down | Uploads and playback still work. Chat and view counting return `500` until it's back. |

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `PORT` | `8080` | The port the API listens on. |
| `QUEUE_URL` | `http://queue:8081` | The queue's URL. |
| `STORAGE_NODES` | `http://node1:9001,http://node2:9002,http://node3:9003` | The storage nodes' URLs, separated by commas. |
| `REDIS_URL` | `redis://redis:6379` | The Redis URL. |
| `DATABASE_URL` | Required | The Postgres connection URL, such as `postgres://postgres:flick@db:5432/postgres?sslmode=disable`. |

## Development

| Command | Description |
| --- | --- |
| `go doc -cmd -u -all .` | Shows every function and type with its documentation. |
| `go test ./...` | Runs the tests. They need no Postgres, Redis or Docker. |
