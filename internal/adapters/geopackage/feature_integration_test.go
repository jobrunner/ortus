package geopackage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
)

// buildParksGPKG creates a GeoPackage "parks" with two polygon layers (SRID 4326):
//
// "parks" models a package tiled with ST_Subdivide that carries the orig_id
// grouping column. The fragments of one feature differ in an attribute (tile),
// so only orig_id ties them together:
//
//	fid 1: orig_id=7    tile=1 name="moor"  (0,0)-(1,2)
//	fid 2: orig_id=7    tile=2 name="moor"  (1,0)-(2,2)
//	fid 3: orig_id=NULL tile=1 name="solo"  (5,0)-(6,1)
//	fid 4: orig_id=NULL tile=1 name="solo"  (8,0)-(9,1)   same attributes as fid 3
//
// "keyed" has an integer primary key that is not called fid (as a package
// exported from a scratch SpatiaLite table has), "nokey" has no primary key at
// all and so nothing to address a feature by. "nocase" holds two adjoining
// features whose names differ only in case, in a COLLATE NOCASE column, keyed 0
// and -5. "huts" is a point layer and "blank" a polygon layer whose only row has
// no geometry. "derived" has a generated column.
func buildParksGPKG(t *testing.T, path string) {
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

	geomExpr := "AsGPB(GeomFromText(?, 4326))"
	if _, err := db.ExecContext(ctx, "SELECT AsGPB(GeomFromText('POINT(0 0)', 4326))"); err != nil {
		geomExpr = "GeomFromText(?, 4326)"
	}

	stmts := []struct {
		sql  string
		args []any
	}{
		{`CREATE TABLE gpkg_contents (
			table_name TEXT PRIMARY KEY, data_type TEXT, identifier TEXT,
			description TEXT, min_x DOUBLE, min_y DOUBLE, max_x DOUBLE, max_y DOUBLE,
			srs_id INTEGER)`, nil},
		{`CREATE TABLE gpkg_geometry_columns (
			table_name TEXT, column_name TEXT, geometry_type_name TEXT,
			srs_id INTEGER, z TINYINT, m TINYINT)`, nil},
		{`CREATE TABLE parks (fid INTEGER PRIMARY KEY AUTOINCREMENT, orig_id INTEGER, tile INTEGER, name TEXT, geom BLOB)`, nil},
		{`CREATE TABLE keyed ("rowid" INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, geom BLOB)`, nil},
		{`CREATE TABLE nokey (name TEXT, geom BLOB)`, nil},
		{`CREATE TABLE derived (fid INTEGER PRIMARY KEY, name TEXT,
			label TEXT GENERATED ALWAYS AS (upper(name)) VIRTUAL, geom BLOB)`, nil},
		{`INSERT INTO gpkg_contents VALUES ('derived','features','derived','generated column',0,0,1,1,4326)`, nil},
		{`INSERT INTO gpkg_geometry_columns VALUES ('derived','geom','POLYGON',4326,0,0)`, nil},
		{`CREATE TABLE huts (fid INTEGER PRIMARY KEY, name TEXT, geom BLOB)`, nil},
		{`CREATE TABLE blank (fid INTEGER PRIMARY KEY, name TEXT, geom BLOB)`, nil},
		{`CREATE TABLE nocase (fid INTEGER PRIMARY KEY, name TEXT COLLATE NOCASE, geom BLOB)`, nil},
		{`CREATE INDEX idx_parks_orig_id ON parks (orig_id)`, nil},
		{`INSERT INTO gpkg_contents VALUES ('parks','features','parks','tiled parks',0,0,6,2,4326)`, nil},
		{`INSERT INTO gpkg_contents VALUES ('keyed','features','keyed','key not called fid',0,0,2,1,4326)`, nil},
		{`INSERT INTO gpkg_contents VALUES ('nokey','features','nokey','no primary key',0,0,1,1,4326)`, nil},
		{`INSERT INTO gpkg_contents VALUES ('huts','features','huts','point layer',0,0,1,1,4326)`, nil},
		{`INSERT INTO gpkg_contents VALUES ('blank','features','blank','row without geometry',0,0,1,1,4326)`, nil},
		{`INSERT INTO gpkg_contents VALUES ('nocase','features','nocase','case-insensitive column',0,0,2,1,4326)`, nil},
		{`INSERT INTO gpkg_geometry_columns VALUES ('parks','geom','MULTIPOLYGON',4326,0,0)`, nil},
		{`INSERT INTO gpkg_geometry_columns VALUES ('keyed','geom','POLYGON',4326,0,0)`, nil},
		{`INSERT INTO gpkg_geometry_columns VALUES ('nokey','geom','POLYGON',4326,0,0)`, nil},
		{`INSERT INTO gpkg_geometry_columns VALUES ('huts','geom','POINT',4326,0,0)`, nil},
		{`INSERT INTO gpkg_geometry_columns VALUES ('blank','geom','POLYGON',4326,0,0)`, nil},
		{`INSERT INTO huts (name, geom) VALUES ('hut', NULL)`, nil},
		{`INSERT INTO blank (name, geom) VALUES ('nothing', NULL)`, nil},
		{`INSERT INTO gpkg_geometry_columns VALUES ('nocase','geom','POLYGON',4326,0,0)`, nil},
		{`INSERT INTO parks (orig_id, tile, name, geom) VALUES (7, 1, 'moor', ` + geomExpr + `)`,
			[]any{"POLYGON((0 0, 1 0, 1 2, 0 2, 0 0))"}},
		{`INSERT INTO parks (orig_id, tile, name, geom) VALUES (7, 2, 'moor', ` + geomExpr + `)`,
			[]any{"POLYGON((1 0, 2 0, 2 2, 1 2, 1 0))"}},
		{`INSERT INTO parks (orig_id, tile, name, geom) VALUES (NULL, 1, 'solo', ` + geomExpr + `)`,
			[]any{"POLYGON((5 0, 6 0, 6 1, 5 1, 5 0))"}},
		{`INSERT INTO keyed (name, geom) VALUES ('strip', ` + geomExpr + `)`,
			[]any{"POLYGON((0 0, 1 0, 1 1, 0 1, 0 0))"}},
		{`INSERT INTO keyed (name, geom) VALUES ('strip', ` + geomExpr + `)`,
			[]any{"POLYGON((1 0, 2 0, 2 1, 1 1, 1 0))"}},
		// Keys 0 and -5: SQLite accepts any signed 64-bit integer as a key.
		{`INSERT INTO nocase (fid, name, geom) VALUES (0, 'Moor', ` + geomExpr + `)`,
			[]any{"POLYGON((0 0, 1 0, 1 1, 0 1, 0 0))"}},
		{`INSERT INTO nocase (fid, name, geom) VALUES (-5, 'moor', ` + geomExpr + `)`,
			[]any{"POLYGON((1 0, 2 0, 2 1, 1 1, 1 0))"}},
		{`INSERT INTO parks (orig_id, tile, name, geom) VALUES (NULL, 1, 'solo', ` + geomExpr + `)`,
			[]any{"POLYGON((8 0, 9 0, 9 1, 8 1, 8 0))"}},
		{`INSERT INTO derived (name, geom) VALUES ('fen', ` + geomExpr + `)`,
			[]any{"POLYGON((0 0, 1 0, 1 1, 0 1, 0 0))"}},
		{`INSERT INTO nokey (name, geom) VALUES ('x', ` + geomExpr + `)`,
			[]any{"POLYGON((0 0, 1 0, 1 1, 0 1, 0 0))"}},
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s.sql, s.args...); err != nil {
			t.Fatalf("fixture statement failed: %v\nSQL: %s", err, s.sql)
		}
	}
}

func newParksRepo(t *testing.T) *Repository {
	t.Helper()
	path := filepath.Join(t.TempDir(), "parks.gpkg")
	buildParksGPKG(t, path)

	repo := NewRepository(Options{})
	t.Cleanup(func() { _ = repo.Close(context.Background(), "parks") })
	if _, err := repo.Open(context.Background(), path); err != nil {
		t.Fatalf("Open: %v", err)
	}
	return repo
}

// outline describes a WKB geometry as SpatiaLite sees it.
type outline struct {
	typ   string
	parts int
	holes int
	area  float64
	srid  int
}

func describeWKB(t *testing.T, tr *RepositoryTransformer, g domain.Geometry) outline {
	t.Helper()
	var o outline
	err := tr.db.QueryRowContext(context.Background(), `
		WITH x(g) AS MATERIALIZED (SELECT GeomFromWKB(?, ?))
		SELECT GeometryType(g), NumGeometries(g), NumInteriorRings(GeometryN(g, 1)), ST_Area(g), SRID(g) FROM x`,
		g.WKB, g.SRID).Scan(&o.typ, &o.parts, &o.holes, &o.area, &o.srid)
	if err != nil {
		t.Fatalf("describing WKB: %v", err)
	}
	return o
}

func TestIntegration_ReadFeature(t *testing.T) {
	regions, _ := newFixtureRepo(t)
	parks := newParksRepo(t)
	tr := newTestTransformer(t)
	ctx := context.Background()

	tests := []struct {
		name      string
		repo      *Repository
		source    string
		layer     string
		fid       int64
		wantName  string
		wantArea  float64
		wantParts int
	}{
		{"untiled feature", regions, "regions", "regions", 1, "west", 16, 1},
		// fid 3 and 4 share all attributes, but the layer has no orig_id: nothing
		// says they are one feature, so each is served on its own.
		{"same attributes without orig_id stay apart", regions, "regions", "regions", 3, "tiled", 4, 1},
		{"each such row is its own feature", regions, "regions", "regions", 4, "tiled", 4, 1},
		// borderA touches borderB but has different attributes.
		{"neighbor with other attributes stays apart", regions, "regions", "regions", 5, "borderA", 4, 1},
		// tile differs between the fragments, orig_id ties them together.
		{"fragments grouped by orig_id", parks, "parks", "parks", 1, "moor", 4, 1},
		{"both fragments yield the same feature", parks, "parks", "parks", 2, "moor", 4, 1},
		{"null orig_id is the row itself", parks, "parks", "parks", 3, "solo", 1, 1},
		{"key column not called fid", parks, "parks", "keyed", 2, "strip", 1, 1},
		// Zero and negative keys are as addressable as any other.
		{"zero key", parks, "parks", "nocase", 0, "Moor", 1, 1},
		{"negative key", parks, "parks", "nocase", -5, "moor", 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := tt.repo.ReadFeature(ctx, tt.source, tt.layer, tt.fid)
			if err != nil {
				t.Fatalf("ReadFeature: %v", err)
			}
			if f.ID != tt.fid || f.LayerName != tt.layer {
				t.Errorf("feature = (%d, %q), want (%d, %q)", f.ID, f.LayerName, tt.fid, tt.layer)
			}
			if got := f.Properties["name"]; got != tt.wantName {
				t.Errorf("name = %v, want %q", got, tt.wantName)
			}
			for _, col := range []string{"geom", "fid", "rowid"} {
				if _, leaked := f.Properties[col]; leaked {
					t.Errorf("column %q leaked into properties", col)
				}
			}
			if f.Geometry.SRID != 4326 {
				t.Errorf("SRID = %d, want 4326", f.Geometry.SRID)
			}
			o := describeWKB(t, tr, f.Geometry)
			if o.typ != "MULTIPOLYGON" {
				t.Errorf("type = %q, want MULTIPOLYGON", o.typ)
			}
			// One part without holes: the cut edge between fragments is dissolved.
			if o.parts != tt.wantParts || o.holes != 0 {
				t.Errorf("parts = %d, holes = %d, want %d part(s) without holes", o.parts, o.holes, tt.wantParts)
			}
			if math.Abs(o.area-tt.wantArea) > 1e-9 {
				t.Errorf("area = %g, want %g", o.area, tt.wantArea)
			}
		})
	}
}

func TestIntegration_ReadFeatureErrors(t *testing.T) {
	regions, _ := newFixtureRepo(t)
	parks := newParksRepo(t)
	ctx := context.Background()

	tests := []struct {
		name   string
		repo   *Repository
		source string
		layer  string
		fid    int64
		want   error
	}{
		{"unknown source", regions, "nope", "regions", 1, domain.ErrSourceNotFound},
		{"unknown layer", regions, "regions", "nope", 1, domain.ErrLayerNotFound},
		{"unknown fid", regions, "regions", "regions", 999, domain.ErrFeatureNotFound},
		{"layer without primary key", parks, "parks", "nokey", 1, domain.ErrUnsupported},
		{"point layer", parks, "parks", "huts", 1, domain.ErrUnsupported},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.repo.ReadFeature(ctx, tt.source, tt.layer, tt.fid)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

// geoJSONGeometry is the part of a GeoJSON geometry object the tests inspect.
type geoJSONGeometry struct {
	Type        string          `json:"type"`
	Coordinates [][][][]float64 `json:"coordinates"`
}

func TestIntegration_ProjectGeometry(t *testing.T) {
	regions, _ := newFixtureRepo(t)
	tr := newTestTransformer(t)
	ctx := context.Background()

	f, err := regions.ReadFeature(ctx, "regions", "regions", 3)
	if err != nil {
		t.Fatalf("ReadFeature: %v", err)
	}

	t.Run("same srid keeps coordinates", func(t *testing.T) {
		raw, ext, err := tr.ProjectGeometry(ctx, f.Geometry.WKB, 4326, 4326)
		if err != nil {
			t.Fatalf("ProjectGeometry: %v", err)
		}
		want := domain.Extent{MinX: 12, MinY: 0, MaxX: 13, MaxY: 4, SRID: 4326}
		if ext != want {
			t.Errorf("extent = %+v, want %+v", ext, want)
		}
		var g geoJSONGeometry
		if err := json.Unmarshal(raw, &g); err != nil {
			t.Fatalf("geometry is not valid GeoJSON: %v\n%s", err, raw)
		}
		if g.Type != "MultiPolygon" || len(g.Coordinates) != 1 || len(g.Coordinates[0]) != 1 {
			t.Errorf("geometry = %s, want one MultiPolygon part without holes", raw)
		}
	})

	t.Run("reprojects geometry and box", func(t *testing.T) {
		raw, ext, err := tr.ProjectGeometry(ctx, f.Geometry.WKB, 4326, 3857)
		if err != nil {
			t.Fatalf("ProjectGeometry: %v", err)
		}
		// Web Mercator: x = R·λ, and y(4°) from the Mercator formula.
		const r = 6378137.0
		rad := math.Pi / 180
		want := domain.Extent{
			MinX: r * 12 * rad, MinY: 0,
			MaxX: r * 13 * rad, MaxY: r * math.Log(math.Tan(math.Pi/4+4*rad/2)),
			SRID: 3857,
		}
		if ext.SRID != want.SRID ||
			math.Abs(ext.MinX-want.MinX) > 0.01 || math.Abs(ext.MinY-want.MinY) > 0.01 ||
			math.Abs(ext.MaxX-want.MaxX) > 0.01 || math.Abs(ext.MaxY-want.MaxY) > 0.01 {
			t.Errorf("extent = %+v, want %+v", ext, want)
		}
		var g geoJSONGeometry
		if err := json.Unmarshal(raw, &g); err != nil {
			t.Fatalf("geometry is not valid GeoJSON: %v\n%s", err, raw)
		}
		if g.Type != "MultiPolygon" {
			t.Errorf("type = %q, want MultiPolygon", g.Type)
		}
	})

	t.Run("round trip returns to the source box", func(t *testing.T) {
		var wkb []byte
		if err := tr.db.QueryRowContext(ctx,
			`SELECT AsBinary(Transform(GeomFromWKB(?, 4326), 25832))`, f.Geometry.WKB).Scan(&wkb); err != nil {
			t.Fatalf("preparing UTM geometry: %v", err)
		}
		_, ext, err := tr.ProjectGeometry(ctx, wkb, 25832, 4326)
		if err != nil {
			t.Fatalf("ProjectGeometry: %v", err)
		}
		// The UTM edges are not meridians/parallels, so the box only matches
		// to the precision of the corner coordinates.
		if math.Abs(ext.MinX-12) > 1e-6 || math.Abs(ext.MaxX-13) > 1e-6 ||
			math.Abs(ext.MinY-0) > 1e-6 || math.Abs(ext.MaxY-4) > 1e-6 {
			t.Errorf("extent = %+v, want (12,0)-(13,4)", ext)
		}
	})

	t.Run("garbage is an error", func(t *testing.T) {
		if _, _, err := tr.ProjectGeometry(ctx, []byte("not wkb"), 4326, 3857); err == nil {
			t.Fatal("want error for an unparsable geometry")
		}
	})
}

// A point query must return an id that ReadFeature can redeem, also when the
// layer's key is not called fid. Before, such a layer reported id 0 and listed
// the key among the attributes — which also kept the fragments of one feature
// from being deduplicated, since each carries a different key.
func TestIntegration_QueryReturnsPrimaryKeyAsID(t *testing.T) {
	parks := newParksRepo(t)
	ctx := context.Background()

	// x=1 is the cut edge between the two "strip" fragments (keys 1 and 2).
	for name, query := range map[string]func() ([]domain.Feature, error){
		"point": func() ([]domain.Feature, error) {
			return parks.QueryPoint(ctx, "parks", "keyed", domain.NewWGS84Coordinate(1, 0.5))
		},
		"batch": func() ([]domain.Feature, error) {
			res, err := parks.QueryPoints(ctx, "parks", "keyed", []domain.Coordinate{domain.NewWGS84Coordinate(1, 0.5)})
			if err != nil {
				return nil, err
			}
			return res[0], nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			features, err := query()
			if err != nil {
				t.Fatalf("query: %v", err)
			}
			if len(features) != 1 {
				t.Fatalf("got %d features, want the two fragments collapsed into one: %+v", len(features), features)
			}
			f := features[0]
			if f.ID != 1 {
				t.Errorf("id = %d, want 1 (the smallest key of the fragments)", f.ID)
			}
			if _, leaked := f.Properties["rowid"]; leaked {
				t.Errorf("key column listed as an attribute: %v", f.Properties)
			}
			if _, err := parks.ReadFeature(ctx, "parks", "keyed", f.ID); err != nil {
				t.Errorf("ReadFeature(%d): %v", f.ID, err)
			}
		})
	}
}

// A layer without a primary key keeps the old convention: a column called fid,
// if there is one, is the id.
func TestIntegration_OpenReadsFeatureIDColumn(t *testing.T) {
	parks := newParksRepo(t)
	want := map[string]string{
		"parks": "fid", "keyed": "rowid", "nokey": "", "nocase": "fid", "huts": "fid", "blank": "fid",
		"derived": "fid",
	}

	layers, err := parks.GetLayers(context.Background(), "parks")
	if err != nil {
		t.Fatalf("GetLayers: %v", err)
	}
	for _, l := range layers {
		if got, known := want[l.Name]; !known || l.IDColumn != got {
			t.Errorf("layer %q: IDColumn = %q, want %q", l.Name, l.IDColumn, got)
		}
	}
}

// A row without geometry has no outline to return. That is a fault of the data,
// reported as a query error naming the layer — not an empty geometry, and not
// "feature not found", since the feature exists.
func TestIntegration_ReadFeatureWithoutGeometry(t *testing.T) {
	parks := newParksRepo(t)

	_, err := parks.ReadFeature(context.Background(), "parks", "blank", 1)
	var qe *domain.QueryError
	if !errors.As(err, &qe) || qe.Layer != "blank" {
		t.Fatalf("err = %v, want a QueryError for layer blank", err)
	}
	if errors.Is(err, domain.ErrFeatureNotFound) {
		t.Errorf("err = %v, must not read as feature not found", err)
	}
}

// A canceled request must surface as a canceled context, so the HTTP layer
// answers 499 instead of blaming the server.
func TestIntegration_ReadFeatureCanceled(t *testing.T) {
	regions, _ := newFixtureRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := regions.ReadFeature(ctx, "regions", "regions", 1)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestIntegration_ProjectGeometryFailures(t *testing.T) {
	regions, _ := newFixtureRepo(t)
	f, err := regions.ReadFeature(context.Background(), "regions", "regions", 1)
	if err != nil {
		t.Fatalf("ReadFeature: %v", err)
	}

	t.Run("canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, err := newTestTransformer(t).ProjectGeometry(ctx, f.Geometry.WKB, 4326, 3857); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	})

	t.Run("no database", func(t *testing.T) {
		tr := &RepositoryTransformer{}
		tr.SetTracer(nil)
		tr.SetMeter(nil)
		if _, _, err := tr.ProjectGeometry(context.Background(), f.Geometry.WKB, 4326, 3857); err == nil {
			t.Fatal("want an error from a transformer without a database")
		}
	})
}

func TestIDColumnOrDefault(t *testing.T) {
	if got := idColumnOrDefault(""); got != "fid" {
		t.Errorf(`idColumnOrDefault("") = %q, want fid`, got)
	}
	if got := idColumnOrDefault("rowid"); got != "rowid" {
		t.Errorf(`idColumnOrDefault("rowid") = %q, want rowid`, got)
	}
}

// The fragments of a feature must be found by lookup, not by scanning the layer:
// a tiled package holds hundreds of thousands of rows (Natura 2000: 689k), and a
// scan per request is what the orig_id index the package ships is there to
// avoid. Pin the plan, so a rewrite of the statement that defeats the index
// (the earlier attribute fallback did) fails here instead of in production.
func TestIntegration_FeatureQueryUsesOrigIDIndex(t *testing.T) {
	parks := newParksRepo(t)
	ctx := context.Background()

	parks.mu.RLock()
	db := parks.connections["parks"]
	src := parks.sources["parks"]
	parks.mu.RUnlock()
	layer, ok := src.GetLayer("parks")
	if !ok {
		t.Fatal("parks layer missing")
	}
	columns, idColumn, err := tableColumnNames(ctx, db, layer.Name)
	if err != nil {
		t.Fatalf("tableColumnNames: %v", err)
	}

	rows, err := db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+buildFeatureQuery(layer, columns, idColumn), 1)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var plan []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan plan row: %v", err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("plan rows: %v", err)
	}

	full := strings.Join(plan, "\n")
	if !strings.Contains(full, "idx_parks_orig_id") {
		t.Errorf("plan does not use the orig_id index:\n%s", full)
	}
	for _, step := range plan {
		if strings.HasPrefix(step, "SCAN") {
			t.Errorf("plan scans a table (%q); full plan:\n%s", step, full)
		}
	}
}

// fixtureDB returns the open connection and the regions layer of the fixture.
func fixtureDB(t *testing.T) (*sql.DB, *domain.Layer) {
	t.Helper()
	repo, src := newFixtureRepo(t)
	repo.mu.RLock()
	db := repo.connections[src.ID]
	repo.mu.RUnlock()
	layer, ok := src.GetLayer("regions")
	if !ok {
		t.Fatal("regions layer missing")
	}
	return db, layer
}

// Every way the outline statement can fail must come back as a QueryError that
// names the layer and keeps the cause — the HTTP layer turns that into a 500
// (or a 499/504 for a canceled or timed-out request), never into "not found".
func TestIntegration_ScanFeatureOutlineFailures(t *testing.T) {
	db, layer := fixtureDB(t)
	ctx := context.Background()
	columns := []string{"fid", "name", "pop", "geom"}

	tests := []struct {
		name  string
		query string
	}{
		// Rejected when the statement is prepared.
		{"statement does not compile", `SELECT nope FROM missing WHERE fid = ?`},
		// Compiles, fails while producing the first row (integer overflow). The
		// driver reports that from the query call, not from the iteration.
		{"statement fails while running", `SELECT abs(-9223372036854775807 - ?)`},
		// Runs, but the row has not the shape the layer's columns announce.
		{"row has an unexpected shape", `SELECT ? AS only_one_column`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := scanFeatureOutline(ctx, db, tt.query, layer, columns, "fid", 1)
			var qe *domain.QueryError
			if !errors.As(err, &qe) || qe.Layer != "regions" || qe.Err == nil {
				t.Fatalf("err = %v, want a QueryError for layer regions with a cause", err)
			}
			if errors.Is(err, domain.ErrFeatureNotFound) {
				t.Errorf("err = %v, must not read as feature not found", err)
			}
		})
	}
}

func TestIntegration_ScanValues(t *testing.T) {
	db, _ := fixtureDB(t)
	rows, err := db.QueryContext(context.Background(), `SELECT 7, 'x', NULL`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		t.Fatal("no row")
	}

	if _, err := scanValues(rows, 2); err == nil {
		t.Error("want an error when the row has more columns than asked for")
	}
	values, err := scanValues(rows, 3)
	if err != nil {
		t.Fatalf("scanValues: %v", err)
	}
	if values[0] != int64(7) || values[1] != "x" || values[2] != nil {
		t.Errorf("values = %#v, want [7 x <nil>]", values)
	}
}

func TestIntegration_ForEachRow(t *testing.T) {
	db, _ := fixtureDB(t)
	ctx := context.Background()
	query := func() *sql.Rows {
		rows, err := db.QueryContext(ctx, `SELECT fid, name FROM regions ORDER BY fid LIMIT 3`)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		t.Cleanup(func() { _ = rows.Close() })
		return rows
	}

	var names []string
	if err := forEachRow(query(), 2, func(row []interface{}) { names = append(names, row[1].(string)) }); err != nil {
		t.Fatalf("forEachRow: %v", err)
	}
	if strings.Join(names, ",") != "west,east,tiled" {
		t.Errorf("names = %v, want [west east tiled]", names)
	}

	// A row of another shape stops the iteration instead of being half-read.
	calls := 0
	if err := forEachRow(query(), 5, func([]interface{}) { calls++ }); err == nil || calls != 0 {
		t.Errorf("err = %v after %d call(s), want an error before the first call", err, calls)
	}
}

// No row can mean two things, and they must not be confused: the feature does
// not exist (a 404), or reading failed (a server error that keeps its cause).
func TestNoRowError(t *testing.T) {
	if err := noRowError(nil, "regions"); !errors.Is(err, domain.ErrFeatureNotFound) {
		t.Errorf("err = %v, want ErrFeatureNotFound", err)
	}

	err := noRowError(context.DeadlineExceeded, "regions")
	var qe *domain.QueryError
	if !errors.As(err, &qe) || qe.Layer != "regions" || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want a QueryError for regions wrapping the cause", err)
	}
	if errors.Is(err, domain.ErrFeatureNotFound) {
		t.Errorf("err = %v, must not read as feature not found", err)
	}
}

// A layer whose table cannot be inspected has no known key — the load must not
// fail over it, the layer just has no addressable features.
func TestIntegration_PrimaryKeyColumn(t *testing.T) {
	db, _ := fixtureDB(t)

	if got := primaryKeyColumn(context.Background(), db, "regions"); got != "fid" {
		t.Errorf("primaryKeyColumn = %q, want fid", got)
	}
	if got := primaryKeyColumn(context.Background(), db, "no_such_table"); got != "" {
		t.Errorf("primaryKeyColumn of a missing table = %q, want empty", got)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if got := primaryKeyColumn(canceled, db, "regions"); got != "" {
		t.Errorf("primaryKeyColumn on a canceled context = %q, want empty", got)
	}
}

// A generated column is part of `SELECT *`, so it is a property a point query
// returns — and the outline statement, which selects `a.*` too, must expect it.
// pragma_table_info leaves generated columns out; reading the columns from it
// made the row one value longer than announced and the lookup fail.
func TestIntegration_ReadFeatureWithGeneratedColumn(t *testing.T) {
	parks := newParksRepo(t)
	ctx := context.Background()

	f, err := parks.ReadFeature(ctx, "parks", "derived", 1)
	if err != nil {
		t.Fatalf("ReadFeature: %v", err)
	}
	if f.Properties["name"] != "fen" || f.Properties["label"] != "FEN" {
		t.Errorf("properties = %v, want name=fen and the generated label=FEN", f.Properties)
	}
	if len(f.Geometry.WKB) == 0 {
		t.Error("no outline")
	}

	// The point query and the lookup agree on what the feature's properties are.
	hits, err := parks.QueryPoint(ctx, "parks", "derived", domain.NewWGS84Coordinate(0.5, 0.5))
	if err != nil || len(hits) != 1 {
		t.Fatalf("QueryPoint: %v, %d hit(s)", err, len(hits))
	}
	if hits[0].Properties["label"] != f.Properties["label"] || hits[0].ID != f.ID {
		t.Errorf("query hit %+v disagrees with the looked-up feature %+v", hits[0], f)
	}
}
