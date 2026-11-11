# Phase 1: Plain Video Site

## What You're Building

A simple website. You pick a video file, upload it, and watch it in the browser. That's all.

Nothing is split across many computers yet. This phase sets up the basic parts that every later phase builds on.

## The Parts

| Part | What it is | Where |
| --- | --- | --- |
| Website | The page you see. Built with React. | `web/` |
| Nginx | A web server. It sends the website to the browser. It passes every request that starts with `/api` to the API. | `nginx/` |
| API | A Go program. It saves uploads and answers questions like "which videos exist?" | `api/` |
| Postgres | A database. It remembers each video's ID, title and upload time. | Runs from a ready-made image |
| Docker Compose | Starts all of the parts above with one command. | `docker-compose.yml` |

**Already done:** Compose already starts Postgres, the API and Nginx. The API has one endpoint, `/health`, which replies `ok`. Nginx is already set up to send `/api/...` to the API. It removes the `/api` part first, so `/api/videos` reaches the API as `/videos`.

## Step 1: Connect The API To The Database

The API needs a library to talk to Postgres. From the `api/` folder, run:

```sh
go get github.com/jackc/pgx/v5/pgxpool github.com/google/uuid
```

`pgxpool` talks to Postgres. `uuid` makes random IDs, such as `3f2a9c1e-...`, so two videos never get the same ID.

When the API starts, it connects and creates the `videos` table if the table isn't there yet:

```go
const schema = `
CREATE TABLE IF NOT EXISTS videos (
  id         UUID PRIMARY KEY,
  title      TEXT NOT NULL,
  status     TEXT NOT NULL DEFAULT 'ready',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`

db, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
// check err
_, err = db.Exec(ctx, schema)
```

Each row in the table is one video. `status` doesn't matter yet. Phase 2 uses it.

**A common problem:** Docker often starts the API before Postgres is ready, so the first connection fails. To fix this, call `db.Ping` in a loop. Try up to 10 times, and wait 1 second between tries.

## Step 2: Add The Endpoints

An endpoint is a web address that the API answers. You need three:

| Method | Path | What it does |
| --- | --- | --- |
| `POST` | `/videos` | Receives an uploaded file, saves it, and adds a row to the table |
| `GET` | `/videos` | Returns every video as a list, newest first |
| `GET` | `/videos/{id}/file` | Sends back the video file itself |

**Upload.** The file goes into `/data`. Compose links that folder to a Docker volume, which is storage that survives restarts. Copy the file straight to disk as it arrives. Never load the whole file into memory first, because a 2 GB video would use 2 GB of RAM.

```go
mux.HandleFunc("POST /videos", func(w http.ResponseWriter, r *http.Request) {
    file, _, err := r.FormFile("file") // the uploaded file
    if err != nil { http.Error(w, "missing file", 400); return }
    defer file.Close()

    id := uuid.NewString()
    out, _ := os.Create(filepath.Join("/data", id+".mp4"))
    defer out.Close()
    io.Copy(out, file) // copies a bit at a time, never all at once

    title := r.FormValue("title")
    db.Exec(r.Context(), `INSERT INTO videos (id, title) VALUES ($1, $2)`, id, title)
    json.NewEncoder(w).Encode(map[string]string{"id": id, "title": title})
})
```

The snippets in these guides skip some error checks to stay short. Your real code should check every error.

**List.** Run `SELECT id, title, status, created_at FROM videos ORDER BY created_at DESC` and send the rows back as JSON.

**Send the file.** Go's `http.ServeFile` does the hard work. It also lets the browser ask for one part of the file at a time, which is how dragging the progress bar works.

```go
mux.HandleFunc("GET /videos/{id}/file", func(w http.ResponseWriter, r *http.Request) {
    id := r.PathValue("id")
    if _, err := uuid.Parse(id); err != nil { http.Error(w, "bad id", 400); return }
    http.ServeFile(w, r, filepath.Join("/data", id+".mp4"))
})
```

The `uuid.Parse` check is for safety. Without it, someone could ask for `../../etc/passwd` and read other files on the server.

## Step 3: Build The Website

Edit `web/src/App.tsx`. It needs two things.

1. **An upload form.** It has a box for the title and a file picker. When the user submits, put both into a `FormData` and send it:

   ```ts
   const body = new FormData()
   body.append('title', title)
   body.append('file', file)
   await fetch('/api/videos', { method: 'POST', body })
   ```

   After the upload finishes, load the list again so the new video shows up.

2. **A video list.** Load `GET /api/videos` when the page opens. For each video, show its title and a player:

   ```tsx
   <video controls width={640} src={`/api/videos/${v.id}/file`} />
   ```

To work on the website without Docker, run `npm run dev` inside `web/`. Vite is the tool that runs the website while you code. It's already set to pass `/api` requests to the API on port 8080.

## Step 4: Run It

From the project folder:

```sh
docker compose up --build
```

Open http://localhost.

## Done When

- You can upload an MP4, see it in the list, and play it.
- Run `docker compose down`, then `docker compose up`. The videos are still there.

Don't use `docker compose down -v`. The `-v` deletes the volumes, and your videos with them.
