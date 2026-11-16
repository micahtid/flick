# Metrics

Counts and times the requests each service handles, and serves the numbers at `/metrics` for Prometheus to collect. Every Go service uses it, so they all report requests the same way.

## Counting Requests

![How a request is counted](diagrams/requests.svg)

| Function | Description |
| --- | --- |
| `Wrap()` | Wraps a service's whole mux, so every request is counted and timed with no code in each handler. |
| `Handler()` | Serves every number in Prometheus's text format. Each service serves it at `GET /metrics`. |
| `Serve()` | Runs a server with only `GET /metrics`, for services with no server of their own: the worker on port 8090, and the ingest service on port 8083. |

## Numbers

| Name | Kind | Description |
| --- | --- | --- |
| `http_requests_total` | Counter | Requests handled, by `path` and `code`, such as `GET /videos/{id}` and `200`. |
| `http_request_seconds` | Histogram | How long requests take, by `path`. |

## Special Cases

| Case | Description |
| --- | --- |
| A Path Holds an ID | The request is counted under its route, such as `GET /videos/{id}`, not its real path. A new label for every video would fill Prometheus's memory. |
| No Route Matches | The request is counted under the path `unmatched`. |
| The Request Becomes a WebSocket | It's counted, but not timed, since it stays open for as long as the viewer stays. |

## Development

| Command | Description |
| --- | --- |
| `go doc -all .` | Shows every function and type with its documentation. |
| `go test ./...` | Runs the tests. They need no Prometheus or Docker. |

## Additional Information: The Three Kinds of Number

| Kind | Description | Example |
| --- | --- | --- |
| Counter | Only goes up. Prometheus works out how fast it rises. | `http_requests_total` |
| Gauge | Goes up and down. | `queue_waiting_jobs` |
| Histogram | Sorts each measurement into ranges, so Prometheus can work out averages and worst cases. | `http_request_seconds` |
