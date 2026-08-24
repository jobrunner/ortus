package input

import (
	"context"
	"sync"

	"github.com/jobrunner/ortus/internal/domain"
)

// PointInPolygonCache memoizes point-in-polygon results for the duration of one
// request.
//
// It exists because the gazetteer's sections are independent by design — each
// answers its own block and owns its own queries — and two of them need the same
// answer. Locate and Bearing both ask which admin polygons contain the query
// point, so every request ran that query twice. Measured on the committed request
// set, those two calls were the single largest cost in the response: 2 calls per
// request, ~2550 ms in total, more than the batched lineage walk.
//
// The cache lives here rather than inside the service because the adapter has to
// be able to open the scope (one per HTTP/MCP request) and adapters cannot import
// the application layer. Absent a scope, nothing is cached and every section
// queries as before, so wiring it is optional and a service used without an
// adapter behaves identically.
type PointInPolygonCache struct {
	mu sync.Mutex
	// Keyed by layer plus coordinate: a request asks about one point, but keying
	// on it too means a cache can never answer for the wrong location.
	entries map[pipKey][]domain.Feature
}

type pipKey struct {
	layer string
	at    domain.Coordinate
}

type pipCacheKey struct{}

// WithPointInPolygonCache returns a context carrying a fresh cache. Call it once
// per request, at the adapter boundary; the scope must not outlive the request,
// or a later request could be answered from a stale entry.
func WithPointInPolygonCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, pipCacheKey{}, &PointInPolygonCache{
		entries: map[pipKey][]domain.Feature{},
	})
}

// PointInPolygonCacheFrom returns the cache carried by ctx, or nil when none was
// opened. A nil receiver is safe on every method, so callers need no branch.
func PointInPolygonCacheFrom(ctx context.Context) *PointInPolygonCache {
	c, _ := ctx.Value(pipCacheKey{}).(*PointInPolygonCache)
	return c
}

// Get returns a memoized result. ok is false on a nil cache or a miss.
func (c *PointInPolygonCache) Get(layer string, at domain.Coordinate) ([]domain.Feature, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	f, ok := c.entries[pipKey{layer: layer, at: at}]
	return f, ok
}

// Put memoizes a result. A nil cache discards it.
//
// The slice is stored as given, not copied: the gazetteer's sections only read
// the features they get back. That is a deliberate trade — a copy per call would
// undo part of what the cache saves — and it means a caller must not mutate a
// slice it has handed over.
func (c *PointInPolygonCache) Put(layer string, at domain.Coordinate, features []domain.Feature) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[pipKey{layer: layer, at: at}] = features
}
