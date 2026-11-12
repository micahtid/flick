package main

// A small client for the job queue, which the API uses to ask for each upload
// to be converted.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
)

// errQueueFull means the queue has too many jobs waiting and turned the new
// one away.
var errQueueFull = errors.New("queue is full")

// queueClient talks to the queue over HTTP. url is where it runs, such as
// http://queue:8081.
type queueClient struct {
	url  string
	http *http.Client
}

// Add asks the queue to convert a video. It returns errQueueFull if the queue
// replies 429 (Too Many Requests).
func (q *queueClient) Add(ctx context.Context, videoID uuid.UUID) error {
	body, err := json.Marshal(map[string]uuid.UUID{"video_id": videoID})
	if err != nil {
		return err
	}
	// NewRequestWithContext ties the request to ctx, so it's abandoned if the
	// uploader hangs up.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, q.url+"/jobs", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := q.http.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusCreated:
		return nil
	case http.StatusTooManyRequests:
		return errQueueFull
	default:
		return fmt.Errorf("queue replied %s", resp.Status)
	}
}
