package main

// The HTTP side of the API: the list of routes, one handler per endpoint, and
// helpers for reading uploads and sending replies.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/micahtid/flick/pkg/metrics"
)

// These limit the size of an upload.
const (
	maxUploadBytes = 2 << 30 // This is 2 GiB (2 × 2³⁰ bytes), the same limit as nginx.conf.
	maxTitleBytes  = 200
)

// videoRepo lists the database actions the handlers need. videoStore in db.go
// is the real one, and the tests use a fake that keeps videos in memory.
type videoRepo interface {
	Create(ctx context.Context, id uuid.UUID, title string) (Video, error)
	Delete(ctx context.Context, id uuid.UUID) error
	List(ctx context.Context) ([]Video, error)
	Stats(ctx context.Context) ([]VideoStats, error)
	AddCounts(ctx context.Context, counts []viewCount) error
}

// jobQueue lists the queue actions the handlers need. queueClient in queue.go
// is the real one, and the tests use a fake.
type jobQueue interface {
	Add(ctx context.Context, videoID uuid.UUID) error
}

// fileStore lists the file actions the handlers need. chunk.Store in
// pkg/chunk is the real one, which keeps files on the storage nodes. The tests
// use a fake that keeps files in memory.
type fileStore interface {
	Save(ctx context.Context, path string, r io.Reader) error
	Copy(ctx context.Context, w io.Writer, path string) error
	Delete(ctx context.Context, path string) error
}

// server holds what every handler needs: the database, the job queue, the
// file store, the chat hub, and the view counter.
type server struct {
	videos videoRepo
	jobs   jobQueue
	files  fileStore
	chat   chatHub
	views  viewCounter
}

func newServer(videos videoRepo, jobs jobQueue, files fileStore, chat chatHub, views viewCounter) *server {
	return &server{videos: videos, jobs: jobs, files: files, chat: chat, views: views}
}

// routes connects each request type and path to its handler. {path...}
// matches the rest of the path, slashes included. metrics.Wrap counts and
// times every request, and Prometheus reads the numbers from /metrics.
func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("POST /videos", s.createVideo)
	mux.HandleFunc("GET /videos", s.listVideos)
	mux.HandleFunc("GET /files/hls/{path...}", s.getHLSFile)
	mux.HandleFunc("GET /chat/{id}", s.joinChat)
	mux.HandleFunc("POST /videos/{id}/view", s.addView)
	mux.HandleFunc("POST /videos/{id}/watch", s.addWatch)
	mux.HandleFunc("GET /stats", s.getStats)
	mux.Handle("GET /metrics", metrics.Handler())
	return metrics.Wrap(mux)
}

// health replies "ok". It's a quick way to check the API is running.
func (s *server) health(w http.ResponseWriter, _ *http.Request) {
	io.WriteString(w, "ok")
}

// createVideo handles an upload. Browsers send files as a "multipart form": a
// body split into parts, here a "title" part and a "file" part. The file is
// sent to the storage nodes one chunk at a time as it arrives, so even a 2 GB
// upload uses little memory. Converting the file takes minutes, so that's
// left to a worker, and the reply is sent without waiting for it.
func (s *server) createVideo(w http.ResponseWriter, r *http.Request) {
	// Stop reading once the body passes the limit.
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	// Read the form one part at a time instead of all at once.
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "expected a multipart form", http.StatusBadRequest)
		return
	}

	// A random ID names both the database row and the file.
	id := uuid.New()
	path := uploadPath(id)
	var title string
	var gotFile bool

	// If anything fails after the file is saved, delete the file and the row,
	// so failed uploads leave nothing behind. defer runs this when the function
	// returns.
	var rowAdded, committed bool
	defer func() {
		if committed {
			return
		}
		// WithoutCancel keeps the deletes going even if the client has hung
		// up, which cancels r.Context().
		ctx := context.WithoutCancel(r.Context())
		if gotFile {
			if err := s.files.Delete(ctx, path); err != nil {
				log.Printf("remove file %s: %v", path, err)
			}
		}
		if rowAdded {
			if err := s.videos.Delete(ctx, id); err != nil {
				log.Printf("remove video %s: %v", id, err)
			}
		}
	}()

	// Go through the parts in order. Parts with other names are ignored.
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) { // EOF means "end of file", so there are no parts left.
			break
		}
		if err != nil {
			uploadError(w, err)
			return
		}

		switch part.FormName() {
		case "title":
			title, err = readTitle(part)
		case "file":
			if gotFile {
				err = badRequest{"only one file per upload"}
			} else {
				gotFile = true
				err = s.files.Save(r.Context(), path, part)
			}
		}
		part.Close()
		if err != nil {
			uploadError(w, err)
			return
		}
	}

	// Both parts are required.
	if title == "" {
		http.Error(w, "missing title", http.StatusBadRequest)
		return
	}
	if !gotFile {
		http.Error(w, "missing file", http.StatusBadRequest)
		return
	}

	// Add the database row, then a job to convert the file. Only once both
	// work is the upload kept. The row comes first so it already exists when a
	// worker finishes the job and marks it ready.
	video, err := s.videos.Create(r.Context(), id, title)
	if err != nil {
		internalError(w, "save video", err)
		return
	}
	rowAdded = true

	if err := s.jobs.Add(r.Context(), id); err != nil {
		// When the queue is full, ask the client to try again later. 503 means
		// "Service Unavailable".
		if errors.Is(err, errQueueFull) {
			http.Error(w, "busy, try again later", http.StatusServiceUnavailable)
		} else {
			internalError(w, "add job", err)
		}
		return
	}
	committed = true

	writeJSON(w, http.StatusCreated, video) // 201 means the video was created.
}

// listVideos sends every video as a JSON list, newest first.
func (s *server) listVideos(w http.ResponseWriter, r *http.Request) {
	videos, err := s.videos.List(r.Context())
	if err != nil {
		internalError(w, "list videos", err)
		return
	}
	writeJSON(w, http.StatusOK, videos)
}

// uploadPath names an uploaded file in the file store, such as
// uploads/<id>.mp4. The worker reads it by the same name.
func uploadPath(id uuid.UUID) string {
	return "uploads/" + id.String() + ".mp4"
}

// pathID reads the video ID from the path. If it isn't a UUID, it replies 400
// and returns false.
func pathID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "bad video ID", http.StatusBadRequest)
		return uuid.Nil, false
	}
	return id, true
}

// badRequest is an error caused by bad input from the client. uploadError
// replies 400 with its message rather than a generic 500.
type badRequest struct{ msg string }

func (e badRequest) Error() string { return e.msg }

// readTitle reads the title and trims spaces from both ends.
func readTitle(part *multipart.Part) (string, error) {
	// Read at most one byte over the limit. That's enough to tell the title is
	// too long without reading however much the client sends.
	raw, err := io.ReadAll(io.LimitReader(part, maxTitleBytes+1))
	if err != nil {
		return "", err
	}
	if len(raw) > maxTitleBytes {
		return "", badRequest{fmt.Sprintf("title must be at most %d bytes", maxTitleBytes)}
	}
	return strings.TrimSpace(string(raw)), nil
}

// uploadError picks the reply for a failed upload: 413 if it was too large,
// 400 for bad or cut-off input, and 500 for anything else.
func uploadError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	var bad badRequest
	// errors.As checks whether err is (or wraps) that type, and if so fills in
	// the variable. errors.Is does the same check for one specific error value.
	switch {
	case errors.As(err, &tooLarge):
		http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
	case errors.As(err, &bad):
		http.Error(w, bad.msg, http.StatusBadRequest)
	case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, multipart.ErrMessageTooLarge):
		http.Error(w, "malformed upload", http.StatusBadRequest)
	default:
		internalError(w, "read upload", err)
	}
}

// internalError logs the real error and sends the client a plain 500, so
// internal details don't leak out.
func internalError(w http.ResponseWriter, action string, err error) {
	log.Printf("%s: %v", action, err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

// writeJSON sends v as JSON with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	// Headers must be set before WriteHeader, which sends them.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}
