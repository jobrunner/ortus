package raster

import (
	"container/list"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"

	"github.com/tingold/gocog"
)

// tileset routes a query point to one COG of a degree-grid tile set and keeps a
// bounded LRU of open handles (a full DEM has far too many tiles to hold open at
// once). It is used only by continuous layers; the decode parameters are carried
// here so a sampled tile is turned into meters the same way the single-COG path
// does.
type tileset struct {
	dir     string
	pattern string
	gridDeg int

	// files is the set of tile basenames present in dir, captured at Open so the
	// hot path never stats the filesystem: a point whose derived filename is not
	// in this set is "no data" (ocean / no coverage).
	files map[string]bool

	// Layer-level continuous decode parameters (same for every tile).
	band       int
	nodata     *float64
	outputProp string
	scale      float64
	offset     float64

	// LRU of open tile handles, keyed by grid cell.
	mu    sync.Mutex
	cap   int
	ll    *list.List // front = most-recently-used; Value is *lruEntry
	byKey map[[2]int]*list.Element
}

// openTile is one open tile COG plus its file handle and cached data type. mu
// serializes reads: gocog's COG holds a single stateful io.ReadSeeker (Seek then
// ReadFull), so concurrent reads on the same tile would interleave and corrupt.
type openTile struct {
	cog   *gocog.COG
	file  *os.File
	dtype gocog.DataType
	mu    sync.Mutex
}

type lruEntry struct {
	key  [2]int
	tile *openTile
	refs int // in-flight readers; eviction never closes an entry with refs>0
}

// defaultTileCacheSize bounds the open-handle LRU when the config leaves it zero.
const defaultTileCacheSize = 64

// newTileset builds a tileset for a tiles layer. It reads dir once to record the
// present tile basenames. cacheSize <= 0 falls back to the default.
func newTileset(dir string, spec tilesSpec, band int, nodata *float64, outputProp string, scale, offset float64, cacheSize int) (*tileset, error) {
	grid := spec.GridDeg
	if grid <= 0 {
		grid = 1
	}
	if cacheSize <= 0 {
		cacheSize = defaultTileCacheSize
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading tile directory %q: %w", spec.Dir, err)
	}
	files := make(map[string]bool, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			files[e.Name()] = true
		}
	}
	return &tileset{
		dir:        dir,
		pattern:    spec.Pattern,
		gridDeg:    grid,
		files:      files,
		band:       band,
		nodata:     nodata,
		outputProp: outputProp,
		scale:      scale,
		offset:     offset,
		cap:        cacheSize,
		ll:         list.New(),
		byKey:      make(map[[2]int]*list.Element),
	}, nil
}

// cellFor returns the SW-corner grid cell (lat, lon integer degrees) containing
// the coordinate, snapped to the grid spacing.
func (t *tileset) cellFor(lon, lat float64) (latDeg, lonDeg int) {
	g := float64(t.gridDeg)
	latDeg = int(math.Floor(lat/g)) * t.gridDeg
	lonDeg = int(math.Floor(lon/g)) * t.gridDeg
	return latDeg, lonDeg
}

// tileFileName renders the filename for a cell from the pattern. Tokens: {ns},
// {ew}, {lat} (abs latitude, 2-digit zero-padded), {lon} (abs longitude, 3-digit
// zero-padded).
func tileFileName(pattern string, latDeg, lonDeg int) string {
	ns, alat := "N", latDeg
	if latDeg < 0 {
		ns, alat = "S", -latDeg
	}
	ew, alon := "E", lonDeg
	if lonDeg < 0 {
		ew, alon = "W", -lonDeg
	}
	r := strings.NewReplacer(
		"{ns}", ns,
		"{ew}", ew,
		"{lat}", fmt.Sprintf("%02d", alat),
		"{lon}", fmt.Sprintf("%03d", alon),
	)
	return r.Replace(pattern)
}

// present reports whether a tile file exists for the given cell.
func (t *tileset) present(name string) bool { return t.files[name] }

// acquire returns the open handle for a cell, opening (and LRU-caching) it on a
// miss, and pins it (refs++) so eviction cannot close it mid-read. The caller
// MUST call release(key) when done reading. The caller holds no lock.
func (t *tileset) acquire(key [2]int, name string) (*openTile, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if el, ok := t.byKey[key]; ok {
		t.ll.MoveToFront(el)
		e := el.Value.(*lruEntry)
		e.refs++
		return e.tile, nil
	}

	ot, err := t.openTile(name)
	if err != nil {
		return nil, err
	}
	el := t.ll.PushFront(&lruEntry{key: key, tile: ot, refs: 1})
	t.byKey[key] = el
	t.evictLocked()
	return ot, nil
}

// release drops one reference taken by acquire, letting the tile become evictable.
func (t *tileset) release(key [2]int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if el, ok := t.byKey[key]; ok {
		if e := el.Value.(*lruEntry); e.refs > 0 {
			e.refs--
		}
	}
}

// openTile opens one tile COG and validates it is a numeric band (continuous).
func (t *tileset) openTile(name string) (*openTile, error) {
	cogPath, err := safeJoin(t.dir, name)
	if err != nil {
		return nil, err
	}
	f, err := openCOGFile(cogPath)
	if err != nil {
		return nil, fmt.Errorf("opening tile %q: %w", name, err)
	}
	c, err := gocog.Read(f)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("reading tile %q: %w", name, err)
	}
	if err := checkContinuous(c, fmt.Sprintf("tile %q", name)); err != nil {
		_ = f.Close()
		return nil, err
	}
	if t.band >= c.BandCount() {
		_ = f.Close()
		return nil, fmt.Errorf("tile %q: band %d out of range (COG has %d band(s))", name, t.band+1, c.BandCount())
	}
	return &openTile{cog: c, file: f, dtype: c.DataType()}, nil
}

// evictLocked closes least-recently-used, NOT-in-use handles until the cache is
// within cap (or only in-use handles remain — the cache may briefly exceed cap
// under heavy concurrency, bounded by the number of in-flight readers). Scans
// oldest-first and skips pinned (refs>0) entries so a handle is never closed
// while a reader holds it. Caller must hold t.mu.
func (t *tileset) evictLocked() {
	for el := t.ll.Back(); el != nil && t.ll.Len() > t.cap; {
		prev := el.Prev()
		ent := el.Value.(*lruEntry)
		if ent.refs == 0 {
			t.ll.Remove(el)
			delete(t.byKey, ent.key)
			_ = ent.tile.file.Close()
		}
		el = prev
	}
}

// close releases every open tile handle. Safe to call once at bundle teardown.
func (t *tileset) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for el := t.ll.Front(); el != nil; el = el.Next() {
		_ = el.Value.(*lruEntry).tile.file.Close()
	}
	t.ll.Init()
	t.byKey = make(map[[2]int]*list.Element)
}
