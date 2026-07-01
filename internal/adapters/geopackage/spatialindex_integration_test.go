package geopackage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// buildGazetteerFixture creates a minimal but valid gazetteer GeoPackage with a
// points layer "places" and a nested-polygon layer "admin_levels", matching the
// planned relational contract (places.admin_id, admin_levels.parent_id). When
// withRtree is true, R-tree indexes are built for both layers (the shape of the
// real file); otherwise the adapter's full-scan fallback is exercised.
//
//	places:   Metropolis(city,10.0,50.0,admin=3) Townsville(town,10.1,50.0,admin=99)
//	          Hamlet(village,10.02,50.0,admin=3)
//	admin:    1 L2 Country/DE (parent NULL)  ⊃  2 L4 Bavaria/DE (parent 1)
//	          ⊃  3 L8 "Metropolis Gemeinde"/DE (parent 2), all containing (10,50)
func buildGazetteerFixture(t *testing.T, path string, withRtree bool) {
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
	// Populate spatial_ref_sys so ellipsoidal Distance(g1,g2,1) can resolve the
	// SRID 4326 ellipsoid — the real ogr-built GeoPackage carries this via
	// gpkg_spatial_ref_sys; a bare fixture must initialize it explicitly.
	if _, err := db.ExecContext(ctx, "SELECT InitSpatialMetaData(1)"); err != nil {
		t.Skipf("InitSpatialMetaData unavailable: %v", err)
	}

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
		`CREATE TABLE places (fid INTEGER PRIMARY KEY AUTOINCREMENT,
			place TEXT, name TEXT, admin_id INTEGER, geom BLOB)`,
		`CREATE TABLE admin_levels (fid INTEGER PRIMARY KEY AUTOINCREMENT,
			parent_id INTEGER, admin_level TEXT, name TEXT, country_iso TEXT, geom BLOB)`,
		`INSERT INTO gpkg_contents (table_name, data_type, identifier, srs_id)
			VALUES ('places','features','places',4326), ('admin_levels','features','admin_levels',4326)`,
		`INSERT INTO gpkg_geometry_columns (table_name, column_name, geometry_type_name, srs_id, z, m)
			VALUES ('places','geom','POINT',4326,0,0), ('admin_levels','geom','POLYGON',4326,0,0)`,
	}
	for _, stmt := range ddl {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("fixture DDL failed: %v\nSQL: %s", err, stmt)
		}
	}

	placeIns := `INSERT INTO places (place, name, admin_id, geom) VALUES (?, ?, ?, ` + geomExpr + `)`
	places := []struct {
		place, name string
		admin       int
		wkt         string
	}{
		{"city", "Metropolis", 3, "POINT(10.0 50.0)"},
		{"town", "Townsville", 99, "POINT(10.1 50.0)"},
		{"village", "Hamlet", 3, "POINT(10.02 50.0)"},
	}
	for _, p := range places {
		if _, err := db.ExecContext(ctx, placeIns, p.place, p.name, p.admin, p.wkt); err != nil {
			t.Fatalf("place insert %s: %v", p.name, err)
		}
	}

	adminIns := `INSERT INTO admin_levels (parent_id, admin_level, name, country_iso, geom) VALUES (?, ?, ?, ?, ` + geomExpr + `)`
	admins := []struct {
		parent any
		level  string
		name   string
		wkt    string
	}{
		{nil, "2", "Country", "POLYGON((0 40, 20 40, 20 60, 0 60, 0 40))"},
		{1, "4", "Bavaria", "POLYGON((9 49, 11 49, 11 51, 9 51, 9 49))"},
		{2, "8", "Metropolis Gemeinde", "POLYGON((9.9 49.95, 10.05 49.95, 10.05 50.05, 9.9 50.05, 9.9 49.95))"},
	}
	for _, a := range admins {
		if _, err := db.ExecContext(ctx, adminIns, a.parent, a.level, a.name, "DE", a.wkt); err != nil {
			t.Fatalf("admin insert %s: %v", a.name, err)
		}
	}

	if withRtree {
		for _, layer := range []string{"places", "admin_levels"} {
			rt := "rtree_" + layer + "_geom"
			if _, err := db.ExecContext(ctx, `CREATE VIRTUAL TABLE "`+rt+`" USING rtree(id, minx, maxx, miny, maxy)`); err != nil {
				t.Fatalf("create rtree %s: %v", rt, err)
			}
			fill := `INSERT INTO "` + rt + `" SELECT fid,
				ST_MinX(CastAutomagic(geom)), ST_MaxX(CastAutomagic(geom)),
				ST_MinY(CastAutomagic(geom)), ST_MaxY(CastAutomagic(geom)) FROM "` + layer + `"`
			if _, err := db.ExecContext(ctx, fill); err != nil {
				t.Fatalf("fill rtree %s: %v", rt, err)
			}
		}
	}
}

func openFixtureIndex(t *testing.T, withRtree bool) *GazetteerIndex {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gazetteer.gpkg")
	buildGazetteerFixture(t, path, withRtree)
	idx, err := OpenGazetteerIndex(context.Background(), path, Options{})
	if err != nil {
		t.Fatalf("OpenGazetteerIndex: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	return idx
}

func names(features []domain.Feature) []string {
	out := make([]string, len(features))
	for i, f := range features {
		out[i] = f.GetStringProperty("name")
	}
	return out
}

func TestGazetteerIndex_QueryKNN(t *testing.T) {
	idx := openFixtureIndex(t, true)
	ctx := context.Background()
	p := domain.NewWGS84Coordinate(10.0, 50.0)

	t.Run("nearest first, all within radius", func(t *testing.T) {
		got, err := idx.QueryKNN(ctx, "places", p, 3, 60, nil)
		if err != nil {
			t.Fatalf("QueryKNN: %v", err)
		}
		want := []string{"Metropolis", "Hamlet", "Townsville"}
		if g := names(got); !equalStrings(g, want) {
			t.Errorf("order = %v, want %v", g, want)
		}
	})

	t.Run("class filter: city", func(t *testing.T) {
		got, err := idx.QueryKNN(ctx, "places", p, 1, 60, &output.Filter{Column: "place", Values: []any{"city"}})
		if err != nil {
			t.Fatalf("QueryKNN: %v", err)
		}
		if g := names(got); len(g) != 1 || g[0] != "Metropolis" {
			t.Errorf("city filter = %v, want [Metropolis]", g)
		}
	})

	t.Run("class filter: village", func(t *testing.T) {
		got, err := idx.QueryKNN(ctx, "places", p, 1, 60, &output.Filter{Column: "place", Values: []any{"village"}})
		if err != nil {
			t.Fatalf("QueryKNN: %v", err)
		}
		if g := names(got); len(g) != 1 || g[0] != "Hamlet" {
			t.Errorf("village filter = %v, want [Hamlet]", g)
		}
	})

	t.Run("admin boundary filter", func(t *testing.T) {
		got, err := idx.QueryKNN(ctx, "places", p, 5, 60, &output.Filter{Column: "admin_id", Values: []any{3}})
		if err != nil {
			t.Fatalf("QueryKNN: %v", err)
		}
		want := []string{"Metropolis", "Hamlet"} // Townsville has admin_id 99, excluded
		if g := names(got); !equalStrings(g, want) {
			t.Errorf("admin filter = %v, want %v", g, want)
		}
	})

	t.Run("radius excludes far town", func(t *testing.T) {
		got, err := idx.QueryKNN(ctx, "places", p, 1, 1, &output.Filter{Column: "place", Values: []any{"town"}})
		if err != nil {
			t.Fatalf("QueryKNN: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("town within 1km = %v, want none (Townsville ~7km)", names(got))
		}
	})
}

func TestGazetteerIndex_PointInPolygonNested(t *testing.T) {
	idx := openFixtureIndex(t, true)
	got, err := idx.PointInPolygon(context.Background(), "admin_levels", domain.NewWGS84Coordinate(10.0, 50.0))
	if err != nil {
		t.Fatalf("PointInPolygon: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d containing polygons, want 3 (nested L2/L4/L8): %v", len(got), names(got))
	}
}

func TestGazetteerIndex_ResolveChain(t *testing.T) {
	idx := openFixtureIndex(t, true)
	chain, err := idx.ResolveChain(context.Background(), "admin_levels", 3)
	if err != nil {
		t.Fatalf("ResolveChain: %v", err)
	}
	if len(chain) != 3 {
		t.Fatalf("chain length = %d, want 3", len(chain))
	}
	wantLevels := []int{8, 4, 2}   // most-local first
	wantParent := []int64{2, 1, 0} // top of chain has parent 0
	for i, r := range chain {
		if r.Level != wantLevels[i] {
			t.Errorf("chain[%d].Level = %d, want %d", i, r.Level, wantLevels[i])
		}
		if r.ParentFID != wantParent[i] {
			t.Errorf("chain[%d].ParentFID = %d, want %d", i, r.ParentFID, wantParent[i])
		}
		if r.CountryISO != "DE" {
			t.Errorf("chain[%d].CountryISO = %q, want DE", i, r.CountryISO)
		}
	}
}

func TestGazetteerIndex_DistanceAndAzimuth(t *testing.T) {
	idx := openFixtureIndex(t, true)
	metropolis := domain.NewWGS84Coordinate(10.0, 50.0)
	townsville := domain.NewWGS84Coordinate(10.1, 50.0)

	km, err := idx.DistanceKM(metropolis, townsville)
	if err != nil {
		t.Fatalf("DistanceKM: %v", err)
	}
	if km < 7.0 || km > 7.5 {
		t.Errorf("DistanceKM = %.3f, want ~7.2 km", km)
	}

	az, err := idx.Azimuth(metropolis, townsville)
	if err != nil {
		t.Fatalf("Azimuth: %v", err)
	}
	if az < 88 || az > 92 {
		t.Errorf("Azimuth = %.2f°, want ~90° (due east)", az)
	}
}

func TestGazetteerIndex_FallbackScanNoRtree(t *testing.T) {
	// No R-tree → exercises the full-scan branches of QueryKNN and PointInPolygon.
	idx := openFixtureIndex(t, false)
	ctx := context.Background()
	p := domain.NewWGS84Coordinate(10.0, 50.0)

	got, err := idx.QueryKNN(ctx, "places", p, 1, 60, nil)
	if err != nil {
		t.Fatalf("QueryKNN (no rtree): %v", err)
	}
	if g := names(got); len(g) != 1 || g[0] != "Metropolis" {
		t.Errorf("no-rtree KNN = %v, want [Metropolis]", g)
	}

	poly, err := idx.PointInPolygon(ctx, "admin_levels", p)
	if err != nil {
		t.Fatalf("PointInPolygon (no rtree): %v", err)
	}
	if len(poly) != 3 {
		t.Errorf("no-rtree PiP = %d, want 3", len(poly))
	}
}

func TestGazetteerIndex_VerifySRID(t *testing.T) {
	// The fixture initializes SpatiaLite metadata, so SRID 4326 resolves and
	// ellipsoidal Distance returns a real value.
	idx := openFixtureIndex(t, true)
	if err := idx.VerifySRID(context.Background()); err != nil {
		t.Errorf("VerifySRID on metadata-initialized fixture: %v", err)
	}
}

func TestGazetteerIndex_UnknownLayer(t *testing.T) {
	idx := openFixtureIndex(t, true)
	if _, err := idx.QueryKNN(context.Background(), "nope", domain.NewWGS84Coordinate(10, 50), 1, 10, nil); err != domain.ErrLayerNotFound {
		t.Errorf("QueryKNN unknown layer err = %v, want ErrLayerNotFound", err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
