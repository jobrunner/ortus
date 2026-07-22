package app

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/jobrunner/ortus/internal/adapters/raster"
	"github.com/jobrunner/ortus/internal/application"
	"github.com/jobrunner/ortus/internal/application/gazetteer"
	"github.com/jobrunner/ortus/internal/config"
	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// noopIndex is a do-nothing output.SpatialIndex so tests can build a
// *gazetteer.Service without a real (CGO/SpatiaLite) GeoPackage — the elevation
// and exposure paths only touch the elevation sampler, never the index.
type noopIndex struct{}

func (noopIndex) QueryKNN(context.Context, string, domain.Coordinate, int, float64, *output.Filter) ([]output.NearFeature, error) {
	return nil, nil
}
func (noopIndex) PointInPolygon(context.Context, string, domain.Coordinate) ([]domain.Feature, error) {
	return nil, nil
}
func (noopIndex) ResolveChain(context.Context, string, int64, output.AdminColumns) ([]output.AdminRow, error) {
	return nil, nil
}
func (noopIndex) DistanceKM(context.Context, domain.Coordinate, domain.Coordinate) (float64, error) {
	return 0, nil
}
func (noopIndex) Azimuth(context.Context, domain.Coordinate, domain.Coordinate) (float64, error) {
	return 0, nil
}

// elevBundleManifest declares a single continuous layer "elevation" over the
// shared raster fixture COG (Byte 100 west / 200 east / 0 nodata), so a sample
// returns the pixel value directly as meters.
const elevBundleManifest = `
schema_version: 1
id: %s
name: Test DEM
license:
  name: CC0-1.0
crs: EPSG:4326
layers:
  - id: elevation
    file: regions.cog.tif
    band: 1
    nodata: 0
    value_type: continuous
    output_property: meters
`

// buildElevBundle writes a gazetteer-owned DEM bundle (<id>.zip) into a temp dir
// and returns its path, reusing the raster package's fixture COG.
func buildElevBundle(t *testing.T, id string) string {
	t.Helper()
	cog, err := os.ReadFile(filepath.Join("..", "adapters", "raster", "testdata", "regions.cog.tif"))
	if err != nil {
		t.Fatalf("read fixture COG: %v", err)
	}
	zipPath := filepath.Join(t.TempDir(), id+".zip")
	zf, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zf.Close() }()
	zw := zip.NewWriter(zf)
	write := func(name string, data []byte) {
		w, werr := zw.Create(name)
		if werr != nil {
			t.Fatal(werr)
		}
		if _, werr := w.Write(data); werr != nil {
			t.Fatal(werr)
		}
	}
	// %s → id, so the manifest id matches the zip filename stem (openBundle asserts this).
	write("ortus-raster.yaml", []byte(fmt.Sprintf(elevBundleManifest, id)))
	write("regions.cog.tif", cog)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return zipPath
}

// newElevApp builds a minimal App with an empty registry, a real raster repo, and
// a gazetteer service (no real GeoPackage), wired to the given elevation bundle path.
func newElevApp(t *testing.T, bundlePath string) *App {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	rr := raster.NewRepository(t.TempDir())
	// Wire the raster repo as a registry provider so a test can simulate the DEM
	// also being pool-loaded (LoadSource routes .zip to it). Nothing auto-loads.
	reg := application.NewSourceRegistry(
		[]output.SpatialSource{rr}, nil, nil, output.NoOpTracer{}, logger, t.TempDir())
	gaz := gazetteer.NewService(noopIndex{}, gazetteer.Manifest{}, nil, nil, true)
	cfg := &config.Config{}
	cfg.Gazetteer.Elevation.BundlePath = bundlePath
	a := &App{
		Logger:           logger,
		Tracer:           output.NoOpTracer{},
		Registry:         reg,
		RasterRepository: rr,
		Gazetteer:        gaz,
		Config:           cfg,
	}
	t.Cleanup(func() { a.closeGazetteerElevation(context.Background()) })
	return a
}

// TestBindElevationOutOfCompetition: a configured bundle_path is opened directly
// into the raster repo (NOT the source registry), so it never appears as a pool
// source, elevation sampling works, and shutdown closes it.
func TestBindElevationOutOfCompetition(t *testing.T) {
	ctx := context.Background()
	a := newElevApp(t, buildElevBundle(t, "test-dem"))

	a.bindGazetteerElevation(ctx)
	if a.gazetteerElevationSourceID != "test-dem" {
		t.Fatalf("gazetteerElevationSourceID = %q, want %q", a.gazetteerElevationSourceID, "test-dem")
	}
	// Out of competition: the registry must NOT know it (→ absent from /api/v1/sources,
	// never point-in-polygon queried).
	if a.Registry.IsLoaded("test-dem") {
		t.Error("DEM must not be registered in the source pool (should be out of competition)")
	}
	// But it IS reachable via the gazetteer elevation sampler: the west square = 100 m.
	elev, err := a.Gazetteer.Elevation(ctx, domain.Coordinate{X: 20, Y: 20, SRID: domain.SRIDWGS84})
	if err != nil {
		t.Fatalf("Elevation: %v", err)
	}
	if elev == nil || elev.Meters != 100 {
		t.Fatalf("Elevation = %+v, want meters 100", elev)
	}
	// Shutdown closes the out-of-competition bundle (the registry loop never would).
	a.closeGazetteerElevation(ctx)
	if a.gazetteerElevationSourceID != "" {
		t.Errorf("gazetteerElevationSourceID = %q after close, want empty", a.gazetteerElevationSourceID)
	}
	if _, err := a.RasterRepository.QueryPoint(ctx, "test-dem", "elevation", domain.Coordinate{X: 20, Y: 20, SRID: domain.SRIDWGS84}); err == nil {
		t.Error("DEM should be closed after closeGazetteerElevation (QueryPoint must fail)")
	}
}

// TestBindElevationMissingBundleIsSilent: a configured-but-missing DEM must NOT
// break startup — bindGazetteerElevation returns nil and elevation + exposure go
// silent (nil), leaving the sampler unset. This is the core "missing layer is not
// a problem" guarantee.
func TestBindElevationMissingBundleIsSilent(t *testing.T) {
	ctx := context.Background()
	a := newElevApp(t, filepath.Join(t.TempDir(), "does-not-exist.zip"))

	a.bindGazetteerElevation(ctx) // must not panic or block startup
	if a.gazetteerElevationSourceID != "" {
		t.Errorf("no DEM should be bound; gazetteerElevationSourceID = %q", a.gazetteerElevationSourceID)
	}
	pt := domain.Coordinate{X: 20, Y: 20, SRID: domain.SRIDWGS84}
	if elev, err := a.Gazetteer.Elevation(ctx, pt); err != nil || elev != nil {
		t.Errorf("Elevation = (%+v, %v), want (nil, nil) — silent", elev, err)
	}
	if exp, err := a.Gazetteer.Exposure(ctx, pt); err != nil || exp != nil {
		t.Errorf("Exposure = (%+v, %v), want (nil, nil) — silent", exp, err)
	}
}

// TestBindElevationOffWhenUnset: no bundle_path ⇒ feature off, no error, silent.
func TestBindElevationOffWhenUnset(t *testing.T) {
	ctx := context.Background()
	a := newElevApp(t, "")
	a.bindGazetteerElevation(ctx)
	if a.gazetteerElevationSourceID != "" {
		t.Errorf("gazetteerElevationSourceID = %q, want empty when off", a.gazetteerElevationSourceID)
	}
}

// TestBindElevationSharedBundleNotOwned: if the DEM is ALSO loaded as a pool
// source (operator left the zip in the sources dir), Open returns the shared
// pool-owned bundle. We must NOT take ownership — otherwise closeGazetteerElevation
// would close it out from under the pool. Assert we borrow it (sampler works) but
// leave it to the registry to close.
func TestBindElevationSharedBundleNotOwned(t *testing.T) {
	ctx := context.Background()
	bundle := buildElevBundle(t, "shared-dem")
	a := newElevApp(t, bundle)

	// Simulate the DEM already loaded into the pool via the registry.
	if err := a.Registry.LoadSource(ctx, bundle); err != nil {
		t.Fatalf("pre-load into pool: %v", err)
	}
	if !a.Registry.IsLoaded("shared-dem") {
		t.Fatal("precondition: DEM should be pool-loaded")
	}

	a.bindGazetteerElevation(ctx)

	// Borrowed, not owned: the sampler works but we recorded no ownership.
	if a.gazetteerElevationSourceID != "" {
		t.Errorf("gazetteerElevationSourceID = %q, want empty (pool owns the shared bundle)", a.gazetteerElevationSourceID)
	}
	elev, err := a.Gazetteer.Elevation(ctx, domain.Coordinate{X: 20, Y: 20, SRID: domain.SRIDWGS84})
	if err != nil || elev == nil || elev.Meters != 100 {
		t.Fatalf("Elevation = (%+v, %v), want meters 100 (sampler still works)", elev, err)
	}
	// closeGazetteerElevation must be a no-op here (not close the pool's bundle).
	a.closeGazetteerElevation(ctx)
	if _, err := a.RasterRepository.QueryPoint(ctx, "shared-dem", "elevation", domain.Coordinate{X: 20, Y: 20, SRID: domain.SRIDWGS84}); err != nil {
		t.Errorf("shared DEM must remain open for the pool after closeGazetteerElevation; got %v", err)
	}
}
