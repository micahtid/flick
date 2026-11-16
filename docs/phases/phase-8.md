# Phase 8: Dashboards And Monitoring

## The Problem

Flick now has many moving parts. When something breaks or slows down, you can't tell which part caused it or when it started. Creators also can't see how many people watch their videos.

## The Fix

- Every service reports numbers about itself, such as "how many requests have I handled?". These are called **metrics**.
- **Prometheus** collects those numbers from every service every 15 seconds and stores them. Collecting them is called **scraping**.
- **Grafana** turns the stored numbers into live charts on a dashboard.
- A separate creator page shows views and watch time for each video.

Prometheus is at http://localhost:9090 and Grafana is at http://localhost:3001.

## The Three Kinds Of Metric

| Kind | What it is | Example |
| --- | --- | --- |
| Counter | A number that only goes up | Total requests |
| Gauge | A number that goes up and down | Jobs waiting right now |
| Histogram | Sorts measurements into ranges, so you can work out averages and worst cases | How long requests take |

## The Parts In This Phase

| Part | Where | Job |
| --- | --- | --- |
| Metrics | `pkg/metrics/` (new) | Counts and times every request, and serves the numbers at `/metrics`. Every Go service shares it. |
| Prometheus | `monitoring/prometheus.yml` | Reads `/metrics` from every service every 15 seconds. |
| Grafana | `monitoring/grafana/` | Draws the numbers on the Flick dashboard, which it loads by itself. |
| View counts | `api/views.go` (new) | Counts views and watch time in Redis, and moves them into Postgres once a minute. |
| Creator dashboard | `web/src/components/Dashboard.tsx` (new) | Shows each video's views and watch time. |

## Step 1: Add Metrics To Every Service

Every service should count requests and time them in the same way. So write the code once, in a shared package at `pkg/metrics/`, set up like `pkg/chunk` in Phase 4. From that folder:

```sh
go get github.com/prometheus/client_golang/prometheus
```

The package has a **middleware**: code that wraps every request, so you don't have to add it to each endpoint by hand.

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
- Use `r.Pattern`, not `r.URL.Path`. The real path holds the video ID. Prometheus would then keep a separate counter for every video ever made, and run out of memory. A request that matches no route has no pattern, so count it as `unmatched`.
- **Give `statusRecorder` an `Unwrap()` method** that returns the original `ResponseWriter`. Chat turns its request into a WebSocket, which needs the real connection underneath. Without it, chat fails.
- **Don't time WebSockets.** A chat connection replies `101` and stays open for as long as the viewer stays, which says nothing about speed.

Two more functions finish the package. `Handler()` serves every number for Prometheus, and `Serve(ctx, port)` runs a small server with only `GET /metrics`.

In the API, queue, cache and storage nodes, wrap the mux and add the endpoint Prometheus reads from:

```go
mux.Handle("GET /metrics", metrics.Handler())
return metrics.Wrap(mux)
```

The worker and the ingest service have no web server for this. Start one in the background with `metrics.Serve()`: port 8090 for the worker, and 8083 for the ingest service, which otherwise only speaks RTMP.

Each service's `go.mod` needs the package, the same way as `pkg/chunk`:

```sh
go mod edit -require=github.com/micahtid/flick/pkg/metrics@v0.0.0 \
  -replace=github.com/micahtid/flick/pkg/metrics=../pkg/metrics
go mod tidy
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

Some of these numbers already exist somewhere. The queue can count waiting jobs in Postgres, and the cache already counts hits and misses for `/stats`. Rather than keep a second copy, register a function that Prometheus calls each time it reads `/metrics`:

```go
promauto.NewGaugeFunc(prometheus.GaugeOpts{Name: "queue_waiting_jobs"}, s.waitingJobs)
```

## Step 2: Point Prometheus At Every Service

Prometheus needs to know where to look. Give each kind of service its own job in `monitoring/prometheus.yml`, so the dashboard can show each one apart:

```yaml
scrape_configs:
  - job_name: api
    static_configs:
      - targets: ["api:8080"]
  # ...the same for queue, cache and ingest...
  - job_name: storage
    static_configs:
      - targets: ["node1:9001", "node2:9002", "node3:9003"]
  - job_name: worker
    dns_sd_configs:
      - names: ["worker"]
        type: A
        port: 8090
```

Because you can run any number of workers, you can't list them by hand. `dns_sd_configs` asks Docker for the address of every `worker` copy and finds them all.

Open http://localhost:9090/targets. Every target should say **UP**.

## Step 3: Build The Grafana Dashboard

Open http://localhost:3001 and log in with `admin` / `admin`. Prometheus is already connected as a data source. Create a dashboard and add one panel for each row below. The queries are written in PromQL, which is Prometheus's query language.

| Panel | Query | What it shows |
| --- | --- | --- |
| Request rate | `sum by (job) (rate(http_requests_total[1m]))` | Requests per second for each service |
| Error rate | `sum by (job) (rate(http_requests_total{code=~"5.."}[1m]))` | Server errors per second |
| Slowest requests | `histogram_quantile(0.95, sum by (job, le) (rate(http_request_seconds_bucket[5m])))` | 95% of requests are faster than this |
| Queue length | `queue_waiting_jobs` | Jobs waiting |
| Cache hit rate | `rate(cache_hits_total[5m]) / (rate(cache_hits_total[5m]) + rate(cache_misses_total[5m]))` | Share of requests served from memory |
| Storage nodes up | `up{job="storage"}` | 1 if a node is running, 0 if not |
| Raft leader | `raft_is_leader` | Which node is the leader |

`rate(...[1m])` turns a counter that only goes up into "how much per second over the last minute".

Notes:

- **Error rate shows "No data" until the first error.** Add `or sum by (job) (rate(http_requests_total[1m])) * 0` to the end, which draws a line at 0 for each service instead.
- **Use a state timeline for the two node panels.** It draws one row per node, colored by its value. Add value mappings so 1 reads "Up" in green and 0 reads "Down" in red, or "Leader" and "Follower". Leave out the threshold colors, or they hide the mapping colors.

**Save the dashboard to the project.** A dashboard made by clicking is lost if Grafana is reset. Export it as JSON into `monitoring/grafana/dashboards/flick.json`. Then add a provisioning file, which is a small settings file Grafana reads when it starts, so the dashboard loads by itself:

```yaml
# monitoring/grafana/dashboards.yml
apiVersion: 1
providers:
  - name: Flick
    type: file
    options:
      path: /var/lib/grafana/dashboards
```

Give the data source in `datasource.yml` a fixed `uid: prometheus`, so the saved dashboard can find it on any machine.

## Step 4: Count Views

The player tells the API what's being watched:

- When playback starts, it calls `POST /api/videos/{id}/view`.
- Every 30 seconds while playing, it calls `POST /api/videos/{id}/watch` with `{"seconds": 30}`. When the viewer leaves the page or closes the tab, it sends whatever is left. Send these with `keepalive: true`, so they finish even as the page closes.

Writing to Postgres on every call would be slow with many viewers. So the API only adds to a counter in Redis, which takes almost no time. It also adds the video to a set, so the flush knows which videos have counts:

```go
rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
    p.IncrBy(ctx, "views:"+id, views)   // add the views
    p.IncrBy(ctx, "watch:"+id, seconds) // add the seconds watched
    p.SAdd(ctx, "counted", id)          // remember the video has counts
    return nil
})
```

`TxPipelined` sends the commands together, and Redis runs them as one.

Once a minute, a loop in the API moves the counts into Postgres. For each video in the set, it removes the video from the set and reads its counters with `GETDEL`, which reads a counter and deletes it in one step, so no count is ever added twice. Then it adds every video's counts in one transaction:

```sql
ALTER TABLE videos ADD COLUMN IF NOT EXISTS views BIGINT NOT NULL DEFAULT 0;
ALTER TABLE videos ADD COLUMN IF NOT EXISTS watch_seconds BIGINT NOT NULL DEFAULT 0;

-- run for each video that had counts
UPDATE videos SET views = views + $2, watch_seconds = watch_seconds + $3 WHERE id = $1;
```

If Postgres fails, put the counts back into Redis, so the next flush tries again. Hide Redis behind an interface, as with chat, so the tests can count in memory.

## Step 5: Build The Creator Dashboard

- Add `GET /api/stats` to the API. It returns every video with its `title`, `views`, `watch_seconds` and `created_at`.
- Add a `/dashboard` page to the website, with a link to it on the home page. Show a table with the title, views, and watch time in hours and minutes. Reload the numbers every 30 seconds.

## Step 6: Update Compose

- Remove `profiles: [full]` from `prometheus` and `grafana`.
- Build `queue`, `cache` and the storage nodes from the project folder, like the API, so they can use `pkg/metrics`. Every Go Dockerfile now copies in `pkg/metrics` as well as `pkg/chunk`.
- Mount `monitoring/grafana/dashboards.yml` at `/etc/grafana/provisioning/dashboards/dashboards.yml`, and `monitoring/grafana/dashboards` at `/var/lib/grafana/dashboards`.

## Done When

- Run `scripts/chaos.sh` from Phase 5 and watch Grafana. You can see each node go down, the leader change, and everything recover.
- The creator dashboard shows numbers that are less than a minute old.
