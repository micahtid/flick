package main

// This file holds the LRU (least recently used) cache. It keeps the most
// recently used segments in memory, up to a limit in bytes. When it's full, it
// removes the segment that has gone unused the longest.

import (
	"container/list"
	"sync"
)

// lru is the cache of segments, capped at max bytes.
type lru struct {
	mu    sync.Mutex               // This guards ll and items, since many viewers use the cache at once.
	max   int                      // This is the most bytes allowed.
	used  int                      // This is the bytes in use now.
	ll    *list.List               // This lists the segments, most recently used first.
	items map[string]*list.Element // This maps each segment's path to its element in ll.
}

// entry is one segment in the list. key is its path, such as
// <id>/720p_003.ts.
type entry struct {
	key  string
	data []byte
}

// newLRU makes an empty LRU that holds up to max bytes.
func newLRU(max int) *lru {
	return &lru{max: max, ll: list.New(), items: map[string]*list.Element{}}
}

// Get returns a segment and marks it as just used. ok is false if the cache
// doesn't have it.
func (c *lru) Get(key string) (data []byte, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	c.ll.MoveToFront(el)
	return el.Value.(*entry).data, true
}

// Put adds a segment and marks it as just used. If that puts the cache over
// its limit, it removes the least recently used segments until it fits. A
// segment bigger than the whole cache is never kept.
func (c *lru) Put(key string, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(data) > c.max {
		return
	}
	// A segment never changes, so the copy already kept is the same.
	if el, ok := c.items[key]; ok {
		c.ll.MoveToFront(el)
		return
	}

	c.items[key] = c.ll.PushFront(&entry{key: key, data: data})
	c.used += len(data)
	for c.used > c.max {
		oldest := c.ll.Back()
		e := oldest.Value.(*entry)
		c.ll.Remove(oldest)
		delete(c.items, e.key)
		c.used -= len(e.data)
	}
}
