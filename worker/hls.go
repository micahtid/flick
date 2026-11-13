package main

// Turning an upload into HLS (HTTP Live Streaming): a playlist and 4-second
// segments for each quality, a poster image, and a master playlist that lists
// the qualities. The player reads the master playlist and switches quality as
// the connection speeds up or slows down.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// File names inside a video's HLS folder, /data/hls/<id>/.
const (
	masterName = "master.m3u8" // Written last, so if it exists, every other file is finished.
	posterName = "poster.jpg"
)

// HLS settings shared by every quality.
const (
	segmentSeconds  = 4       // Each segment's length. Every quality uses the same length, so the player can switch between them cleanly.
	audioBitrate    = 128_000 // Bits per second of AAC audio.
	posterMaxHeight = 720     // The poster is never taller than this.
)

// rendition is one quality of a video. bandwidth is roughly how many bits per
// second it needs, which the player uses to choose between them.
type rendition struct {
	height    int
	bandwidth int
}

// renditions lists every quality, tallest first.
var renditions = []rendition{
	{height: 1080, bandwidth: 5_000_000},
	{height: 720, bandwidth: 2_800_000},
	{height: 360, bandwidth: 800_000},
}

// name is the rendition's label, such as "720p". Its playlist is 720p.m3u8 and
// its segments are 720p_000.ts, 720p_001.ts and so on.
func (r rendition) name() string { return fmt.Sprintf("%dp", r.height) }

func (r rendition) playlist() string { return r.name() + ".m3u8" }

// width is how wide the rendition is when the source's shape is kept. It's
// rounded to an even number, the same as FFmpeg's "scale=-2:<height>".
func (r rendition) width(src source) int {
	return evenRound(float64(src.width) * float64(r.height) / float64(src.height))
}

// source is what ffprobe reports about an upload. width and height are as the
// video is shown, so a phone video filmed upright is taller than it is wide.
// duration is in seconds, or 0 if unknown.
type source struct {
	width, height int
	duration      float64
}

// renditionsFor picks the qualities to make for a source, tallest first. Any
// quality taller than the source is skipped, since making it would only waste
// space. A source shorter than every quality gets one rendition at its own
// height, so every video can still play.
func renditionsFor(src source) []rendition {
	var picked []rendition
	for _, r := range renditions {
		if r.height <= src.height {
			picked = append(picked, r)
		}
	}
	if len(picked) == 0 {
		smallest := renditions[len(renditions)-1]
		// H.264 needs an even height, so round down to one.
		picked = []rendition{{height: src.height &^ 1, bandwidth: smallest.bandwidth}}
	}
	return picked
}

// masterPlaylist lists the renditions so the player can choose between them.
func masterPlaylist(src source, rs []rendition) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	for _, r := range rs {
		fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d\n%s\n",
			r.bandwidth, r.width(src), r.height, r.playlist())
	}
	return b.String()
}

// convertToHLS makes every HLS file for the upload at in, inside dir. It
// returns the video's length in seconds. Running it again on the same upload
// overwrites the files, so a retried job starts clean.
func convertToHLS(ctx context.Context, in, dir string) (duration float64, err error) {
	src, err := probe(ctx, in)
	if err != nil {
		return 0, err
	}
	// Remove any master playlist left by an earlier attempt, so the video
	// doesn't count as finished until this attempt is.
	if err := os.Remove(filepath.Join(dir, masterName)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return 0, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}

	rs := renditionsFor(src)
	for _, r := range rs {
		if err := encodeRendition(ctx, in, dir, r); err != nil {
			return 0, fmt.Errorf("make %s: %w", r.name(), err)
		}
	}
	if err := makePoster(ctx, in, dir, src); err != nil {
		return 0, fmt.Errorf("make poster: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(dir, masterName), []byte(masterPlaylist(src, rs))); err != nil {
		return 0, fmt.Errorf("write master playlist: %w", err)
	}
	return src.duration, nil
}

// encodeRendition makes one rendition's playlist and segments.
func encodeRendition(ctx context.Context, in, dir string, r rendition) error {
	// Cap the video's bit rate so the rendition fits the bandwidth the master
	// playlist promises, leaving room for the audio.
	maxrate := r.bandwidth - audioBitrate
	return runFFmpeg(ctx,
		"-i", in,
		"-vf", fmt.Sprintf("scale=-2:%d", r.height), // -2 picks an even width that keeps the shape.
		"-c:v", "libx264", // H.264, the most common video format.
		"-preset", "veryfast", // Convert quickly, for slightly bigger files.
		"-maxrate", strconv.Itoa(maxrate),
		"-bufsize", strconv.Itoa(2*maxrate), // How far the bit rate may briefly go over the cap.
		"-c:a", "aac", "-b:a", strconv.Itoa(audioBitrate), // AAC, the most common audio format.
		// A segment can only start on a key frame (a full picture). Putting one
		// every segmentSeconds in every rendition makes their segments start at
		// exactly the same moments.
		"-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", segmentSeconds),
		"-hls_time", strconv.Itoa(segmentSeconds),
		"-hls_playlist_type", "vod", // A finished video, not a live one. VOD means "video on demand".
		"-hls_segment_filename", filepath.Join(dir, r.name()+"_%03d.ts"),
		filepath.Join(dir, r.playlist()),
	)
}

// makePoster saves one frame as a JPEG, shown before the video plays and as
// its thumbnail. It's taken a second in, or halfway through a shorter video,
// since the very first frame is often black.
func makePoster(ctx context.Context, in, dir string, src source) error {
	at := math.Min(1, src.duration/2)
	return runFFmpeg(ctx,
		"-ss", strconv.FormatFloat(at, 'f', 2, 64), // Jump here before reading, which is fast.
		"-i", in,
		"-frames:v", "1", // Save one frame.
		"-vf", fmt.Sprintf("scale=-2:'min(%d,ih)'", posterMaxHeight), // Shrink it, but never enlarge it.
		"-q:v", "3", // JPEG quality, from 2 (best) to 31 (worst).
		"-update", "1", // Write a single image, not a numbered series.
		filepath.Join(dir, posterName),
	)
}

// probe asks ffprobe for the first video stream's size and rotation, and the
// file's length.
func probe(ctx context.Context, in string) (source, error) {
	out, err := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height:stream_side_data=rotation:format=duration",
		"-of", "json",
		in,
	).Output()
	if err != nil {
		// Output only collects ffprobe's normal output. Its error messages are
		// kept on the error instead.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return source{}, fmt.Errorf("ffprobe: %w: %s", err, bytes.TrimSpace(exitErr.Stderr))
		}
		return source{}, fmt.Errorf("ffprobe: %w", err)
	}
	return parseProbe(out)
}

// probeOutput is the part of ffprobe's JSON that probe asks for.
type probeOutput struct {
	Streams []struct {
		Width        int `json:"width"`
		Height       int `json:"height"`
		SideDataList []struct {
			Rotation float64 `json:"rotation"`
		} `json:"side_data_list"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"` // ffprobe sends this as text, such as "12.345000".
	} `json:"format"`
}

// parseProbe reads ffprobe's JSON. Phones store upright videos sideways with
// a rotation of 90 degrees, and FFmpeg turns them upright when converting, so
// width and height are swapped to match.
func parseProbe(data []byte) (source, error) {
	var p probeOutput
	if err := json.Unmarshal(data, &p); err != nil {
		return source{}, fmt.Errorf("read ffprobe output: %w", err)
	}
	if len(p.Streams) == 0 || p.Streams[0].Width <= 0 || p.Streams[0].Height <= 0 {
		return source{}, fmt.Errorf("no video stream found")
	}
	s := p.Streams[0]
	src := source{width: s.Width, height: s.Height}
	for _, sd := range s.SideDataList {
		if math.Mod(math.Abs(sd.Rotation), 180) == 90 {
			src.width, src.height = src.height, src.width
		}
	}
	// The length is only used for display, so an unknown one is left at 0
	// rather than failing the job.
	src.duration, _ = strconv.ParseFloat(p.Format.Duration, 64)
	return src, nil
}

// runFFmpeg runs FFmpeg with the given arguments, overwriting any existing
// output. CommandContext stops FFmpeg if ctx is cancelled, such as when the
// worker is told to stop.
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

// writeFileAtomic writes data to a temporary file, then renames it to path.
// A rename happens all at once, so path is never seen half written.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".part"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// evenRound rounds x to the nearest even whole number.
func evenRound(x float64) int {
	return int(math.Round(x/2)) * 2
}
