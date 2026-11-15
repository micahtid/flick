package main

// The work itself: claim a job, convert the video to HLS (see hls.go), save
// the HLS files, mark the video ready, and mark the job done. Then do it all
// again.

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
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

// fileStore lists the file actions the worker needs. chunk.Store in pkg/chunk
// is the real one, which keeps files on the storage nodes. The tests use a
// fake.
type fileStore interface {
	Save(ctx context.Context, path string, r io.Reader) error
	Copy(ctx context.Context, w io.Writer, path string) error
}

// worker holds what the work loop needs. convert turns the upload at in into
// HLS files inside dir, and returns the video's length in seconds. It's
// convertToHLS in real use, and something quicker in the tests.
type worker struct {
	queue   jobQueue
	videos  videoRepo
	files   fileStore
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

// process converts a job's video, saves the HLS files, marks the video ready
// with its length, and marks the job done, in that order. If a later step
// fails, the job is simply done again later, which does no harm.
//
// FFmpeg needs real files on disk, so the work happens in a temporary folder:
// the upload is rebuilt there, converted, and the results saved to the
// storage nodes. The folder is deleted at the end, even if a step fails.
func (w *worker) process(ctx context.Context, job Job) error {
	log.Printf("job %s: converting video %s", job.ID, job.VideoID)
	start := time.Now()

	tmp, err := os.MkdirTemp("", "flick-job-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	in := filepath.Join(tmp, "upload.mp4")
	if err := w.download(ctx, uploadPath(job.VideoID), in); err != nil {
		return fmt.Errorf("fetch upload: %w", err)
	}
	out := filepath.Join(tmp, "hls")
	duration, err := w.convert(ctx, in, out)
	if err != nil {
		return err
	}
	if err := w.uploadDir(ctx, out, hlsPath(job.VideoID)); err != nil {
		return fmt.Errorf("save HLS files: %w", err)
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

// download rebuilds the stored file at path into a new file on disk at dst.
func (w *worker) download(ctx context.Context, path, dst string) error {
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	if err := w.files.Copy(ctx, f, path); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// uploadDir saves every file in dir to the store under prefix, such as
// hls/<id>/720p_000.ts. The master playlist goes last, so once it's saved,
// every file it points to is too.
func (w *worker) uploadDir(ctx context.Context, dir, prefix string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if e.Name() != masterName {
			names = append(names, e.Name())
		}
	}
	names = append(names, masterName)

	for _, name := range names {
		if err := w.uploadFile(ctx, filepath.Join(dir, name), prefix+"/"+name); err != nil {
			return fmt.Errorf("save %s: %w", name, err)
		}
	}
	return nil
}

// uploadFile saves the file on disk at src to the store at path.
func (w *worker) uploadFile(ctx context.Context, src, path string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	return w.files.Save(ctx, path, f)
}

// uploadPath names an upload in the file store, such as uploads/<id>.mp4. It
// must match the name the API saves it under.
func uploadPath(id uuid.UUID) string {
	return "uploads/" + id.String() + ".mp4"
}

// hlsPath is where a video's HLS files go in the file store, such as
// hls/<id>. The cache fetches /hls/<id>/... from the API, which reads them
// from there.
func hlsPath(id uuid.UUID) string {
	return "hls/" + id.String()
}

// sleep waits for d, but returns early if ctx is cancelled.
func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
