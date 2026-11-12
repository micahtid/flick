package main

// The work itself: claim a job, convert the video with FFmpeg, mark the video
// ready, and mark the job done. Then do it all again.

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

// idleDelay is how long to wait before asking again when there's no job, or
// when the queue can't be reached.
const idleDelay = 2 * time.Second

// jobQueue lists the queue actions the worker needs. queueClient in queue.go
// is the real one, and the tests use a fake.
type jobQueue interface {
	Claim(ctx context.Context) (job Job, ok bool, err error)
	Done(ctx context.Context, id uuid.UUID) error
}

// videoRepo lists the database actions the worker needs. videoStore in db.go
// is the real one, and the tests use a fake.
type videoRepo interface {
	MarkReady(ctx context.Context, id uuid.UUID) error
}

// worker holds what the work loop needs. convert turns the file at in into a
// new file at out. It's convertTo720p in real use, and something quicker in
// the tests.
type worker struct {
	queue   jobQueue
	videos  videoRepo
	dataDir string
	convert func(ctx context.Context, in, out string) error
}

// run does one job after another until ctx is cancelled.
func (w *worker) run(ctx context.Context) {
	for ctx.Err() == nil {
		if !w.next(ctx) {
			sleep(ctx, idleDelay)
		}
	}
}

// next claims one job and does it. It returns false if there was no job to
// claim, so run knows to wait before asking again.
func (w *worker) next(ctx context.Context) bool {
	job, ok, err := w.queue.Claim(ctx)
	if err != nil {
		log.Printf("claim job: %v", err)
		return false
	}
	if !ok {
		return false
	}

	if err := w.process(ctx, job); err != nil {
		// Leave the job unfinished. The queue hands it out again after a few
		// minutes, which is how failed jobs get retried.
		log.Printf("job %s failed: %v", job.ID, err)
	}
	return true
}

// process converts a job's video, marks the video ready, and marks the job
// done, in that order. If the last step fails, the job is simply done again
// later, which does no harm.
func (w *worker) process(ctx context.Context, job Job) error {
	log.Printf("job %s: converting video %s", job.ID, job.VideoID)
	start := time.Now()

	if err := w.convert(ctx, w.uploadPath(job.VideoID), w.convertedPath(job.VideoID)); err != nil {
		return err
	}
	if err := w.videos.MarkReady(ctx, job.VideoID); err != nil {
		return fmt.Errorf("mark video ready: %w", err)
	}
	if err := w.queue.Done(ctx, job.ID); err != nil {
		return fmt.Errorf("finish job: %w", err)
	}

	log.Printf("job %s: done in %s", job.ID, time.Since(start).Round(time.Second))
	return nil
}

// uploadPath is where the API saved the uploaded file, such as
// /data/<id>.mp4.
func (w *worker) uploadPath(id uuid.UUID) string {
	return filepath.Join(w.dataDir, id.String()+".mp4")
}

// convertedPath is where the 720p copy goes, such as /data/<id>_720p.mp4.
func (w *worker) convertedPath(id uuid.UUID) string {
	return filepath.Join(w.dataDir, id.String()+"_720p.mp4")
}

// convertTo720p runs FFmpeg to make a 720p copy of in at out. FFmpeg writes to
// a temporary file first, which is renamed to out once it's complete. A rename
// happens all at once, so out is never half written, even if the worker is
// stopped partway through.
func convertTo720p(ctx context.Context, in, out string) error {
	tmp := out + ".part"
	// CommandContext stops FFmpeg if ctx is cancelled, such as when the worker
	// is told to stop.
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y",                 // Overwrite the output file if it already exists.
		"-loglevel", "error", // Only print errors, so a failure's output is short.
		"-i", in,
		"-vf", "scale=-2:720", // 720 pixels tall. -2 picks an even width that keeps the shape.
		"-c:v", "libx264", // H.264, the most common video format.
		"-preset", "veryfast", // Convert quickly, for a slightly bigger file.
		"-c:a", "aac", // AAC, the most common audio format.
		"-movflags", "+faststart", // Put the index at the start, so playback can begin before the whole file loads.
		"-f", "mp4", // Name the format, since the .part ending doesn't say it.
		tmp,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("ffmpeg: %w: %s", err, bytes.TrimSpace(output))
	}
	return os.Rename(tmp, out)
}

// sleep waits for d, but returns early if ctx is cancelled.
func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
