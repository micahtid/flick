# API Design

```mermaid
flowchart LR
    m["main.go"]:::main ~~~ d["db.go"]:::db ~~~ v["videos.go"]:::videos ~~~ x["Outside Our Code"]:::ext

    classDef main fill:#e9ecef,stroke:#495057,color:#000
    classDef db fill:#d3f9d8,stroke:#2b8a3e,color:#000
    classDef videos fill:#d0ebff,stroke:#1864ab,color:#000
    classDef ext fill:#fff3bf,stroke:#e67700,color:#000
```

## How the API Starts

```mermaid
flowchart TB
    subgraph row1["Step 1: Database"]
        direction LR
        run["run()<br/>Starts the API."]:::main --> connectDB["connectDB()<br/>Opens a database pool."]:::db --> store["videoStore<br/>Holds the pool."]:::db
    end

    subgraph row2["Step 2: Server"]
        direction LR
        newServer["newServer()<br/>Is given the videoStore."]:::videos --> server["server<br/>Keeps the videoStore."]:::videos --> routes["routes()<br/>Returns a router."]:::videos
    end

    subgraph row3["Step 3: Listen"]
        direction LR
        http["http.Server<br/>Uses the router."]:::ext --> port["Port 8080<br/>Receives requests."]:::ext
    end

    row1 --> row2 --> row3

    classDef main fill:#e9ecef,stroke:#495057,color:#000
    classDef db fill:#d3f9d8,stroke:#2b8a3e,color:#000
    classDef videos fill:#d0ebff,stroke:#1864ab,color:#000
    classDef ext fill:#fff3bf,stroke:#e67700,color:#000
```

All three steps happen in one line of `run()`:

```go
newServer(&videoStore{db: db}, dataDir).routes()
```

## How the Types Connect

```mermaid
flowchart LR
    server["<b>server Struct</b><br/>videos videoRepo<br/>dataDir string"]:::videos
    repo["<b>videoRepo Interface</b><br/>Must Have Create()<br/>Must Have List()"]:::videos
    store["<b>videoStore Struct</b><br/>db *pgxpool.Pool<br/>Create()<br/>List()"]:::db

    server -->|"Holds"| repo
    repo -->|"Filled By"| store

    classDef db fill:#d3f9d8,stroke:#2b8a3e,color:#000
    classDef videos fill:#d0ebff,stroke:#1864ab,color:#000
```

## How a Request Is Handled

`routes()` sends each request to its handler.

```mermaid
flowchart LR
    post["POST /videos"]:::ext --> createVideo["createVideo()"]:::videos --> create["videoStore.Create()"]:::db --> pg1[("Postgres")]:::ext
    get["GET /videos"]:::ext --> listVideos["listVideos()"]:::videos --> list["videoStore.List()"]:::db --> pg2[("Postgres")]:::ext
    file["GET /videos/{id}/file"]:::ext --> serveVideoFile["serveVideoFile()"]:::videos --> disk[("Video Files<br/>in /data")]:::ext

    classDef db fill:#d3f9d8,stroke:#2b8a3e,color:#000
    classDef videos fill:#d0ebff,stroke:#1864ab,color:#000
    classDef ext fill:#fff3bf,stroke:#e67700,color:#000
```

## Functions

| Function | File | Belongs To | Description |
| --- | --- | --- | --- |
| `main` | main.go | | Starts the program by calling `run`. |
| `run` | main.go | | Sets everything up, serves requests, and shuts down cleanly. |
| `getenv` | main.go | | Reads a setting, or returns a default. |
| `connectDB` | db.go | | Opens the database pool and creates the `videos` table. |
| `pingWithRetry` | db.go | | Waits until Postgres is ready, trying up to 10 times. |
| `Create` | db.go | `videoStore` | Adds a video row. |
| `List` | db.go | `videoStore` | Returns every video, newest first. |
| `newServer` | videos.go | | Builds a `server` from a `videoStore` and the data folder. |
| `routes` | videos.go | `server` | Connects each URL to its handler. |
| `createVideo` | videos.go | `server` | Saves an upload to disk, then adds its row. |
| `listVideos` | videos.go | `server` | Sends every video as JSON. |
| `serveVideoFile` | videos.go | `server` | Sends a video file so the browser can play it. |
| `health` | videos.go | `server` | Replies `ok`. |
| `videoPath` | videos.go | `server` | Builds a file path, such as `/data/<id>.mp4`. |
| `readTitle` | videos.go | | Reads and trims the uploaded title. |
| `saveFile` | videos.go | | Copies the uploaded file to disk. |
| `uploadError` | videos.go | | Picks the error reply for a failed upload. |
| `internalError` | videos.go | | Logs an error and replies with a 500. |
| `writeJSON` | videos.go | | Sends data as JSON. |
