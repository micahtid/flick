# Chunk

Stores files on the storage nodes for the API and the worker. It cuts each file into chunks of up to 4 MB, sends every chunk to all three nodes, and joins the chunks back together when the file is read.

## Saving and Reading

![How a file is saved and read](diagrams/files.svg)

| Function | Description |
| --- | --- |
| `Store.Save()` | Cuts the file into chunks, sends them to the nodes, and records the chunk IDs in the `files` table. Saving to the same path again replaces the file. |
| `Store.Copy()` | Joins the file's chunks back together, in order. Returns `ErrNotFound` if there's no such file. |
| `Store.Delete()` | Removes the file from the `files` table. Its chunks stay on the nodes, since another file may use the same chunks. |
| `ID()` | Names a chunk after a SHA-256 fingerprint of its bytes. If the bytes are damaged, they no longer match the name. Chunks with the same bytes get the same name, so they're stored once. |

## The `files` Table

| Column | Description |
| --- | --- |
| `path` | The file's name, such as `uploads/<id>.mp4` or `hls/<id>/720p_003.ts`. |
| `chunks` | The chunk IDs, in order. |

## Special Cases

| Case | Description |
| --- | --- |
| One Node Is Down | A chunk counts as saved once 2 of the 3 nodes have it, so uploads keep working. Reading a chunk asks the other nodes instead. |
| Two Nodes Are Down | Saving fails, since a chunk with only one copy could easily be lost. |
| A Node Sends a Damaged Chunk | The bytes no longer match the chunk's name, so they're ignored and another node is asked. |

## Development

| Command | Description |
| --- | --- |
| `go doc -all .` | Shows every function and type with its documentation. |
| `go test ./...` | Runs the tests. They need no Docker. |

## Additional Information: How Files Become Chunks

Each file is split into its own chunks. A chunk holds up to 4 MB, so a file under 4 MB is stored as one chunk, and a larger file is split into several.

| File | Size | Chunks |
| --- | --- | --- |
| `hls/<id>/720p_000.ts` | 1 MB | One chunk. |
| `uploads/<id>.mp4` | 12 MB | Three chunks of 4 MB, in order. |

Segments are stored the same way as any other file. A segment is a 4-second video file that FFmpeg makes for playback. Most segments are under 4 MB, so each one is usually stored as one chunk.
