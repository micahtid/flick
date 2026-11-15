package main

// Tests for hls.go. Most need no FFmpeg. TestRecord runs the real FFmpeg and
// is skipped if it isn't installed.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// parsePlaylist reads each segment's name and length, in order, and skips
// tags it doesn't need and a segment FFmpeg hasn't finished listing.
func TestParsePlaylist(t *testing.T) {
	data := []byte(`#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:2
#EXT-X-MEDIA-SEQUENCE:0
#EXTINF:2.000000,
seg_00000.ts
#EXTINF:1.966667,
seg_00001.ts
#EXTINF:2.000000,
`)
	want := []segment{{"seg_00000.ts", 2}, {"seg_00001.ts", 1.966667}}
	if got := parsePlaylist(data); !slices.Equal(got, want) {
		t.Errorf("parsePlaylist = %v, want %v", got, want)
	}
}

// The live playlist lists only the newest 6 segments, numbered from where
// they start in the whole stream, and doesn't end.
func TestLivePlaylist(t *testing.T) {
	var segs []segment
	for _, name := range []string{"a.ts", "b.ts", "c.ts", "d.ts", "e.ts", "f.ts", "g.ts", "h.ts"} {
		segs = append(segs, segment{name, 2})
	}
	want := `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:2
#EXT-X-MEDIA-SEQUENCE:2
#EXTINF:2.000,
c.ts
#EXTINF:2.000,
d.ts
#EXTINF:2.000,
e.ts
#EXTINF:2.000,
f.ts
#EXTINF:2.000,
g.ts
#EXTINF:2.000,
h.ts
`
	if got := livePlaylist(segs); got != want {
		t.Errorf("live playlist =\n%s\nwant\n%s", got, want)
	}
}

// The finished playlist lists every segment, rounds the target duration up,
// and marks the video as finished.
func TestFinalPlaylist(t *testing.T) {
	segs := []segment{{"seg_00000.ts", 2}, {"seg_00001.ts", 2.5}}
	want := `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:3
#EXT-X-MEDIA-SEQUENCE:0
#EXT-X-PLAYLIST-TYPE:VOD
#EXTINF:2.000,
seg_00000.ts
#EXTINF:2.500,
seg_00001.ts
#EXT-X-ENDLIST
`
	if got := finalPlaylist(segs); got != want {
		t.Errorf("final playlist =\n%s\nwant\n%s", got, want)
	}
	if got := totalDuration(segs); got != 4.5 {
		t.Errorf("totalDuration = %v, want 4.5", got)
	}
}

// A real FFmpeg waits for a stream, turns a short generated clip sent over
// RTMP into 2-second segments, and makes a poster from the first one. It uses
// port 1935, so it fails if something else, such as the ingest container, is
// using it.
func TestRecord(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	dir := t.TempDir()

	done := make(chan error, 1)
	go func() { done <- record(ctx, dir) }()

	// Stand in for OBS. lavfi makes a 6-second test pattern with a tone, so
	// the test needs no video file. FFmpeg takes a moment to start listening,
	// so keep trying until it connects.
	for {
		err := runFFmpeg(ctx,
			"-f", "lavfi", "-i", "testsrc=duration=6:size=640x360:rate=24",
			"-f", "lavfi", "-i", "sine=duration=6",
			"-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-shortest",
			"-f", "flv", "rtmp://127.0.0.1:1935/live/stream",
		)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("send stream: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, recordName))
	if err != nil {
		t.Fatal(err)
	}
	segs := parsePlaylist(data)
	if len(segs) < 3 {
		t.Fatalf("got %d segments, want at least 3 for 6 seconds: %v", len(segs), segs)
	}
	if total := totalDuration(segs); total < 5.9 || total > 6.1 {
		t.Errorf("total duration = %v, want about 6", total)
	}

	poster := filepath.Join(dir, posterName)
	if err := makePoster(ctx, filepath.Join(dir, segs[0].name), poster); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(poster); err != nil {
		t.Errorf("missing poster: %v", err)
	}
}
