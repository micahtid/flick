# Monitoring

Sets up Prometheus and Grafana. Prometheus collects every service's metrics every 15 seconds, and Grafana draws them as live charts on one dashboard. The dashboard shows which part slowed down or failed, and when.

## Collecting

![How Prometheus and Grafana collect and show the metrics](diagrams/collect.svg)

| File | Description |
| --- | --- |
| `prometheus.yml` | Lists every service Prometheus collects from. Each kind of service is its own job, so the dashboard can show them separately. Workers are discovered through Docker, since any number of them can run. |
| `grafana/datasource.yml` | Connects Grafana to Prometheus. |
| `grafana/dashboards.yml` | Tells Grafana to load every dashboard in `grafana/dashboards/` when it starts. |
| `grafana/dashboards/flick.json` | The Flick dashboard. It's reloaded automatically, even if Grafana is reset. |

## Dashboard Panels

| Panel | Query | Shows |
| --- | --- | --- |
| Request Rate | `sum by (job) (rate(http_requests_total[1m]))` | Requests per second for each service. |
| Error Rate | `sum by (job) (rate(http_requests_total{code=~"5.."}[1m])) or sum by (job) (rate(http_requests_total[1m])) * 0` | Server errors per second for each service. The part after `or` shows 0 for a service with no errors yet, instead of no line. |
| Slowest Requests | `histogram_quantile(0.95, sum by (job, le) (rate(http_request_seconds_bucket[5m])))` | The time 95% of each service's requests finish within (the 95th percentile). |
| Queue Length | `queue_waiting_jobs` | Jobs waiting for a worker. |
| Cache Hit Rate | `rate(cache_hits_total[5m]) / (rate(cache_hits_total[5m]) + rate(cache_misses_total[5m]))` | The fraction of segment requests served from memory. |
| Storage Nodes Up | `up{job="storage"}` | Whether each node is running. |
| Raft Leader | `raft_is_leader` | Which node is the leader. |
| Chunks Stored | `chunks_stored` | Chunks saved on each node. |
| Conversions | `sum(increase(jobs_done_total[5m]))` and `sum(increase(jobs_failed_total[5m]))` | Videos converted and failed attempts in the last 5 minutes. A job is tried up to 3 times. |
| Chat Connections | `sum(chat_connections)` | Viewers connected to chat. |

## Addresses

| Tool | Address | Description |
| --- | --- | --- |
| Prometheus | http://localhost:9090 | Shows each service at `/targets`. Each should show **UP**. |
| Grafana | http://localhost:3001 | Log in with `admin` and `admin`, then open the **Flick** dashboard. |

## Development

| Command | Description |
| --- | --- |
| `docker compose restart prometheus` | Reloads `prometheus.yml` after a change. |
| `docker compose restart grafana` | Reloads the dashboard after a change to `flick.json`. |
