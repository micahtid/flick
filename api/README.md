# API

## Startup

![How the API starts](diagrams/startup.svg)

## Types

![How the API's types connect](diagrams/types.svg)

## Requests

![How the API handles each request](diagrams/requests.svg)

## Functions

| Function | File | Belongs To | Description |
| --- | --- | --- | --- |
| `main` | main.go | | Starts the program by calling `run`. |
| `run` | main.go | | Sets everything up, serves requests, and shuts down cleanly. |
| `getenv` | main.go | | Reads a setting, or returns a default. |
| `connectDB` | db.go | | Opens the database pool and creates the `videos` table. |
| `pingWithRetry` | db.go | | Waits until Postgres is ready, trying up to 10 times. |
| `Create` | db.go | `videoStore` | Adds a video row with the status `processing`. |
| `Delete` | db.go | `videoStore` | Removes a video row, to undo an upload that couldn't be queued. |
| `List` | db.go | `videoStore` | Returns every video, newest first. |
| `newServer` | videos.go | | Builds a `server` from a `videoStore`, a `queueClient` and the data folder. |
| `routes` | videos.go | `server` | Connects each URL to its handler. |
| `createVideo` | videos.go | `server` | Saves an upload to disk, adds its row, then adds a job to convert it. |
| `listVideos` | videos.go | `server` | Sends every video as JSON. |
| `serveVideoFile` | videos.go | `server` | Sends the converted 720p file so the browser can play it. |
| `health` | videos.go | `server` | Replies `ok`. |
| `videoPath` | videos.go | `server` | Builds the upload's path, such as `/data/<id>.mp4`. |
| `convertedPath` | videos.go | `server` | Builds the 720p copy's path, such as `/data/<id>_720p.mp4`. |
| `readTitle` | videos.go | | Reads and trims the uploaded title. |
| `saveFile` | videos.go | | Copies the uploaded file to disk. |
| `uploadError` | videos.go | | Picks the error reply for a failed upload. |
| `internalError` | videos.go | | Logs an error and replies with a 500. |
| `writeJSON` | videos.go | | Sends data as JSON. |
| `Add` | queue.go | `queueClient` | Asks the queue to convert a video. |
