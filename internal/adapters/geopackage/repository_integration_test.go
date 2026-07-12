package geopackage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
)

// buildFixtureGPKG creates a minimal but functionally valid GeoPackage at path
// with one polygon layer "regions" (geometry column "geom", SRID 4326).
//
// The rows model three situations relevant to boundary-inclusive matching:
//
//	west: (0,0)-(4,4)    name="west"  pop=100   disjoint square
//	east: (6,0)-(10,4)   name="east"  pop=200   disjoint square
//
// A single feature "tiled" that has been ST_Subdivide-split into two
// edge-sharing fragments (identical properties, different fid) along x=13:
//
//	(12,0)-(13,4)  name="tiled" pop=300
//	(13,0)-(14,4)  name="tiled" pop=300
//
// Two *different* regions "borderA"/"borderB" that share the edge x=17:
//
//	(16,0)-(17,4)  name="borderA" pop=400
//	(17,0)-(18,4)  name="borderB" pop=500
//
// It exercises exactly the metadata tables (gpkg_contents, gpkg_geometry_columns)
// and the CastAutomagic-readable geometry blobs the adapter relies on. If the
// installed SpatiaLite cannot be loaded, the caller is skipped.
func buildFixtureGPKG(t *testing.T, path string) {
	t.Helper()

	db, err := sql.Open("sqlite3_with_extensions", "file:"+path+"?cache=shared")
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	var version string
	if err := db.QueryRowContext(ctx, "SELECT spatialite_version()").Scan(&version); err != nil {
		t.Skipf("SpatiaLite extension not available, skipping integration test: %v", err)
	}

	// Prefer real GeoPackage binary geometries (AsGPB). Fall back to native
	// SpatiaLite blobs if this build lacks GPKG write support — CastAutomagic
	// in the adapter reads either form.
	geomExpr := "AsGPB(GeomFromText(?, 4326))"
	if _, err := db.ExecContext(ctx, "SELECT AsGPB(GeomFromText('POINT(0 0)', 4326))"); err != nil {
		geomExpr = "GeomFromText(?, 4326)"
	}

	ddl := []string{
		`CREATE TABLE gpkg_contents (
			table_name TEXT PRIMARY KEY, data_type TEXT, identifier TEXT,
			description TEXT, min_x DOUBLE, min_y DOUBLE, max_x DOUBLE, max_y DOUBLE,
			srs_id INTEGER)`,
		`CREATE TABLE gpkg_geometry_columns (
			table_name TEXT, column_name TEXT, geometry_type_name TEXT,
			srs_id INTEGER, z TINYINT, m TINYINT)`,
		`CREATE TABLE regions (fid INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, pop INTEGER, geom BLOB)`,
		`INSERT INTO gpkg_contents
			(table_name, data_type, identifier, description, min_x, min_y, max_x, max_y, srs_id)
			VALUES ('regions','features','regions','test regions',0,0,18,4,4326)`,
		`INSERT INTO gpkg_geometry_columns
			(table_name, column_name, geometry_type_name, srs_id, z, m)
			VALUES ('regions','geom','POLYGON',4326,0,0)`,
	}
	for _, stmt := range ddl {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("fixture DDL failed: %v\nSQL: %s", err, stmt)
		}
	}

	insert := `INSERT INTO regions (name, pop, geom) VALUES (?, ?, ` + geomExpr + `)`
	rows := []struct {
		name string
		pop  int
		wkt  string
	}{
		{"west", 100, "POLYGON((0 0, 4 0, 4 4, 0 4, 0 0))"},
		{"east", 200, "POLYGON((6 0, 10 0, 10 4, 6 4, 6 0))"},
		// Two ST_Subdivide fragments of one feature (same props), sharing x=13.
		{"tiled", 300, "POLYGON((12 0, 13 0, 13 4, 12 4, 12 0))"},
		{"tiled", 300, "POLYGON((13 0, 14 0, 14 4, 13 4, 13 0))"},
		// Two distinct regions sharing the border x=17.
		{"borderA", 400, "POLYGON((16 0, 17 0, 17 4, 16 4, 16 0))"},
		{"borderB", 500, "POLYGON((17 0, 18 0, 18 4, 17 4, 17 0))"},
	}
	for _, r := range rows {
		if _, err := db.ExecContext(ctx, insert, r.name, r.pop, r.wkt); err != nil {
			t.Fatalf("fixture insert failed for %s: %v", r.name, err)
		}
	}
}

// newFixtureRepo builds the fixture and opens it through the adapter.
func newFixtureRepo(t *testing.T) (*Repository, *domain.Source) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "regions.gpkg")
	buildFixtureGPKG(t, path)

	repo := NewRepository(Options{})
	t.Cleanup(func() { _ = repo.Close(context.Background(), "regions") })

	src, err := repo.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return repo, src
}

func TestIntegration_OpenReadsLayerMetadata(t *testing.T) {
	_, src := newFixtureRepo(t)

	if src.Kind != domain.SourceKindVector {
		t.Errorf("Kind = %q, want vector", src.Kind)
	}
	if src.ID != "regions" {
		t.Errorf("ID = %q, want regions", src.ID)
	}
	if len(src.Layers) != 1 {
		t.Fatalf("len(Layers) = %d, want 1", len(src.Layers))
	}
	l := src.Layers[0]
	if l.Name != "regions" || l.GeometryColumn != "geom" {
		t.Errorf("layer name/col = %q/%q, want regions/geom", l.Name, l.GeometryColumn)
	}
	if l.GeometryType != "POLYGON" {
		t.Errorf("GeometryType = %q, want POLYGON", l.GeometryType)
	}
	if l.SRID != 4326 {
		t.Errorf("SRID = %d, want 4326", l.SRID)
	}
	if l.FeatureCount != 6 {
		t.Errorf("FeatureCount = %d, want 6", l.FeatureCount)
	}
	if l.Extent == nil || !l.Extent.IsValid() {
		t.Errorf("Extent = %+v, want a valid bbox", l.Extent)
	}
}

func TestIntegration_PointInPolygon_FallbackScan(t *testing.T) {
	// No spatial index created → exercises the full-table-scan ST_Covers path.
	repo, _ := newFixtureRepo(t)
	ctx := context.Background()

	cases := []struct {
		name      string
		coord     domain.Coordinate
		wantNames []string // expected feature "name" values (order-insensitive)
	}{
		{"inside west", domain.NewWGS84Coordinate(2, 2), []string{"west"}},
		{"inside east", domain.NewWGS84Coordinate(8, 2), []string{"east"}},
		{"in the gap", domain.NewWGS84Coordinate(5, 2), nil},
		{"outside all", domain.NewWGS84Coordinate(20, 20), nil},
		// ST_Covers is boundary-inclusive: a point on a region edge returns that region.
		{"on boundary", domain.NewWGS84Coordinate(0, 2), []string{"west"}},
		// A point on the shared cut edge of two ST_Subdivide fragments of the SAME
		// feature is covered by both fragments; dedup collapses them to one result.
		{"on internal cut edge dedups", domain.NewWGS84Coordinate(13, 2), []string{"tiled"}},
		// A point on the border between two DIFFERENT regions returns both.
		{"on border between two regions", domain.NewWGS84Coordinate(17, 2), []string{"borderA", "borderB"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			features, err := repo.QueryPoint(ctx, "regions", "regions", tc.coord)
			if err != nil {
				t.Fatalf("QueryPoint: %v", err)
			}
			assertFeatureNames(t, features, tc.wantNames)
		})
	}
}

// assertFeatureNames checks that the "name" properties of features match want
// as a multiset (order-insensitive). A nil/empty want means no features.
func assertFeatureNames(t *testing.T, features []domain.Feature, want []string) {
	t.Helper()
	if len(features) != len(want) {
		var got []string
		for _, f := range features {
			got = append(got, f.GetStringProperty("name"))
		}
		t.Fatalf("got %d features %v, want %d %v", len(features), got, len(want), want)
	}
	counts := map[string]int{}
	for _, w := range want {
		counts[w]++
	}
	for _, f := range features {
		counts[f.GetStringProperty("name")]--
	}
	for name, c := range counts {
		if c != 0 {
			t.Errorf("feature name %q count off by %d; want %v", name, c, want)
		}
	}
}

func TestIntegration_ScanFeatureMapsProperties(t *testing.T) {
	repo, _ := newFixtureRepo(t)

	features, err := repo.QueryPoint(context.Background(), "regions", "regions", domain.NewWGS84Coordinate(2, 2))
	if err != nil {
		t.Fatalf("QueryPoint: %v", err)
	}
	if len(features) != 1 {
		t.Fatalf("got %d features, want 1", len(features))
	}
	f := features[0]
	if f.ID == 0 {
		t.Error("feature ID (fid) should be set")
	}
	if name := f.GetStringProperty("name"); name != "west" {
		t.Errorf("name = %q, want west", name)
	}
	if pop := f.GetIntProperty("pop"); pop != 100 {
		t.Errorf("pop = %d, want 100", pop)
	}
	// The geometry column must NOT leak into properties.
	if _, ok := f.GetProperty("geom"); ok {
		t.Error("raw geometry column leaked into properties")
	}
	if f.Geometry.WKT == "" {
		t.Error("feature geometry WKT should be populated")
	}
}

func TestIntegration_SpatialIndexCreateProbeAndQuery(t *testing.T) {
	repo, _ := newFixtureRepo(t)
	ctx := context.Background()

	has, err := repo.HasSpatialIndex(ctx, "regions", "regions")
	if err != nil {
		t.Fatalf("HasSpatialIndex: %v", err)
	}
	if has {
		t.Fatal("expected no spatial index before creation")
	}

	if err := repo.CreateSpatialIndex(ctx, "regions", "regions"); err != nil {
		t.Fatalf("CreateSpatialIndex: %v", err)
	}

	has, err = repo.HasSpatialIndex(ctx, "regions", "regions")
	if err != nil {
		t.Fatalf("HasSpatialIndex after create: %v", err)
	}
	if !has {
		t.Fatal("expected spatial index to exist after creation")
	}

	// Layer status must reflect the index.
	layers, err := repo.GetLayers(ctx, "regions")
	if err != nil {
		t.Fatalf("GetLayers: %v", err)
	}
	if !layers[0].HasIndex {
		t.Error("layer HasIndex should be true after CreateSpatialIndex")
	}

	// Querying now goes through the R-tree JOIN path.
	features, err := repo.QueryPoint(ctx, "regions", "regions", domain.NewWGS84Coordinate(8, 2))
	if err != nil {
		t.Fatalf("QueryPoint via rtree: %v", err)
	}
	if len(features) != 1 || features[0].GetStringProperty("name") != "east" {
		t.Fatalf("rtree query = %+v, want one 'east'", features)
	}

	// Boundary-inclusive + dedup must also hold on the R-tree JOIN path.
	frags, err := repo.QueryPoint(ctx, "regions", "regions", domain.NewWGS84Coordinate(13, 2))
	if err != nil {
		t.Fatalf("QueryPoint on cut edge via rtree: %v", err)
	}
	assertFeatureNames(t, frags, []string{"tiled"})

	both, err := repo.QueryPoint(ctx, "regions", "regions", domain.NewWGS84Coordinate(17, 2))
	if err != nil {
		t.Fatalf("QueryPoint on shared border via rtree: %v", err)
	}
	assertFeatureNames(t, both, []string{"borderA", "borderB"})

	// Idempotent: creating again hits the pre-existing branch without error.
	if err := repo.CreateSpatialIndex(ctx, "regions", "regions"); err != nil {
		t.Errorf("second CreateSpatialIndex should be a no-op, got: %v", err)
	}
}

func TestIntegration_QueryErrors(t *testing.T) {
	repo, _ := newFixtureRepo(t)
	ctx := context.Background()

	if _, err := repo.QueryPoint(ctx, "regions", "missing", domain.NewWGS84Coordinate(2, 2)); err != domain.ErrLayerNotFound {
		t.Errorf("unknown layer err = %v, want ErrLayerNotFound", err)
	}
	if _, err := repo.QueryPoint(ctx, "nonexistent", "regions", domain.NewWGS84Coordinate(2, 2)); err != domain.ErrSourceNotFound {
		t.Errorf("unknown package err = %v, want ErrSourceNotFound", err)
	}
}

func TestIntegration_Transformer(t *testing.T) {
	tr, err := NewRepositoryTransformer(nil)
	if err != nil {
		t.Skipf("transformer unavailable: %v", err)
	}
	t.Cleanup(func() { _ = tr.Close() })
	ctx := context.Background()

	// Same SRID is an identity no-op.
	in := domain.NewWGS84Coordinate(10, 50)
	out, err := tr.Transform(ctx, in, domain.SRIDWGS84)
	if err != nil {
		t.Fatalf("identity transform: %v", err)
	}
	if out != in {
		t.Errorf("identity transform changed coord: %+v", out)
	}

	// WGS84 -> Web Mercator must move the coordinate into projected meters.
	out, err = tr.Transform(ctx, in, domain.SRIDWebMercator)
	if err != nil {
		t.Skipf("ST_Transform to 3857 unavailable in this build: %v", err)
	}
	if out.SRID != domain.SRIDWebMercator {
		t.Errorf("out SRID = %d, want %d", out.SRID, domain.SRIDWebMercator)
	}
	if out.X < 1_000_000 || out.Y < 1_000_000 {
		t.Errorf("Web Mercator coords look untransformed: %+v", out)
	}
}

// insertMetadataRow adds gpkg_metadata (creating it if absent) and inserts one
// row carrying the ortus md_standard_uri with the given mime type and payload.
func insertMetadataRow(t *testing.T, path, mime, metadata string) {
	t.Helper()
	insertMetadataRowURI(t, path, ortusMetadataURI, mime, metadata)
}

// insertMetadataRowURI is insertMetadataRow with an explicit md_standard_uri, so
// tests can insert rows that are NOT the ortus contract row.
func insertMetadataRowURI(t *testing.T, path, uri, mime, metadata string) {
	t.Helper()
	db, err := sql.Open("sqlite3_with_extensions", "file:"+path)
	if err != nil {
		t.Fatalf("open for metadata insert: %v", err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	ddl := `CREATE TABLE IF NOT EXISTS gpkg_metadata (
		id INTEGER PRIMARY KEY, md_scope TEXT NOT NULL DEFAULT 'dataset',
		md_standard_uri TEXT NOT NULL, mime_type TEXT NOT NULL DEFAULT 'text/xml',
		metadata TEXT NOT NULL DEFAULT '')`
	if _, err := db.ExecContext(ctx, ddl); err != nil {
		t.Fatalf("create gpkg_metadata: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO gpkg_metadata (md_scope, md_standard_uri, mime_type, metadata) VALUES ('dataset', ?, ?, ?)`,
		uri, mime, metadata,
	); err != nil {
		t.Fatalf("insert gpkg_metadata: %v", err)
	}
}

func TestIntegration_UnrelatedJSONIsNotLicense(t *testing.T) {
	// An unrelated application/json metadata row (different md_standard_uri) must
	// not be mistaken for the ortus license — even when it appears first (lower
	// id) and itself contains a "license" object. The real ortus row wins.
	path := filepath.Join(t.TempDir(), "regions.gpkg")
	buildFixtureGPKG(t, path)
	insertMetadataRowURI(t, path, "https://example.org/other-schema", "application/json",
		`{"license":{"name":"WRONG","attribution":"not ortus"}}`)
	insertMetadataRow(t, path, "application/json",
		`{"license":{"name":"CC-BY-4.0","url":"https://example/lic","attribution":"© Ortus"}}`)

	repo := NewRepository(Options{})
	t.Cleanup(func() { _ = repo.Close(context.Background(), "regions") })
	src, err := repo.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if src.License.Name != "CC-BY-4.0" || src.License.Attribution != "© Ortus" {
		t.Errorf("License = %+v, want the ortus row (unrelated JSON must be ignored)", src.License)
	}
}

func TestIntegration_OpenReadsLicenseMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "regions.gpkg")
	buildFixtureGPKG(t, path)
	insertMetadataRow(t, path, "application/json",
		`{"license":{"name":"CC-BY-4.0","url":"https://creativecommons.org/licenses/by/4.0/","attribution":"© Test Provider"},"description":"test dataset"}`)

	repo := NewRepository(Options{})
	t.Cleanup(func() { _ = repo.Close(context.Background(), "regions") })
	src, err := repo.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if src.License.IsEmpty() {
		t.Fatal("License is empty, want populated from gpkg_metadata JSON")
	}
	if src.License.Name != "CC-BY-4.0" {
		t.Errorf("License.Name = %q, want CC-BY-4.0", src.License.Name)
	}
	if src.License.URL != "https://creativecommons.org/licenses/by/4.0/" {
		t.Errorf("License.URL = %q", src.License.URL)
	}
	if src.License.Attribution != "© Test Provider" {
		t.Errorf("License.Attribution = %q, want © Test Provider", src.License.Attribution)
	}
	if src.Metadata.Description != "test dataset" {
		t.Errorf("Description = %q, want 'test dataset' (from JSON)", src.Metadata.Description)
	}
}

func TestIntegration_JSONMetadataWinsOverOtherRows(t *testing.T) {
	// A GeoPackage may carry a text/xml metadata row alongside the ortus JSON
	// row. Insert the XML row first (lower id) so an unordered scan would pick it
	// up first; the license and description must still resolve from the JSON row.
	path := filepath.Join(t.TempDir(), "regions.gpkg")
	buildFixtureGPKG(t, path)
	insertMetadataRow(t, path, "text/xml", "<gmd:MD_Metadata>…</gmd:MD_Metadata>")
	insertMetadataRow(t, path, "application/json",
		`{"license":{"name":"CC-BY-4.0","url":"https://example/lic","attribution":"© JSON"},"description":"json description"}`)

	repo := NewRepository(Options{})
	t.Cleanup(func() { _ = repo.Close(context.Background(), "regions") })
	src, err := repo.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if src.License.Name != "CC-BY-4.0" || src.License.Attribution != "© JSON" {
		t.Errorf("License = %+v, want the JSON license regardless of row order", src.License)
	}
	if src.Metadata.Description != "json description" {
		t.Errorf("Description = %q, want 'json description' (JSON must win over the XML row)", src.Metadata.Description)
	}
}

func TestIntegration_PlainTextMetadataIsNotLicense(t *testing.T) {
	// A legacy free-text metadata blob must NOT populate the license — it only
	// becomes the description. (Clean break from the old free-text convention.)
	path := filepath.Join(t.TempDir(), "regions.gpkg")
	buildFixtureGPKG(t, path)
	insertMetadataRow(t, path, "text/plain", "Source: X | License: CC-BY-4.0 | Attribution: Y")

	repo := NewRepository(Options{})
	t.Cleanup(func() { _ = repo.Close(context.Background(), "regions") })
	src, err := repo.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if !src.License.IsEmpty() {
		t.Errorf("License = %+v, want empty (plain text must not be parsed as license)", src.License)
	}
	if src.Metadata.Description != "Source: X | License: CC-BY-4.0 | Attribution: Y" {
		t.Errorf("Description = %q, want the plain-text blob", src.Metadata.Description)
	}
}
