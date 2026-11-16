package main

// Tests for the work loop in worker.go and the queue client in queue.go. They
// use fakes and a pretend queue, so `go test` needs no FFmpeg, Postgres or
// Docker.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// fakeQueue hands out its jobs in order, and records which ones were marked
// done.
type fakeQueue struct {
	jobs []Job
	done []uuid.UUID
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

// fakeVideos records which videos were marked ready, and the last length
// saved.
type fakeVideos struct {
	ready    []uuid.UUID
	duration float64
}

func (f *fakeVideos) MarkReady(_ context.Context, id uuid.UUID, duration float64) error {
	f.ready = append(f.ready, id)
	f.duration = duration
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

// newTestWorker makes a worker with one waiting job, whose upload is in the
// store. convertErr is what the fake conversion returns.
func newTestWorker(convertErr error) (*worker, *fakeQueue, *fakeVideos, *fakeFiles, Job) {
	job := Job{ID: uuid.New(), VideoID: uuid.New()}
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
	if len(videos.ready) != 0 || len(queue.done) != 0 || len(files.saved) != 0 {
		t.Errorf("ready = %v, done = %v, saved = %v, want all empty", videos.ready, queue.done, files.saved)
	}
	if got := testutil.ToFloat64(jobsFailed) - before; got != 1 {
		t.Errorf("jobs_failed_total went up by %v, want 1", got)
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
	job := Job{ID: uuid.New(), VideoID: uuid.New()}
	tests := []struct {
		name    string
		status  int
		body    string
		wantOK  bool
		wantErr bool
	}{
		{"job", http.StatusOK, `{"id": "` + job.ID.String() + `", "video_id": "` + job.VideoID.String() + `"}`, true, false},
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

// Done posts to the job's address and expects 204.
func TestQueueClientDone(t *testing.T) {
	id := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jobs/"+id.String()+"/done" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	q := &queueClient{url: srv.URL, http: srv.Client()}
	if err := q.Done(context.Background(), id); err != nil {
		t.Errorf("Done(known job) = %v, want nil", err)
	}
	if err := q.Done(context.Background(), uuid.New()); err == nil {
		t.Error("Done(unknown job) = nil, want an error")
	}
}
