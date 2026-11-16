package main

// This file counts views and watch time. The player reports each view, and
// every 30 seconds of watching. Writing to Postgres on every report would be
// slow with many viewers, so each report only adds to a counter in Redis.
// Once a minute, flushLoop moves the counts into Postgres.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// These set how often counts move to Postgres, and how much watch time one
// report can add. The player reports every 30 seconds, so 60 leaves room.
const (
	flushEvery      = time.Minute
	maxWatchSeconds = 60
)

// countedKey names the Redis set of videos whose counts are waiting to move
// to Postgres.
const countedKey = "counted"

// viewCounter lists the counting actions the handlers need. redisCounter is
// the real one, and the tests use a fake that counts in memory.
type viewCounter interface {
	// Add adds views and seconds of watch time to a video's counts.
	Add(ctx context.Context, id uuid.UUID, views, seconds int64) error
	// Take removes every count so far and returns them. After an error, it
	// still returns the counts it took, so they aren't lost.
	Take(ctx context.Context) ([]viewCount, error)
}

// viewCount is the views and watch time a video gained since the last flush.
type viewCount struct {
	ID      uuid.UUID
	Views   int64
	Seconds int64
}

// addView counts one view. The player reports it when playback starts.
func (s *server) addView(w http.ResponseWriter, r *http.Request) {
	if id, ok := pathID(w, r); ok {
		s.count(w, r, id, 1, 0)
	}
}

// addWatch adds watch time from a JSON body such as {"seconds": 30}. The
// player reports it every 30 seconds while the video plays.
func (s *server) addWatch(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Seconds int64 `json:"seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Seconds < 1 || body.Seconds > maxWatchSeconds {
		http.Error(w, fmt.Sprintf("expected a JSON body with seconds from 1 to %d", maxWatchSeconds), http.StatusBadRequest)
		return
	}
	s.count(w, r, id, 0, body.Seconds)
}

// count adds to a video's counts in Redis, and replies 204.
func (s *server) count(w http.ResponseWriter, r *http.Request, id uuid.UUID, views, seconds int64) {
	if err := s.views.Add(r.Context(), id, views, seconds); err != nil {
		internalError(w, "count view", err)
		return
	}
	w.WriteHeader(http.StatusNoContent) // 204 means counted, with nothing to send back.
}

// getStats sends every video's views and watch time, newest first, for the
// creator dashboard.
func (s *server) getStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.videos.Stats(r.Context())
	if err != nil {
		internalError(w, "list stats", err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// flushLoop moves the counts into Postgres every flushEvery, until ctx is
// cancelled.
func (s *server) flushLoop(ctx context.Context) {
	ticker := time.NewTicker(flushEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		s.flush(ctx)
	}
}

// flush moves every count so far from Redis into Postgres. If Postgres
// fails, the counts go back into Redis for the next try, so none are lost.
func (s *server) flush(ctx context.Context) {
	counts, err := s.views.Take(ctx)
	if err != nil {
		log.Printf("take view counts: %v", err)
	}
	if len(counts) == 0 {
		return
	}
	if err := s.videos.AddCounts(ctx, counts); err != nil {
		log.Printf("save view counts: %v", err)
		for _, c := range counts {
			if err := s.views.Add(ctx, c.ID, c.Views, c.Seconds); err != nil {
				log.Printf("put back counts for %s: %v", c.ID, err)
			}
		}
	}
}

// redisCounter keeps each video's counts in Redis, as views:<id> and
// watch:<id>, and lists the videos with counts in the countedKey set.
type redisCounter struct {
	rdb *redis.Client
}

// Add adds to a video's counters, and to the set. TxPipelined sends the
// commands together, and Redis runs them as one, so Take never sees half.
func (c *redisCounter) Add(ctx context.Context, id uuid.UUID, views, seconds int64) error {
	key := id.String()
	_, err := c.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.IncrBy(ctx, "views:"+key, views)
		p.IncrBy(ctx, "watch:"+key, seconds)
		p.SAdd(ctx, countedKey, key)
		return nil
	})
	return err
}

// Take reads and deletes each video's counters with GETDEL, which does both
// in one step, so no count is ever added twice. Each video's commands run as
// one, so a view counted at the same moment is either taken now or left for
// next time.
func (c *redisCounter) Take(ctx context.Context) ([]viewCount, error) {
	keys, err := c.rdb.SMembers(ctx, countedKey).Result()
	if err != nil {
		return nil, err
	}

	var counts []viewCount
	for _, key := range keys {
		id, err := uuid.Parse(key)
		if err != nil {
			continue // Add only adds IDs, so this never happens.
		}
		var views, watch *redis.StringCmd
		_, err = c.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
			p.SRem(ctx, countedKey, key)
			views = p.GetDel(ctx, "views:"+key)
			watch = p.GetDel(ctx, "watch:"+key)
			return nil
		})
		// redis.Nil means a counter was missing, such as when another copy of
		// the API took it first. It reads as 0 below.
		if err != nil && !errors.Is(err, redis.Nil) {
			return counts, err
		}
		v, _ := views.Int64()
		w, _ := watch.Int64()
		if v > 0 || w > 0 {
			counts = append(counts, viewCount{ID: id, Views: v, Seconds: w})
		}
	}
	return counts, nil
}
