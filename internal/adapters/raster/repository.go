package raster

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/paulmach/orb"
	"github.com/tingold/gocog"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// Repository implements output.SpatialSource for raster bundles (*.zip).
type Repository struct {
	mu             sync.RWMutex
	sources        map[string]*bundle
	cacheDir       string // parent dir for per-bundle unpack directories
	tracer         output.Tracer
	tileCacheSize  int   // open-handle LRU bound for tiled layers (0 → default)
	maxBundleBytes int64 // per-bundle extraction cap; 0 → defaultMaxBundleBytes
}

type bundle struct {
	source *domain.Source
	dir    string // unpacked directory (removed on Close)
	layers map[string]*rasterLayer
}

type rasterLayer struct {
	cog     *gocog.COG
	file    *os.File
	band    int // 0-based band index into RasterData
	nodata  *float64
	mapping map[int64]map[string]interface{}

	// Continuous layers (value_type: continuous) return the sampled pixel value
	// directly as a float instead of looking it up in mapping. outputProp is the
	// Feature property name (default "value"); the returned value is
	// raw*scale + offset. dtype is cached from the COG so QueryPoint decodes the
	// sample correctly (float bands come back as IEEE bit patterns).
	continuous bool
	outputProp string
	scale      float64
	offset     float64
	dtype      gocog.DataType

	// tiles is set for a multi-tile continuous layer. When non-nil, cog/file are
	// nil and QueryPoint routes to the tile covering the point instead.
	tiles *tileset

	// readMu serializes reads on the single COG handle (gocog's reader is
	// stateful). Unused for tiled layers, which lock per-tile in the tileset.
	readMu sync.Mutex
}

// NewRepository creates a raster bundle repository. cacheDir is where bundle
// ZIPs are unpacked; "" uses the OS temp dir.
func NewRepository(cacheDir string) *Repository {
	return &Repository{
		sources:  make(map[string]*bundle),
		cacheDir: cacheDir,
		tracer:   output.NoOpTracer{},
	}
}

// SetTracer wires a tracer in. Pass output.NoOpTracer{} to disable.
func (r *Repository) SetTracer(t output.Tracer) {
	if t == nil {
		t = output.NoOpTracer{}
	}
	r.tracer = t
}

// SetTileCacheSize sets the open-handle LRU bound applied to tiled layers loaded
// afterwards. A value <= 0 leaves the default. Applies at layer-open time, so set
// it before sources load.
func (r *Repository) SetTileCacheSize(n int) { r.tileCacheSize = n }

// SetMaxBundleBytes sets the per-bundle extracted-size cap (a decompression-bomb
// guard). A value <= 0 leaves the default (8 GiB). Raise it for large trusted
// bundles such as continental DEM tile sets. Set before sources load.
func (r *Repository) SetMaxBundleBytes(n int64) { r.maxBundleBytes = n }

// bundleCap returns the effective per-bundle extraction cap.
func (r *Repository) bundleCap() int64 {
	if r.maxBundleBytes > 0 {
		return r.maxBundleBytes
	}
	return defaultMaxBundleBytes
}

// tempDirPrefix is the prefix of the per-bundle unpack directories. Kept in one
// place so CleanupOrphaned can find them.
const tempDirPrefix = "ortus-raster-"

// cacheRoot is where unpack dirs live (the OS temp dir when cacheDir is "").
func (r *Repository) cacheRoot() string {
	if r.cacheDir != "" {
		return r.cacheDir
	}
	return os.TempDir()
}

// CleanupOrphaned removes leftover unpack directories from a previous run that
// crashed before Close could remove them (Close is the only normal cleanup, so
// a SIGKILL/OOM/panic would otherwise leak them and eventually fill the disk).
// Call once at startup, before loading. NOTE: it removes ALL ortus-raster-*
// directories under the cache root, so do not point two instances at the same
// cacheDir.
func (r *Repository) CleanupOrphaned() (int, error) {
	matches, err := filepath.Glob(filepath.Join(r.cacheRoot(), tempDirPrefix+"*"))
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, m := range matches {
		// Lstat (not Stat) so a symlink matching the prefix is skipped rather
		// than followed — our unpack dirs are always real directories.
		info, statErr := os.Lstat(m)
		if statErr != nil || !info.IsDir() {
			continue
		}
		if err := os.RemoveAll(m); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// Supports reports whether this adapter handles the path. Raster bundles are
// ZIP archives.
func (r *Repository) Supports(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".zip")
}

// Prepare is a no-op: a raster source is ready as soon as it is opened.
func (r *Repository) Prepare(_ context.Context, _ string, _ string) error {
	return nil
}

// Open unpacks and validates a raster bundle and returns its domain.Source.
func (r *Repository) Open(ctx context.Context, path string) (*domain.Source, error) {
	_, span := r.tracer.Start(ctx, "raster.Open",
		output.WithAttributes(output.String("ortus.source.path", path)),
	)
	defer span.End()

	sourceID := domain.DeriveSourceID(path)

	r.mu.RLock()
	existing, ok := r.sources[sourceID]
	r.mu.RUnlock()
	if ok {
		span.AddEvent("already_open")
		return existing.source, nil
	}

	dir, err := os.MkdirTemp(r.cacheDir, tempDirPrefix+sourceID+"-")
	if err != nil {
		return nil, fmt.Errorf("creating unpack dir: %w", err)
	}

	b, err := r.openBundle(path, sourceID, dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		span.RecordError(err)
		span.SetStatus(output.StatusError, "open failed")
		return nil, err
	}

	r.mu.Lock()
	if winner, raced := r.sources[sourceID]; raced {
		// Lost a concurrent Open for the same source — discard our work so the
		// freshly-unpacked dir and COG handles don't leak.
		r.mu.Unlock()
		b.closeFiles()
		_ = os.RemoveAll(b.dir)
		span.AddEvent("lost_open_race")
		return winner.source, nil
	}
	r.sources[sourceID] = b
	r.mu.Unlock()

	span.SetAttributes(
		output.String("ortus.source.id", b.source.ID),
		output.Int("ortus.layers.count", len(b.source.Layers)),
	)
	return b.source, nil
}

// openBundle does the heavy lifting of Open without touching r.sources, so a
// failure leaves no partial registration behind.
func (r *Repository) openBundle(path, sourceID, dir string) (*bundle, error) {
	if err := unzip(path, dir, r.bundleCap()); err != nil {
		return nil, fmt.Errorf("unpacking bundle: %w", err)
	}

	rawManifest, err := readFileLimited(filepath.Join(dir, manifestName), maxManifestBytes)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", manifestName, err)
	}
	m, err := parseAndValidateManifest(rawManifest)
	if err != nil {
		return nil, err
	}

	// The bundle filename stem must equal the manifest id so the registry's
	// filename-based dedup (derive*ID) stays consistent.
	if m.ID != sourceID {
		return nil, fmt.Errorf("bundle filename stem %q does not match manifest id %q", sourceID, m.ID)
	}

	srid, err := parseEPSG(m.CRS)
	if err != nil {
		return nil, err
	}

	b := &bundle{
		dir:    dir,
		layers: make(map[string]*rasterLayer),
	}
	src := &domain.Source{
		ID:      m.ID,
		Name:    m.Name,
		Path:    path,
		Kind:    domain.SourceKindRaster,
		Indexed: true,
		License: domain.License{Name: m.License.Name, URL: m.License.URL, Attribution: m.License.Attribution},
		Metadata: domain.Metadata{
			Title:       m.Name,
			Description: m.Description,
		},
	}

	seen := make(map[string]bool, len(m.Layers))
	for i := range m.Layers {
		spec := m.Layers[i]
		if seen[spec.ID] {
			return nil, fmt.Errorf("duplicate layer id %q", spec.ID)
		}
		seen[spec.ID] = true

		if spec.Sampling != "" && spec.Sampling != "nearest" {
			return nil, fmt.Errorf("layer %q: sampling %q not supported (only nearest)", spec.ID, spec.Sampling)
		}

		rl, err := r.openLayer(dir, spec)
		if err != nil {
			b.closeFiles() // release any COGs already opened
			return nil, err
		}
		b.layers[spec.ID] = rl

		src.Layers = append(src.Layers, domain.Layer{
			Name:         spec.ID,
			Description:  spec.Description,
			GeometryType: string(domain.GeomRaster),
			SRID:         srid,
			HasIndex:     true,
		})
	}

	b.source = src
	return b, nil
}

// openCOGFile opens a COG whose path was produced by safeJoin (so it is confined
// to the bundle dir). One suppression covers both the single-COG and tiled paths.
func openCOGFile(path string) (*os.File, error) {
	return os.Open(path) //#nosec G304 -- path validated by safeJoin to stay within the bundle dir
}

// continuousParams resolves a continuous layer's output property name and linear
// scale/offset, applying the defaults (property "value", scale 1, offset 0).
func continuousParams(spec layerSpec) (outputProp string, scale, offset float64) {
	outputProp = spec.OutputProperty
	if outputProp == "" {
		outputProp = "value"
	}
	scale = 1.0
	if spec.Scale != nil {
		scale = *spec.Scale
	}
	offset = 0.0
	if spec.Offset != nil {
		offset = *spec.Offset
	}
	return outputProp, scale, offset
}

// openTiledLayer builds a multi-tile continuous layer: it records the tile set
// under the bundle's tile directory (no COGs opened yet; they are opened lazily
// through the LRU on first query).
func (r *Repository) openTiledLayer(dir string, spec layerSpec) (*rasterLayer, error) {
	if !spec.isContinuous() {
		return nil, fmt.Errorf("layer %q: tiles require value_type continuous", spec.ID)
	}
	band := spec.Band
	if band == 0 {
		band = 1
	}
	outputProp, scale, offset := continuousParams(spec)
	tileDir, err := safeJoin(dir, spec.Tiles.Dir)
	if err != nil {
		return nil, err
	}
	ts, err := newTileset(tileDir, *spec.Tiles, band-1, spec.Nodata, outputProp, scale, offset, r.tileCacheSize)
	if err != nil {
		return nil, fmt.Errorf("layer %q: %w", spec.ID, err)
	}
	return &rasterLayer{
		band:       band - 1,
		nodata:     spec.Nodata,
		continuous: true,
		outputProp: outputProp,
		scale:      scale,
		offset:     offset,
		tiles:      ts,
	}, nil
}

// openLayer opens one COG and resolves its value mapping.
func (r *Repository) openLayer(dir string, spec layerSpec) (*rasterLayer, error) {
	if spec.Tiles != nil {
		return r.openTiledLayer(dir, spec)
	}

	cogPath, err := safeJoin(dir, spec.File)
	if err != nil {
		return nil, err
	}
	f, err := openCOGFile(cogPath)
	if err != nil {
		return nil, fmt.Errorf("opening COG %q: %w", spec.File, err)
	}
	c, err := gocog.Read(f)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("reading COG %q: %w", spec.File, err)
	}

	band := spec.Band
	if band == 0 {
		band = 1
	}
	if band > c.BandCount() {
		_ = f.Close()
		return nil, fmt.Errorf("layer %q: band %d out of range (COG has %d band(s))", spec.ID, band, c.BandCount())
	}

	if spec.isContinuous() {
		if err := checkContinuous(c, fmt.Sprintf("layer %q", spec.ID)); err != nil {
			_ = f.Close()
			return nil, err
		}
		outputProp, scale, offset := continuousParams(spec)
		return &rasterLayer{
			cog:        c,
			file:       f,
			band:       band - 1,
			nodata:     spec.Nodata,
			continuous: true,
			outputProp: outputProp,
			scale:      scale,
			offset:     offset,
			dtype:      c.DataType(),
		}, nil
	}

	mapping, err := resolveMapping(spec, func(name string) ([]byte, error) {
		p, perr := safeJoin(dir, name)
		if perr != nil {
			return nil, perr
		}
		return readFileLimited(p, maxManifestBytes)
	})
	if err != nil {
		_ = f.Close()
		return nil, err
	}

	return &rasterLayer{
		cog:     c,
		file:    f,
		band:    band - 1,
		nodata:  spec.Nodata,
		mapping: mapping,
	}, nil
}

// checkContinuous validates that a COG is safe to sample as a continuous value:
// a numeric band, and NOT WhiteIsZero — gocog's decoder inverts WhiteIsZero
// (photometric 0) single-band data (value → max-value), which would silently
// corrupt elevations. `what` is a label for the error (e.g. `layer "elevation"`).
func checkContinuous(c *gocog.COG, what string) error {
	if !isNumericDataType(c.DataType()) {
		return fmt.Errorf("%s: value_type continuous requires a numeric COG band, got data type %d", what, c.DataType())
	}
	if c.PhotometricInterpretation() == 0 { // WhiteIsZero
		return fmt.Errorf("%s: WhiteIsZero photometric interpretation is not supported for continuous layers (would invert values)", what)
	}
	return nil
}

// isNumericDataType reports whether a COG data type can be sampled as a
// continuous numeric value. All integer and IEEE float types qualify; ASCII,
// undefined, and rational types do not.
func isNumericDataType(dt gocog.DataType) bool {
	switch dt {
	case gocog.DTByte, gocog.DTSByte,
		gocog.DTSShort, gocog.DTSShortS,
		gocog.DTSLong, gocog.DTSLongS,
		gocog.DTFloat, gocog.DTDouble:
		return true
	default:
		return false
	}
}

// sampleToFloat decodes a raw sample (as returned by RasterData.At) to a float64
// according to the band data type. Integer types come back as their true value;
// IEEE float types come back as bit patterns and must be reinterpreted. The bool
// is false for data types that cannot be sampled as a continuous value.
func sampleToFloat(dt gocog.DataType, raw uint64) (float64, bool) {
	// gocog packs the band value into the low bits of the uint64. Integer bands are
	// decoded by masking to the band width (and sign-extending signed types) so no
	// narrowing conversion is needed; float bands are IEEE bit patterns.
	switch dt {
	case gocog.DTFloat:
		return float64(math.Float32frombits(uint32(raw))), true //#nosec G115 -- low 32 bits are the float32 pattern
	case gocog.DTDouble:
		return math.Float64frombits(raw), true
	case gocog.DTByte:
		return uintSample(raw, 8), true
	case gocog.DTSShort:
		return uintSample(raw, 16), true
	case gocog.DTSLong:
		return uintSample(raw, 32), true
	case gocog.DTSByte:
		return intSample(raw, 8), true
	case gocog.DTSShortS:
		return intSample(raw, 16), true
	case gocog.DTSLongS:
		return intSample(raw, 32), true
	default:
		return 0, false
	}
}

// uintSample interprets the low `bits` of raw as an unsigned integer value.
func uintSample(raw uint64, bits uint) float64 {
	mask := uint64(1)<<bits - 1
	return float64(raw & mask)
}

// intSample interprets the low `bits` of raw as a two's-complement signed integer
// value, sign-extending via unsigned arithmetic (so no signed-narrowing cast).
func intSample(raw uint64, bits uint) float64 {
	mask := uint64(1)<<bits - 1
	v := raw & mask
	if v&(uint64(1)<<(bits-1)) != 0 { // sign bit set → negative
		return -float64((^v + 1) & mask)
	}
	return float64(v)
}

// QueryPoint samples the layer at the coordinate (nearest-neighbor) and maps
// the pixel value to attributes. The coordinate must already be in the layer's
// CRS (the query service transforms it beforehand).
func (r *Repository) QueryPoint(ctx context.Context, sourceID, layerName string, coord domain.Coordinate) ([]domain.Feature, error) {
	_, span := r.tracer.Start(ctx, "raster.QueryPoint",
		output.WithAttributes(
			output.String("ortus.source.id", sourceID),
			output.String("ortus.layer.name", layerName),
		),
	)
	defer span.End()

	r.mu.RLock()
	b, ok := r.sources[sourceID]
	r.mu.RUnlock()
	if !ok {
		return nil, domain.ErrSourceNotFound
	}
	layer, ok := b.layers[layerName]
	if !ok {
		return nil, domain.ErrLayerNotFound
	}

	// Multi-tile continuous layer: route to the tile covering the point.
	if layer.tiles != nil {
		return r.queryTiled(sourceID, layerName, coord, layer.tiles, span)
	}

	px, py := layer.cog.PixelFromPoint(orb.Point{coord.X, coord.Y}, 0)
	if px < 0 || py < 0 || px >= layer.cog.Width() || py >= layer.cog.Height() {
		// Outside the raster extent — no data, not an error.
		return nil, nil
	}

	// gocog's COG has a single stateful reader; serialize reads to it so
	// concurrent queries don't interleave Seek/Read on the same handle.
	layer.readMu.Lock()
	rd, err := layer.cog.ReadWindow(gocog.Rectangle{X: px, Y: py, Width: 1, Height: 1})
	layer.readMu.Unlock()
	if err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "sample failed")
		return nil, &domain.QueryError{SourceID: sourceID, Layer: layerName, Err: err}
	}
	raw := rd.At(layer.band, 0, 0)

	if layer.continuous {
		f, ok := sampleToFloat(layer.dtype, raw)
		if !ok {
			err := fmt.Errorf("continuous layer %q: unsupported data type %d", layerName, layer.dtype)
			span.RecordError(err)
			span.SetStatus(output.StatusError, "bad data type")
			return nil, &domain.QueryError{SourceID: sourceID, Layer: layerName, Err: err}
		}
		if layer.nodata != nil && f == *layer.nodata {
			return nil, nil // nodata sample — no match
		}
		return []domain.Feature{{
			LayerName:  layerName,
			Properties: map[string]interface{}{layer.outputProp: f*layer.scale + layer.offset},
		}}, nil
	}

	value := int64(raw) //#nosec G115 -- categorical pixel values fit int64

	if layer.nodata != nil && float64(value) == *layer.nodata {
		return nil, nil // nodata sample — no match
	}

	props, ok := layer.mapping[value]
	if !ok {
		err := fmt.Errorf("pixel value %d has no mapping entry (raster and legend disagree)", value)
		span.RecordError(err)
		span.SetStatus(output.StatusError, "unmapped value")
		return nil, &domain.QueryError{SourceID: sourceID, Layer: layerName, Err: err}
	}

	return []domain.Feature{{
		ID:         value,
		LayerName:  layerName,
		Properties: props,
	}}, nil
}

// queryTiled samples a multi-tile continuous layer: it routes the point to its
// grid cell, opens (via the LRU) the tile covering it, and decodes the sample. A
// point over a missing tile or outside the tile extent yields no feature (the
// ocean/no-coverage convention), not an error.
func (r *Repository) queryTiled(sourceID, layerName string, coord domain.Coordinate, ts *tileset, span output.Span) ([]domain.Feature, error) {
	latDeg, lonDeg := ts.cellFor(coord.X, coord.Y)
	name := tileFileName(ts.pattern, latDeg, lonDeg)
	if !ts.present(name) {
		return nil, nil // no tile → sea level / no coverage
	}
	key := [2]int{latDeg, lonDeg}
	ot, err := ts.acquire(key, name)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "tile open failed")
		return nil, &domain.QueryError{SourceID: sourceID, Layer: layerName, Err: err}
	}
	defer ts.release(key) // keep the tile pinned (uncloseable) until the read is done

	px, py := ot.cog.PixelFromPoint(orb.Point{coord.X, coord.Y}, 0)
	if px < 0 || py < 0 || px >= ot.cog.Width() || py >= ot.cog.Height() {
		return nil, nil // outside this tile's extent — no data
	}
	// Serialize reads on this tile's stateful reader.
	ot.mu.Lock()
	rd, err := ot.cog.ReadWindow(gocog.Rectangle{X: px, Y: py, Width: 1, Height: 1})
	ot.mu.Unlock()
	if err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "sample failed")
		return nil, &domain.QueryError{SourceID: sourceID, Layer: layerName, Err: err}
	}
	f, ok := sampleToFloat(ot.dtype, rd.At(ts.band, 0, 0))
	if !ok {
		err := fmt.Errorf("continuous tile layer %q: unsupported data type %d", layerName, ot.dtype)
		span.RecordError(err)
		span.SetStatus(output.StatusError, "bad data type")
		return nil, &domain.QueryError{SourceID: sourceID, Layer: layerName, Err: err}
	}
	if ts.nodata != nil && f == *ts.nodata {
		return nil, nil // nodata sample — no match
	}
	return []domain.Feature{{
		LayerName:  layerName,
		Properties: map[string]interface{}{ts.outputProp: f*ts.scale + ts.offset},
	}}, nil
}

// Close releases the bundle's COG handles and removes its unpacked directory.
func (r *Repository) Close(ctx context.Context, sourceID string) error {
	_, span := r.tracer.Start(ctx, "raster.Close",
		output.WithAttributes(output.String("ortus.source.id", sourceID)),
	)
	defer span.End()

	r.mu.Lock()
	b, ok := r.sources[sourceID]
	if ok {
		delete(r.sources, sourceID)
	}
	r.mu.Unlock()
	if !ok {
		return nil
	}

	b.closeFiles()
	if b.dir != "" {
		if err := os.RemoveAll(b.dir); err != nil {
			span.RecordError(err)
		}
	}
	return nil
}

func (b *bundle) closeFiles() {
	for _, l := range b.layers {
		if l.file != nil {
			_ = l.file.Close()
		}
		if l.tiles != nil {
			l.tiles.close() // release the tile LRU's open handles
		}
	}
}

// parseEPSG parses an "EPSG:<n>" CRS string into its numeric SRID.
func parseEPSG(crs string) (int, error) {
	const prefix = "EPSG:"
	if !strings.HasPrefix(crs, prefix) {
		return 0, fmt.Errorf("unsupported CRS %q (expected EPSG:<code>)", crs)
	}
	n, err := strconv.Atoi(strings.TrimPrefix(crs, prefix))
	if err != nil {
		return 0, fmt.Errorf("invalid EPSG code in CRS %q: %w", crs, err)
	}
	return n, nil
}

// safeJoin joins rel onto base, rejecting absolute paths and traversal that
// would escape base (zip-slip / path-traversal guard).
func safeJoin(base, rel string) (string, error) {
	if rel == "" {
		return "", errors.New("empty relative path")
	}
	clean := filepath.Clean(rel)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("illegal path %q escapes bundle", rel)
	}
	joined := filepath.Join(base, clean)
	if joined != base && !strings.HasPrefix(joined, base+string(os.PathSeparator)) {
		return "", fmt.Errorf("illegal path %q escapes bundle", rel)
	}
	return joined, nil
}

// Extraction bounds — defense-in-depth against decompression bombs. Bundles
// come from trusted storage, but a corrupt/hostile archive must not exhaust the
// host's disk.
const (
	defaultMaxBundleBytes = 8 << 30  // 8 GiB total extracted per bundle (default)
	maxManifestBytes      = 16 << 20 // 16 MiB for the manifest itself
)

// unzip extracts a ZIP archive into dest, guarding against zip-slip and bounding
// the total extracted size.
func unzip(src, dest string, maxBundleBytes int64) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer func() { _ = zr.Close() }()

	var total int64
	for _, f := range zr.File {
		target, err := safeJoin(dest, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o750); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		n, err := extractFile(f, target, maxBundleBytes-total)
		if err != nil {
			return err
		}
		total += n
		if total > maxBundleBytes {
			return fmt.Errorf("bundle exceeds maximum extracted size of %d bytes", maxBundleBytes)
		}
	}
	return nil
}

// extractFile writes one ZIP entry to target, copying at most limit bytes, and
// returns the number of bytes written.
func extractFile(f *zip.File, target string, limit int64) (int64, error) {
	rc, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer func() { _ = rc.Close() }()

	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //#nosec G304 -- target validated by safeJoin
	if err != nil {
		return 0, err
	}
	defer func() { _ = out.Close() }()

	n, err := io.CopyN(out, rc, limit+1) // +1 so an over-limit entry is detectable
	if err != nil && !errors.Is(err, io.EOF) {
		return n, err
	}
	if n > limit {
		// Fail loudly rather than silently truncate the entry.
		return n, fmt.Errorf("bundle entry %q exceeds the extraction size limit", f.Name)
	}
	return n, nil
}

// readFileLimited reads up to max bytes from path, erroring if the file is
// larger (guards against an oversized manifest / sidecar exhausting memory).
func readFileLimited(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path) //#nosec G304 -- path validated by safeJoin / fixed name under our unpack dir
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file %s exceeds maximum size of %d bytes", filepath.Base(path), limit)
	}
	return data, nil
}
