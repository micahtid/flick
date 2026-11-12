package main

// Tests for the work loop in worker.go and the queue client in queue.go. They
// use fakes and a pretend queue, so `go test` needs no FFmpeg, Postgres or
// Docker.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
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

// fakeVideos records which videos were marked ready.
type fakeVideos struct {
	ready []uuid.UUID
}

func (f *fakeVideos) MarkReady(_ context.Context, id uuid.UUID) error {
	f.ready = append(f.ready, id)
	return nil
}

// newTestWorker makes a worker with one waiting job. convertErr is what the
// fake conversion returns.
func newTestWorker(convertErr error) (*worker, *fakeQueue, *fakeVideos, Job) {
	job := Job{ID: uuid.New(), VideoID: uuid.New()}
	queue := &fakeQueue{jobs: []Job{job}}
	videos := &fakeVideos{}
	w := &worker{
		queue:   queue,
		videos:  videos,
		dataDir: "/data",
		convert: func(context.Context, string, string) error { return convertErr },
	}
	return w, queue, videos, job
}

// A job that converts cleanly marks its video ready and the job done.
func TestNextDoesJob(t *testing.T) {
	w, queue, videos, job := newTestWorker(nil)

	// Check the paths the conversion is given, too.
	var gotIn, gotOut string
	w.convert = func(_ context.Context, in, out string) error {
		gotIn, gotOut = in, out
		return nil
	}

	if !w.next(context.Background()) {
		t.Fatal("next = false, want true when a job is waiting")
	}
	if gotIn != w.uploadPath(job.VideoID) || gotOut != w.convertedPath(job.VideoID) {
		t.Errorf("converted %q to %q, want the upload to its 720p copy", gotIn, gotOut)
	}
	if len(videos.ready) != 1 || videos.ready[0] != job.VideoID {
		t.Errorf("ready videos = %v, want [%s]", videos.ready, job.VideoID)
	}
	if len(queue.done) != 1 || queue.done[0] != job.ID {
		t.Errorf("done jobs = %v, want [%s]", queue.done, job.ID)
	}
}

// A failed conversion leaves the video processing and the job unfinished, so
// the queue hands it out again later.
func TestNextLeavesFailedJob(t *testing.T) {
	w, queue, videos, _ := newTestWorker(errors.New("ffmpeg failed"))

	if !w.next(context.Background()) {
		t.Fatal("next = false, want true when a job was claimed")
	}
	if len(videos.ready) != 0 || len(queue.done) != 0 {
		t.Errorf("ready = %v, done = %v, want both empty", videos.ready, queue.done)
	}
}

// With nothing waiting, next reports false so the loop waits before asking
// again.
func TestNextWithNoJob(t *testing.T) {
	w, queue, _, _ := newTestWorker(nil)
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
