package main

// A small client for the job queue: claiming the next job and marking it done.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"
)

// Job is one job, in the shape the queue sends it.
type Job struct {
	ID      uuid.UUID `json:"id"`
	VideoID uuid.UUID `json:"video_id"`
}

// queueClient talks to the queue over HTTP. url is where it runs, such as
// http://queue:8081.
type queueClient struct {
	url  string
	http *http.Client
}

// Claim asks the queue for the oldest waiting job. ok is false if no job is
// waiting.
func (q *queueClient) Claim(ctx context.Context) (job Job, ok bool, err error) {
	resp, err := q.post(ctx, "/jobs/claim")
	if err != nil {
		return Job{}, false, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
			return Job{}, false, fmt.Errorf("read job: %w", err)
		}
		return job, true, nil
	case http.StatusNoContent: // 204 means nothing is waiting.
		return Job{}, false, nil
	default:
		return Job{}, false, fmt.Errorf("queue replied %s", resp.Status)
	}
}

// Done tells the queue a job is finished, so it's never handed out again.
func (q *queueClient) Done(ctx context.Context, id uuid.UUID) error {
	resp, err := q.post(ctx, "/jobs/"+id.String()+"/done")
	if err != nil {
		return err
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("queue replied %s", resp.Status)
	}
	return nil
}

// post sends an empty POST request to path on the queue. The caller must
// close the reply's body.
func (q *queueClient) post(ctx context.Context, path string) (*http.Response, error) {
	// NewRequestWithContext ties the request to ctx, so it's abandoned if the
	// worker is told to stop.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, q.url+path, nil)
	if err != nil {
		return nil, err
	}
	return q.http.Do(req)
}
