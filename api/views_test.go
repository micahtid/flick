package main

// This file tests the view counting in views.go. A fake counter stands in for
// Redis, so `go test` needs no Redis or Docker.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// fakeCounter stands in for Redis and keeps each video's counts in a map.
type fakeCounter map[uuid.UUID]viewCount

func newFakeCounter() fakeCounter {
	return fakeCounter{}
}

func (f fakeCounter) Add(_ context.Context, id uuid.UUID, views, seconds int64) error {
	c := f[id]
	f[id] = viewCount{ID: id, Views: c.Views + views, Seconds: c.Seconds + seconds}
	return nil
}

func (f fakeCounter) Take(context.Context) ([]viewCount, error) {
	var counts []viewCount
	for id, c := range f {
		counts = append(counts, c)
		delete(f, id)
	}
	return counts, nil
}

// newCountingServer makes a server with a fake database and counter, and
// returns them so the test can check them.
func newCountingServer() (*server, *fakeRepo, fakeCounter) {
	repo := &fakeRepo{}
	views := newFakeCounter()
	return newServer(repo, &fakeQueue{}, fakeFiles{}, newFakeHub(), views), repo, views
}

// post sends a POST request with body straight to the handler.
func post(h http.Handler, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	return rec
}

// A view adds 1 view, and watch time adds its seconds. Both reply 204.
func TestCountViews(t *testing.T) {
	s, _, views := newCountingServer()
	h := s.routes()
	id := uuid.New()
	base := "/videos/" + id.String()

	for _, rec := range []*httptest.ResponseRecorder{
		post(h, base+"/view", ""),
		post(h, base+"/view", ""),
		post(h, base+"/watch", `{"seconds":30}`),
	} {
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, body = %q", rec.Code, rec.Body)
		}
	}
	if got := views[id]; got.Views != 2 || got.Seconds != 30 {
		t.Errorf("counts = %+v, want 2 views and 30 seconds", got)
	}
}

// A bad ID or bad watch time replies 400 and counts nothing.
func TestCountViewsRejectsBadInput(t *testing.T) {
	id := uuid.NewString()
	tests := []struct{ name, path, body string }{
		{"bad view ID", "/videos/abc/view", ""},
		{"bad watch ID", "/videos/abc/watch", `{"seconds":30}`},
		{"not JSON", "/videos/" + id + "/watch", "30"},
		{"no seconds", "/videos/" + id + "/watch", `{}`},
		{"too many seconds", "/videos/" + id + "/watch", `{"seconds":61}`},
		{"negative seconds", "/videos/" + id + "/watch", `{"seconds":-5}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _, views := newCountingServer()
			if rec := post(s.routes(), tt.path, tt.body); rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
			if len(views) != 0 {
				t.Errorf("counted %v, want nothing", views)
			}
		})
	}
}

// flush moves the counts into the database and clears them.
func TestFlush(t *testing.T) {
	s, repo, views := newCountingServer()
	id := uuid.New()
	views.Add(context.Background(), id, 3, 90)

	s.flush(context.Background())
	if len(repo.counts) != 1 || repo.counts[0] != (viewCount{ID: id, Views: 3, Seconds: 90}) {
		t.Errorf("saved %+v, want 3 views and 90 seconds", repo.counts)
	}
	if len(views) != 0 {
		t.Errorf("counter still has %v, want nothing", views)
	}
}

// When the API stops, flushLoop moves the counts one last time.
func TestFlushLoopFlushesOnStop(t *testing.T) {
	s, repo, views := newCountingServer()
	views.Add(context.Background(), uuid.New(), 1, 30)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.flushLoop(ctx)
	if len(repo.counts) != 1 || len(views) != 0 {
		t.Errorf("saved %+v, counter has %v, want the counts saved", repo.counts, views)
	}
}

// If the database fails, the counts go back into the counter.
func TestFlushPutsCountsBack(t *testing.T) {
	s, repo, views := newCountingServer()
	repo.countErr = errors.New("db down")
	id := uuid.New()
	views.Add(context.Background(), id, 3, 90)

	s.flush(context.Background())
	if got := views[id]; got.Views != 3 || got.Seconds != 90 {
		t.Errorf("counter has %+v, want 3 views and 90 seconds back", got)
	}
}

// The stats list every video.
func TestGetStats(t *testing.T) {
	s, repo, _ := newCountingServer()
	repo.Create(context.Background(), uuid.New(), "first")

	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stats", nil))

	var got []VideoStats
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || len(got) != 1 || got[0].Title != "first" {
		t.Errorf("got %d %+v, want the one video", rec.Code, got)
	}
}
