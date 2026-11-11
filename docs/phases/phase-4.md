# Phase 4: Your Own Storage

## The Problem

Every file lives in one Docker volume, `uploads`. If that disk breaks, every video is gone. Big sites never keep only one copy of anything.

## The Fix

Build your own storage system that keeps **three copies** of everything, on three separate **nodes**. A node is one storage server. Then any one node can die and nothing is lost.

The words to know:

- **Chunk.** Big files are hard to move around, so you cut each file into blocks of 4 MB. Each block is a chunk.
- **Checksum.** A short fingerprint of the data. If even one byte changes, the fingerprint changes. Checking it on every read catches damaged data.
- **SHA-256.** The method used to make the fingerprint. Go has it built in.

## The Parts In This Phase

| Part | Where | Job |
| --- | --- | --- |
| Storage node | `storage/`, ports 9001 to 9003 | Saves and returns chunks. You run three copies: `node1`, `node2` and `node3`. |
| Chunk package | `pkg/chunk/` (new) | Shared Go code that the API and the workers use. It cuts files into chunks, sends them to the nodes and puts them back together. |
| `files` table | Postgres | Remembers which chunks make up each file |

## Step 1: Build The Storage Node

The node has three endpoints:

| Method | Path | What it does |
| --- | --- | --- |
| `PUT` | `/chunks/{id}` | Saves the request body as a file named `{id}` in `/chunks` |
| `GET` | `/chunks/{id}` | Reads the chunk, checks it isn't damaged, and sends it back |
| `GET` | `/health` | Replies `ok`. Other parts use it to see if the node is alive. |

**A useful trick:** use the chunk's SHA-256 fingerprint as its ID. Then the ID is also the checksum, so there's nothing extra to store. And if two chunks have the same bytes, they get the same ID, so they're only stored once.

```go
mux.HandleFunc("GET /chunks/{id}", func(w http.ResponseWriter, r *http.Request) {
    id := r.PathValue("id")
    data, err := os.ReadFile(filepath.Join("/chunks", id))
    if err != nil { http.NotFound(w, r); return }

    sum := sha256.Sum256(data)
    if hex.EncodeToString(sum[:]) != id {
        http.Error(w, "corrupt chunk", 500) // never send damaged data
        return
    }
    w.Write(data)
})
```

For `PUT`, do two things:

- Work out the fingerprint of the body. If it doesn't match the ID in the address, reject the request.
- Save the chunk to a temporary file first, such as `{id}.tmp`, then rename it to `{id}` with `os.Rename`. A rename happens all at once. So if the node crashes mid-save, you're left with a stray `.tmp` file, never half a chunk.

## Step 2: Make The Shared Chunk Package

The API and the workers both need the same chunk code, so put it in one shared place.

Right now each service in this project is its own Go **module**, which is a separate Go project with its own `go.mod`. The shared package has to be one too:

```sh
mkdir -p pkg/chunk && cd pkg/chunk
go mod init github.com/micahtid/flick/pkg/chunk
cd ../.. && go work use ./pkg/chunk
```

Then connect it to `api` and `worker`:

1. In each one's `go.mod`, add:

   ```
   require github.com/micahtid/flick/pkg/chunk v0.0.0
   replace github.com/micahtid/flick/pkg/chunk => ../pkg/chunk
   ```

   `replace` tells Go "this package is in a folder nearby, not on the internet".

2. Docker can only copy files from inside its **build context**, which is the folder it builds from. Right now that folder is `api/`, so Docker can't see `pkg/`. In Compose, change the build to use the project folder:

   ```yaml
   api:
     build: { context: ., dockerfile: api/Dockerfile }
   ```

   Then update the Dockerfile to copy both `api/` and `pkg/`. Do the same for `worker`.

The package has two main functions:

```go
const Size = 4 << 20 // 4 MB

// Put cuts r into chunks, sends each chunk to the nodes, and returns the chunk IDs in order.
func Put(r io.Reader) ([]string, error) {
    var ids []string
    buf := make([]byte, Size)
    for {
        n, err := io.ReadFull(r, buf) // read up to 4 MB
        if n > 0 {
            sum := sha256.Sum256(buf[:n])
            id := hex.EncodeToString(sum[:])
            if err := writeAll(id, buf[:n]); err != nil { return nil, err }
            ids = append(ids, id)
        }
        if err == io.EOF || err == io.ErrUnexpectedEOF { return ids, nil } // end of file
        if err != nil { return nil, err }
    }
}

// Get writes the chunks to w in order. For each chunk, it tries the nodes until one answers.
func Get(w io.Writer, ids []string) error
```

`writeAll` sends the chunk to all 3 nodes at the same time. It counts as a success if **at least 2** of them save it. Waiting for all 3 would mean one dead node stops every upload.

`Get` asks the nodes one at a time and uses the first good answer.

The package learns the node addresses from an environment variable, `STORAGE_NODES`.

## Step 3: Remember Which Chunks Make Each File

Add a table that maps each file to its list of chunks:

```sql
CREATE TABLE IF NOT EXISTS files (
  path   TEXT PRIMARY KEY,  -- for example hls/<video_id>/720p_003.ts
  chunks TEXT[] NOT NULL    -- the chunk IDs, in order
);
```

## Step 4: Switch Everything Over

Stop using the `/data` folder:

- **API upload.** Pass the upload straight into `chunk.Put`. Save the result in `files` with the path `uploads/{id}.mp4`.
- **Worker.** FFmpeg needs real files on disk, so the worker:
  1. Rebuilds the upload into a temporary folder with `chunk.Get`.
  2. Runs FFmpeg there, as before.
  3. Sends every output file through `chunk.Put` and adds a `files` row for each one.
  4. Deletes the temporary folder.
- **Playing videos.** Add `GET /files/{path...}` to the API. It looks up the file's chunks, sets the right `Content-Type` for the file ending (`.m3u8` or `.ts`), and streams the chunks back with `chunk.Get`. In `nginx.conf`, change the `/hls/` block to `proxy_pass http://api:8080/files/hls/;`. Remove the `/data` volume from Nginx.

## Step 5: Update Compose

- Give each node its own volume: `node1data:/chunks`, `node2data:/chunks` and `node3data:/chunks`. Separate volumes act like separate disks.
- Pass `STORAGE_NODES=http://node1:9001,http://node2:9002,http://node3:9003` to the API and the workers.
- Remove the `uploads` volume. Nothing uses it now.
- Remove `profiles: [full]` from the nodes.

## Done When

- Run `docker compose stop node2`. Every video still plays, and uploads still work.
- Upload a video while node2 is stopped, then start node2 again. Node2 never received that video's chunks, and nothing will ever fill them in. If node1 dies later, those chunks now have only one copy. Write this problem down. Phase 5 fixes it.
