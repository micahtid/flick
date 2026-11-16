# Worker

Claims jobs from the queue and converts each upload to HLS in up to 3 qualities. It saves the HLS files on the storage nodes, then marks the video ready.

## Startup

![How the worker starts](diagrams/startup.svg)

## Job Steps

![How the worker does a job](diagrams/job.svg)

## Files

| File | Path | Description |
| --- | --- | --- |
| Upload | `uploads/<id>.mp4` | Saved by the API. The worker copies it into a temporary folder for FFmpeg and deletes the folder when the job ends. |
| Quality Playlist | `hls/<id>/720p.m3u8` | Made by FFmpeg, one per quality. |
| Segments | `hls/<id>/720p_000.ts`, `720p_001.ts` and so on | Made by FFmpeg, 4 seconds each. |
| Poster | `hls/<id>/poster.jpg` | Made by FFmpeg from a frame 1 second in, or halfway through a video shorter than 2 seconds. |
| Master Playlist | `hls/<id>/master.m3u8` | Saved last, so if it exists, every other file is saved too. |

## Qualities

| Quality | Height | Bandwidth | Video Bitrate |
| --- | --- | --- | --- |
| `1080p` | 1080 | 5 Mbps | 4.872 Mbps |
| `720p` | 720 | 2.8 Mbps | 2.672 Mbps |
| `360p` | 360 | 0.8 Mbps | 0.672 Mbps |

## Edge Cases

| Case | Description |
| --- | --- |
| No Job Is Waiting | Waits 2 seconds, then asks again. |
| A Job Takes a Long Time | Renews the job with the queue every minute, so no other worker gets it. |
| A Step Fails | Leaves the job unfinished and counts it in `jobs_failed_total`. The queue reassigns it after 5 minutes. |
| A Job Fails 3 Times | Gives up. It marks the video `failed` so the website stops waiting for it, then marks the job done. If workers keep crashing instead, the worker gives up when the job is claimed a 4th time. |
| The Worker Is Stopped | Stops FFmpeg, so the video stays `processing`. The queue reassigns the job after 5 minutes. It isn't counted as a failure. |
| The Upload Is Smaller than a Quality | Skips any quality taller than the upload. A 720p upload gets 720p and 360p. An upload under 360p gets one quality at its own height. |

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `QUEUE_URL` | `http://queue:8081` | The queue's URL. |
| `METRICS_PORT` | `8090` | The port that serves `/metrics` to Prometheus. It counts finished jobs as `jobs_done_total` and failed attempts as `jobs_failed_total`. |
| `STORAGE_NODES` | `http://node1:9001,http://node2:9002,http://node3:9003` | The storage nodes' URLs, separated by commas. |
| `DATABASE_URL` | Required | The Postgres connection URL, such as `postgres://postgres:flick@db:5432/postgres?sslmode=disable`. |

## Development

| Command | Description |
| --- | --- |
| `go doc -cmd -u -all .` | Shows every function and type with its documentation. |
| `go test ./...` | Runs the tests. They need no Postgres or Docker. `TestConvertToHLS` is skipped if FFmpeg isn't installed. |
