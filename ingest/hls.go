package main

// This file turns a live stream into HLS (HTTP Live Streaming). FFmpeg writes
// one 2-second segment at a time as the stream comes in, plus a playlist that
// lists every segment so far. The ingest service reads that playlist and makes
// the two that viewers load: a live playlist of only the newest segments and,
// once the stream ends, a finished playlist of all of them.

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// rtmpURL is where FFmpeg waits for OBS. OBS sends to
// rtmp://localhost:1935/live with the stream key "stream", which together make
// this address.
const rtmpURL = "rtmp://0.0.0.0:1935/live/stream"

// These are the file names inside a stream's HLS folder.
const (
	recordName = "record.m3u8" // This is FFmpeg's own playlist. It's read, but never saved.
	liveName   = "live.m3u8"   // This is the playlist viewers load while the stream is live.
	masterName = "master.m3u8" // This is the finished playlist. Uploads use the same name, so the website plays both the same way.
	posterName = "poster.jpg"
)

// These HLS settings shape the stream.
const (
	segmentSeconds  = 2       // Short segments keep viewers close to live.
	liveSegments    = 6       // The live playlist lists this many of the newest segments.
	audioBitrate    = 128_000 // This is the audio bit rate, in bits per second.
	posterMaxHeight = 720     // The poster is never taller than this.
)

// segment is one segment of a stream. name is its file name, such as
// seg_00042.ts, and duration is its length in seconds.
type segment struct {
	name     string
	duration float64
}

// record runs FFmpeg until a stream has come in and ended, writing its
// segments and FFmpeg's playlist inside dir. It returns an error if FFmpeg
// fails, or if ctx is cancelled.
func record(ctx context.Context, dir string) error {
	return runFFmpeg(ctx,
		"-listen", "1", // Wait for OBS to connect, instead of reading a file.
		"-i", rtmpURL,
		"-c:v", "libx264", // Use H.264, the most common video format.
		"-preset", "veryfast", // Convert quickly, at the cost of slightly bigger files.
		"-tune", "zerolatency", // Send each frame on as soon as it's converted, to keep the delay small.
		"-c:a", "aac", "-b:a", strconv.Itoa(audioBitrate), // Use AAC, the most common audio format.
		// A segment can only start on a key frame (a full picture), so put one
		// every segmentSeconds.
		"-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", segmentSeconds),
		"-f", "hls",
		"-hls_time", strconv.Itoa(segmentSeconds),
		"-hls_list_size", "0", // List every segment, not only the newest.
		"-hls_flags", "temp_file", // Write each file under a temporary name, then rename it once it's finished.
		"-hls_segment_filename", filepath.Join(dir, "seg_%05d.ts"),
		filepath.Join(dir, recordName),
	)
}

// makePoster saves the first frame of the segment at seg as a JPEG at out.
// The poster is the stream's thumbnail, and shows before the video plays.
// Every segment starts on a key frame, so the first frame is a full picture.
func makePoster(ctx context.Context, seg, out string) error {
	return runFFmpeg(ctx,
		"-i", seg,
		"-frames:v", "1", // Save one frame.
		"-vf", fmt.Sprintf("scale=-2:'min(%d,ih)'", posterMaxHeight), // Shrink it, but never enlarge it.
		"-q:v", "3", // Set the JPEG quality, from 2 (best) to 31 (worst).
		"-update", "1", // Write a single image, not a numbered series.
		out,
	)
}

// parsePlaylist reads the segments from FFmpeg's playlist, in order. Each
// segment is an #EXTINF line with its length, such as "#EXTINF:2.000000,",
// then a line with its file name.
func parsePlaylist(data []byte) []segment {
	var segs []segment
	duration := -1.0 // This is the length on the last #EXTINF line, or -1 if there isn't one waiting.
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "#EXTINF:"); ok {
			length, _, _ := strings.Cut(rest, ",")
			if d, err := strconv.ParseFloat(length, 64); err == nil {
				duration = d
			}
		} else if line != "" && !strings.HasPrefix(line, "#") && duration >= 0 {
			segs = append(segs, segment{name: line, duration: duration})
			duration = -1
		}
	}
	return segs
}

// livePlaylist lists the newest segments. The player loads it again every few
// seconds to find new ones. The target duration counts every segment so far,
// not only the listed ones, since HLS doesn't allow it to change mid-stream.
func livePlaylist(segs []segment) string {
	first := max(len(segs)-liveSegments, 0)
	return playlist(segs[first:], first, targetDuration(segs), false)
}

// finalPlaylist lists every segment and says the stream has ended, so the
// player treats it as a finished video.
func finalPlaylist(segs []segment) string {
	return playlist(segs, 0, targetDuration(segs), true)
}

// playlist makes the text of a playlist of segs. seq is the number of the
// first segment in the whole stream, which tells the player where the list
// now starts. target is the longest a segment can be, in whole seconds. ended
// marks the playlist as a finished video.
func playlist(segs []segment, seq, target int, ended bool) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n")
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", target)
	fmt.Fprintf(&b, "#EXT-X-MEDIA-SEQUENCE:%d\n", seq)
	if ended {
		b.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n") // VOD means "video on demand".
	}
	for _, s := range segs {
		fmt.Fprintf(&b, "#EXTINF:%.3f,\n%s\n", s.duration, s.name)
	}
	if ended {
		b.WriteString("#EXT-X-ENDLIST\n")
	}
	return b.String()
}

// targetDuration returns the longest segment's length, rounded up to whole
// seconds, which every playlist must give. It's never under segmentSeconds.
func targetDuration(segs []segment) int {
	target := segmentSeconds
	for _, s := range segs {
		target = max(target, int(math.Ceil(s.duration)))
	}
	return target
}

// totalDuration adds up the length of every segment, in seconds.
func totalDuration(segs []segment) float64 {
	var total float64
	for _, s := range segs {
		total += s.duration
	}
	return total
}

// runFFmpeg runs FFmpeg with the given arguments, overwriting any existing
// output. CommandContext stops FFmpeg if ctx is cancelled, such as when the
// ingest service is told to stop.
func runFFmpeg(ctx context.Context, args ...string) error {
	args = append([]string{
		"-y",                 // Overwrite output files that already exist.
		"-loglevel", "error", // Only print errors, so a failure's output is short.
	}, args...)
	if output, err := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, bytes.TrimSpace(output))
	}
	return nil
}
