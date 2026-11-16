package main

// This file tests hls.go. Most tests need no FFmpeg. TestConvertToHLS runs
// the real FFmpeg and is skipped if it isn't installed.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Qualities taller than the source are skipped, and a very short source gets
// one rendition at its own (even) height.
func TestRenditionsFor(t *testing.T) {
	tests := []struct {
		height int
		want   []int
	}{
		{2160, []int{1080, 720, 360}},
		{1080, []int{1080, 720, 360}},
		{720, []int{720, 360}},
		{480, []int{360}},
		{241, []int{240}},
	}
	for _, tt := range tests {
		var got []int
		for _, r := range renditionsFor(source{width: 640, height: tt.height}) {
			got = append(got, r.height)
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("renditionsFor(height %d) = %v, want %v", tt.height, got, tt.want)
		}
	}
}

// The master playlist lists each rendition with its bandwidth, its size in
// the source's shape, and its playlist file.
func TestMasterPlaylist(t *testing.T) {
	src := source{width: 1920, height: 1080}
	got := masterPlaylist(src, renditionsFor(src))
	want := `#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=5000000,RESOLUTION=1920x1080
1080p.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=2800000,RESOLUTION=1280x720
720p.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=640x360
360p.m3u8
`
	if got != want {
		t.Errorf("master playlist =\n%s\nwant\n%s", got, want)
	}

	// An upright phone video keeps its shape: 360 tall is about 202 wide.
	upright := source{width: 1080, height: 1920}
	if w := (rendition{height: 360}).width(upright); w != 202 {
		t.Errorf("upright 360p width = %d, want 202", w)
	}
}

// parseProbe reads the size and length, swaps the size for rotated videos,
// and rejects files with no video.
func TestParseProbe(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		want    source
		wantErr bool
	}{
		{"plain", `{"streams":[{"width":1920,"height":1080}],"format":{"duration":"12.5"}}`, source{1920, 1080, 12.5}, false},
		{"rotated", `{"streams":[{"width":1920,"height":1080,"side_data_list":[{"rotation":-90}]}],"format":{"duration":"3"}}`, source{1080, 1920, 3}, false},
		{"unknown length", `{"streams":[{"width":640,"height":360}],"format":{}}`, source{640, 360, 0}, false},
		{"no video", `{"streams":[],"format":{"duration":"3"}}`, source{}, true},
		{"not json", `oops`, source{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseProbe([]byte(tt.json))
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("parseProbe = %+v, %v, want %+v and an error: %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

// A real conversion of a short generated clip makes every file the player
// needs and skips qualities taller than the clip.
func TestConvertToHLS(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
	dir := t.TempDir()
	in := filepath.Join(dir, "in.mp4")

	// lavfi makes a 6-second 1280x720 test pattern with a tone, so the test
	// needs no video file.
	if err := runFFmpeg(context.Background(),
		"-f", "lavfi", "-i", "testsrc=duration=6:size=1280x720:rate=24",
		"-f", "lavfi", "-i", "sine=duration=6",
		"-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-shortest", in,
	); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "hls")
	duration, err := convertToHLS(context.Background(), in, out)
	if err != nil {
		t.Fatal(err)
	}
	if duration < 5.9 || duration > 6.1 {
		t.Errorf("duration = %v, want about 6", duration)
	}

	for _, name := range []string{masterName, posterName, "720p.m3u8", "720p_000.ts", "720p_001.ts", "360p.m3u8", "360p_000.ts"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "1080p.m3u8")); err == nil {
		t.Error("made 1080p from a 720p source, want it skipped")
	}
	master, err := os.ReadFile(filepath.Join(out, masterName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(master), "1080p") {
		t.Errorf("master playlist lists 1080p:\n%s", master)
	}
}
