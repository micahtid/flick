# Queue

Hands out conversion jobs one at a time, and retries any job a worker doesn't finish within 5 minutes.

## Startup

![How the queue starts](diagrams/startup.svg)

## Requests

![How the queue handles each request](diagrams/requests.svg)

| Request | Reply |
| --- | --- |
| `POST /jobs` | `201` with the new job. `400` for a bad body, and `429` if too many jobs are waiting. |
| `POST /jobs/claim` | `200` with the oldest waiting job. `204` if no job is waiting. |
| `POST /jobs/<id>/done` | `204` once the job is marked done. `400` for a bad ID, and `404` if there's no such job. |

## Job States

| State | Description | Moves On When |
| --- | --- | --- |
| `waiting` | The job is in line for a worker. | A worker claims it, and it becomes `running`. |
| `running` | A worker is converting the video. | The worker marks it `done`, or after 5 minutes it goes back to `waiting`. |
| `done` | The video is converted. | Never. This is the end. |

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `PORT` | `8081` | The port the queue listens on. |
| `DATABASE_URL` | Required | Where Postgres is, such as `postgres://postgres:flick@db:5432/postgres?sslmode=disable`. |

## Development

| Command | Description |
| --- | --- |
| `go doc -cmd -u -all .` | Shows every function and type with its documentation. |
| `go test ./...` | Runs the tests. They need no Postgres or Docker. |
