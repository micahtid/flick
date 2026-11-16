package main

// This file tests the handlers in hls.go. The tests use a fake API, run by
// httptest on a local port, so `go test` needs no Docker.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// fakeAPI stands in for the API. It sends files from a map, by path, and
// counts the requests for each path. Call setFail to make every request
// reply 500.
type fakeAPI struct {
	mu    sync.Mutex
	files map[string]string
	asked map[string]int
	fail  bool
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked[r.URL.Path]++
	if f.fail {
		http.Error(w, "failed", http.StatusInternalServerError)
		return
	}
	data, ok := f.files[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Write([]byte(data))
}

// count returns how many times the API was asked for path.
func (f *fakeAPI) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.asked[path]
}

// setFail turns failing on or off.
func (f *fakeAPI) setFail(fail bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = fail
}

// newTestServer makes a cache in front of a fake API that holds one segment
// and one playlist.
func newTestServer(t *testing.T) (*fakeAPI, http.Handler) {
	api := &fakeAPI{
		files: map[string]string{
			"/files/hls/v1/720p_000.ts": "segment",
			"/files/hls/v1/720p.m3u8":   "playlist",
		},
		asked: map[string]int{},
	}
	ts := httptest.NewServer(api)
	t.Cleanup(ts.Close)

	s, err := newServer(ts.URL, newLRU(1<<20), ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	return api, s.routes()
}

// get sends a GET request straight to the handler, with no real network.
// httptest.NewRecorder captures the reply so the test can check it.
func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// readStats asks for /stats and decodes the reply.
func readStats(t *testing.T, h http.Handler) (hits, misses int64, rate float64) {
	var got struct {
		Hits    int64   `json:"hits"`
		Misses  int64   `json:"misses"`
		HitRate float64 `json:"hit_rate"`
	}
	if err := json.NewDecoder(get(h, "/stats").Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	return got.Hits, got.Misses, got.HitRate
}

// A segment is fetched from the API once. The second request is a hit, sent
// from the cache.
func TestSegmentIsCached(t *testing.T) {
	api, h := newTestServer(t)

	for range 2 {
		rec := get(h, "/hls/v1/720p_000.ts")
		if rec.Code != http.StatusOK || rec.Body.String() != "segment" {
			t.Fatalf("status = %d, body = %q", rec.Code, rec.Body)
		}
		if ct := rec.Header().Get("Content-Type"); ct != segmentType {
			t.Errorf("Content-Type = %q, want %q", ct, segmentType)
		}
	}

	if n := api.count("/files/hls/v1/720p_000.ts"); n != 1 {
		t.Errorf("API asked %d times, want 1", n)
	}
	if hits, misses, rate := readStats(t, h); hits != 1 || misses != 1 || rate != 0.5 {
		t.Errorf("stats = %d hits, %d misses, %v rate, want 1, 1 and 0.5", hits, misses, rate)
	}
}

// A playlist passes straight through with the API's Content-Type, and is
// never kept.
func TestPlaylistPassesThrough(t *testing.T) {
	api, h := newTestServer(t)

	for range 2 {
		rec := get(h, "/hls/v1/720p.m3u8")
		if rec.Code != http.StatusOK || rec.Body.String() != "playlist" {
			t.Fatalf("status = %d, body = %q", rec.Code, rec.Body)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/vnd.apple.mpegurl" {
			t.Errorf("Content-Type = %q, want the API's", ct)
		}
	}

	if n := api.count("/files/hls/v1/720p.m3u8"); n != 2 {
		t.Errorf("API asked %d times, want 2", n)
	}
	if hits, misses, _ := readStats(t, h); hits != 0 || misses != 0 {
		t.Errorf("stats = %d hits, %d misses, want 0 and 0, since playlists aren't counted", hits, misses)
	}
}

// A missing segment replies 404 and isn't kept, so it's fetched again once
// the video is ready.
func TestMissingSegment(t *testing.T) {
	api, h := newTestServer(t)

	for range 2 {
		if rec := get(h, "/hls/v1/720p_999.ts"); rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rec.Code)
		}
	}
	if n := api.count("/files/hls/v1/720p_999.ts"); n != 2 {
		t.Errorf("API asked %d times, want 2", n)
	}
}

// If the API fails, the cache replies 502 and keeps nothing.
func TestAPIFails(t *testing.T) {
	api, h := newTestServer(t)
	api.setFail(true)

	if rec := get(h, "/hls/v1/720p_000.ts"); rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rec.Code)
	}

	api.setFail(false)
	if rec := get(h, "/hls/v1/720p_000.ts"); rec.Code != http.StatusOK {
		t.Errorf("status = %d after the API recovered, want 200", rec.Code)
	}
}
