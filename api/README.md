# API

Saves uploads on the storage nodes, records them in Postgres, and asks the queue to convert them. It also sends HLS files to the cache.

## Startup

![How the API starts](diagrams/startup.svg)

## Requests

![How the API handles each request](diagrams/requests.svg)

| Request | Reply |
| --- | --- |
| `POST /videos` | `201` with the new video. `400` for a bad form, `413` for a file over 2 GB, and `503` if the queue is full. |
| `GET /videos` | `200` with every video, newest first. |
| `GET /files/hls/<id>/<file>` | `200` with the HLS file. `404` if there's no such file. The cache asks for `/hls/...` files here. |

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `PORT` | `8080` | The port the API listens on. |
| `QUEUE_URL` | `http://queue:8081` | Where the queue is. |
| `STORAGE_NODES` | `http://node1:9001,http://node2:9002,http://node3:9003` | Where the storage nodes are, separated by commas. |
| `DATABASE_URL` | Required | Where Postgres is, such as `postgres://postgres:flick@db:5432/postgres?sslmode=disable`. |

## Development

| Command | Description |
| --- | --- |
| `go doc -cmd -u -all .` | Shows every function and type with its documentation. |
| `go test ./...` | Runs the tests. They need no Postgres or Docker. |
