# Phase 2: Background Conversion

## The Problem

Videos come in many formats and sizes. To play smoothly everywhere, each one must be converted into a standard format. That work is called transcoding, and it can take minutes.

If the API does it during the upload, the user stares at a spinner the whole time. If many people upload at once, the API gets overloaded.

## The Fix

Hand the slow work to someone else.

- The API saves the file, writes down "this video needs converting", and replies at once.
- That note is called a **job**. Jobs wait in a **queue**, which is a waiting line.
- **Workers** are separate programs. Each one takes a job from the queue, does the conversion, and marks the job done.

If you need more speed, start more workers.

## The Parts In This Phase

| Part | Folder | Job |
| --- | --- | --- |
| Queue | `queue/` (port 8081) | Holds jobs and hands them out |
| Worker | `worker/` | Runs FFmpeg to convert videos |
| FFmpeg | Inside the worker image | The standard tool for converting video |

## Step 1: Build The Queue

A job looks like this:

```go
type Job struct {
    ID        string    `json:"id"`
    VideoID   string    `json:"video_id"`
    State     string    `json:"state"`      // waiting, running or done
    ClaimedAt time.Time `json:"claimed_at"` // when a worker took it
}
```

The queue has three endpoints:

| Method | Path | What it does |
| --- | --- | --- |
| `POST` | `/jobs` | Adds a job. Replies `429` if too many jobs are already waiting |
| `POST` | `/jobs/claim` | Gives the oldest waiting job to the worker that asked. Replies `204` (empty) if there's none |
| `POST` | `/jobs/{id}/done` | Marks a job done |

**Start simple.** Keep the jobs in a Go slice, and protect it with a `sync.Mutex` so two requests don't change it at the same time. Get it working this way first.

**Then make it survive restarts.** A slice is lost when the program stops. Move the jobs into a Postgres table instead:

```sql
CREATE TABLE IF NOT EXISTS jobs (
  id         UUID PRIMARY KEY,
  video_id   UUID NOT NULL,
  state      TEXT NOT NULL DEFAULT 'waiting',
  claimed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

**One job, one worker.** If two workers ask for a job at the same moment, they must not get the same one. This query handles that. `FOR UPDATE` locks the row it picks. `SKIP LOCKED` tells the other worker to skip that row and take the next one.

```sql
UPDATE jobs SET state = 'running', claimed_at = now()
WHERE id = (
  SELECT id FROM jobs WHERE state = 'waiting'
  ORDER BY created_at LIMIT 1
  FOR UPDATE SKIP LOCKED
)
RETURNING id, video_id;
```

**Retries.** A worker might crash halfway through a job. Then the job stays `running` forever and the video is never finished. To fix this, run a loop inside the queue every 30 seconds. It puts any job that has been running for more than 5 minutes back in line:

```sql
UPDATE jobs SET state = 'waiting'
WHERE state = 'running' AND claimed_at < now() - interval '5 minutes';
```

**Backpressure.** This means "push back when you're too busy". Before adding a job, count the waiting jobs. If there are 100 or more, reply `429 Too Many Requests`. This stops the queue from growing forever.

In Compose, give the queue `DATABASE_URL` so it can reach Postgres. Remove `profiles: [full]` from it so it starts by default.

## Step 2: Build The Worker

The worker's Dockerfile already installs FFmpeg. In Compose, give the worker the same `uploads` volume at `/data` so it can see the uploaded files.

The worker runs a loop that never ends:

```go
for {
    job, ok := claim() // POST to queue:8081/jobs/claim
    if !ok { time.Sleep(2 * time.Second); continue } // nothing to do, wait

    in  := "/data/" + job.VideoID + ".mp4"
    out := "/data/" + job.VideoID + "_720p.mp4"
    cmd := exec.Command("ffmpeg", "-y", "-i", in,
        "-vf", "scale=-2:720", "-c:v", "libx264", "-preset", "veryfast",
        "-c:a", "aac", out)
    if err := cmd.Run(); err != nil {
        log.Printf("job %s failed: %v", job.ID, err)
        continue // don't mark done; the retry loop will hand it out again
    }

    db.Exec(ctx, `UPDATE videos SET status = 'ready' WHERE id = $1`, job.VideoID)
    markDone(job.ID) // POST to queue:8081/jobs/{id}/done
}
```

What the FFmpeg options mean:

- `-y`: overwrite the output file if it already exists.
- `scale=-2:720`: make the video 720 pixels tall. The `-2` picks a width that keeps the same shape and is an even number, which the video format needs.
- `libx264`: the most common video format (H.264).
- `veryfast`: convert quickly. The file is a little bigger as a result.
- `aac`: the most common audio format.

## Step 3: Change The API

The upload endpoint `POST /videos` now does four things:

1. Saves the file, as before.
2. Adds the row with `status = 'processing'`.
3. Adds a job by calling `POST queue:8081/jobs`. If the queue replies `429`, delete the file and the row, and reply `503` with the message "busy, try later".
4. Replies straight away. It doesn't wait for the conversion.

`GET /videos/{id}/file` now sends the converted file, `{id}_720p.mp4`.

## Step 4: Update The Website

- Show each video's status next to its title.
- Only show the player when the status is `ready`.
- While any video is still `processing`, load the list again every 3 seconds. That way the status changes on screen without a refresh.

## Step 5: Update Compose

`docker-compose.yml` has two workers, `worker1` and `worker2`. Replace them with one service called `worker`. With one service, Compose can start as many copies as you ask for.

```yaml
worker:
  build: ./worker
  environment:
    QUEUE_URL: http://queue:8081
    DATABASE_URL: postgres://postgres:flick@db:5432/postgres?sslmode=disable
  volumes: [uploads:/data]
  depends_on: [queue, db]
```

## Done When

- Uploads finish at once, and the status changes from `processing` to `ready` by itself.
- `docker compose up --scale worker=2` starts two workers, and they convert two videos at the same time.
- Stop a worker halfway through a job with `docker stop`. About 5 minutes later, the other worker finishes that job.
