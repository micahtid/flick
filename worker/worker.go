package main

// The work itself: claim a job, convert the video to HLS (see hls.go), mark
// the video ready, and mark the job done. Then do it all again.

import (
	"context"
	"fmt"
	"log"
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
	MarkReady(ctx context.Context, id uuid.UUID, duration float64) error
}

// worker holds what the work loop needs. convert turns the upload at in into
// HLS files inside dir, and returns the video's length in seconds. It's
// convertToHLS in real use, and something quicker in the tests.
type worker struct {
	queue   jobQueue
	videos  videoRepo
	dataDir string
	convert func(ctx context.Context, in, dir string) (duration float64, err error)
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

// process converts a job's video, marks the video ready with its length, and
// marks the job done, in that order. If the last step fails, the job is simply done again
// later, which does no harm.
func (w *worker) process(ctx context.Context, job Job) error {
	log.Printf("job %s: converting video %s", job.ID, job.VideoID)
	start := time.Now()

	duration, err := w.convert(ctx, w.uploadPath(job.VideoID), w.hlsDir(job.VideoID))
	if err != nil {
		return err
	}
	if err := w.videos.MarkReady(ctx, job.VideoID, duration); err != nil {
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

// hlsDir is the folder for a video's HLS files, such as /data/hls/<id>. Nginx
// serves it to the browser as /hls/<id>/.
func (w *worker) hlsDir(id uuid.UUID) string {
	return filepath.Join(w.dataDir, "hls", id.String())
}

// sleep waits for d, but returns early if ctx is cancelled.
func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
