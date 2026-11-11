package main

// Tests for the handlers in videos.go. They use a fake in-memory database and
// a temporary folder, so `go test` needs no Postgres or Docker.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeRepo stands in for the database. Set createErr to make Create fail.
type fakeRepo struct {
	videos    []Video
	createErr error
}

// Create puts the new video first, matching the real newest-first order.
func (f *fakeRepo) Create(_ context.Context, id uuid.UUID, title string) (Video, error) {
	if f.createErr != nil {
		return Video{}, f.createErr
	}
	v := Video{ID: id, Title: title, Status: "ready", CreatedAt: time.Now()}
	f.videos = append([]Video{v}, f.videos...)
	return v, nil
}

// List returns a copy, so tests can't change the fake's data by accident.
func (f *fakeRepo) List(context.Context) ([]Video, error) {
	return append([]Video{}, f.videos...), nil
}

// formPart is one field of a test form. "file" is sent as a file, and any
// other name is sent as text.
type formPart struct{ name, value string }

// multipartBody builds an upload form the way a browser would. It returns the
// body and the Content-Type header, which names the boundary between parts.
func multipartBody(t *testing.T, parts ...formPart) (*bytes.Buffer, string) {
	t.Helper() // Failures are reported at the caller's line, not in here.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, p := range parts {
		if p.name != "file" {
			if err := mw.WriteField(p.name, p.value); err != nil {
				t.Fatal(err)
			}
			continue
		}
		fw, err := mw.CreateFormFile("file", "clip.mp4")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(fw, p.value); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, mw.FormDataContentType()
}

// newTestServer makes a server with an empty fake database and an empty
// folder. t.TempDir is deleted automatically when the test ends.
func newTestServer(t *testing.T) (*server, *fakeRepo, http.Handler) {
	t.Helper()
	repo := &fakeRepo{}
	s := newServer(repo, t.TempDir())
	return s, repo, s.routes()
}

// upload sends POST /videos straight to the handler, with no real network.
// httptest.NewRecorder captures the reply so the test can check it.
func upload(h http.Handler, body *bytes.Buffer, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/videos", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// dirEntries counts files in dir, to check failed uploads left none behind.
func dirEntries(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

// A good upload replies 201, saves the file, and stores the trimmed title.
func TestCreateVideo(t *testing.T) {
	s, repo, h := newTestServer(t)
	body, ct := multipartBody(t, formPart{"title", "  My clip  "}, formPart{"file", "fake video bytes"})

	rec := upload(h, body, ct)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body)
	}

	var got Video
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Title != "My clip" {
		t.Errorf("title = %q, want trimmed %q", got.Title, "My clip")
	}
	if len(repo.videos) != 1 {
		t.Fatalf("repo has %d videos, want 1", len(repo.videos))
	}

	data, err := os.ReadFile(filepath.Join(s.dataDir, got.ID.String()+".mp4"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "fake video bytes" {
		t.Errorf("saved file = %q", data)
	}
}

// Each bad form replies 400 and leaves no row or file behind. This is a
// "table test": one list of cases, each run as its own subtest by t.Run.
func TestCreateVideoRejectsBadInput(t *testing.T) {
	tests := []struct {
		name  string
		parts []formPart
	}{
		{"missing title", []formPart{{"file", "x"}}},
		{"blank title", []formPart{{"title", "   "}, {"file", "x"}}},
		{"missing file", []formPart{{"title", "t"}}},
		{"title too long", []formPart{{"title", strings.Repeat("a", maxTitleBytes+1)}, {"file", "x"}}},
		{"two files", []formPart{{"title", "t"}, {"file", "x"}, {"file", "y"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, repo, h := newTestServer(t)
			body, ct := multipartBody(t, tt.parts...)

			rec := upload(h, body, ct)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
			if len(repo.videos) != 0 {
				t.Errorf("repo has %d videos, want 0", len(repo.videos))
			}
			if n := dirEntries(t, s.dataDir); n != 0 {
				t.Errorf("data dir has %d files, want 0", n)
			}
		})
	}
}

// A body that isn't a form at all replies 400.
func TestCreateVideoRejectsNonMultipart(t *testing.T) {
	_, _, h := newTestServer(t)
	rec := upload(h, bytes.NewBufferString("{}"), "application/json")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// If the database fails after the file is saved, the file is deleted.
func TestCreateVideoRemovesFileWhenDatabaseFails(t *testing.T) {
	s, repo, h := newTestServer(t)
	repo.createErr = errors.New("db down")
	body, ct := multipartBody(t, formPart{"title", "t"}, formPart{"file", "x"})

	rec := upload(h, body, ct)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if n := dirEntries(t, s.dataDir); n != 0 {
		t.Errorf("data dir has %d files, want 0", n)
	}
}

// An empty list is [] (not null), and videos come newest first.
func TestListVideos(t *testing.T) {
	_, repo, h := newTestServer(t)

	get := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/videos", nil))
		return rec
	}

	if rec := get(); strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("empty list body = %q, want []", rec.Body)
	}

	repo.Create(context.Background(), uuid.New(), "first")
	repo.Create(context.Background(), uuid.New(), "second")

	var got []Video
	if err := json.NewDecoder(get().Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Title != "second" {
		t.Errorf("got %+v, want newest first", got)
	}
}

// File serving handles range requests (used for seeking), sends the video
// type, and replies 400 for a bad ID and 404 for a missing file.
func TestServeVideoFile(t *testing.T) {
	s, _, h := newTestServer(t)
	id := uuid.New()
	if err := os.WriteFile(s.videoPath(id), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("range request", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/videos/"+id.String()+"/file", nil)
		req.Header.Set("Range", "bytes=2-4") // This asks for bytes 2 to 4 only, which are "234".
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		// 206 Partial Content means only the requested slice was sent.
		if rec.Code != http.StatusPartialContent || rec.Body.String() != "234" {
			t.Errorf("status = %d, body = %q, want 206 %q", rec.Code, rec.Body, "234")
		}
		if ct := rec.Header().Get("Content-Type"); ct != "video/mp4" {
			t.Errorf("Content-Type = %q, want video/mp4", ct)
		}
	})

	for path, want := range map[string]int{
		"/videos/not-a-uuid/file":               http.StatusBadRequest,
		"/videos/" + uuid.NewString() + "/file": http.StatusNotFound,
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want {
			t.Errorf("GET %s: status = %d, want %d", path, rec.Code, want)
		}
	}
}
