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

## The Parts In This Phase

| Part | Where | Job |
| --- | --- | --- |
| LRU | `cache/lru.go` (new) | Keeps the most recently used segments in memory, up to a limit in bytes. |
| Cache service | `cache/hls.go` (new), port 8082 | Sends segments from the LRU, and fetches any it doesn't have from the API. It also counts hits and misses. |
| Nginx | `nginx/nginx.conf` | Sends `/hls` requests to the cache instead of the API. |

## Step 1: Write The LRU

Put this in `cache/lru.go`. Go's `container/list` package gives you the linked list.

```go
type entry struct {
    key  string // the segment's path, such as <id>/720p_003.ts
    data []byte // the segment itself
}

type lru struct {
    mu    sync.Mutex
    max   int // the most bytes allowed
    used  int // the bytes in use now
    ll    *list.List
    items map[string]*list.Element
}

func (c *lru) Get(key string) ([]byte, bool) {
    c.mu.Lock(); defer c.mu.Unlock()
    if el, ok := c.items[key]; ok {
        c.ll.MoveToFront(el) // just used, so it's now the newest
        return el.Value.(*entry).data, true
    }
    return nil, false
}

func (c *lru) Put(key string, data []byte) {
    c.mu.Lock(); defer c.mu.Unlock()
    if len(data) > c.max { return } // it could never fit
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

Without the size check at the top of `Put`, a segment bigger than the whole cache would push everything else out, and then be removed itself.

**Write a test before going further.** Make a tiny cache, fill it, read one item, then add one more. Check that the item removed was the oldest one, not the one you just read.

## Step 2: Build The Cache Service

Put the handlers in `cache/hls.go`. The cache runs on port 8082, and has two endpoints:

| Method | Path | What it does |
| --- | --- | --- |
| `GET` | `/hls/{path...}` | Sends a segment from the LRU. On a miss, it fetches the segment from the API and keeps it. |
| `GET` | `/stats` | Replies with the hits, the misses and the hit rate. |

```go
func (s *server) getHLSFile(w http.ResponseWriter, r *http.Request) {
    key := r.PathValue("path")
    if path.Ext(key) != ".ts" { s.proxy.ServeHTTP(w, r); return } // not a segment, so pass it through

    data, ok := s.cache.Get(key)
    if ok {
        s.hits.Add(1)
    } else {
        s.misses.Add(1)
        var err error
        data, err = s.fetch(r.Context(), key) // GET http://api:8080/files/hls/<key>
        if err != nil { http.Error(w, "not found", 404); return }
        s.cache.Put(key, data)
    }
    w.Header().Set("Content-Type", "video/mp2t")
    w.Write(data)
}

func (s *server) stats(w http.ResponseWriter, r *http.Request) {
    h, m := s.hits.Load(), s.misses.Load()
    json.NewEncoder(w).Encode(map[string]any{
        "hits": h, "misses": m,
        "hit_rate": float64(h) / float64(max(h+m, 1)), // share of requests that were hits
    })
}
```

`hits` and `misses` are `atomic.Int64` fields on `server`. An `atomic.Int64` is a number that many requests can add to at the same time safely.

`fetch` is a small helper you write. It does an HTTP GET on the API and returns the body. Have it tell a `404` apart from other failures, so the cache can reply `404` for a missing segment and `502` (Bad Gateway) when the API itself fails. Never keep a failed reply.

Notes:

- **Only cache `.ts` segments.** A finished segment never changes, so a cached copy is always correct. Playlists are tiny, and in Phase 7 live playlists change every few seconds. A cached copy of those would go stale. Pass them straight through to the API with `httputil.ReverseProxy`, which also copies the API's `Content-Type` and status code.
- **Share one fetch between viewers.** When a popular video starts, many viewers miss the same segment at once, and each would fetch it. `singleflight.Group` from `golang.org/x/sync` lets only the first one fetch it, while the rest wait and share the result.
- **Make the size a setting.** Read the limit from an environment variable, `CACHE_MAX_MB`, so you can change it without rebuilding. Read where the API is from `API_URL`.

## Step 3: Send Video Requests Through The Cache

In `nginx/nginx.conf`, change the `/hls/` block so it goes to the cache instead of the API:

```nginx
location /hls/ {
    proxy_pass http://cache:8082/hls/;
}
```

## Step 4: Update Compose

- Remove `profiles: [full]` from `cache`, and add `depends_on: [api]`.
- Set `API_URL: http://api:8080` and `CACHE_MAX_MB: "500"` on `cache`.
- Add `cache` to Nginx's `depends_on`.

## Done When

- Watch a video, then check `curl localhost:8082/stats`. Watch the same video again and check once more. During the second viewing, almost every request is a hit.
- `/stats` reports the hit rate.
