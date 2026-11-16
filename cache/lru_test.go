package main

// This file tests the LRU cache in lru.go.

import (
	"strings"
	"testing"
)

// has reports whether the cache holds each key. It reads through c.items, not
// Get, so checking doesn't change the order.
func has(c *lru, keys ...string) map[string]bool {
	got := map[string]bool{}
	for _, k := range keys {
		_, got[k] = c.items[k]
	}
	return got
}

// When the cache is full, the segment used longest ago is removed, not the
// one just read.
func TestLRURemovesOldest(t *testing.T) {
	c := newLRU(3)
	c.Put("a", []byte("1"))
	c.Put("b", []byte("2"))
	c.Put("c", []byte("3"))
	c.Get("a") // Now b is the oldest.
	c.Put("d", []byte("4"))

	want := map[string]bool{"a": true, "b": false, "c": true, "d": true}
	for k, ok := range has(c, "a", "b", "c", "d") {
		if ok != want[k] {
			t.Errorf("has %s = %v, want %v", k, ok, want[k])
		}
	}
	if c.used != 3 {
		t.Errorf("used = %d, want 3", c.used)
	}
}

// The limit is in bytes, so one big segment can push out several small ones.
func TestLRUCountsBytes(t *testing.T) {
	c := newLRU(10)
	c.Put("a", []byte("123"))
	c.Put("b", []byte("456"))
	c.Put("c", []byte("12345678"))

	want := map[string]bool{"a": false, "b": false, "c": true}
	for k, ok := range has(c, "a", "b", "c") {
		if ok != want[k] {
			t.Errorf("has %s = %v, want %v", k, ok, want[k])
		}
	}
	if c.used != 8 {
		t.Errorf("used = %d, want 8", c.used)
	}
}

// A segment bigger than the whole cache isn't kept, and doesn't push anything
// out.
func TestLRUSkipsTooBig(t *testing.T) {
	c := newLRU(4)
	c.Put("a", []byte("1"))
	c.Put("big", []byte(strings.Repeat("x", 5)))

	if _, ok := c.Get("big"); ok {
		t.Error("kept a segment bigger than the cache")
	}
	if _, ok := c.Get("a"); !ok {
		t.Error("removed a to make room for a segment that doesn't fit")
	}
}

// Putting the same segment twice keeps one copy and counts its bytes once.
func TestLRUPutTwice(t *testing.T) {
	c := newLRU(10)
	c.Put("a", []byte("123"))
	c.Put("a", []byte("123"))

	if c.ll.Len() != 1 || c.used != 3 {
		t.Errorf("len = %d, used = %d, want 1 and 3", c.ll.Len(), c.used)
	}
}
