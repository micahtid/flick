package main

// Tests for the handlers in jobs.go. They use a fake in-memory database, so
// `go test` needs no Postgres or Docker. The SQL that stops two workers
// getting the same job is checked by running the real stack instead.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeRepo stands in for the database. Jobs are kept oldest first, the same
// order the real queue hands them out in. Set err to make every call fail.
type fakeRepo struct {
	jobs []Job
	err  error
}

func (f *fakeRepo) Add(_ context.Context, videoID uuid.UUID) (Job, error) {
	if f.err != nil {
		return Job{}, f.err
	}
	j := Job{ID: uuid.New(), VideoID: videoID, State: "waiting"}
	f.jobs = append(f.jobs, j)
	return j, nil
}

func (f *fakeRepo) Waiting(context.Context) (int, error) {
	n := 0
	for _, j := range f.jobs {
		if j.State == "waiting" {
			n++
		}
	}
	return n, f.err
}

func (f *fakeRepo) Claim(context.Context) (Job, bool, error) {
	if f.err != nil {
		return Job{}, false, f.err
	}
	for i := range f.jobs {
		if f.jobs[i].State == "waiting" {
			now := time.Now()
			f.jobs[i].State, f.jobs[i].ClaimedAt = "running", &now
			return f.jobs[i], true, nil
		}
	}
	return Job{}, false, nil
}

func (f *fakeRepo) Done(_ context.Context, id uuid.UUID) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	for i := range f.jobs {
		if f.jobs[i].ID == id {
			f.jobs[i].State = "done"
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeRepo) Requeue(context.Context, time.Duration) (int64, error) {
	return 0, f.err
}

// newTestServer makes a server with an empty fake database.
func newTestServer() (*fakeRepo, http.Handler) {
	repo := &fakeRepo{}
	return repo, newServer(repo).routes()
}

// post sends a POST request straight to the handler, with no real network.
// httptest.NewRecorder captures the reply so the test can check it.
func post(h http.Handler, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// addBody is the JSON the API sends to add a job for videoID.
func addBody(videoID uuid.UUID) string {
	return `{"video_id": "` + videoID.String() + `"}`
}

// A good request replies 201 with a waiting job for that video.
func TestAddJob(t *testing.T) {
	repo, h := newTestServer()
	videoID := uuid.New()

	rec := post(h, "/jobs", addBody(videoID))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body)
	}

	var got Job
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.VideoID != videoID || got.State != "waiting" {
		t.Errorf("got %+v, want a waiting job for %s", got, videoID)
	}
	if len(repo.jobs) != 1 {
		t.Errorf("repo has %d jobs, want 1", len(repo.jobs))
	}
}

// Each bad body replies 400 and adds no job. This is a "table test": one list
// of cases, each run as its own subtest by t.Run.
func TestAddJobRejectsBadInput(t *testing.T) {
	tests := []struct{ name, body string }{
		{"not JSON", "video"},
		{"missing video_id", "{}"},
		{"bad video_id", `{"video_id": "not-a-uuid"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, h := newTestServer()
			if rec := post(h, "/jobs", tt.body); rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
			if len(repo.jobs) != 0 {
				t.Errorf("repo has %d jobs, want 0", len(repo.jobs))
			}
		})
	}
}

// Once maxWaiting jobs are waiting, a new job replies 429 and isn't added.
func TestAddJobWhenFull(t *testing.T) {
	repo, h := newTestServer()
	for range maxWaiting {
		repo.Add(context.Background(), uuid.New())
	}

	if rec := post(h, "/jobs", addBody(uuid.New())); rec.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", rec.Code)
	}
	if len(repo.jobs) != maxWaiting {
		t.Errorf("repo has %d jobs, want %d", len(repo.jobs), maxWaiting)
	}
}

// Claiming hands out jobs oldest first, then replies 204 once none are left.
func TestClaimJob(t *testing.T) {
	repo, h := newTestServer()
	first, _ := repo.Add(context.Background(), uuid.New())
	second, _ := repo.Add(context.Background(), uuid.New())

	for _, want := range []Job{first, second} {
		rec := post(h, "/jobs/claim", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var got Job
		if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got.ID != want.ID || got.State != "running" || got.ClaimedAt == nil {
			t.Errorf("got %+v, want job %s running with a claim time", got, want.ID)
		}
	}

	if rec := post(h, "/jobs/claim", ""); rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204 when the queue is empty", rec.Code)
	}
}

// Finishing a job marks it done, and replies 400 for a bad ID and 404 for an
// unknown one.
func TestFinishJob(t *testing.T) {
	repo, h := newTestServer()
	job, _ := repo.Add(context.Background(), uuid.New())

	if rec := post(h, "/jobs/"+job.ID.String()+"/done", ""); rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	if repo.jobs[0].State != "done" {
		t.Errorf("state = %q, want done", repo.jobs[0].State)
	}

	for path, want := range map[string]int{
		"/jobs/not-a-uuid/done":               http.StatusBadRequest,
		"/jobs/" + uuid.NewString() + "/done": http.StatusNotFound,
	} {
		if rec := post(h, path, ""); rec.Code != want {
			t.Errorf("POST %s: status = %d, want %d", path, rec.Code, want)
		}
	}
}

// If the database fails, every endpoint replies a plain 500.
func TestDatabaseErrors(t *testing.T) {
	repo, h := newTestServer()
	repo.err = errors.New("db down")

	for _, path := range []string{"/jobs", "/jobs/claim", "/jobs/" + uuid.NewString() + "/done"} {
		if rec := post(h, path, addBody(uuid.New())); rec.Code != http.StatusInternalServerError {
			t.Errorf("POST %s: status = %d, want 500", path, rec.Code)
		}
	}
}
