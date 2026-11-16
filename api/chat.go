package main

// This file runs live chat. Each viewer keeps a WebSocket open to
// /chat/<id>. A WebSocket is a connection that stays open, so either side can
// send a message at any time. Redis passes each message to every copy of the
// API, and each copy sends it on to its own viewers. Messages aren't saved, so
// a viewer only sees ones sent after they joined.

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/redis/go-redis/v9"
)

// These limit the size of a chat message. The website's chat box has the
// same limits.
const (
	maxNameChars    = 50
	maxMessageChars = 500
	maxMessageBytes = 4 << 10 // This is 4 KiB, enough for the longest name and text written as JSON.
)

// chatConnections is how many viewers are connected to chat right now.
// Prometheus reads it from /metrics.
var chatConnections = promauto.NewGauge(prometheus.GaugeOpts{
	Name: "chat_connections",
	Help: "Viewers connected to chat right now.",
})

// writeTimeout is how long a viewer gets to take each message. A viewer who
// falls further behind is dropped, so they can't hold up the others.
const writeTimeout = 5 * time.Second

// chatHub lists the chat actions the handlers need. redisHub is the real one,
// and the tests use a fake that passes messages through memory.
type chatHub interface {
	// Publish sends msg to everyone subscribed to channel.
	Publish(ctx context.Context, channel string, msg []byte) error
	// Subscribe starts receiving channel's messages on msgs. stop ends it,
	// which closes msgs.
	Subscribe(ctx context.Context, channel string) (msgs <-chan []byte, stop func(), err error)
}

// chatMessage is one chat message, such as {"name":"Guest 4821","text":"Hi!"}.
type chatMessage struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

// joinChat connects a viewer to a stream's chat. Messages from Redis go out
// to the viewer, and messages from the viewer go into Redis, until the viewer
// leaves.
func (s *server) joinChat(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	channel := "chat:" + id.String() // Each stream has its own channel.

	// Subscribe before switching to a WebSocket, so a failure can still get a
	// normal error reply.
	msgs, stop, err := s.chat.Subscribe(r.Context(), channel)
	if err != nil {
		internalError(w, "subscribe to "+channel, err)
		return
	}
	defer stop()

	// Accept turns the request into a WebSocket. If it fails, it has already
	// replied with the error.
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	// A message over the limit closes the connection. The website never sends
	// one that big.
	conn.SetReadLimit(maxMessageBytes)
	chatConnections.Inc()
	defer chatConnections.Dec()

	// ctx is cancelled when either direction stops, which stops the other.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Messages from Redis go out to this viewer, in the background.
	go func() {
		defer cancel()
		for msg := range msgs {
			if err := writeMessage(ctx, conn, msg); err != nil {
				return
			}
		}
	}()

	// Messages from this viewer go into Redis.
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return // The viewer left.
		}
		msg, ok := cleanMessage(data)
		if !ok {
			continue // Skip a bad message, but keep the viewer.
		}
		if err := s.chat.Publish(ctx, channel, msg); err != nil {
			log.Printf("publish to %s: %v", channel, err)
			return
		}
	}
}

// writeMessage sends one message to a viewer, giving up after writeTimeout.
func writeMessage(ctx context.Context, conn *websocket.Conn, msg []byte) error {
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return conn.Write(ctx, websocket.MessageText, msg)
}

// cleanMessage reads a message from a viewer and trims spaces from both ends
// of its name and text. It returns the message as JSON again, or false if
// it isn't JSON or its name or text is empty or too long. Writing it out again
// drops anything else the viewer sent.
func cleanMessage(data []byte) ([]byte, bool) {
	var m chatMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, false
	}
	m.Name, m.Text = strings.TrimSpace(m.Name), strings.TrimSpace(m.Text)
	if !fits(m.Name, maxNameChars) || !fits(m.Text, maxMessageChars) {
		return nil, false
	}
	out, err := json.Marshal(m)
	return out, err == nil
}

// fits reports whether text is not empty and at most limit characters long.
func fits(text string, limit int) bool {
	return text != "" && utf8.RuneCountInString(text) <= limit
}

// redisHub passes chat messages through Redis pub/sub ("publish and
// subscribe"). Each copy of the API subscribes to a stream's channel for each
// of its viewers there, and Redis sends every message published to that
// channel to every subscriber.
type redisHub struct {
	rdb *redis.Client
}

// Publish publishes msg to channel in Redis, which sends it to every copy of
// the API subscribed to it.
func (h *redisHub) Publish(ctx context.Context, channel string, msg []byte) error {
	return h.rdb.Publish(ctx, channel, msg).Err()
}

// Subscribe subscribes to channel in Redis and passes on each message until
// stop is called.
func (h *redisHub) Subscribe(ctx context.Context, channel string) (<-chan []byte, func(), error) {
	sub := h.rdb.Subscribe(ctx, channel)
	// Receive waits until Redis confirms the subscription, so no message sent
	// after Subscribe returns is missed.
	if _, err := sub.Receive(ctx); err != nil {
		sub.Close()
		return nil, nil, err
	}

	// Pass each message on in the background until stop is called. done lets
	// stop end this even while it waits to hand over a message.
	msgs := make(chan []byte)
	done := make(chan struct{})
	go func() {
		defer close(msgs)
		for m := range sub.Channel() {
			select {
			case msgs <- []byte(m.Payload):
			case <-done:
				return
			}
		}
	}()
	stop := func() {
		close(done)
		sub.Close()
	}
	return msgs, stop, nil
}
