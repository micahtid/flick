package main

// This file tests the queue client in queue.go. A pretend queue stands in for
// the real one, so `go test` needs no Docker.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// Add sends the video ID, and turns each queue reply into the right error:
// none for 201, errQueueFull for 429, and a plain error for anything else.
func TestQueueClientAdd(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		wantErr func(error) bool
	}{
		{"added", http.StatusCreated, func(err error) bool { return err == nil }},
		{"full", http.StatusTooManyRequests, func(err error) bool { return errors.Is(err, errQueueFull) }},
		{"broken", http.StatusInternalServerError, func(err error) bool { return err != nil && !errors.Is(err, errQueueFull) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			videoID := uuid.New()
			// httptest.NewServer runs a real HTTP server on a free local port,
			// standing in for the queue.
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					VideoID uuid.UUID `json:"video_id"`
				}
				json.NewDecoder(r.Body).Decode(&body)
				if r.Method != http.MethodPost || r.URL.Path != "/jobs" || body.VideoID != videoID {
					t.Errorf("got %s %s for %s, want POST /jobs for %s", r.Method, r.URL.Path, body.VideoID, videoID)
				}
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()

			q := &queueClient{url: srv.URL, http: srv.Client()}
			if err := q.Add(context.Background(), videoID); !tt.wantErr(err) {
				t.Errorf("Add = %v, not the error expected", err)
			}
		})
	}
}
