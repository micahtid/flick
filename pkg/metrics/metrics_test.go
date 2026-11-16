package metrics

// Tests for Wrap and Handler in metrics.go. They send requests straight to
// the handlers, so `go test` needs no Prometheus or Docker.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// newTestMux makes a wrapped mux with one route that replies 204, and one
// that replies 500.
func newTestMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /videos/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /broken", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "broken", http.StatusInternalServerError)
	})
	return Wrap(mux)
}

// count returns how many requests have been counted for path and code.
func count(path, code string) float64 {
	return testutil.ToFloat64(requests.WithLabelValues(path, code))
}

// Each request is counted under its route and status code. Two videos share
// one route, and a path that matches no route is counted as unmatched.
func TestWrap(t *testing.T) {
	h := newTestMux()
	before := map[[2]string]float64{}
	cases := [][2]string{
		{"GET /videos/{id}", "204"},
		{"GET /broken", "500"},
		{unmatched, "404"},
	}
	for _, c := range cases {
		before[c] = count(c[0], c[1])
	}

	for _, path := range []string{"/videos/a", "/videos/b", "/broken", "/missing"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}

	want := map[[2]string]float64{cases[0]: 2, cases[1]: 1, cases[2]: 1}
	for c, n := range want {
		if got := count(c[0], c[1]) - before[c]; got != n {
			t.Errorf("%s %s counted %v times, want %v", c[0], c[1], got, n)
		}
	}
}

// Unwrap gives back the original ResponseWriter, so a WebSocket can still
// take over the connection.
func TestUnwrap(t *testing.T) {
	w := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: w}
	if rec.Unwrap() != w {
		t.Error("Unwrap didn't return the original ResponseWriter")
	}
}

// Handler serves the counts in Prometheus's text format.
func TestHandler(t *testing.T) {
	newTestMux().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/broken", nil))

	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if body := rec.Body.String(); !strings.Contains(body, `http_requests_total{code="500",path="GET /broken"}`) {
		t.Errorf("metrics don't list the request:\n%s", body)
	}
}
