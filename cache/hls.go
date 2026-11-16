package main

// This file handles the cache's requests. Nginx forwards /hls/... here. HLS
// splits a video into short segments, listed by playlists. A segment is sent
// from the cache when it's there. When it isn't, it's fetched from the API and
// kept. Every other HLS file, such as a playlist, passes straight through to
// the API.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strconv"
	"sync/atomic"

	"github.com/micahtid/flick/pkg/metrics"
	"golang.org/x/sync/singleflight"
)

// segmentType is the Content-Type of a .ts segment, the only kind of file the
// cache keeps.
const segmentType = "video/mp2t"

// errNotFound means the API has no such file.
var errNotFound = errors.New("not found")

// server holds what every handler needs: the cache, and a way to reach the
// API.
type server struct {
	files string       // This is where the API sends files, such as http://api:8080/files.
	cache *lru         // This holds the segments.
	http  *http.Client // This fetches segments from the API.
	// proxy passes every other HLS file to the API without keeping it.
	proxy *httputil.ReverseProxy
	// fetches lets viewers who miss the same segment at once share one fetch.
	fetches singleflight.Group
	// These count segment requests. Many requests update them at once, so
	// they're atomic integers, which stay correct when changed from many places.
	hits, misses atomic.Int64
}

// newServer makes a server that fetches files from the API at apiURL, such as
// http://api:8080.
func newServer(apiURL string, cache *lru, client *http.Client) (*server, error) {
	files, err := url.Parse(apiURL + "/files")
	if err != nil {
		return nil, err
	}
	s := &server{files: files.String(), cache: cache, http: client}
	// SetURL adds the request's path to the API's, so /hls/<id>/master.m3u8
	// goes to the API as /files/hls/<id>/master.m3u8.
	s.proxy = &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) { r.SetURL(files) }}
	return s, nil
}

// routes connects each request type and path to its handler. {path...}
// matches the rest of the path, slashes included. metrics.Wrap counts and
// times every request, and Prometheus collects the metrics from /metrics.
func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /hls/{path...}", s.getHLSFile)
	mux.HandleFunc("GET /stats", s.stats)
	mux.Handle("GET /metrics", metrics.Handler())
	return metrics.Wrap(mux)
}

// health replies "ok". It's a quick way to check the cache is running.
func (s *server) health(w http.ResponseWriter, _ *http.Request) {
	io.WriteString(w, "ok")
}

// getHLSFile sends one HLS file, such as <id>/720p_003.ts. Only segments are
// kept. A finished segment never changes, so a kept copy is always correct.
// Playlists are tiny, and a live playlist changes every 2 seconds, so they
// always come from the API.
func (s *server) getHLSFile(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("path")
	if path.Ext(key) != ".ts" {
		s.proxy.ServeHTTP(w, r)
		return
	}

	data, err := s.segment(r.Context(), key)
	if errors.Is(err, errNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		// The API failed, so reply 502 Bad Gateway.
		log.Printf("fetch %s: %v", key, err)
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", segmentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Write(data)
}

// segment returns a segment from the cache. On a miss, it fetches the segment
// from the API and keeps it for next time.
func (s *server) segment(ctx context.Context, key string) ([]byte, error) {
	if data, ok := s.cache.Get(key); ok {
		s.hits.Add(1)
		return data, nil
	}
	s.misses.Add(1)

	// If many viewers miss the same segment at once, only the first fetches
	// it. The rest wait and share its result. WithoutCancel keeps the fetch
	// going even if the first viewer hangs up, since the others still need it.
	v, err, _ := s.fetches.Do(key, func() (any, error) {
		data, err := s.fetch(context.WithoutCancel(ctx), key)
		if err != nil {
			return nil, err
		}
		s.cache.Put(key, data)
		return data, nil
	})
	if err != nil {
		return nil, err
	}
	return v.([]byte), nil
}

// fetch reads one HLS file from the API. It returns errNotFound if the API
// replies 404.
func (s *server) fetch(ctx context.Context, key string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.files+"/hls/"+key, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return io.ReadAll(resp.Body)
	case http.StatusNotFound:
		return nil, errNotFound
	default:
		return nil, fmt.Errorf("api replied %s", resp.Status)
	}
}

// stats sends the hits, the misses, and the share of segment requests that
// were hits, such as {"hit_rate":0.95,"hits":95,"misses":5}.
func (s *server) stats(w http.ResponseWriter, _ *http.Request) {
	hits, misses := s.hits.Load(), s.misses.Load()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"hits":   hits,
		"misses": misses,
		// max stops a divide by zero before the first request.
		"hit_rate": float64(hits) / float64(max(hits+misses, 1)),
	})
}
