package main

// Tests for the chat handler in chat.go. They use a fake hub in place of
// Redis, so `go test` needs no Redis or Docker.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

// fakeHub stands in for Redis. It sends each published message to every
// subscriber of the channel, through memory.
type fakeHub struct {
	mu   sync.Mutex
	subs map[string][]chan []byte
}

func newFakeHub() *fakeHub {
	return &fakeHub{subs: map[string][]chan []byte{}}
}

func (h *fakeHub) Publish(_ context.Context, channel string, msg []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range h.subs[channel] {
		ch <- msg
	}
	return nil
}

// Subscribe gives each subscriber room for 10 messages, plenty for a test.
func (h *fakeHub) Subscribe(_ context.Context, channel string) (<-chan []byte, func(), error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan []byte, 10)
	h.subs[channel] = append(h.subs[channel], ch)
	stop := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		for i, sub := range h.subs[channel] {
			if sub == ch {
				h.subs[channel] = append(h.subs[channel][:i], h.subs[channel][i+1:]...)
				close(ch)
				return
			}
		}
	}
	return ch, stop, nil
}

// dialChat joins a stream's chat on a test server, as a viewer's browser
// would.
func dialChat(t *testing.T, ctx context.Context, srv *httptest.Server, id string) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/chat/" + id
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return conn
}

// A message from one viewer reaches every viewer of the same stream, with
// spaces trimmed, and a bad message is skipped. A viewer of another stream
// gets nothing.
func TestJoinChat(t *testing.T) {
	_, _, _, h := newTestServer()
	// httptest.NewServer runs a real HTTP server on a free local port, since a
	// WebSocket needs a real connection.
	srv := httptest.NewServer(h)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	id := uuid.NewString()
	ann, bob := dialChat(t, ctx, srv, id), dialChat(t, ctx, srv, id)
	other := dialChat(t, ctx, srv, uuid.NewString())

	for _, msg := range []string{`not json`, `{"name":"Ann","text":"   "}`, `{"name":" Ann ","text":" Hi! "}`} {
		if err := ann.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
			t.Fatal(err)
		}
	}

	want := `{"name":"Ann","text":"Hi!"}`
	for name, conn := range map[string]*websocket.Conn{"ann": ann, "bob": bob} {
		_, got, err := conn.Read(ctx)
		if err != nil || string(got) != want {
			t.Errorf("%s got %s, %v, want %s", name, got, err, want)
		}
	}

	// Wait briefly. Nothing should arrive for the other stream's viewer.
	shortCtx, shortCancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer shortCancel()
	if _, got, err := other.Read(shortCtx); err == nil {
		t.Errorf("other stream got %s, want nothing", got)
	}
}

// A stream ID that isn't a UUID replies 400 without switching to a WebSocket.
func TestJoinChatRejectsBadID(t *testing.T) {
	_, _, _, h := newTestServer()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/chat/not-a-uuid", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// cleanMessage trims the name and text, and rejects bad JSON, empty fields
// and fields over the limits.
func TestCleanMessage(t *testing.T) {
	long := strings.Repeat("é", maxMessageChars) // Each é is one character but two bytes.
	tests := []struct {
		name string
		in   string
		want string // This is empty if the message should be rejected.
	}{
		{"trimmed", `{"name":" Ann ","text":" Hi! ","extra":1}`, `{"name":"Ann","text":"Hi!"}`},
		{"longest text", `{"name":"Ann","text":"` + long + `"}`, `{"name":"Ann","text":"` + long + `"}`},
		{"text too long", `{"name":"Ann","text":"` + long + `é"}`, ""},
		{"name too long", `{"name":"` + strings.Repeat("a", maxNameChars+1) + `","text":"Hi"}`, ""},
		{"no name", `{"text":"Hi"}`, ""},
		{"blank text", `{"name":"Ann","text":"  "}`, ""},
		{"not json", `Hi`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := cleanMessage([]byte(tt.in))
			if ok != (tt.want != "") || string(got) != tt.want {
				t.Errorf("cleanMessage = %s, %v, want %q", got, ok, tt.want)
			}
		})
	}
}
