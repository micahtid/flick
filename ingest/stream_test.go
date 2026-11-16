package main

// This file tests the stream loop in stream.go. Fakes stand in for FFmpeg,
// Postgres and the storage nodes, so `go test` needs none of them.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// fakeVideos stands in for Postgres. It keeps each video's status and length
// by ID. Set createErr to make CreateLive fail.
type fakeVideos struct {
	status    map[uuid.UUID]string
	duration  map[uuid.UUID]float64
	createErr error
}

func newFakeVideos() *fakeVideos {
	return &fakeVideos{status: map[uuid.UUID]string{}, duration: map[uuid.UUID]float64{}}
}

func (f *fakeVideos) CreateLive(_ context.Context, id uuid.UUID, _ string) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.status[id] = "live"
	return nil
}

func (f *fakeVideos) MarkReady(_ context.Context, id uuid.UUID, duration float64) error {
	f.status[id] = "ready"
	f.duration[id] = duration
	return nil
}

// fakeFiles stands in for the file store. It keeps each file's bytes by
// path and records the order files were saved in.
type fakeFiles struct {
	data  map[string][]byte
	saved []string
}

func (f *fakeFiles) Save(_ context.Context, path string, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.data[path] = data
	f.saved = append(f.saved, path)
	return nil
}

// newTestIngester makes an ingester whose fake poster writes a small file in
// place of a JPEG. Each test sets its own record.
func newTestIngester() (*ingester, *fakeVideos, *fakeFiles) {
	videos := newFakeVideos()
	files := &fakeFiles{data: map[string][]byte{}}
	in := &ingester{
		videos: videos,
		files:  files,
		poster: func(_ context.Context, _, out string) error { return os.WriteFile(out, []byte("poster"), 0o644) },
	}
	return in, videos, files
}

// writeSegments fakes what FFmpeg leaves in dir after n segments of 2
// seconds each: the segments, and a playlist that lists them all.
func writeSegments(t *testing.T, dir string, n int) {
	t.Helper()
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	for i := range n {
		name := fmt.Sprintf("seg_%05d.ts", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "#EXTINF:2.000000,\n%s\n", name)
	}
	if err := os.WriteFile(filepath.Join(dir, recordName), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Each sync saves only the new segments, then the live playlist. The first
// one also saves the poster and adds the video as live.
func TestSync(t *testing.T) {
	in, videos, files := newTestIngester()
	s := &stream{id: uuid.New(), dir: t.TempDir()}
	ctx := context.Background()

	// Before OBS connects, there's nothing to save.
	if err := in.sync(ctx, s); err != nil || len(files.saved) != 0 {
		t.Fatalf("sync before the stream: err = %v, saved = %v, want nothing", err, files.saved)
	}

	writeSegments(t, s.dir, 1)
	if err := in.sync(ctx, s); err != nil {
		t.Fatal(err)
	}
	want := []string{s.path("seg_00000.ts"), s.path(posterName), s.path(liveName)}
	if !slices.Equal(files.saved, want) {
		t.Errorf("saved %v, want %v", files.saved, want)
	}
	if videos.status[s.id] != "live" {
		t.Errorf("status = %q, want live", videos.status[s.id])
	}

	files.saved = nil
	writeSegments(t, s.dir, 3)
	if err := in.sync(ctx, s); err != nil {
		t.Fatal(err)
	}
	want = []string{s.path("seg_00001.ts"), s.path("seg_00002.ts"), s.path(liveName)}
	if !slices.Equal(files.saved, want) {
		t.Errorf("saved %v, want %v", files.saved, want)
	}
}

// If adding the video fails, the next sync tries again, even when no new
// segment has come in. A sync with nothing left to do saves nothing.
func TestSyncRetriesFailedStart(t *testing.T) {
	in, videos, files := newTestIngester()
	s := &stream{id: uuid.New(), dir: t.TempDir()}
	ctx := context.Background()
	writeSegments(t, s.dir, 1)

	videos.createErr = errors.New("db down")
	if err := in.sync(ctx, s); err == nil {
		t.Fatal("sync worked, want an error")
	}

	videos.createErr = nil
	files.saved = nil
	if err := in.sync(ctx, s); err != nil {
		t.Fatal(err)
	}
	if videos.status[s.id] != "live" {
		t.Errorf("status = %q, want live", videos.status[s.id])
	}
	want := []string{s.path(posterName), s.path(liveName)}
	if !slices.Equal(files.saved, want) {
		t.Errorf("saved %v, want %v", files.saved, want)
	}

	files.saved = nil
	if err := in.sync(ctx, s); err != nil || len(files.saved) != 0 {
		t.Errorf("sync with nothing new: err = %v, saved = %v, want nothing", err, files.saved)
	}
}

// When a stream ends, the segments made since the last sync are saved, then
// the finished playlist, and the video is marked ready with its length.
func TestTakeFinishesStream(t *testing.T) {
	in, videos, files := newTestIngester()
	var dir string
	in.record = func(_ context.Context, d string) error {
		dir = d
		writeSegments(t, d, 3)
		return nil
	}

	if err := in.take(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(videos.status) != 1 {
		t.Fatalf("got %d videos, want 1", len(videos.status))
	}
	for id, status := range videos.status {
		if status != "ready" || videos.duration[id] != 6 {
			t.Errorf("status = %q, duration = %v, want ready and 6", status, videos.duration[id])
		}
		s := &stream{id: id}
		if last := files.saved[len(files.saved)-1]; last != s.path(masterName) {
			t.Errorf("last file saved = %s, want %s", last, s.path(masterName))
		}
		if !strings.HasSuffix(string(files.data[s.path(masterName)]), "#EXT-X-ENDLIST\n") {
			t.Errorf("finished playlist doesn't end:\n%s", files.data[s.path(masterName)])
		}
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("temporary folder still there: %v", err)
	}
}

// If FFmpeg fails before any stream comes in, nothing is saved or added, and
// FFmpeg's error is returned.
func TestTakeWithNoStream(t *testing.T) {
	in, videos, files := newTestIngester()
	ffmpegErr := errors.New("ffmpeg failed")
	in.record = func(context.Context, string) error { return ffmpegErr }

	if err := in.take(context.Background()); !errors.Is(err, ffmpegErr) {
		t.Errorf("err = %v, want %v", err, ffmpegErr)
	}
	if len(videos.status) != 0 || len(files.saved) != 0 {
		t.Errorf("videos = %v, saved = %v, want both empty", videos.status, files.saved)
	}
}
