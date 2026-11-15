# Phase 7: Live Streaming And Chat

## What You're Building

A creator goes live from **OBS**, a free streaming app. Viewers watch in the browser less than 10 seconds behind, and everyone can chat in real time. When the stream ends, it becomes a normal video that anyone can replay.

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
| Live ingest | `ingest/` (new), port 1935 | Receives the stream from OBS, turns it into HLS segments, and saves each one on the storage nodes. |
| Chat | `api/chat.go` (new) | Passes messages between viewers over WebSockets. |
| Redis | Ready-made image | Passes each chat message to every copy of the API. |
| Nginx | `nginx/nginx.conf` | Passes chat's WebSockets to the API, and stops the browser keeping old live playlists. |
| Live page | `web/src/components/Chat.tsx` (new) | Plays the stream with chat beside it. |

## Step 1: Build Live Ingest

The ingest service starts FFmpeg. FFmpeg waits for OBS to connect, then writes HLS files into a temporary folder as the stream comes in. It builds from the project folder, like the worker, so it can use `pkg/chunk`. Its Dockerfile also needs `RUN apk add --no-cache ffmpeg`.

```go
cmd := exec.Command("ffmpeg", "-listen", "1",
    "-i", "rtmp://0.0.0.0:1935/live/stream",
    "-c:v", "libx264", "-preset", "veryfast", "-tune", "zerolatency",
    "-c:a", "aac", "-force_key_frames", "expr:gte(t,n_forced*2)",
    "-f", "hls", "-hls_time", "2", "-hls_list_size", "0",
    "-hls_flags", "temp_file",
    "-hls_segment_filename", dir+"/seg_%05d.ts",
    dir+"/record.m3u8")
cmd.Run() // returns once the stream ends
```

What the new options mean:

- `-listen 1`: wait for OBS to connect, instead of reading a file.
- `-tune zerolatency`: convert each frame as fast as possible, to keep the delay small.
- `-hls_time 2`: make 2 second segments. They're shorter than before, so viewers wait less.
- `-hls_list_size 0`: list every segment in `record.m3u8`, not just the newest.
- `-hls_flags temp_file`: write each file under a temporary name, and rename it once it's finished. That way the service never reads half a file.

When OBS stops, FFmpeg exits. Start it again in a loop so the next stream works.

**Save segments as they appear.** Every second, read `record.m3u8`. Each segment in it is an `#EXTINF` line with its length, then a line with its name.

1. Save each segment you haven't saved yet with `chunk.Store.Save` (from Phase 4), at `hls/{streamID}/seg_00042.ts`.
2. Write the **live playlist** yourself, listing only the newest 6 segments, and save it at `hls/{streamID}/live.m3u8`. Set `#EXT-X-MEDIA-SEQUENCE` to the number of the first segment it lists, so the player knows where the list starts.

The segments go first, so the live playlist never lists one that isn't saved yet. Keep FFmpeg's full list to yourself. You need it in Step 2.

**Stream IDs.** On the first save of a stream, make a poster from its first segment, then add a row to `videos` with `status = 'live'`. That row's ID is the `streamID`. Viewers use it to find the stream.

**Why `hls/` and not a new folder?** Live files then go through the cache like any other. Segments never change, so the cache keeps them, and a popular stream's segments are read from storage once. When the stream ends, the replay uses the same segments, so nothing has to be copied.

**Never keep an old live playlist.** The cache already passes every playlist straight through. The browser still needs telling, so add this to `nginx/nginx.conf`:

```nginx
location ~ ^/hls/.+/live\.m3u8$ {
    proxy_pass http://cache:8082;
    add_header Cache-Control no-cache;
}
```

`no-cache` matters. The live playlist changes every 2 seconds, so the browser must never reuse an old copy. A regex location like this one wins over the `/hls/` block, which tells the browser to keep files for a year.

## Step 2: Turn The Stream Into A Normal Video

When the stream ends:

1. Save any segments made since the last save.
2. Write a final playlist that lists **every** segment, not just the last 6. Put `#EXT-X-PLAYLIST-TYPE:VOD` at the top and `#EXT-X-ENDLIST` at the bottom. Those two lines tell the player that the video is finished.
3. Save it at `hls/{streamID}/master.m3u8`, the same name as an upload's, and set the video's status to `ready` with its length.

Now the stream shows up in the list and plays like an uploaded video.

If the service is told to stop mid-stream, finish the stream with what you have. If it crashes, the list of segments is lost with the temporary folder, so remove any video still marked `live` when the service next starts.

## Step 3: Add Chat

Chat uses **WebSockets**. Normally the browser asks and the server answers once. A WebSocket stays open, so either side can send a message at any time. That's what makes chat instant.

There's a catch. Later you might run several copies of the API. Viewer A could be connected to copy 1 and viewer B to copy 2. A message sent to copy 1 must still reach viewer B.

**Redis pub/sub** solves this. "Pub/sub" means publish and subscribe. Each copy of the API **subscribes** to a channel for the stream. When a message arrives, the API **publishes** it to that channel, and Redis delivers it to every subscriber.

From the `api/` folder:

```sh
go get github.com/coder/websocket github.com/redis/go-redis/v9
```

```go
mux.HandleFunc("GET /chat/{id}", func(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    channel := "chat:" + r.PathValue("id") // one channel per stream

    sub := rdb.Subscribe(ctx, channel)
    defer sub.Close()

    conn, err := websocket.Accept(w, r, nil) // turn this request into a WebSocket
    if err != nil { return }
    defer conn.CloseNow()

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
        rdb.Publish(ctx, channel, data)
    }
})
```

Notes:

- **Check every message.** Read it as JSON with a `name` and `text`, trim the spaces, and skip it if either is empty or too long (50 and 500 characters). Then write it out as JSON again before publishing it, which drops anything else the viewer sent.
- **Don't let one slow viewer hold up the rest.** Give each write a few seconds. A viewer who can't keep up is dropped.
- **Hide Redis behind an interface,** the way the API already hides the database. The tests can then pass messages through memory instead.

Nginx needs extra settings to pass WebSockets through. Without them, the connection closes at once:

```nginx
location /api/chat/ {
    proxy_pass http://api:8080/chat/;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_set_header Host $host;
    proxy_read_timeout 1h; # keep quiet chats open
}
```

`Host $host` matters too. `websocket.Accept` only accepts a page from the same site, which it checks by comparing the page's address to the `Host` header. Without this line, Nginx changes `Host` to `api:8080`, and every browser gets `403`.

## Step 4: Build The Live Page

A stream uses the same watch page as any other video, at `/watch?v={id}`. That way the same link works while it's live and after it ends. While the video's status is `live`, show the player with chat beside it.

- **Player.** Reuse the Phase 3 player with `src = /hls/{id}/live.m3u8`. Create it with `new Hls({ liveSyncDuration: 6 })`, which tells it to stay about 6 seconds behind live.
- **Chat.** Open a WebSocket, show each message that arrives, and send what the viewer types. If it drops, open it again a few seconds later.

  ```ts
  const ws = new WebSocket(`ws://${location.host}/api/chat/${id}`)
  ws.onmessage = (e) => setMessages((m) => [...m, JSON.parse(e.data)])
  ws.send(JSON.stringify({ name, text }))
  ```

- **List.** Keep loading the video list every few seconds while a video is live, so the page switches to the finished video once the stream ends.

## Step 5: Update Compose

- Remove `profiles: [full]` from `redis` and `ingest`.
- Build `ingest` from the project folder, like the worker, and give it the same `DATABASE_URL` and `STORAGE_NODES`. Add `depends_on: [db, api, node1, node2, node3]`. It needs the API, which creates the `videos` and `files` tables.
- Set `REDIS_URL: redis://redis:6379` on the API, and add `redis` to its `depends_on`.

## Step 6: Test With OBS

In OBS, open **Settings**, then **Stream**, and set:

- Service: **Custom**
- Server: `rtmp://localhost:1935/live`
- Stream key: `stream`

Click **Start Streaming**.

## Done When

- You stream from OBS and watch in the browser less than 10 seconds behind.
- A chat message sent in one browser window appears instantly in another.
- After the stream ends, it can be watched again as a normal video.
