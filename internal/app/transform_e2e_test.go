package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/jobrunner/ortus/internal/config"
)

// TestQueryAcrossSRIDsEndToEnd is the end-to-end regression test for the
// transformer bug that hit production: /query with srid≠4326 or mgrs came back
// without wgs84, gazetteer and every source in a foreign SRID — and 4326 queries
// lost every non-4326 source — because the transformer's pooled ":memory:"
// connections lost their SpatiaLite metadata under concurrent load.
//
// It wires the real App (real SpatiaLite transformer, real GeoPackage sources in
// EPSG:4326 and EPSG:25832, the committed gazetteer fixture), fires a burst of
// parallel requests, and then requires every input form to return the full
// answer: wgs84, gazetteer and BOTH sources.
func TestQueryAcrossSRIDsEndToEnd(t *testing.T) {
	dir := t.TempDir()
	// Wertheim (in the gazetteer fixture). Both polygons contain the point.
	writeSquareGPKG(t, filepath.Join(dir, "wgs.gpkg"), 4326, 9.50, 49.74, 9.55, 49.78)
	writeSquareGPKG(t, filepath.Join(dir, "utm.gpkg"), 25832, 535000, 5510000, 540000, 5515000)

	cfg := &config.Config{}
	cfg.Storage.Type = config.StorageTypeLocal
	cfg.Storage.LocalPath = dir
	cfg.Metrics.Enabled = false
	cfg.Gazetteer.Enabled = true
	// A copy: opening the gazetteer through New writes to the GeoPackage, and the
	// committed fixture must stay untouched.
	cfg.Gazetteer.GeoPackagePath = filepath.Join(t.TempDir(), "gazetteer.gpkg")
	if err := os.WriteFile(cfg.Gazetteer.GeoPackagePath, mustRead(t, "testdata/gazetteer-fixture.gpkg"), 0o600); err != nil {
		t.Fatalf("copy gazetteer fixture: %v", err)
	}
	cfg.Gazetteer.ManifestPath = "testdata/gazetteer-manifest.yaml"
	cfg.Gazetteer.LevelReferencePath = "testdata/gazetteer-sidecar.yaml"
	cfg.Gazetteer.NameSourceManifestPath = "testdata/gazetteer-name-sources.yaml"

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a, err := New(ctx, cfg, logger)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Shutdown(context.Background()) })
	if err := a.Registry.LoadAll(ctx); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	h := a.HTTPServer.Router()

	queries := map[string]string{
		"wgs84": "/api/v1/query?lon=9.52316&lat=49.76028",
		"utm":   "/api/v1/query?x=537679.359&y=5512109.406&srid=25832",
		"mgrs":  "/api/v1/query?mgrs=32UNA3767912109",
	}

	// The burst that used to leave the pool with only uninitialized connections.
	var wg sync.WaitGroup
	for i := 0; i < 48; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, queries["utm"], nil))
		}()
	}
	wg.Wait()

	for name, q := range queries {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, q, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
			}
			var body struct {
				WGS84     map[string]float64 `json:"wgs84"`
				Gazetteer map[string]any     `json:"gazetteer"`
				Results   []struct {
					SourceID string `json:"source_id"`
				} `json:"results"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.WGS84 == nil {
				t.Error("wgs84 block missing")
			}
			if body.Gazetteer == nil {
				t.Error("gazetteer block missing")
			}
			ids := make([]string, 0, len(body.Results))
			for _, r := range body.Results {
				ids = append(ids, r.SourceID)
			}
			sort.Strings(ids)
			if fmt.Sprint(ids) != "[utm wgs]" {
				t.Errorf("sources = %v, want [utm wgs]", ids)
			}
		})
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	var health struct {
		Ready      bool              `json:"ready"`
		Components map[string]string `json:"components"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &health); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if !health.Ready || health.Components["transformer"] != "ok" {
		t.Errorf("health = %+v, want ready with components.transformer=ok", health)
	}
}

// writeSquareGPKG writes a minimal one-layer GeoPackage ("area") holding one
// axis-aligned square polygon in the given SRID.
func writeSquareGPKG(t *testing.T, path string, srid int, minX, minY, maxX, maxY float64) {
	t.Helper()
	db, err := sql.Open("sqlite3_with_extensions", "file:"+path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	geomExpr := fmt.Sprintf("AsGPB(GeomFromText(?, %d))", srid)
	if _, err := db.ExecContext(ctx, "SELECT AsGPB(GeomFromText('POINT(0 0)', 4326))"); err != nil {
		geomExpr = fmt.Sprintf("GeomFromText(?, %d)", srid)
	}
	wkt := fmt.Sprintf("POLYGON((%[1]f %[2]f, %[3]f %[2]f, %[3]f %[4]f, %[1]f %[4]f, %[1]f %[2]f))", minX, minY, maxX, maxY)
	stmts := []string{
		`CREATE TABLE gpkg_contents (
			table_name TEXT PRIMARY KEY, data_type TEXT, identifier TEXT,
			description TEXT, min_x DOUBLE, min_y DOUBLE, max_x DOUBLE, max_y DOUBLE,
			srs_id INTEGER)`,
		`CREATE TABLE gpkg_geometry_columns (
			table_name TEXT, column_name TEXT, geometry_type_name TEXT,
			srs_id INTEGER, z TINYINT, m TINYINT)`,
		`CREATE TABLE area (fid INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, geom BLOB)`,
		fmt.Sprintf(`INSERT INTO gpkg_contents VALUES ('area','features','area','',%f,%f,%f,%f,%d)`, minX, minY, maxX, maxY, srid),
		fmt.Sprintf(`INSERT INTO gpkg_geometry_columns VALUES ('area','geom','POLYGON',%d,0,0)`, srid),
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatalf("%s: %v\nSQL: %s", path, err, s)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO area (name, geom) VALUES (?, `+geomExpr+`)`, "square", wkt); err != nil {
		t.Fatalf("%s: insert: %v", path, err)
	}
}
