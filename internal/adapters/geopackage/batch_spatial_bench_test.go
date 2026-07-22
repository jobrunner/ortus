package geopackage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
)

// These benchmarks measure the SPATIAL cost of resolving a batch of points against
// a polygon layer, to complement the HTTP-transport benchmark in the http package.
// Each iteration resolves the SAME batch of `batchPoints` points, so ns/op is the
// cost of one batch — comparing "N points via N point-queries" against "N points
// via one set-based SQL query".
//
//   - Serial / Concurrent run through the real GeoPackage adapter (adapter path).
//   - PerPointRaw / SetBased run raw SQL on a native SpatiaLite fixture (engine
//     level, correct ST_Covers), isolating the query-shape question.
//
// Self-contained; skips when SpatiaLite (or InitSpatialMetaData) is unavailable.
//
//	SPATIALITE_LIBRARY_PATH=$(ls /nix/store/*/lib/mod_spatialite*.dylib|head -1) \
//	go test -run=^$ -bench=BenchmarkBatchSpatial -benchmem ./internal/adapters/geopackage/
const (
	gridN       = 40   // 40x40 = 1600 polygons over lon/lat 0..40
	batchPoints = 1000 // points resolved per batch iteration
)

// genPoints returns deterministic points spread across the grid extent (an LCG,
// so no rand and stable across runs).
func genPoints() []domain.Coordinate {
	pts := make([]domain.Coordinate, batchPoints)
	seed := uint64(1)
	next := func() float64 {
		seed = seed*6364136223846793005 + 1442695040888963407
		return float64(seed>>33%uint64(gridN*1000)) / 1000.0
	}
	for k := range pts {
		pts[k] = domain.NewWGS84Coordinate(next(), next())
	}
	return pts
}

// buildGridGPKG writes an indexed 1°-cell polygon grid as a GeoPackage (for the
// adapter path).
func buildGridGPKG(b *testing.B, path string) {
	b.Helper()
	db, err := sql.Open("sqlite3_with_extensions", "file:"+path+"?cache=shared")
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	var v string
	if err := db.QueryRowContext(ctx, "SELECT spatialite_version()").Scan(&v); err != nil {
		b.Skipf("SpatiaLite unavailable: %v", err)
	}
	geom := "AsGPB(GeomFromText(?, 4326))"
	if _, err := db.ExecContext(ctx, "SELECT AsGPB(GeomFromText('POINT(0 0)',4326))"); err != nil {
		geom = "GeomFromText(?, 4326)"
	}
	for _, stmt := range []string{
		`CREATE TABLE gpkg_contents (table_name TEXT PRIMARY KEY, data_type TEXT, identifier TEXT, description TEXT, min_x DOUBLE, min_y DOUBLE, max_x DOUBLE, max_y DOUBLE, srs_id INTEGER)`,
		`CREATE TABLE gpkg_geometry_columns (table_name TEXT, column_name TEXT, geometry_type_name TEXT, srs_id INTEGER, z TINYINT, m TINYINT)`,
		`CREATE TABLE grid (fid INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, geom BLOB)`,
		fmt.Sprintf(`INSERT INTO gpkg_contents VALUES ('grid','features','grid','grid',0,0,%d,%d,4326)`, gridN, gridN),
		`INSERT INTO gpkg_geometry_columns VALUES ('grid','geom','POLYGON',4326,0,0)`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			b.Fatalf("ddl: %v\n%s", err, stmt)
		}
	}
	insertGrid(b, db, `INSERT INTO grid (name, geom) VALUES (?, `+geom+`)`)
}

// buildNativeGrid writes the same grid as a NATIVE SpatiaLite spatial table with a
// SpatiaLite R-tree (idx_grid_geom), so raw ST_Covers works without gpkg casts.
func buildNativeGrid(b *testing.B, path string) *sql.DB {
	b.Helper()
	db, err := sql.Open("sqlite3_with_extensions", "file:"+path+"?cache=shared")
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	ctx := context.Background()
	var v string
	if err := db.QueryRowContext(ctx, "SELECT spatialite_version()").Scan(&v); err != nil {
		_ = db.Close()
		b.Skipf("SpatiaLite unavailable: %v", err)
	}
	if _, err := db.ExecContext(ctx, "SELECT InitSpatialMetaData(1)"); err != nil {
		_ = db.Close()
		b.Skipf("InitSpatialMetaData unavailable: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE grid (fid INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT)`,
		`SELECT AddGeometryColumn('grid','geom',4326,'POLYGON','XY')`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			_ = db.Close()
			b.Skipf("native spatial setup unavailable: %v", err)
		}
	}
	insertGrid(b, db, `INSERT INTO grid (name, geom) VALUES (?, GeomFromText(?, 4326))`)
	if _, err := db.ExecContext(ctx, "SELECT CreateSpatialIndex('grid','geom')"); err != nil {
		_ = db.Close()
		b.Skipf("CreateSpatialIndex unavailable: %v", err)
	}
	return db
}

func insertGrid(b *testing.B, db *sql.DB, insert string) {
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatalf("begin tx: %v", err)
	}
	for i := 0; i < gridN; i++ {
		for j := 0; j < gridN; j++ {
			wkt := fmt.Sprintf("POLYGON((%d %d, %d %d, %d %d, %d %d, %d %d))", i, j, i+1, j, i+1, j+1, i, j+1, i, j)
			if _, err := tx.ExecContext(ctx, insert, fmt.Sprintf("c%d_%d", i, j), wkt); err != nil {
				b.Fatalf("insert: %v", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatalf("commit: %v", err)
	}
}

// BenchmarkBatchSpatialSerial: batchPoints points through the adapter's QueryPoint,
// one at a time — today's path (one HTTP request per point maps onto this).
func BenchmarkBatchSpatialSerial(b *testing.B) {
	path := b.TempDir() + "/grid.gpkg"
	buildGridGPKG(b, path)
	pts := genPoints()
	repo := NewRepository(Options{})
	ctx := context.Background()
	src, err := repo.Open(ctx, path)
	if err != nil {
		b.Skipf("open: %v", err)
	}
	b.Cleanup(func() { _ = repo.Close(ctx, src.ID) })
	if err := repo.Prepare(ctx, src.ID, "grid"); err != nil {
		b.Fatalf("prepare index: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, p := range pts {
			if _, err := repo.QueryPoint(ctx, src.ID, "grid", p); err != nil {
				b.Fatalf("query: %v", err)
			}
		}
	}
	perPoint(b)
}

// BenchmarkBatchSpatialConcurrent: the same batch across 8 workers sharing the repo
// — the naive server-side parallelism approach.
func BenchmarkBatchSpatialConcurrent(b *testing.B) {
	path := b.TempDir() + "/grid.gpkg"
	buildGridGPKG(b, path)
	pts := genPoints()
	repo := NewRepository(Options{})
	ctx := context.Background()
	src, err := repo.Open(ctx, path)
	if err != nil {
		b.Skipf("open: %v", err)
	}
	b.Cleanup(func() { _ = repo.Close(ctx, src.ID) })
	if err := repo.Prepare(ctx, src.ID, "grid"); err != nil {
		b.Fatalf("prepare index: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sem := make(chan struct{}, 8)
		done := make(chan error, len(pts))
		for _, p := range pts {
			sem <- struct{}{}
			go func(c domain.Coordinate) {
				defer func() { <-sem }()
				_, err := repo.QueryPoint(ctx, src.ID, "grid", c)
				done <- err
			}(p)
		}
		for range pts {
			if err := <-done; err != nil {
				b.Fatalf("query: %v", err)
			}
		}
	}
	perPoint(b)
}

// BenchmarkBatchSpatialPerPointRaw: N single-point raw SQL queries (index-assisted)
// on the native fixture — the engine-level baseline for the set-based comparison.
func BenchmarkBatchSpatialPerPointRaw(b *testing.B) {
	path := b.TempDir() + "/grid-native.sqlite"
	db := buildNativeGrid(b, path)
	defer func() { _ = db.Close() }()
	pts := genPoints()
	ctx := context.Background()
	q := `SELECT name FROM grid
	       WHERE fid IN (SELECT pkid FROM idx_grid_geom WHERE xmin<=? AND xmax>=? AND ymin<=? AND ymax>=?)
	         AND ST_Covers(geom, MakePoint(?, ?, 4326)) LIMIT 1`
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, p := range pts {
			var name sql.NullString
			row := db.QueryRowContext(ctx, q, p.X, p.X, p.Y, p.Y, p.X, p.Y)
			if err := row.Scan(&name); err != nil && err != sql.ErrNoRows {
				b.Fatalf("query: %v", err)
			}
		}
	}
	perPoint(b)
}

// BenchmarkBatchSpatialSetBased: all batchPoints resolved in ONE SQL query (points
// passed as a JSON array, joined via the R-tree) — the "batch in the database"
// hypothesis.
func BenchmarkBatchSpatialSetBased(b *testing.B) {
	path := b.TempDir() + "/grid-native.sqlite"
	db := buildNativeGrid(b, path)
	defer func() { _ = db.Close() }()
	pts := genPoints()
	ctx := context.Background()

	var sb strings.Builder
	sb.WriteByte('[')
	for i, p := range pts {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `{"x":%g,"y":%g}`, p.X, p.Y)
	}
	sb.WriteByte(']')
	jsonPts := sb.String()

	q := `SELECT je.x, je.y,
	        (SELECT name FROM grid
	          WHERE fid IN (SELECT pkid FROM idx_grid_geom
	                         WHERE xmin<=je.x AND xmax>=je.x AND ymin<=je.y AND ymax>=je.y)
	            AND ST_Covers(geom, MakePoint(je.x, je.y, 4326)) LIMIT 1)
	      FROM (SELECT CAST(value->>'x' AS REAL) AS x, CAST(value->>'y' AS REAL) AS y
	              FROM json_each(?)) je`
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if n := runSetBased(ctx, b, db, q, jsonPts); n != batchPoints {
			b.Fatalf("got %d rows, want %d", n, batchPoints)
		}
	}
	perPoint(b)
}

// runSetBased runs the one-shot batch query and returns the row count (defer-closed).
func runSetBased(ctx context.Context, b *testing.B, db *sql.DB, q, jsonPts string) int {
	rows, err := db.QueryContext(ctx, q, jsonPts)
	if err != nil {
		b.Fatalf("set-based query: %v", err)
	}
	defer func() { _ = rows.Close() }()
	n := 0
	for rows.Next() {
		var x, y float64
		var name sql.NullString
		if err := rows.Scan(&x, &y, &name); err != nil {
			b.Fatalf("scan: %v", err)
		}
		n++
	}
	return n
}

// perPoint reports the amortized per-point cost (ns/op is per batch of batchPoints).
func perPoint(b *testing.B) {
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(batchPoints), "ns/point")
}
