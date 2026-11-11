# Phase 3: Adaptive Streaming

## The Problem

Right now each video exists in one quality, 720p. On a slow connection, the player can't download it fast enough, so the video keeps stopping to load. On a fast connection, the viewer could have watched in higher quality.

## The Fix

Make each video in three qualities: 1080p, 720p and 360p. Then let the player switch between them as the connection speeds up or slows down. This is called **adaptive streaming**. YouTube and Netflix work this way.

The standard way to do it is **HLS** (HTTP Live Streaming). The words to know:

- **Segment.** The video is cut into many small files, each 4 seconds long. They end in `.ts`.
- **Playlist.** A plain text file, ending in `.m3u8`, that lists the segments in order. Each quality has its own playlist.
- **Master playlist.** A small text file that lists the three quality playlists.

The player first loads the master playlist. Before each segment, it checks how fast the last download was, then picks the 1080p, 720p or 360p version of the next segment.

## Step 1: Try It By Hand

Before changing any code, run FFmpeg yourself on any MP4 so you can see what it makes:

```sh
ffmpeg -i input.mp4 -vf scale=-2:720 -c:v libx264 -preset veryfast -c:a aac \
  -force_key_frames "expr:gte(t,n_forced*4)" \
  -hls_time 4 -hls_playlist_type vod \
  -hls_segment_filename "720p_%03d.ts" 720p.m3u8
```

What the new options mean:

- `-hls_time 4`: make each segment 4 seconds long.
- `-hls_playlist_type vod`: this is a finished video, not a live one. VOD means "video on demand".
- `-hls_segment_filename "720p_%03d.ts"`: name the segments `720p_000.ts`, `720p_001.ts` and so on.
- `-force_key_frames ...`: a segment can only start on a key frame, which is a full picture. This option puts one every 4 seconds. Because every quality uses the same setting, their segments start at exactly the same moments. That's what lets the player switch cleanly.

Run it two more times, with `1080` and `360` in place of `720`. Then open `720p.m3u8` in a text editor. It's just a list of file names and lengths.

## Step 2: Write The Master Playlist

Save this as `master.m3u8` in the same folder:

```
#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=5000000,RESOLUTION=1920x1080
1080p.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=2800000,RESOLUTION=1280x720
720p.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=640x360
360p.m3u8
```

`BANDWIDTH` is roughly how many bits per second each quality needs. The player uses it to choose.

## Step 3: Move It Into The Worker

Now make the worker do the same thing for every upload. It puts all the files for a video into one folder, `/data/hls/{video_id}/`:

```go
dir := "/data/hls/" + job.VideoID
os.MkdirAll(dir, 0o755)

for _, h := range []int{1080, 720, 360} {
    name := fmt.Sprintf("%dp", h) // "1080p", "720p", "360p"
    cmd := exec.Command("ffmpeg", "-y", "-i", in,
        "-vf", fmt.Sprintf("scale=-2:%d", h),
        "-c:v", "libx264", "-preset", "veryfast", "-c:a", "aac",
        "-force_key_frames", "expr:gte(t,n_forced*4)",
        "-hls_time", "4", "-hls_playlist_type", "vod",
        "-hls_segment_filename", filepath.Join(dir, name+"_%03d.ts"),
        filepath.Join(dir, name+".m3u8"))
    if err := cmd.Run(); err != nil { /* log it and don't mark the job done */ }
}
os.WriteFile(filepath.Join(dir, "master.m3u8"), []byte(master), 0o644)
```

Two details:

- **Don't make a quality bigger than the original.** A 480p upload should not become 1080p. That only wastes space. Find the original's height with `ffprobe`, which comes with FFmpeg, and skip any quality taller than it. Leave skipped qualities out of the master playlist too.

  ```sh
  ffprobe -v error -select_streams v:0 -show_entries stream=height -of csv=p=0 input.mp4
  ```

- **Write `master.m3u8` last.** If it exists, you know every other file is finished.

## Step 4: Serve The Files With Nginx

Nginx needs to see the files. In Compose, add the `uploads` volume to the `nginx` service as `uploads:/data:ro`. The `ro` means read only, so Nginx can't change anything. Then add this to `nginx/nginx.conf`:

```nginx
location /hls/ {
    alias /data/hls/;
    types { application/vnd.apple.mpegurl m3u8; video/mp2t ts; }
    add_header Cache-Control "public, max-age=31536000";
}
```

- `alias` maps `/hls/...` in the address to `/data/hls/...` on disk.
- `types` tells the browser what kind of file each one is.
- `Cache-Control` lets the browser keep files for a year. That's safe because a finished segment never changes.

## Step 5: Update The Player

Chrome and Firefox can't play HLS on their own. The `hls.js` library adds it, and it does the quality switching for you.

```sh
cd web && npm install hls.js
```

```tsx
import Hls from 'hls.js'

function Player({ id }: { id: string }) {
  const ref = useRef<HTMLVideoElement>(null)
  useEffect(() => {
    const src = `/hls/${id}/master.m3u8`
    const video = ref.current!
    if (Hls.isSupported()) {
      const hls = new Hls()
      hls.loadSource(src)
      hls.attachMedia(video)
      return () => hls.destroy() // clean up when the player goes away
    }
    video.src = src // Safari can play HLS on its own
  }, [id])
  return <video ref={ref} controls width={640} />
}
```

You no longer need the `GET /videos/{id}/file` endpoint, so delete it.

## Done When

- In Chrome, open DevTools and go to the **Network** tab. Change **No throttling** to **Slow 4G**. The picture gets blurrier, but the video keeps playing.
- Change it back to **No throttling**. The picture gets sharp again.
