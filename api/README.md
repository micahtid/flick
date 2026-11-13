# API

Saves uploads to `/data`, records them in Postgres, and asks the queue to convert them.

## Startup

![How the API starts](diagrams/startup.svg)

## Requests

![How the API handles each request](diagrams/requests.svg)

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `PORT` | `8080` | The port the API listens on. |
| `DATA_DIR` | `/data` | The folder uploads are saved to. |
| `QUEUE_URL` | `http://queue:8081` | Where the queue is. |
| `DATABASE_URL` | Required | Where Postgres is, such as `postgres://postgres:flick@db:5432/postgres?sslmode=disable`. |

## Development

| Command | Description |
| --- | --- |
| `go doc -cmd -u -all .` | Shows every function and type with its documentation. |
| `go test ./...` | Runs the tests. They need no Postgres or Docker. |
