# Cache

Keeps recently watched segments in memory, so a popular video's segments are fetched from the API once instead of once per viewer. When it's full, it evicts the least recently used segment.

## Startup

![How the cache starts](diagrams/startup.svg)

## Requests

![How the cache handles each request](diagrams/requests.svg)

| Request | Reply |
| --- | --- |
| `GET /hls/<id>/<name>.ts` | `200` with the segment, from memory on a hit or from the API on a miss. `404` if there's no such segment, and `502` if the API fails. |
| `GET /hls/<id>/<file>` | Any other file, such as a playlist, passes straight through to the API, which sends the reply. |
| `GET /stats` | `200` with the hits, the misses and the hit rate, such as `{"hit_rate":0.95,"hits":95,"misses":5}`. Only segments are counted. |

## Special Cases

| Case | Description |
| --- | --- |
| A Playlist Is Asked For | It's never kept. Playlists are tiny, and in Phase 7 live playlists change every few seconds, so a kept copy would go stale. |
| Many Viewers Miss the Same Segment at Once | Only the first one fetches it from the API. The rest wait and share the result. |
| A Segment Is Missing | The cache replies `404` and keeps nothing, so a video still processing can be fetched again once it's ready. |
| The Cache Is Full | It evicts the least recently used segments until the new one fits. A segment bigger than the whole cache is sent but never kept. |
| The Cache Restarts | Memory is lost, so it starts empty. Every segment is a miss once, then a hit again. |

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `PORT` | `8082` | The port the cache listens on. |
| `API_URL` | `http://api:8080` | Where the API is. |
| `CACHE_MAX_MB` | `500` | The most segments to keep, in MB. Change it in `docker-compose.yml` and restart the cache. No rebuild is needed. |

## Development

| Command | Description |
| --- | --- |
| `go doc -cmd -u -all .` | Shows every function and type with its documentation. |
| `go test ./...` | Runs the tests. They use a fake API, so they need no Docker. |

## Additional Information: The LRU

The cache is a standard LRU: a hash map for lookups, and a doubly linked list that keeps segments in order of use. Every operation is O(1).

| Part | Description |
| --- | --- |
| The List | A doubly linked list (`ll` in `lru.go`), from Go's `container/list`. It runs from most recently used at the front to least recently used at the back. |
| The Map | A Go map from each segment's path to its element in the list (`items` in `lru.go`). |
| On a Hit | `Get()` moves the segment to the front. |
| On a Miss | `Put()` adds the segment at the front. If the cache is now over its limit, it evicts from the back until it fits. |
| The Lock | A mutex (`mu`) guards the list and the map, since many viewers use the cache at once. |
