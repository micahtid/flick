# Phase 8: Dashboards And Monitoring

## The Problem

Flick now has many moving parts. When something breaks or slows down, you can't tell which part caused it or when it started. Creators also can't see how many people watch their videos.

## The Fix

- Every service reports numbers about itself, such as "how many requests have I handled?". These are called **metrics**.
- **Prometheus** collects those numbers from every service every 15 seconds and stores them. Collecting them is called **scraping**.
- **Grafana** turns the stored numbers into live charts on a dashboard.
- A separate creator page shows views and watch time for each video.

Both Prometheus and Grafana are already in `docker-compose.yml`. Prometheus is at http://localhost:9090 and Grafana is at http://localhost:3001.

## The Three Kinds Of Metric

| Kind | What it is | Example |
| --- | --- | --- |
| Counter | A number that only goes up | Total requests |
| Gauge | A number that goes up and down | Jobs waiting right now |
| Histogram | Sorts measurements into ranges, so you can work out averages and worst cases | How long requests take |

## Step 1: Add Metrics To Every Service

From each service's folder:

```sh
go get github.com/prometheus/client_golang/prometheus/promhttp
```

Every service should count requests and time them in the same way. So write the code once, in a shared package at `pkg/metrics/`, set up like `pkg/chunk` in Phase 4. It's a **middleware**: code that wraps every request, so you don't have to add it to each endpoint by hand.

```go
var (
    requests = promauto.NewCounterVec(prometheus.CounterOpts{
        Name: "http_requests_total"}, []string{"path", "code"})
    duration = promauto.NewHistogramVec(prometheus.HistogramOpts{
        Name: "http_request_seconds"}, []string{"path"})
)

func Wrap(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        rec := &statusRecorder{ResponseWriter: w, code: 200}
        next.ServeHTTP(rec, r) // run the real endpoint
        path := r.Pattern      // for example "GET /videos/{id}"
        requests.WithLabelValues(path, strconv.Itoa(rec.code)).Inc()
        duration.WithLabelValues(path).Observe(time.Since(start).Seconds())
    })
}
```

- `statusRecorder` is a small type you write. It wraps the `ResponseWriter` and remembers the status code the endpoint sent, such as 200 or 500.
- Use `r.Pattern`, not `r.URL.Path`. The real path holds the video ID. Prometheus would then keep a separate counter for every video ever made, and run out of memory.

In each service, wrap the mux with `metrics.Wrap(mux)` and add the endpoint Prometheus reads from:

```go
mux.Handle("GET /metrics", promhttp.Handler())
```

Then add metrics that only one service has:

| Service | Metric | Kind | Shows |
| --- | --- | --- | --- |
| queue | `queue_waiting_jobs` | Gauge | How many jobs are waiting |
| cache | `cache_hits_total`, `cache_misses_total` | Counter | How well the cache works |
| storage | `raft_is_leader` | Gauge | 1 if this node is the leader, 0 if not |
| storage | `chunks_stored` | Gauge | How many chunks this node holds |
| worker | `jobs_done_total`, `jobs_failed_total` | Counter | Finished and failed conversions |
| api | `chat_connections` | Gauge | Viewers connected to chat |

## Step 2: Point Prometheus At Every Service

Prometheus needs to know where to look. Edit `monitoring/prometheus.yml`:

```yaml
scrape_configs:
  - job_name: flick
    static_configs:
      - targets: [api:8080, queue:8081, cache:8082, ingest:8083,
                  node1:9001, node2:9002, node3:9003]
  - job_name: worker
    dns_sd_configs:
      - names: [worker]
        type: A
        port: 8090
```

Two services need extra work:

- **Ingest** only speaks RTMP on port 1935. Give it a small HTTP server on port 8083 just for `/metrics`.
- **Workers** have no web server at all. Give each one a small HTTP server on port 8090. Because you can run any number of workers, you can't list them by hand. `dns_sd_configs` asks Docker for the address of every `worker` copy and finds them all.

Open http://localhost:9090/targets. Every target should say **UP**.

## Step 3: Build The Grafana Dashboard

Open http://localhost:3001 and log in with `admin` / `admin`. Prometheus is already connected as a data source. Create a dashboard and add one panel for each row below. The queries are written in PromQL, which is Prometheus's query language.

| Panel | Query | What it shows |
| --- | --- | --- |
| Request rate | `sum by (job) (rate(http_requests_total[1m]))` | Requests per second for each service |
| Error rate | `sum(rate(http_requests_total{code=~"5.."}[1m]))` | Server errors per second |
| Slowest requests | `histogram_quantile(0.95, sum by (le) (rate(http_request_seconds_bucket[5m])))` | 95% of requests are faster than this |
| Queue length | `queue_waiting_jobs` | Jobs waiting |
| Cache hit rate | `rate(cache_hits_total[5m]) / (rate(cache_hits_total[5m]) + rate(cache_misses_total[5m]))` | Share of requests served from memory |
| Storage nodes up | `up{instance=~"node.*"}` | 1 if a node is running, 0 if not |
| Raft leader | `raft_is_leader` | Which node is the leader |

`rate(...[1m])` turns a counter that only goes up into "how much per second over the last minute".

**Save the dashboard to the project.** A dashboard made by clicking is lost if Grafana is reset. Export it as JSON into `monitoring/grafana/dashboards/flick.json`. Then add a provisioning file, which is a small settings file Grafana reads when it starts, so the dashboard loads by itself.

## Step 4: Count Views

The player tells the API what's being watched:

- When playback starts, it calls `POST /api/videos/{id}/view`.
- Every 30 seconds while playing, it calls `POST /api/videos/{id}/watch` with `{"seconds": 30}`.

Writing to Postgres on every call would be slow with many viewers. So the API only adds to a counter in Redis, which takes almost no time:

```go
rdb.Incr(ctx, "views:"+id)            // add 1 view
rdb.IncrBy(ctx, "watch:"+id, seconds) // add the seconds watched
```

Once a minute, a loop in the API moves the counts into Postgres. It uses `GETDEL`, which reads a counter and deletes it in one step, so no count is ever added twice.

```sql
ALTER TABLE videos ADD COLUMN IF NOT EXISTS views BIGINT NOT NULL DEFAULT 0;
ALTER TABLE videos ADD COLUMN IF NOT EXISTS watch_seconds BIGINT NOT NULL DEFAULT 0;

-- run for each video that had counts
UPDATE videos SET views = views + $2, watch_seconds = watch_seconds + $3 WHERE id = $1;
```

## Step 5: Build The Creator Dashboard

- Add `GET /api/stats` to the API. It returns every video with its `title`, `views`, `watch_seconds` and `created_at`.
- Add a `/dashboard` page to the website. Show a table with the title, views, and watch time in hours and minutes. Reload the numbers every 30 seconds.

## Done When

- Run `scripts/chaos.sh` from Phase 5 and watch Grafana. You can see each node go down, the leader change, and everything recover.
- The creator dashboard shows numbers that are less than a minute old.
