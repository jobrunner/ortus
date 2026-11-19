package raster

import (
	"container/list"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"

	"github.com/paulmach/orb"
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

	// Decoded-window cache (guarded by mu). gocog re-reads and LZW-decodes the whole
	// covering COG block on every ReadWindow, so sampling N nearby pixels one-by-one
	// decodes the block N times. A gazetteer request samples the elevation pixel plus
	// a 3×3 exposure window (~10 pixels within ±1–2 px at 30 m spacing); reading one
	// small window and serving neighbors from it collapses those to a single decode,
	// and keeps repeated same-area requests warm across requests. reads counts actual
	// ReadWindow calls (cache misses) — asserted by tests.
	winRect gocog.Rectangle
	winData *gocog.RasterData
	winOK   bool
	reads   int
}

// winRadius sizes the cached read window (2r+1 per side). 4 → a 9×9 window, which
// covers a gazetteer request's elevation pixel plus its 3×3 exposure neighbors
// while staying far below gocog's overview-downsample threshold, so cached values
// are read at full resolution (overview 0) and are bit-identical to a 1×1 read.
const winRadius = 4

// clampPixel clamps a pixel coordinate to the valid range [0, hi].
func clampPixel(v, hi int) int {
	if v < 0 {
		return 0
	}
	if v > hi {
		return hi
	}
	return v
}

// readPixel decodes an exact 1×1 window at (px,py) — the fallback taken when a cached
// window can't be trusted. Counts as one ReadWindow decode.
func (ot *openTile) readPixel(band, px, py int) (uint64, error) {
	rd, err := ot.cog.ReadWindow(gocog.Rectangle{X: px, Y: py, Width: 1, Height: 1})
	if err != nil {
		return 0, err
	}
	ot.reads++
	return rd.At(band, 0, 0), nil
}

// sampleAt returns the raw band sample at pixel (px,py), reusing the cached decoded
// window when the pixel falls inside it, otherwise decoding a small window around
// the pixel once and caching it. Only the exact requested pixel is ever returned,
// so this is a pure decode-cost optimization with no effect on results. The caller
// MUST hold ot.mu (it already serializes reads on the stateful gocog reader).
func (ot *openTile) sampleAt(band, px, py int) (uint64, error) {
	if ot.winOK &&
		px >= ot.winRect.X && px < ot.winRect.X+ot.winRect.Width &&
		py >= ot.winRect.Y && py < ot.winRect.Y+ot.winRect.Height {
		return ot.winData.At(band, px-ot.winRect.X, py-ot.winRect.Y), nil
	}
	w, h := ot.cog.Width(), ot.cog.Height()
	rect := gocog.Rectangle{X: clampPixel(px-winRadius, w), Y: clampPixel(py-winRadius, h)}
	rect.Width = clampPixel(px+winRadius+1, w) - rect.X
	rect.Height = clampPixel(py+winRadius+1, h) - rect.Y
	rd, err := ot.cog.ReadWindow(rect)
	if err != nil {
		return 0, err
	}
	ot.reads++
	// Defensive: ReadWindow picks a downsampled overview when the window is a tiny
	// fraction of the image — impossible for a 9×9 window on a real DEM tile, but a
	// hypothetical tiny/overviewed tile could return dims != the request, and then
	// indexing with full-res offsets would silently read the wrong pixel. In that case
	// skip the window cache and fall back to an exact single-pixel read (pre-cache behavior).
	if rd.Width != rect.Width || rd.Height != rect.Height {
		return ot.readPixel(band, px, py)
	}
	ot.winRect, ot.winData, ot.winOK = rect, rd, true
	return rd.At(band, px-rect.X, py-rect.Y), nil
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
// the coordinate, snapped to the grid spacing. It is locate's first guess: exact
// for area-registered tiles, while point-registered ones may hand the point on
// to a neighbor.
//
// The two axes close on opposite edges because they must agree with gocog's
// PixelFromPoint: it counts columns east from the west edge and rows south from
// the north edge, so a tile owns its west and north edges but not its east and
// south ones. Longitude therefore floors, while latitude ceils minus one cell: a
// point on a whole-degree parallel is the top row of the cell below, and flooring
// it into the cell above would read one row past that tile's last.
func (t *tileset) cellFor(lon, lat float64) (latDeg, lonDeg int) {
	g := float64(t.gridDeg)
	latDeg = (int(math.Ceil(lat/g)) - 1) * t.gridDeg
	lonDeg = int(math.Floor(lon/g)) * t.gridDeg
	return latDeg, lonDeg
}

// tileHit is a point resolved to the acquired tile that holds it; the caller
// must release(key) once done with tile.
type tileHit struct {
	key    [2]int
	tile   *openTile
	px, py int
}

// locate finds the tile holding (lon, lat) and the pixel within it.
//
// The grid cell alone is not enough: a PixelIsPoint tile (Copernicus DEM) puts
// its outer pixel centers ON the cell edges, so its footprint is shifted half a
// pixel and a point in the rim along one edge belongs to the neighboring tile.
// locate therefore tries the cell's tile first and, if the point falls outside
// it, the neighbors — the one in the direction it fell out first, since that is
// almost always the answer. The rest still matter: where adjacent tiles differ in
// longitude spacing (Copernicus widens it at 50°/60°/70° N) a coarse tile's rim
// ends short of the finer one's, so a point can leave the coarse tile to the
// south-east and yet belong to the tile straight south. When the cell has no
// tile, there is no direction and all neighbors are tried. Misses are rare and
// sit on tile edges, so the extra probes cost nothing on the common path.
// found=false means no present tile holds the point.
func (t *tileset) locate(lon, lat float64) (hit tileHit, found bool, err error) {
	latDeg, lonDeg := t.cellFor(lon, lat)
	hit, found, dir, err := t.probe(latDeg, lonDeg, lon, lat)
	if err != nil || found {
		return hit, found, err
	}
	for _, d := range neighborOrder(dir) {
		nLonDeg, qLon := wrapLon(lonDeg+d[1]*t.gridDeg, lon)
		hit, found, _, err = t.probe(latDeg+d[0]*t.gridDeg, nLonDeg, qLon, lat)
		if err != nil || found {
			return hit, found, err
		}
	}
	return tileHit{}, false, nil
}

// neighborOrder lists the eight adjacent cell steps ({dLat, dLon}), first one
// first unless it is zero.
func neighborOrder(first [2]int) [][2]int {
	steps := make([][2]int, 0, 8)
	if first != ([2]int{}) {
		steps = append(steps, first)
	}
	for dLat := -1; dLat <= 1; dLat++ {
		for dLon := -1; dLon <= 1; dLon++ {
			if d := [2]int{dLat, dLon}; d != ([2]int{}) && d != first {
				steps = append(steps, d)
			}
		}
	}
	return steps
}

// wrapLon folds a neighbor cell's longitude into [-180, 180) across the
// antimeridian and turns the query longitude by the same 360°, so E179's east
// rim continues into W180 (whose rim reaches below -180) rather than a
// nonexistent E180.
func wrapLon(lonDeg int, lon float64) (cellLon int, queryLon float64) {
	switch {
	case lonDeg >= 180:
		return lonDeg - 360, lon - 360
	case lonDeg < -180:
		return lonDeg + 360, lon + 360
	}
	return lonDeg, lon
}

// probe checks whether the tile of one cell holds (lon, lat). On a hit the tile
// stays acquired. On a miss against a present tile, dir is the cell step
// ({dLat, dLon}, each -1/0/+1) toward the side the point fell out; a zero dir
// means the cell has no tile.
func (t *tileset) probe(latDeg, lonDeg int, lon, lat float64) (hit tileHit, found bool, dir [2]int, err error) {
	name := tileFileName(t.pattern, latDeg, lonDeg)
	if !t.present(name) {
		return tileHit{}, false, [2]int{}, nil
	}
	key := [2]int{latDeg, lonDeg}
	ot, err := t.acquire(key, name)
	if err != nil {
		return tileHit{}, false, [2]int{}, err
	}
	px, py := ot.cog.PixelFromPoint(orb.Point{lon, lat}, 0)
	w, h := ot.cog.Width(), ot.cog.Height()
	if px >= 0 && py >= 0 && px < w && py < h {
		return tileHit{key: key, tile: ot, px: px, py: py}, true, [2]int{}, nil
	}
	t.release(key)
	// Rows grow southward, so falling out below the last row steps one cell south.
	switch {
	case py >= h:
		dir[0] = -1
	case py < 0:
		dir[0] = 1
	}
	switch {
	case px >= w:
		dir[1] = 1
	case px < 0:
		dir[1] = -1
	}
	return tileHit{}, false, dir, nil
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
