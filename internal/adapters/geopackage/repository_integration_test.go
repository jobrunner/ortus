package geopackage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
)

// buildFixtureGPKG creates a minimal but functionally valid GeoPackage at path
// with one polygon layer "regions" (geometry column "geom", SRID 4326) holding
// two disjoint squares:
//
//	west: (0,0)-(4,4)   name="west" pop=100
//	east: (6,0)-(10,4)  name="east" pop=200
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
			VALUES ('regions','features','regions','test regions',0,0,10,4,4326)`,
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
	if l.FeatureCount != 2 {
		t.Errorf("FeatureCount = %d, want 2", l.FeatureCount)
	}
	if l.Extent == nil || !l.Extent.IsValid() {
		t.Errorf("Extent = %+v, want a valid bbox", l.Extent)
	}
}

func TestIntegration_PointInPolygon_FallbackScan(t *testing.T) {
	// No spatial index created → exercises the full-table-scan ST_Contains path.
	repo, _ := newFixtureRepo(t)
	ctx := context.Background()

	cases := []struct {
		name      string
		coord     domain.Coordinate
		wantName  string // "" => expect no feature
		wantCount int
	}{
		{"inside west", domain.NewWGS84Coordinate(2, 2), "west", 1},
		{"inside east", domain.NewWGS84Coordinate(8, 2), "east", 1},
		{"in the gap", domain.NewWGS84Coordinate(5, 2), "", 0},
		{"outside all", domain.NewWGS84Coordinate(20, 20), "", 0},
		// ST_Contains is strict: a point on the boundary is not contained.
		{"on boundary", domain.NewWGS84Coordinate(0, 2), "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			features, err := repo.QueryPoint(ctx, "regions", "regions", tc.coord)
			if err != nil {
				t.Fatalf("QueryPoint: %v", err)
			}
			if len(features) != tc.wantCount {
				t.Fatalf("got %d features, want %d", len(features), tc.wantCount)
			}
			if tc.wantCount > 0 {
				got, _ := features[0].GetProperty("name")
				if got != tc.wantName {
					t.Errorf("name = %v, want %q", got, tc.wantName)
				}
			}
		})
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
