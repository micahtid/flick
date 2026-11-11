# Phases

Build Flick one phase at a time, in order. Each phase ends with something that works.

| # | Phase | New idea |
| --- | --- | --- |
| 1 | [Plain Video Site](phase-1.md) | API, database, Docker Compose |
| 2 | [Background Conversion](phase-2.md) | Job queue, workers, retries |
| 3 | [Adaptive Streaming](phase-3.md) | HLS |
| 4 | [Your Own Storage](phase-4.md) | Chunks, checksums, copies |
| 5 | [Consensus With Raft](phase-5.md) | Leader election, log replication |
| 6 | [Edge Cache](phase-6.md) | LRU cache |
| 7 | [Live Streaming And Chat](phase-7.md) | RTMP, WebSockets, pub/sub |
| 8 | [Dashboards And Monitoring](phase-8.md) | Metrics, Grafana |
| 9 | [Real Machines](phase-9.md) | Real networks |

Rules:

- Pass every check under **Done When** before moving on.
- Commit each time something new works.
- Keep a notes file of what broke and how you fixed it.
