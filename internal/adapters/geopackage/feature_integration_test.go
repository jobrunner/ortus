package geopackage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
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
//
// "keyed" has an integer primary key that is not called fid (as a package
// exported from a scratch SpatiaLite table has), "nokey" has no primary key at
// all and so nothing to address a feature by.
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
		{`INSERT INTO gpkg_contents VALUES ('parks','features','parks','tiled parks',0,0,6,2,4326)`, nil},
		{`INSERT INTO gpkg_contents VALUES ('keyed','features','keyed','key not called fid',0,0,2,1,4326)`, nil},
		{`INSERT INTO gpkg_contents VALUES ('nokey','features','nokey','no primary key',0,0,1,1,4326)`, nil},
		{`INSERT INTO gpkg_geometry_columns VALUES ('parks','geom','MULTIPOLYGON',4326,0,0)`, nil},
		{`INSERT INTO gpkg_geometry_columns VALUES ('keyed','geom','POLYGON',4326,0,0)`, nil},
		{`INSERT INTO gpkg_geometry_columns VALUES ('nokey','geom','POLYGON',4326,0,0)`, nil},
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
		// fid 3 and 4 are fragments with identical attributes and no orig_id.
		{"fragments grouped by attributes", regions, "regions", "regions", 3, "tiled", 8, 1},
		{"second fragment yields the same feature", regions, "regions", "regions", 4, "tiled", 8, 1},
		// borderA touches borderB but has different attributes.
		{"neighbor with other attributes stays apart", regions, "regions", "regions", 5, "borderA", 4, 1},
		// tile differs between the fragments, orig_id ties them together.
		{"fragments grouped by orig_id", parks, "parks", "parks", 1, "moor", 4, 1},
		{"null orig_id falls back to attributes", parks, "parks", "parks", 3, "solo", 1, 1},
		// The key column is called rowid; the fragments share their attributes.
		{"key column not called fid", parks, "parks", "keyed", 2, "strip", 2, 1},
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
		want := domain.Extent{MinX: 12, MinY: 0, MaxX: 14, MaxY: 4, SRID: 4326}
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
			MaxX: r * 14 * rad, MaxY: r * math.Log(math.Tan(math.Pi/4+4*rad/2)),
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
		if math.Abs(ext.MinX-12) > 1e-6 || math.Abs(ext.MaxX-14) > 1e-6 ||
			math.Abs(ext.MinY-0) > 1e-6 || math.Abs(ext.MaxY-4) > 1e-6 {
			t.Errorf("extent = %+v, want (12,0)-(14,4)", ext)
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
	want := map[string]string{"parks": "fid", "keyed": "rowid", "nokey": ""}

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
