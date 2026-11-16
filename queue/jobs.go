package main

// This file holds the queue's routes, a handler for each endpoint, helpers
// for sending replies and the loop that puts stuck jobs back to waiting.

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/micahtid/flick/pkg/metrics"
)

// These limit how long the line can get and how long a job can be held.
const (
	// maxWaiting is how many jobs may wait at once. Past this, new jobs are
	// turned away so the line can't grow forever.
	maxWaiting = 100
	// claimTimeout is how long a worker may hold a job without renewing it.
	// After that, the job is reassigned in case the worker crashed.
	claimTimeout = 5 * time.Minute
	// requeueEvery is how often the queue looks for jobs held too long.
	requeueEvery = 30 * time.Second
)

// jobRepo lists the database actions the handlers need. jobStore in db.go is
// the real one, and the tests use a fake that keeps jobs in memory.
type jobRepo interface {
	Add(ctx context.Context, videoID uuid.UUID) (Job, error)
	Waiting(ctx context.Context) (int, error)
	Claim(ctx context.Context) (job Job, ok bool, err error)
	Done(ctx context.Context, id uuid.UUID) (ok bool, err error)
	Renew(ctx context.Context, id uuid.UUID) (ok bool, err error)
	Requeue(ctx context.Context, timeout time.Duration) (int64, error)
}

// server holds what every handler needs: the database.
type server struct {
	jobs jobRepo
}

func newServer(jobs jobRepo) *server {
	return &server{jobs: jobs}
}

// routes connects each request type and path to its handler. {id} matches any
// value in that spot of the path. metrics.Wrap counts and times every
// request, and Prometheus collects the metrics from /metrics.
func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("POST /jobs", s.addJob)
	mux.HandleFunc("POST /jobs/claim", s.claimJob)
	mux.HandleFunc("POST /jobs/{id}/done", s.finishJob)
	mux.HandleFunc("POST /jobs/{id}/renew", s.renewJob)
	mux.Handle("GET /metrics", metrics.Handler())
	return metrics.Wrap(mux)
}

// health replies "ok". It's a quick way to check the queue is running.
func (s *server) health(w http.ResponseWriter, _ *http.Request) {
	io.WriteString(w, "ok")
}

// addJob adds a job for the video named in the JSON body, such as
// {"video_id": "3f2a9c1e-..."}. It replies 429 if too many jobs are already
// waiting.
func (s *server) addJob(w http.ResponseWriter, r *http.Request) {
	var body struct {
		VideoID uuid.UUID `json:"video_id"`
	}
	// A missing or badly written video_id fails here or is left as the zero
	// UUID, so both count as bad input.
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.VideoID == uuid.Nil {
		http.Error(w, "expected a JSON body with a video_id", http.StatusBadRequest)
		return
	}

	// Turn the job away if the line is full. Two requests at the same moment
	// can both pass this check, so the line can go slightly over the limit.
	// That's fine, since the goal is only to stop it growing forever.
	waiting, err := s.jobs.Waiting(r.Context())
	if err != nil {
		internalError(w, "count waiting jobs", err)
		return
	}
	if waiting >= maxWaiting {
		http.Error(w, "too many jobs waiting", http.StatusTooManyRequests)
		return
	}

	job, err := s.jobs.Add(r.Context(), body.VideoID)
	if err != nil {
		internalError(w, "add job", err)
		return
	}
	writeJSON(w, http.StatusCreated, job) // 201 means the job was created.
}

// claimJob gives the oldest waiting job to the worker that asked. It replies
// 204 (No Content) with an empty body if nothing is waiting.
func (s *server) claimJob(w http.ResponseWriter, r *http.Request) {
	job, ok, err := s.jobs.Claim(r.Context())
	if err != nil {
		internalError(w, "claim job", err)
		return
	}
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// finishJob marks a job done, so it's never reassigned. It replies 404
// if no job has that ID.
func (s *server) finishJob(w http.ResponseWriter, r *http.Request) {
	s.changeJob(w, r, "finish job", s.jobs.Done)
}

// renewJob tells the queue that a worker is still busy with a job, so it
// isn't reassigned. It replies 404 if no running job has that ID.
func (s *server) renewJob(w http.ResponseWriter, r *http.Request) {
	s.changeJob(w, r, "renew job", s.jobs.Renew)
}

// changeJob runs change on the job named in the path and replies 204. It
// replies 404 if change found no such job. action names the step in the log
// if it fails.
func (s *server) changeJob(w http.ResponseWriter, r *http.Request, action string, change func(context.Context, uuid.UUID) (bool, error)) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid job id", http.StatusBadRequest)
		return
	}
	ok, err := change(r.Context(), id)
	if err != nil {
		internalError(w, action, err)
		return
	}
	if !ok {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent) // 204 means changed, with nothing to send back.
}

// waitingJobs counts the jobs waiting for a worker. Prometheus reads it as
// queue_waiting_jobs each time it collects the metrics. It reports 0 if
// Postgres can't be reached.
func (s *server) waitingJobs() float64 {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	n, err := s.jobs.Waiting(ctx)
	if err != nil {
		log.Printf("count waiting jobs: %v", err)
		return 0
	}
	return float64(n)
}

// requeueLoop puts jobs held for too long back to waiting, every requeueEvery,
// until ctx is cancelled. This is how a job survives its worker crashing.
func (s *server) requeueLoop(ctx context.Context) {
	ticker := time.NewTicker(requeueEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		n, err := s.jobs.Requeue(ctx, claimTimeout)
		if err != nil {
			log.Printf("requeue jobs: %v", err)
			continue
		}
		if n > 0 {
			log.Printf("put %d stuck job(s) back in line", n)
		}
	}
}

// internalError logs the real error and sends the client a plain 500, so
// internal details don't leak out.
func internalError(w http.ResponseWriter, action string, err error) {
	log.Printf("%s: %v", action, err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

// writeJSON sends v as JSON with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	// Headers must be set before WriteHeader, which sends them.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}
