package raster

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
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
	mu       sync.RWMutex
	sources  map[string]*bundle
	cacheDir string // parent dir for per-bundle unpack directories
	tracer   output.Tracer
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

	sourceID := deriveSourceID(path)

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
	if err := unzip(path, dir); err != nil {
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

// openLayer opens one COG and resolves its value mapping.
func (r *Repository) openLayer(dir string, spec layerSpec) (*rasterLayer, error) {
	cogPath, err := safeJoin(dir, spec.File)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(cogPath) //#nosec G304 -- path validated by safeJoin to stay within dir
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

	px, py := layer.cog.PixelFromPoint(orb.Point{coord.X, coord.Y}, 0)
	if px < 0 || py < 0 || px >= layer.cog.Width() || py >= layer.cog.Height() {
		// Outside the raster extent — no data, not an error.
		return nil, nil
	}

	rd, err := layer.cog.ReadWindow(gocog.Rectangle{X: px, Y: py, Width: 1, Height: 1})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "sample failed")
		return nil, &domain.QueryError{SourceID: sourceID, Layer: layerName, Err: err}
	}
	value := int64(rd.At(layer.band, 0, 0)) //#nosec G115 -- categorical pixel values fit int64

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
	}
}

// deriveSourceID extracts the source id from a bundle path (filename stem).
// It mirrors the registry's deriveSourceID, including the extension-only edge
// case (e.g. ".zip" → ".zip"), so dedup/unload stay consistent across adapters.
func deriveSourceID(path string) string {
	base := filepath.Base(path)
	if base == "" || base == "." {
		return ""
	}
	ext := filepath.Ext(base)
	if ext == "" || len(base) == len(ext) {
		return base
	}
	return strings.TrimSuffix(base, ext)
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
	maxBundleBytes   = 8 << 30  // 8 GiB total extracted per bundle
	maxManifestBytes = 16 << 20 // 16 MiB for the manifest itself
)

// unzip extracts a ZIP archive into dest, guarding against zip-slip and bounding
// the total extracted size.
func unzip(src, dest string) error {
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
		if total >= maxBundleBytes {
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
