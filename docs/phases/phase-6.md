# Phase 6: Edge Cache

## The Problem

Every time someone watches a video, every segment is fetched from the storage nodes and rebuilt from chunks. If 1,000 people watch the same popular video, the same work is done 1,000 times.

## The Fix

Put a **cache** in front of storage. A cache is a fast store in memory that keeps copies of things people asked for recently.

- When a segment is asked for, look in the cache first.
- If it's there, that's a **hit**. Send it right away.
- If it isn't, that's a **miss**. Fetch it from storage, send it, and keep a copy for next time.

Real video sites put caches near the viewers, at the "edge" of the network, which is why it's called an **edge cache**.

## The LRU Rule

Memory is limited, so the cache must sometimes throw things out. **LRU** means "least recently used". When the cache is full, it removes the segment nobody's asked for in the longest time.

To do this quickly, the cache uses two things together:

- A **map** from the segment's name to its place in the list. This finds any segment in one step.
- A **doubly linked list**. The most recently used segment is at the front, and the least recently used is at the back. "Doubly linked" means each item points to the one before and the one after, so any item can be moved to the front in one step.

On every hit, move the segment to the front. When the cache is too full, remove from the back.

## Step 1: Write The LRU

Put this in `cache/`. Go's `container/list` package gives you the linked list.

```go
type entry struct {
    key  string // the segment's path
    data []byte // the segment itself
}

type LRU struct {
    mu    sync.Mutex
    max   int // the most bytes allowed
    used  int // the bytes in use now
    ll    *list.List
    items map[string]*list.Element
}

func (c *LRU) Get(key string) ([]byte, bool) {
    c.mu.Lock(); defer c.mu.Unlock()
    if el, ok := c.items[key]; ok {
        c.ll.MoveToFront(el) // just used, so it's now the newest
        return el.Value.(*entry).data, true
    }
    return nil, false
}

func (c *LRU) Put(key string, data []byte) {
    c.mu.Lock(); defer c.mu.Unlock()
    if el, ok := c.items[key]; ok { c.ll.MoveToFront(el); return } // already there
    c.items[key] = c.ll.PushFront(&entry{key, data})
    c.used += len(data)
    for c.used > c.max { // too full, so remove the oldest until it fits
        old := c.ll.Back()
        e := old.Value.(*entry)
        c.ll.Remove(old)
        delete(c.items, e.key)
        c.used -= len(e.data)
    }
}
```

The `mu` lock matters. Many viewers use the cache at the same time, and without the lock two of them could change the list at once and break it.

**Write a test before going further.** Make a tiny cache, fill it, read one item, then add one more. Check that the item removed was the oldest one, not the one you just read.

## Step 2: Build The Cache Service

The cache runs on port 8082. It has two endpoints:

```go
cache := &LRU{max: 500 << 20, ll: list.New(), items: map[string]*list.Element{}} // 500 MB
var hits, misses atomic.Int64

mux.HandleFunc("GET /hls/{path...}", func(w http.ResponseWriter, r *http.Request) {
    key := r.PathValue("path")
    data, ok := cache.Get(key)
    if ok {
        hits.Add(1)
    } else {
        misses.Add(1)
        var err error
        data, err = fetch("http://api:8080/files/hls/" + key) // ask storage
        if err != nil { http.Error(w, "not found", 404); return }
        cache.Put(key, data)
    }
    w.Header().Set("Content-Type", contentType(key))
    w.Write(data)
})

mux.HandleFunc("GET /stats", func(w http.ResponseWriter, r *http.Request) {
    h, m := hits.Load(), misses.Load()
    json.NewEncoder(w).Encode(map[string]any{
        "hits": h, "misses": m,
        "hit_rate": float64(h) / float64(max(h+m, 1)), // share of requests that were hits
    })
})
```

`fetch` and `contentType` are small helpers you write. `fetch` does an HTTP GET and returns the body. `contentType` returns the right type for `.m3u8` and `.ts` files, as in Phase 3.

`atomic.Int64` is a number that many requests can add to at the same time safely.

Notes:

- **Only cache `.ts` segments.** A finished segment never changes, so a cached copy is always correct. Playlists are tiny, and in Phase 7 live playlists change every few seconds. A cached copy of those would go stale. Pass playlist requests straight through without keeping them.
- **Make the size a setting.** Read the limit from an environment variable, `CACHE_MAX_MB`, so you can change it without rebuilding.

## Step 3: Send Video Requests Through The Cache

In `nginx/nginx.conf`, change the `/hls/` block so it goes to the cache instead of the API:

```nginx
location /hls/ {
    proxy_pass http://cache:8082/hls/;
}
```

In Compose, remove `profiles: [full]` from `cache` and add `depends_on: [api]`.

## Done When

- Watch a video, then check `curl localhost:8082/stats`. Watch the same video again and check once more. During the second viewing, almost every request is a hit.
- `/stats` reports the hit rate.
