# Worker

Claims jobs from the queue, converts each upload to HLS in 1080p, 720p and 360p, saves the HLS files on the storage nodes, and marks the video ready.

## Startup

![How the worker starts](diagrams/startup.svg)

## Job Steps

![How the worker does a job](diagrams/job.svg)

## Files

| File | Path | Description |
| --- | --- | --- |
| Upload | `uploads/<id>.mp4` | Saved by the API. The worker copies it into a temporary folder for FFmpeg, and deletes the folder when the job ends. |
| Quality Playlist | `hls/<id>/720p.m3u8` | Made by FFmpeg, one per quality. |
| Segments | `hls/<id>/720p_000.ts`, `720p_001.ts` and so on | Made by FFmpeg, 4 seconds each. |
| Poster | `hls/<id>/poster.jpg` | Made by FFmpeg from a frame 1 second in. |
| Master Playlist | `hls/<id>/master.m3u8` | Saved last, so if it exists, every other file is saved too. |

## Qualities

| Quality | Height | Bandwidth | Video Bit Rate |
| --- | --- | --- | --- |
| `1080p` | 1080 | 5 Mbps | 4.872 Mbps |
| `720p` | 720 | 2.8 Mbps | 2.672 Mbps |
| `360p` | 360 | 0.8 Mbps | 0.672 Mbps |

## Special Cases

| Case | Description |
| --- | --- |
| No Job Is Waiting | Waits 2 seconds, then asks again. |
| A Step Fails | Leaves the job unfinished, and counts it in `jobs_failed_total`. The queue hands it out again after 5 minutes. |
| The Worker Is Stopped | Stops FFmpeg. `master.m3u8` isn't saved, so the video stays `processing`. The queue hands the job out again after 5 minutes, and the next try saves every file again. |
| The Upload Is Smaller Than a Quality | Skips any quality taller than the upload. A 720p upload gets 720p and 360p. An upload under 360p gets one quality at its own height. |

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `QUEUE_URL` | `http://queue:8081` | Where the queue is. |
| `METRICS_PORT` | `8090` | The port Prometheus reads `/metrics` from. It counts finished jobs as `jobs_done_total` and failed ones as `jobs_failed_total`. |
| `STORAGE_NODES` | `http://node1:9001,http://node2:9002,http://node3:9003` | Where the storage nodes are, separated by commas. |
| `DATABASE_URL` | Required | Where Postgres is, such as `postgres://postgres:flick@db:5432/postgres?sslmode=disable`. |

## Development

| Command | Description |
| --- | --- |
| `go doc -cmd -u -all .` | Shows every function and type with its documentation. |
| `go test ./...` | Runs the tests. They need no Postgres or Docker. `TestConvertToHLS` is skipped if FFmpeg isn't installed. |
