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
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// Upload limits.
const (
	maxUploadBytes = 2 << 30 // This is 2 GiB (2 × 2³⁰ bytes), the same limit as nginx.conf.
	maxTitleBytes  = 200
)

// videoRepo lists the database actions the handlers need. videoStore in db.go
// is the real one, and the tests use a fake that keeps videos in memory.
type videoRepo interface {
	Create(ctx context.Context, id uuid.UUID, title string) (Video, error)
	List(ctx context.Context) ([]Video, error)
}

// server holds what every handler needs: the database and the folder where
// video files live.
type server struct {
	videos  videoRepo
	dataDir string
}

func newServer(videos videoRepo, dataDir string) *server {
	return &server{videos: videos, dataDir: dataDir}
}

// routes connects each request type and path to its handler. {id} matches any
// value in that spot of the path.
func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("POST /videos", s.createVideo)
	mux.HandleFunc("GET /videos", s.listVideos)
	mux.HandleFunc("GET /videos/{id}/file", s.serveVideoFile)
	return mux
}

// health replies "ok". It's a quick way to check the API is running.
func (s *server) health(w http.ResponseWriter, _ *http.Request) {
	io.WriteString(w, "ok")
}

// createVideo handles an upload. Browsers send files as a "multipart form": a
// body split into parts, here a "title" part and a "file" part. The file is
// written to disk piece by piece as it arrives, so even a 2 GB upload uses
// little memory.
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
	path := s.videoPath(id)
	var title string
	var gotFile bool

	// If anything fails after the file is saved, delete it, so failed uploads
	// leave nothing behind. defer runs this when the function returns.
	committed := false
	defer func() {
		if gotFile && !committed {
			os.Remove(path)
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
				err = saveFile(path, part)
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

	// Add the database row. Only once this works is the file kept.
	video, err := s.videos.Create(r.Context(), id, title)
	if err != nil {
		internalError(w, "save video", err)
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

// serveVideoFile sends a video's file. http.ServeFile also handles "range
// requests", where the browser asks for just one slice of the file. That's
// what lets you jump to any point in the video.
func (s *server) serveVideoFile(w http.ResponseWriter, r *http.Request) {
	// Only accept a real UUID. This also blocks tricks like "../../etc/passwd"
	// that try to read files outside the data folder.
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid video id", http.StatusBadRequest)
		return
	}
	http.ServeFile(w, r, s.videoPath(id))
}

// videoPath is where a video's file is saved, such as /data/<id>.mp4.
func (s *server) videoPath(id uuid.UUID) string {
	return filepath.Join(s.dataDir, id.String()+".mp4")
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

// saveFile copies src into a new file at path, a small piece at a time.
func saveFile(path string, src io.Reader) error {
	out, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		return err
	}
	// Check Close's error too: it can be the first sign the data didn't reach
	// the disk.
	return out.Close()
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
