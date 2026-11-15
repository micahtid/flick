package main

// The work itself: wait for a stream, save its files as FFmpeg makes them
// (see hls.go), and turn it into a normal video once it ends. Then wait for
// the next stream.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// syncInterval is how often to look for new segments.
const syncInterval = time.Second

// retryDelay is how long to wait before starting FFmpeg again after it fails.
const retryDelay = 2 * time.Second

// streamTitle is the title every stream gets.
const streamTitle = "Live Stream"

// videoRepo lists the database actions the ingest service needs. videoStore
// in db.go is the real one, and the tests use a fake.
type videoRepo interface {
	CreateLive(ctx context.Context, id uuid.UUID, title string) error
	MarkReady(ctx context.Context, id uuid.UUID, duration float64) error
}

// fileStore lists the file actions the ingest service needs. chunk.Store in
// pkg/chunk is the real one, which keeps files on the storage nodes. The
// tests use a fake.
type fileStore interface {
	Save(ctx context.Context, path string, r io.Reader) error
}

// ingester holds what the stream loop needs. record runs FFmpeg until a
// stream ends, and poster saves one frame of a segment as a JPEG. They're
// record and makePoster in real use, and something quicker in the tests.
type ingester struct {
	videos videoRepo
	files  fileStore
	record func(ctx context.Context, dir string) error
	poster func(ctx context.Context, seg, out string) error
}

// stream is one live stream. dir is the temporary folder FFmpeg writes into,
// segments lists every segment saved so far, in order, and live is true once
// the video has been added.
type stream struct {
	id       uuid.UUID
	dir      string
	segments []segment
	live     bool
}

// path names one of the stream's files in the file store, such as
// hls/<id>/seg_00042.ts. The cache fetches /hls/<id>/... from the API, which
// reads them from there.
func (s *stream) path(name string) string {
	return "hls/" + s.id.String() + "/" + name
}

// run takes one stream after another until ctx is cancelled.
func (in *ingester) run(ctx context.Context) {
	for ctx.Err() == nil {
		if err := in.take(ctx); err != nil && ctx.Err() == nil {
			log.Printf("stream failed: %v", err)
			sleep(ctx, retryDelay)
		}
	}
}

// take waits for OBS to connect, saves the stream's files every second while
// it's live, and finishes it once it ends.
//
// FFmpeg needs real files on disk, so it writes into a temporary folder, and
// each file is saved to the storage nodes from there. The folder is deleted
// at the end, even if a step fails.
func (in *ingester) take(ctx context.Context) error {
	dir, err := os.MkdirTemp("", "flick-live-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	s := &stream{id: uuid.New(), dir: dir}

	// Run FFmpeg in the background (a goroutine), so this function can save
	// files while it runs. When the stream ends, FFmpeg's error arrives on
	// done.
	done := make(chan error, 1)
	go func() { done <- in.record(ctx, dir) }()

	ticker := time.NewTicker(syncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			// A failed save is tried again on the next tick, so it's only
			// logged.
			if err := in.sync(ctx, s); err != nil {
				log.Printf("stream %s: %v", s.id, err)
			}
		case err := <-done:
			// WithoutCancel lets the stream finish even when the service is
			// told to stop, which also stops FFmpeg.
			return in.finish(context.WithoutCancel(ctx), s, err)
		}
	}
}

// sync saves the segments FFmpeg has finished since the last sync, then the
// live playlist. The segments go first, so the playlist never lists one that
// isn't saved yet. On the stream's first sync, it also adds the video, so it
// shows up on the website.
func (in *ingester) sync(ctx context.Context, s *stream) error {
	data, err := os.ReadFile(filepath.Join(s.dir, recordName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil // OBS hasn't connected yet.
	}
	if err != nil {
		return err
	}

	// FFmpeg's playlist lists every segment so far, so any past the ones
	// already saved are new.
	segs := parsePlaylist(data)
	if len(segs) <= len(s.segments) {
		return nil
	}
	for _, seg := range segs[len(s.segments):] {
		if err := in.saveFile(ctx, s, seg.name); err != nil {
			return err
		}
		s.segments = append(s.segments, seg)
	}

	if !s.live {
		if err := in.start(ctx, s); err != nil {
			return err
		}
	}
	return in.save(ctx, s, liveName, livePlaylist(s.segments))
}

// start saves a poster from the stream's first segment, then adds the video
// as "live". The poster goes first, so the website's thumbnail works as soon
// as the video appears.
func (in *ingester) start(ctx context.Context, s *stream) error {
	if err := in.poster(ctx, filepath.Join(s.dir, s.segments[0].name), filepath.Join(s.dir, posterName)); err != nil {
		return fmt.Errorf("make poster: %w", err)
	}
	if err := in.saveFile(ctx, s, posterName); err != nil {
		return err
	}
	if err := in.videos.CreateLive(ctx, s.id, streamTitle); err != nil {
		return fmt.Errorf("add video: %w", err)
	}
	s.live = true
	log.Printf("stream %s: live", s.id)
	return nil
}

// finish saves the segments FFmpeg made since the last sync, then the
// finished playlist, and marks the video ready with its length. If no stream
// came in, there's nothing to finish, and FFmpeg's error is returned instead.
func (in *ingester) finish(ctx context.Context, s *stream, recordErr error) error {
	if err := in.sync(ctx, s); err != nil {
		return err
	}
	if !s.live {
		return recordErr
	}
	// FFmpeg failing partway through still leaves a stream worth keeping.
	if recordErr != nil {
		log.Printf("stream %s: recording stopped early: %v", s.id, recordErr)
	}

	if err := in.save(ctx, s, masterName, finalPlaylist(s.segments)); err != nil {
		return err
	}
	duration := totalDuration(s.segments)
	if err := in.videos.MarkReady(ctx, s.id, duration); err != nil {
		return fmt.Errorf("mark video ready: %w", err)
	}

	log.Printf("stream %s: ended after %s", s.id, time.Duration(duration*float64(time.Second)).Round(time.Second))
	return nil
}

// saveFile saves a file from the stream's folder to the store.
func (in *ingester) saveFile(ctx context.Context, s *stream, name string) error {
	f, err := os.Open(filepath.Join(s.dir, name))
	if err != nil {
		return err
	}
	defer f.Close()
	if err := in.files.Save(ctx, s.path(name), f); err != nil {
		return fmt.Errorf("save %s: %w", name, err)
	}
	return nil
}

// save saves a playlist made in memory to the store.
func (in *ingester) save(ctx context.Context, s *stream, name, text string) error {
	if err := in.files.Save(ctx, s.path(name), strings.NewReader(text)); err != nil {
		return fmt.Errorf("save %s: %w", name, err)
	}
	return nil
}

// sleep waits for d, but returns early if ctx is cancelled.
func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
