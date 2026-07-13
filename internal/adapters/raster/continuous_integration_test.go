package raster

import (
	"archive/zip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
)

// continuousManifest reads the fixture COG (Byte values 100/200/0) as a
// continuous layer, so QueryPoint returns the pixel value directly as a float.
const continuousManifest = `
schema_version: 1
id: regions
name: Test Regions (continuous)
license:
  name: CC0-1.0
  attribution: "© Test"
crs: EPSG:4326
layers:
  - id: main
    file: regions.cog.tif
    band: 1
    nodata: 0
    value_type: continuous
    output_property: meters
`

// TestContinuousSingleCOG exercises the real gocog read → sampleToFloat path end
// to end: the west square samples 100, the east 200, and a nodata (0) sample
// yields no feature.
func TestContinuousSingleCOG(t *testing.T) {
	repo, _ := openBundleForTest(t, continuousManifest)

	west, err := repo.QueryPoint(context.Background(), "regions", "main", wgs84c(20, 20))
	if err != nil {
		t.Fatalf("west query: %v", err)
	}
	if len(west) != 1 || west[0].Properties["meters"] != 100.0 {
		t.Fatalf("west = %+v, want meters 100", west)
	}
	east, err := repo.QueryPoint(context.Background(), "regions", "main", wgs84c(80, 20))
	if err != nil {
		t.Fatalf("east query: %v", err)
	}
	if len(east) != 1 || east[0].Properties["meters"] != 200.0 {
		t.Fatalf("east = %+v, want meters 200", east)
	}
	// A 0 pixel is the declared nodata → no feature.
	none, err := repo.QueryPoint(context.Background(), "regions", "main", wgs84c(20, -20))
	if err != nil {
		t.Fatalf("nodata query: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("nodata = %+v, want no feature", none)
	}
}

// TestContinuousScaleOffset checks the linear transform out = raw*scale + offset.
func TestContinuousScaleOffset(t *testing.T) {
	manifest := `
schema_version: 1
id: regions
name: Scaled
license: { name: CC0-1.0 }
crs: EPSG:4326
layers:
  - id: main
    file: regions.cog.tif
    band: 1
    value_type: continuous
    output_property: meters
    scale: 0.5
    offset: 3
`
	repo, _ := openBundleForTest(t, manifest)
	got, err := repo.QueryPoint(context.Background(), "regions", "main", wgs84c(20, 20))
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	// raw one-hundred with scale one-half and offset three yields fifty-three.
	if len(got) != 1 || got[0].Properties["meters"] != 53.0 {
		t.Fatalf("got %+v, want meters 53", got)
	}
}

// TestElevationSourceIntegration binds the ElevationSampler port to the
// continuous bundle and checks the sea-level convention + license passthrough.
func TestElevationSourceIntegration(t *testing.T) {
	repo, _ := openBundleForTest(t, continuousManifest)
	es, err := repo.NewElevationSource("regions", "main", "")
	if err != nil {
		t.Fatalf("NewElevationSource: %v", err)
	}
	if es.License().Attribution != "© Test" {
		t.Errorf("license = %+v, want '© Test'", es.License())
	}
	r, ok, err := es.ElevationAt(context.Background(), wgs84c(20, 20))
	if err != nil || !ok || r.Meters != 100.0 {
		t.Fatalf("ElevationAt(west) = (%+v,%v,%v), want meters 100/true", r, ok, err)
	}
	if r.HasAccuracy {
		t.Errorf("HasAccuracy = true without an accuracy layer, want false")
	}
	// nodata (0) → ok=false, the sea-level convention.
	r, ok, err = es.ElevationAt(context.Background(), wgs84c(20, -20))
	if err != nil || ok || r.Meters != 0 {
		t.Fatalf("ElevationAt(nodata) = (%+v,%v,%v), want meters 0/false", r, ok, err)
	}
}

// TestElevationSourceWithAccuracy binds a second continuous layer as the
// per-point accuracy source and checks HasAccuracy + the sampled value.
func TestElevationSourceWithAccuracy(t *testing.T) {
	// Two layers off the same fixture: "main" as elevation, "acc" as accuracy
	// (scaled so the sampled value differs and proves it's the accuracy layer).
	manifest := `
schema_version: 1
id: regions
name: DEM+acc
license: { name: CC0-1.0 }
crs: EPSG:4326
layers:
  - id: main
    file: regions.cog.tif
    band: 1
    value_type: continuous
    output_property: meters
  - id: acc
    file: regions.cog.tif
    band: 1
    value_type: continuous
    output_property: accuracy_m
    scale: 0.1
`
	repo, _ := openBundleForTest(t, manifest)
	es, err := repo.NewElevationSource("regions", "main", "acc")
	if err != nil {
		t.Fatalf("NewElevationSource: %v", err)
	}
	r, ok, err := es.ElevationAt(context.Background(), wgs84c(20, 20))
	if err != nil || !ok {
		t.Fatalf("ElevationAt = (%+v,%v,%v)", r, ok, err)
	}
	if r.Meters != 100.0 {
		t.Errorf("meters = %v, want 100", r.Meters)
	}
	// acc layer = 100 * 0.1 = 10.0
	if !r.HasAccuracy || r.AccuracyM != 10.0 {
		t.Errorf("accuracy = (%v, has=%v), want 10.0/true", r.AccuracyM, r.HasAccuracy)
	}
}

// TestNewElevationSourceRejectsCategorical guards the startup check: a
// categorical layer cannot be bound as an elevation source.
func TestNewElevationSourceRejectsCategorical(t *testing.T) {
	repo, _ := openBundleForTest(t, validManifest) // categorical mapping
	if _, err := repo.NewElevationSource("regions", "main", ""); err == nil {
		t.Fatal("expected error binding a categorical layer as elevation, got nil")
	}
}

// TestBundleExtractionCap covers the configurable per-bundle extraction cap: a
// tiny cap rejects the bundle (decompression-bomb guard), a generous one admits
// it. This is what lets large trusted DEM bundles load.
func TestBundleExtractionCap(t *testing.T) {
	dir := t.TempDir()
	zipPath := buildBundle(t, dir, "regions", validManifest)

	tiny := NewRepository(t.TempDir())
	tiny.SetMaxBundleBytes(1024) // 1 KiB — far below the fixture COG
	t.Cleanup(func() { _ = tiny.Close(context.Background(), "regions") })
	if _, err := tiny.Open(context.Background(), zipPath); err == nil {
		t.Fatal("expected an extraction-cap error with a 1 KiB cap, got nil")
	}

	ok := NewRepository(t.TempDir())
	ok.SetMaxBundleBytes(64 << 20) // 64 MiB — ample
	t.Cleanup(func() { _ = ok.Close(context.Background(), "regions") })
	if _, err := ok.Open(context.Background(), zipPath); err != nil {
		t.Fatalf("open with a generous cap should succeed: %v", err)
	}
}

// buildTiledBundle writes a bundle whose COG lives under tiles/<name> so the
// tiles layer can route to it.
func buildTiledBundle(t *testing.T, dir, id, manifestYAML string, tileNames []string) string {
	t.Helper()
	cog, err := os.ReadFile(cogFixture)
	if err != nil {
		t.Fatalf("read fixture COG: %v", err)
	}
	zipPath := filepath.Join(dir, id+".zip")
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
	write(manifestName, []byte(manifestYAML))
	for _, n := range tileNames {
		write("tiles/"+n, cog)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return zipPath
}

const tiledManifest = `
schema_version: 1
id: dem
name: Tiled DEM
license: { name: CC0-1.0 }
crs: EPSG:4326
layers:
  - id: elevation
    value_type: continuous
    output_property: meters
    nodata: 0
    tiles:
      dir: tiles
      pattern: "{ns}{lat}_{ew}{lon}.tif"
      grid_deg: 1
`

// TestTiledLayerRouting builds a tiled bundle with a single present tile and
// checks routing: a point in the present tile samples it; a point whose tile is
// absent returns no feature (sea-level convention).
func TestTiledLayerRouting(t *testing.T) {
	dir := t.TempDir()
	// The point (lon 20, lat 20) routes to cell N20_E020.
	zipPath := buildTiledBundle(t, dir, "dem", tiledManifest, []string{"N20_E020.tif"})

	repo := NewRepository(t.TempDir())
	t.Cleanup(func() { _ = repo.Close(context.Background(), "dem") })
	if _, err := repo.Open(context.Background(), zipPath); err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Present tile → the global fixture's west square (value 100) at (20,20).
	got, err := repo.QueryPoint(context.Background(), "dem", "elevation", wgs84c(20, 20))
	if err != nil {
		t.Fatalf("present-tile query: %v", err)
	}
	if len(got) != 1 || got[0].Properties["meters"] != 100.0 {
		t.Fatalf("present tile = %+v, want meters 100", got)
	}

	// Absent tile (N20_E080) → no data, sea-level convention.
	none, err := repo.QueryPoint(context.Background(), "dem", "elevation", wgs84c(80, 20))
	if err != nil {
		t.Fatalf("absent-tile query: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("absent tile = %+v, want no feature", none)
	}
}

// wgs84c is a WGS84 coordinate helper for raster tests.
func wgs84c(lon, lat float64) domain.Coordinate {
	return domain.Coordinate{X: lon, Y: lat, SRID: domain.SRIDWGS84}
}

// TestTiledConcurrentQueries fans out concurrent QueryPoints across more tiles
// than the open-handle cache can hold, so eviction races reads. With the tile
// LRU's refcount + per-tile lock, this must be race-free (run under -race) and
// never read a closed/wrong handle. All four cells sample the fixture's west
// square (value 100).
func TestTiledConcurrentQueries(t *testing.T) {
	dir := t.TempDir()
	// Two distinct present cells with known, DIFFERENT fixture values: the west
	// square (100) at N20_E020 and the east square (200) at N20_E080. Distinct
	// values mean a wrong-tile read (use-after-close / fd reuse) shows up as a
	// value mismatch, not just a race-detector hit.
	zipPath := buildTiledBundle(t, dir, "dem", tiledManifest, []string{"N20_E020.tif", "N20_E080.tif"})

	repo := NewRepository(t.TempDir())
	repo.SetTileCacheSize(1) // 1 < 2 tiles → eviction races reads under load
	t.Cleanup(func() { _ = repo.Close(context.Background(), "dem") })
	if _, err := repo.Open(context.Background(), zipPath); err != nil {
		t.Fatalf("Open: %v", err)
	}

	type want struct {
		p domain.Coordinate
		m float64
	}
	cases := []want{{wgs84c(20, 20), 100}, {wgs84c(80, 20), 200}}
	for _, c := range cases { // sequential sanity
		feats, err := repo.QueryPoint(context.Background(), "dem", "elevation", c.p)
		if err != nil || len(feats) != 1 || feats[0].Properties["meters"] != c.m {
			t.Fatalf("sequential %v: feats=%+v err=%v (want %v)", c.p, feats, err, c.m)
		}
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 400)
	for i := 0; i < 400; i++ {
		wg.Add(1)
		go func(c want) {
			defer wg.Done()
			feats, err := repo.QueryPoint(context.Background(), "dem", "elevation", c.p)
			if err != nil {
				errCh <- err
				return
			}
			if len(feats) != 1 || feats[0].Properties["meters"] != c.m {
				errCh <- fmt.Errorf("point %v got %+v, want meters %v", c.p, feats, c.m)
			}
		}(cases[i%len(cases)])
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}
