# Phase 7: Live Streaming And Chat

## What You're Building

A creator goes live from **OBS**, a free streaming app. Viewers watch in the browser about 10 seconds behind, and everyone can chat in real time. When the stream ends, it becomes a normal video that anyone can replay.

## How Live Video Differs

An uploaded video is converted once, and its playlist never changes.

A live video is converted **while it's happening**. Every couple of seconds a new segment is made. The playlist lists only the newest few segments, and the player downloads it again every few seconds to find out what's new.

The words to know:

- **RTMP.** The format OBS uses to send a live stream to a server. It uses port 1935.
- **Ingest.** Taking in the live stream from the creator.
- **Latency.** How far behind real life the viewer is.

## The Parts In This Phase

| Part | Where | Job |
| --- | --- | --- |
| Live ingest | `ingest/`, port 1935 | Receives the stream from OBS and turns it into HLS segments |
| Chat | Inside the API | Passes messages between viewers |
| Redis | Ready-made image | Passes each chat message to every copy of the API |

## Step 1: Build Live Ingest

The ingest service starts FFmpeg. FFmpeg waits for OBS to connect, then writes HLS files into a temporary folder as the stream comes in. The ingest Dockerfile needs `RUN apk add --no-cache ffmpeg`, just like the worker's.

```go
dir := "/tmp/live/" + streamID
os.MkdirAll(dir, 0o755)
cmd := exec.Command("ffmpeg", "-listen", "1",
    "-i", "rtmp://0.0.0.0:1935/live/stream",
    "-c:v", "libx264", "-preset", "veryfast", "-tune", "zerolatency",
    "-c:a", "aac", "-force_key_frames", "expr:gte(t,n_forced*2)",
    "-f", "hls", "-hls_time", "2", "-hls_list_size", "6",
    "-hls_segment_filename", dir+"/seg_%05d.ts",
    dir+"/live.m3u8")
cmd.Start()
```

What the new options mean:

- `-listen 1`: wait for OBS to connect, instead of reading a file.
- `-tune zerolatency`: convert each frame as fast as possible, to keep the delay small.
- `-hls_time 2`: make 2 second segments. They're shorter than before, so viewers wait less.
- `-hls_list_size 6`: keep only the newest 6 segments in the playlist.

When OBS stops, FFmpeg exits. Start it again in a loop so the next stream works.

**Stream IDs.** When a stream starts, add a row to `videos` with `status = 'live'`. That row's ID is the `streamID`. Viewers use it to find the stream.

**Upload segments as they appear.** Every second, look in `dir` for new `.ts` files:

1. Skip the newest file. FFmpeg is still writing it.
2. Send each finished segment through `chunk.Put` (from Phase 4). Add a `files` row with the path `live/{streamID}/seg_00042.ts`.
3. Upload `live.m3u8` too, replacing the old version in `files`.

**Let viewers reach it.** Add this to `nginx/nginx.conf`:

```nginx
location /live/ {
    proxy_pass http://api:8080/files/live/;
    add_header Cache-Control no-cache;
}
```

`no-cache` matters. The live playlist changes every 2 seconds, so the browser must never reuse an old copy.

## Step 2: Test With OBS

In OBS, open **Settings**, then **Stream**, and set:

- Service: **Custom**
- Server: `rtmp://localhost:1935/live`
- Stream key: `stream`

Click **Start Streaming**.

## Step 3: Turn The Stream Into A Normal Video

When the stream ends:

1. Write a final playlist that lists **every** segment, not just the last 6. Put `#EXT-X-PLAYLIST-TYPE:VOD` at the top and `#EXT-X-ENDLIST` at the bottom. Those two lines tell the player that the video is finished.
2. Save it like any other file, and set the video's status to `ready`.

Now the stream shows up in the list and plays like an uploaded video.

## Step 4: Add Chat

Chat uses **WebSockets**. Normally the browser asks and the server answers once. A WebSocket stays open, so either side can send a message at any time. That's what makes chat instant.

There's a catch. Later you might run several copies of the API. Viewer A could be connected to copy 1 and viewer B to copy 2. A message sent to copy 1 must still reach viewer B.

**Redis pub/sub** solves this. "Pub/sub" means publish and subscribe. Each copy of the API **subscribes** to a channel for the stream. When a message arrives, the API **publishes** it to that channel, and Redis delivers it to every subscriber.

From the `api/` folder:

```sh
go get github.com/coder/websocket github.com/redis/go-redis/v9
```

```go
mux.HandleFunc("GET /chat/{id}", func(w http.ResponseWriter, r *http.Request) {
    conn, err := websocket.Accept(w, r, nil) // turn this request into a WebSocket
    if err != nil { return }
    defer conn.CloseNow()
    ctx := r.Context()
    channel := "chat:" + r.PathValue("id") // one channel per stream

    sub := rdb.Subscribe(ctx, channel)
    defer sub.Close()

    // Messages from Redis go out to this viewer.
    go func() {
        for msg := range sub.Channel() {
            conn.Write(ctx, websocket.MessageText, []byte(msg.Payload))
        }
    }()

    // Messages from this viewer go into Redis.
    for {
        _, data, err := conn.Read(ctx)
        if err != nil { return } // the viewer left
        if len(data) > 500 { continue } // ignore huge messages
        rdb.Publish(ctx, channel, data)
    }
})
```

Nginx needs extra settings to pass WebSockets through. Without them, the connection closes at once:

```nginx
location /api/chat/ {
    proxy_pass http://api:8080/chat/;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_read_timeout 1h; # keep quiet chats open
}
```

In Compose, give the API `REDIS_URL: redis:6379`, and remove `profiles: [full]` from `redis` and `ingest`.

## Step 5: Build The Live Page

Add a page at `/live/{id}` with the player on one side and chat on the other.

- **Player.** Reuse the Phase 3 player with `src = /live/{id}/live.m3u8`. Create it with `new Hls({ liveSyncDuration: 6 })`, which tells it to stay about 6 seconds behind live.
- **Chat.** Open a WebSocket, show each message that arrives, and send what the viewer types:

  ```ts
  const ws = new WebSocket(`ws://${location.host}/api/chat/${id}`)
  ws.onmessage = (e) => setMessages((m) => [...m, JSON.parse(e.data)])
  ws.send(JSON.stringify({ name, text }))
  ```

## Done When

- You stream from OBS and watch in the browser roughly 10 seconds behind.
- A chat message sent in one browser window appears instantly in another.
- After the stream ends, it can be watched again as a normal video.
