// Package metrics counts and times the requests each service handles, and
// serves those numbers for Prometheus to collect. Every Flick service uses it,
// so they all report requests the same way.
package metrics

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// These are the numbers every service reports. promauto registers each one
// when the program starts, so Handler serves it without any more setup.
var (
	requests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Requests handled, by route and status code.",
	}, []string{"path", "code"})
	duration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "http_request_seconds",
		Help: "How long requests take, by route.",
	}, []string{"path"})
)

// unmatched is the route recorded for a request that matches no route.
const unmatched = "unmatched"

// Wrap counts and times every request that next handles. It's wrapped around
// a service's whole mux, so no handler needs any code of its own.
func Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rec, r)

		// The mux fills in the route that matched, such as "GET /videos/{id}".
		// The real path holds the video's ID, and a new label for every video
		// would fill Prometheus's memory.
		path := r.Pattern
		if path == "" {
			path = unmatched
		}
		requests.WithLabelValues(path, strconv.Itoa(rec.code)).Inc()
		// A WebSocket stays open for as long as the viewer stays, so its time
		// says nothing about how fast the service is.
		if rec.code != http.StatusSwitchingProtocols {
			duration.WithLabelValues(path).Observe(time.Since(start).Seconds())
		}
	})
}

// statusRecorder remembers the status code a handler sends, which Go doesn't
// report once the handler returns.
type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.code = code
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap gives back the original ResponseWriter. Code that needs more than a
// ResponseWriter, such as turning a request into a WebSocket, uses it to
// reach the real connection.
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// Handler serves every number for Prometheus to collect. Each service serves
// it at GET /metrics.
func Handler() http.Handler {
	return promhttp.Handler()
}

// Serve runs a server on port with only GET /metrics, until ctx is cancelled.
// It's for services with no server of their own: the worker and the ingest
// service.
func Serve(ctx context.Context, port string) error {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", Handler())
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Close the server once ctx is cancelled, which ends ListenAndServe.
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
