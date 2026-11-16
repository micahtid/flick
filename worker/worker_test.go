package main

// This file tests the work loop in worker.go and the queue client in
// queue.go. Fakes and a pretend queue stand in for the real services, so
// `go test` needs no FFmpeg, Postgres or Docker.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// fakeQueue hands out its jobs in order, and records which ones were marked
// done or renewed. mu guards renewed, since keepClaim renews in the
// background.
type fakeQueue struct {
	jobs    []Job
	done    []uuid.UUID
	mu      sync.Mutex
	renewed []uuid.UUID
}

func (f *fakeQueue) Claim(context.Context) (Job, bool, error) {
	if len(f.jobs) == 0 {
		return Job{}, false, nil
	}
	job := f.jobs[0]
	f.jobs = f.jobs[1:]
	return job, true, nil
}

func (f *fakeQueue) Done(_ context.Context, id uuid.UUID) error {
	f.done = append(f.done, id)
	return nil
}

func (f *fakeQueue) Renew(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renewed = append(f.renewed, id)
	return nil
}

// fakeVideos records which videos were marked ready or failed, and the last
// length saved.
type fakeVideos struct {
	ready    []uuid.UUID
	failed   []uuid.UUID
	duration float64
}

func (f *fakeVideos) MarkReady(_ context.Context, id uuid.UUID, duration float64) error {
	f.ready = append(f.ready, id)
	f.duration = duration
	return nil
}

func (f *fakeVideos) MarkFailed(_ context.Context, id uuid.UUID) error {
	f.failed = append(f.failed, id)
	return nil
}

// fakeFiles stands in for the file store. It keeps each file's bytes by
// path, and records the order files were saved in.
type fakeFiles struct {
	data  map[string][]byte
	saved []string
}

func (f *fakeFiles) Save(_ context.Context, path string, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.data[path] = data
	f.saved = append(f.saved, path)
	return nil
}

func (f *fakeFiles) Copy(_ context.Context, w io.Writer, path string) error {
	data, ok := f.data[path]
	if !ok {
		return errors.New("file not found")
	}
	_, err := w.Write(data)
	return err
}

// newTestWorker makes a worker with one waiting job, on its first try, whose
// upload is in the store. convertErr is what the fake conversion returns.
func newTestWorker(convertErr error) (*worker, *fakeQueue, *fakeVideos, *fakeFiles, Job) {
	job := Job{ID: uuid.New(), VideoID: uuid.New(), Attempts: 1}
	queue := &fakeQueue{jobs: []Job{job}}
	videos := &fakeVideos{}
	files := &fakeFiles{data: map[string][]byte{uploadPath(job.VideoID): []byte("upload")}}
	w := &worker{
		queue:   queue,
		videos:  videos,
		files:   files,
		convert: func(context.Context, string, string) (float64, error) { return 0, convertErr },
	}
	return w, queue, videos, files, job
}

// A job that converts cleanly saves its HLS files with the master playlist
// last, marks its video ready with its length, marks the job done, and is
// counted as done.
func TestNextDoesJob(t *testing.T) {
	w, queue, videos, files, job := newTestWorker(nil)

	// The fake conversion checks it was given the upload, and writes two files.
	w.convert = func(_ context.Context, in, dir string) (float64, error) {
		if data, err := os.ReadFile(in); err != nil || string(data) != "upload" {
			t.Errorf("upload on disk = %q, %v, want the stored upload", data, err)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return 0, err
		}
		for _, name := range []string{masterName, "360p_000.ts"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
				return 0, err
			}
		}
		return 12.5, nil
	}
	before := testutil.ToFloat64(jobsDone)

	if !w.next(context.Background()) {
		t.Fatal("next = false, want true when a job is waiting")
	}
	prefix := hlsPath(job.VideoID) + "/"
	if want := []string{prefix + "360p_000.ts", prefix + masterName}; !slices.Equal(files.saved, want) {
		t.Errorf("saved %v, want %v", files.saved, want)
	}
	if videos.duration != 12.5 {
		t.Errorf("duration = %v, want 12.5", videos.duration)
	}
	if len(videos.ready) != 1 || videos.ready[0] != job.VideoID {
		t.Errorf("ready videos = %v, want [%s]", videos.ready, job.VideoID)
	}
	if len(queue.done) != 1 || queue.done[0] != job.ID {
		t.Errorf("done jobs = %v, want [%s]", queue.done, job.ID)
	}
	if got := testutil.ToFloat64(jobsDone) - before; got != 1 {
		t.Errorf("jobs_done_total went up by %v, want 1", got)
	}
}

// A failed conversion leaves the video processing and the job unfinished, so
// the queue hands it out again later. It's counted as failed.
func TestNextLeavesFailedJob(t *testing.T) {
	w, queue, videos, files, _ := newTestWorker(errors.New("ffmpeg failed"))
	before := testutil.ToFloat64(jobsFailed)

	if !w.next(context.Background()) {
		t.Fatal("next = false, want true when a job was claimed")
	}
	if len(videos.ready) != 0 || len(videos.failed) != 0 || len(queue.done) != 0 || len(files.saved) != 0 {
		t.Errorf("ready = %v, failed = %v, done = %v, saved = %v, want all empty", videos.ready, videos.failed, queue.done, files.saved)
	}
	if got := testutil.ToFloat64(jobsFailed) - before; got != 1 {
		t.Errorf("jobs_failed_total went up by %v, want 1", got)
	}
}

// A job is given up on when its last try fails, or when it's claimed more
// than maxAttempts times, such as after its worker kept crashing. Its video
// is marked failed and the job done. A job claimed too often isn't converted
// again.
func TestNextGivesUp(t *testing.T) {
	tests := []struct {
		name      string
		attempts  int
		wantTries int
	}{
		{"last try fails", maxAttempts, 1},
		{"claimed too often", maxAttempts + 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, queue, videos, _, job := newTestWorker(nil)
			queue.jobs[0].Attempts = tt.attempts
			tries := 0
			w.convert = func(context.Context, string, string) (float64, error) {
				tries++
				return 0, errors.New("ffmpeg failed")
			}

			w.next(context.Background())
			if tries != tt.wantTries {
				t.Errorf("converted %d times, want %d", tries, tt.wantTries)
			}
			if !slices.Equal(videos.failed, []uuid.UUID{job.VideoID}) || !slices.Equal(queue.done, []uuid.UUID{job.ID}) {
				t.Errorf("failed = %v, done = %v, want the video failed and the job done", videos.failed, queue.done)
			}
		})
	}
}

// keepClaim renews the job every interval, and stops once it's told to.
func TestKeepClaim(t *testing.T) {
	w, queue, _, _, job := newTestWorker(nil)
	renewals := func() []uuid.UUID {
		queue.mu.Lock()
		defer queue.mu.Unlock()
		return slices.Clone(queue.renewed)
	}

	stop := w.keepClaim(context.Background(), job.ID, 10*time.Millisecond)
	time.Sleep(55 * time.Millisecond)
	stop()
	time.Sleep(20 * time.Millisecond) // Let a renewal already under way finish.

	got := renewals()
	if len(got) < 2 || got[0] != job.ID {
		t.Errorf("renewed %v, want job %s renewed several times", got, job.ID)
	}
	time.Sleep(30 * time.Millisecond)
	if after := renewals(); len(after) != len(got) {
		t.Errorf("renewed %d more times after stop, want 0", len(after)-len(got))
	}
}

// With nothing waiting, next reports false so the loop waits before asking
// again.
func TestNextWithNoJob(t *testing.T) {
	w, queue, _, _, _ := newTestWorker(nil)
	queue.jobs = nil

	if w.next(context.Background()) {
		t.Error("next = true, want false when no job is waiting")
	}
}

// The client reads a claimed job, treats 204 as "nothing waiting", and turns
// any other reply into an error.
func TestQueueClientClaim(t *testing.T) {
	job := Job{ID: uuid.New(), VideoID: uuid.New(), Attempts: 2}
	tests := []struct {
		name    string
		status  int
		body    string
		wantOK  bool
		wantErr bool
	}{
		{"job", http.StatusOK, `{"id": "` + job.ID.String() + `", "video_id": "` + job.VideoID.String() + `", "attempts": 2}`, true, false},
		{"nothing waiting", http.StatusNoContent, "", false, false},
		{"queue error", http.StatusInternalServerError, "internal server error", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// httptest.NewServer runs a real HTTP server on a free local port,
			// standing in for the queue.
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/jobs/claim" {
					t.Errorf("got %s %s, want POST /jobs/claim", r.Method, r.URL.Path)
				}
				w.WriteHeader(tt.status)
				w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			q := &queueClient{url: srv.URL, http: srv.Client()}
			got, ok, err := q.Claim(context.Background())
			if ok != tt.wantOK || (err != nil) != tt.wantErr {
				t.Fatalf("ok = %v, err = %v, want ok = %v and an error: %v", ok, err, tt.wantOK, tt.wantErr)
			}
			if ok && got != job {
				t.Errorf("job = %+v, want %+v", got, job)
			}
		})
	}
}

// Done and Renew post to the job's address and expect 204.
func TestQueueClientChangeJob(t *testing.T) {
	id := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jobs/"+id.String()+"/done" && r.URL.Path != "/jobs/"+id.String()+"/renew" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	q := &queueClient{url: srv.URL, http: srv.Client()}
	for name, call := range map[string]func(context.Context, uuid.UUID) error{"Done": q.Done, "Renew": q.Renew} {
		if err := call(context.Background(), id); err != nil {
			t.Errorf("%s(known job) = %v, want nil", name, err)
		}
		if err := call(context.Background(), uuid.New()); err == nil {
			t.Errorf("%s(unknown job) = nil, want an error", name)
		}
	}
}
