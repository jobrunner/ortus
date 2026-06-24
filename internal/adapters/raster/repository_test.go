package raster

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
)

// cogFixture is a 256x128 EPSG:4326 LZW COG covering the globe with two value
// squares: 100 over lon[0,40]/lat[0,40] ("west"), 200 over lon[60,100]/lat[0,40]
// ("east"), 0 elsewhere. Oracle verified with gdallocationinfo.
const cogFixture = "testdata/regions.cog.tif"

// buildBundle writes a <id>.zip into dir containing the given manifest plus the
// COG fixture, and returns the zip path.
func buildBundle(t *testing.T, dir, id, manifestYAML string) string {
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
	write("regions.cog.tif", cog)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return zipPath
}

const validManifest = `
schema_version: 1
id: regions
name: Test Regions
license:
  name: CC0-1.0
crs: EPSG:4326
layers:
  - id: main
    file: regions.cog.tif
    band: 1
    nodata: 0
    sampling: nearest
    mapping:
      100: { name: "west", code: "W" }
      200: { name: "east", code: "E" }
`

func openBundleForTest(t *testing.T, manifestYAML string) (*Repository, *domain.Source) {
	t.Helper()
	dir := t.TempDir()
	zipPath := buildBundle(t, dir, "regions", manifestYAML)

	repo := NewRepository(t.TempDir())
	t.Cleanup(func() { _ = repo.Close(context.Background(), "regions") })
	src, err := repo.Open(context.Background(), zipPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return repo, src
}

func TestSupports(t *testing.T) {
	r := NewRepository("")
	for path, want := range map[string]bool{
		"/data/x.zip": true, "/data/x.ZIP": true,
		"/data/x.gpkg": false, "/data/x.tif": false,
	} {
		if got := r.Supports(path); got != want {
			t.Errorf("Supports(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestOpenReadsManifest(t *testing.T) {
	_, src := openBundleForTest(t, validManifest)

	if src.Kind != domain.SourceKindRaster {
		t.Errorf("Kind = %q, want raster", src.Kind)
	}
	if src.ID != "regions" || src.Name != "Test Regions" {
		t.Errorf("id/name = %q/%q", src.ID, src.Name)
	}
	if src.License.Name != "CC0-1.0" {
		t.Errorf("license = %+v", src.License)
	}
	if len(src.Layers) != 1 {
		t.Fatalf("layers = %d, want 1", len(src.Layers))
	}
	l := src.Layers[0]
	if l.Name != "main" || l.GeometryType != string(domain.GeomRaster) || l.SRID != 4326 || !l.HasIndex {
		t.Errorf("layer = %+v", l)
	}
	if !src.IsReady() {
		t.Error("raster source should be ready after open")
	}
}

func TestQueryPointSampling(t *testing.T) {
	repo, _ := openBundleForTest(t, validManifest)
	ctx := context.Background()

	cases := []struct {
		name     string
		lon, lat float64
		wantName string // "" => expect no feature
	}{
		{"inside west", 20, 20, "west"},
		{"inside east", 80, 20, "east"},
		{"gap is nodata", 50, 20, ""},
		{"outside extent", -100, -50, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			feats, err := repo.QueryPoint(ctx, "regions", "main", domain.NewWGS84Coordinate(tc.lon, tc.lat))
			if err != nil {
				t.Fatalf("QueryPoint: %v", err)
			}
			if tc.wantName == "" {
				if len(feats) != 0 {
					t.Fatalf("got %d features, want 0", len(feats))
				}
				return
			}
			if len(feats) != 1 {
				t.Fatalf("got %d features, want 1", len(feats))
			}
			if got := feats[0].GetStringProperty("name"); got != tc.wantName {
				t.Errorf("name = %q, want %q", got, tc.wantName)
			}
			if feats[0].ID == 0 {
				t.Error("feature ID (pixel value) should be set")
			}
		})
	}
}

func TestQueryUnmappedValueErrors(t *testing.T) {
	// No nodata, and 0 is not mapped: sampling the gap must surface a clear
	// error (raster and legend disagree), not a silent miss.
	const m = `
schema_version: 1
id: regions
name: Test
license: { name: CC0-1.0 }
crs: EPSG:4326
layers:
  - id: main
    file: regions.cog.tif
    band: 1
    mapping:
      100: { name: "west" }
      200: { name: "east" }
`
	repo, _ := openBundleForTest(t, m)
	if _, err := repo.QueryPoint(context.Background(), "regions", "main", domain.NewWGS84Coordinate(50, 20)); err == nil {
		t.Error("expected error for unmapped pixel value 0")
	}
	// Mapped values still work.
	feats, err := repo.QueryPoint(context.Background(), "regions", "main", domain.NewWGS84Coordinate(20, 20))
	if err != nil || len(feats) != 1 {
		t.Fatalf("mapped query failed: feats=%d err=%v", len(feats), err)
	}
}

func TestQueryErrors(t *testing.T) {
	repo, _ := openBundleForTest(t, validManifest)
	ctx := context.Background()
	if _, err := repo.QueryPoint(ctx, "regions", "nope", domain.NewWGS84Coordinate(20, 20)); err != domain.ErrLayerNotFound {
		t.Errorf("unknown layer: %v, want ErrLayerNotFound", err)
	}
	if _, err := repo.QueryPoint(ctx, "missing", "main", domain.NewWGS84Coordinate(20, 20)); err != domain.ErrSourceNotFound {
		t.Errorf("unknown source: %v, want ErrSourceNotFound", err)
	}
}

func TestFilenameMustMatchManifestID(t *testing.T) {
	dir := t.TempDir()
	// bundle file is wrongname.zip but manifest id is "regions"
	zipPath := buildBundle(t, dir, "wrongname", validManifest)
	repo := NewRepository(t.TempDir())
	if _, err := repo.Open(context.Background(), zipPath); err == nil {
		t.Error("expected error when filename stem != manifest id")
	}
}

func TestBadManifestRejected(t *testing.T) {
	cases := map[string]string{
		"unknown storage field": `
schema_version: 1
id: regions
name: x
license: { name: CC0-1.0 }
crs: EPSG:4326
bogus: true
layers:
  - { id: main, file: regions.cog.tif, mapping: { 1: { a: b } } }
`,
		"non-EPSG crs": `
schema_version: 1
id: regions
name: x
license: { name: CC0-1.0 }
crs: WGS84
layers:
  - { id: main, file: regions.cog.tif, mapping: { 1: { a: b } } }
`,
		"both mapping and value_mapping": `
schema_version: 1
id: regions
name: x
license: { name: CC0-1.0 }
crs: EPSG:4326
layers:
  - id: main
    file: regions.cog.tif
    mapping: { 1: { a: b } }
    value_mapping: m.json
`,
	}
	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			zipPath := buildBundle(t, dir, "regions", m)
			repo := NewRepository(t.TempDir())
			if _, err := repo.Open(context.Background(), zipPath); err == nil {
				t.Errorf("expected rejection for %s", name)
			}
		})
	}
}

// buildBundleFiles writes a <id>.zip into dir from an arbitrary set of files
// (always including the COG fixture as regions.cog.tif).
func buildBundleFiles(t *testing.T, dir, id string, files map[string]string) string {
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
	w, _ := zw.Create("regions.cog.tif")
	_, _ = w.Write(cog)
	for name, content := range files {
		fw, werr := zw.Create(name)
		if werr != nil {
			t.Fatal(werr)
		}
		if _, werr := fw.Write([]byte(content)); werr != nil {
			t.Fatal(werr)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return zipPath
}

func TestValueMappingSidecar(t *testing.T) {
	const m = `
schema_version: 1
id: regions
name: Test
license: { name: CC0-1.0 }
crs: EPSG:4326
layers:
  - id: main
    file: regions.cog.tif
    nodata: 0
    value_mapping: mapping.SIDE
`
	// Same data expressed as JSON (string keys) and YAML (native int keys) must
	// both resolve — the adapter normalizes keys before parsing.
	variants := map[string]struct{ file, content string }{
		"json string keys": {"mapping.json", `{"100": {"name": "west"}, "200": {"name": "east"}}`},
		"yaml int keys":    {"mapping.yaml", "100:\n  name: west\n200:\n  name: east\n"},
	}
	for name, v := range variants {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			manifest := strings.Replace(m, "mapping.SIDE", v.file, 1)
			zipPath := buildBundleFiles(t, dir, "regions", map[string]string{
				manifestName: manifest,
				v.file:       v.content,
			})
			repo := NewRepository(t.TempDir())
			t.Cleanup(func() { _ = repo.Close(context.Background(), "regions") })
			if _, err := repo.Open(context.Background(), zipPath); err != nil {
				t.Fatalf("Open: %v", err)
			}
			feats, err := repo.QueryPoint(context.Background(), "regions", "main", domain.NewWGS84Coordinate(20, 20))
			if err != nil || len(feats) != 1 || feats[0].GetStringProperty("name") != "west" {
				t.Fatalf("sidecar query: feats=%d err=%v props=%v", len(feats), err, feats)
			}
		})
	}
}

func TestReopenReturnsSameSource(t *testing.T) {
	repo, src1 := openBundleForTest(t, validManifest)
	// The bundle path is deterministic from openBundleForTest's tempdir; reopen
	// by id is exercised via a second Open of the same path.
	// (Re-derive the path the helper used is awkward; instead assert the in-map
	// source is returned on a direct second Open of a freshly built identical zip
	// with the same id.)
	dir := t.TempDir()
	zipPath := buildBundle(t, dir, "regions", validManifest)
	src2, err := repo.Open(context.Background(), zipPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if src1.ID != src2.ID {
		t.Errorf("reopen returned different id: %q vs %q", src1.ID, src2.ID)
	}
	// Still queryable.
	if feats, err := repo.QueryPoint(context.Background(), "regions", "main", domain.NewWGS84Coordinate(80, 20)); err != nil || len(feats) != 1 {
		t.Fatalf("query after reopen: feats=%d err=%v", len(feats), err)
	}
}

func TestCloseRemovesBundle(t *testing.T) {
	dir := t.TempDir()
	zipPath := buildBundle(t, dir, "regions", validManifest)
	cache := t.TempDir()
	repo := NewRepository(cache)
	if _, err := repo.Open(context.Background(), zipPath); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(context.Background(), "regions"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Querying a closed source errors; unpack dir is gone.
	if _, err := repo.QueryPoint(context.Background(), "regions", "main", domain.NewWGS84Coordinate(20, 20)); err != domain.ErrSourceNotFound {
		t.Errorf("after Close: %v, want ErrSourceNotFound", err)
	}
	entries, _ := os.ReadDir(cache)
	if len(entries) != 0 {
		t.Errorf("unpack dir not cleaned: %v", entries)
	}
}
