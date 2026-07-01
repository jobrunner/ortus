package geopackage

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// dsnFor builds the SQLite DSN from the given options. Values are normalized
// against fixed whitelists before being concatenated, so an invalid or hostile
// config value cannot break DB open or smuggle extra DSN parameters via '&'.
// Defaults to a private cache (each connection gets its own — allows true
// concurrent reads, unlike the legacy shared cache).
func dsnFor(path string, opts Options) string {
	params := []string{"cache=" + normalizeCacheMode(opts.CacheMode)}
	if opts.BusyTimeoutMS > 0 {
		params = append(params, fmt.Sprintf("_busy_timeout=%d", opts.BusyTimeoutMS))
	}
	if jm := normalizeJournalMode(opts.JournalMode); jm != "" {
		params = append(params, "_journal_mode="+jm)
	}
	return fmt.Sprintf("file:%s?%s", path, strings.Join(params, "&"))
}

// openSpatiaLite opens a SpatiaLite-backed SQLite connection with the configured
// pool limits and verifies it is reachable. It is the single open path shared by
// the vector Repository and the GazetteerIndex, so both use the same registered
// cgo driver, DSN whitelist, and pool policy.
func openSpatiaLite(ctx context.Context, path string, opts Options) (*sql.DB, error) {
	db, err := sql.Open("sqlite3_with_extensions", dsnFor(path, opts))
	if err != nil {
		return nil, err
	}
	if opts.MaxOpenConns > 0 {
		db.SetMaxOpenConns(opts.MaxOpenConns)
	}
	if opts.MaxIdleConns > 0 {
		db.SetMaxIdleConns(opts.MaxIdleConns)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// GazetteerIndex implements the output.SpatialIndex port against the dedicated
// gazetteer GeoPackage (places + admin_levels). It owns its own connection but
// reuses this package's registered cgo/SpatiaLite driver, so cgo stays confined
// to one adapter package. It is opened separately from the generic source pool —
// the gazetteer dataset is read "out of competition", not as a PiP source.
type GazetteerIndex struct {
	db *sql.DB
}

// OpenGazetteerIndex opens the gazetteer GeoPackage and verifies SpatiaLite is
// available. Call Close when done.
func OpenGazetteerIndex(ctx context.Context, path string, opts Options) (*GazetteerIndex, error) {
	db, err := openSpatiaLite(ctx, path, opts)
	if err != nil {
		return nil, err
	}
	var version string
	if err := db.QueryRowContext(ctx, "SELECT spatialite_version()").Scan(&version); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("gazetteer index: SpatiaLite unavailable: %w", err)
	}
	return &GazetteerIndex{db: db}, nil
}

// Close releases the underlying connection.
func (g *GazetteerIndex) Close() error {
	return g.db.Close()
}

// QueryKNN returns up to k nearest features of a layer within maxKM of p, ordered
// by ellipsoidal distance, optionally restricted by an attribute filter. The
// R-tree provides a bounding-box pre-filter when present; the exact geodesic
// Distance then enforces the radius and ordering.
//
// The attribute Filter is why this uses an R-tree bbox pre-filter rather than
// SpatiaLite's VirtualKNN2: KNN2 cannot push an attribute predicate (place-class
// or admin membership) into the nearest search, and every gazetteer query is
// class- and boundary-constrained, so a filtered radius search is the right tool.
func (g *GazetteerIndex) QueryKNN(ctx context.Context, layer string, p domain.Coordinate, k int, maxKM float64, f *output.Filter) ([]domain.Feature, error) {
	geom, err := geomColumn(ctx, g.db, layer)
	if err != nil {
		return nil, err
	}
	if k < 1 {
		k = 1
	}
	rtree := fmt.Sprintf("rtree_%s_%s", layer, geom)
	query, args := buildKNNQuery(layer, geom, tableExists(ctx, g.db, rtree), p, k, maxKM, f)
	return g.runFeatureQuery(ctx, layer, geom, query, args...)
}

// PointInPolygon returns the features of a polygon layer that contain p. It uses
// the R-tree bbox pre-filter when present, else a full-table ST_Contains scan.
func (g *GazetteerIndex) PointInPolygon(ctx context.Context, layer string, p domain.Coordinate) ([]domain.Feature, error) {
	geom, err := geomColumn(ctx, g.db, layer)
	if err != nil {
		return nil, err
	}
	rtree := fmt.Sprintf("rtree_%s_%s", layer, geom)
	var b strings.Builder
	var args []any
	fmt.Fprintf(&b, `SELECT t.*, AsText(CastAutomagic(t."%s")) FROM "%s" t`, geom, layer)
	if tableExists(ctx, g.db, rtree) {
		fmt.Fprintf(&b, ` JOIN "%s" r ON t.rowid = r.id WHERE r.minx <= ? AND r.maxx >= ? AND r.miny <= ? AND r.maxy >= ? AND `, rtree)
		args = append(args, p.X, p.X, p.Y, p.Y)
	} else {
		b.WriteString(` WHERE `)
	}
	fmt.Fprintf(&b, `ST_Contains(CastAutomagic(t."%s"), GeomFromText(?, ?))`, geom)
	args = append(args, p.WKT(), p.SRID)
	return g.runFeatureQuery(ctx, layer, geom, b.String(), args...)
}

// ResolveChain walks a layer's parent_id links from a starting feature id up to
// the top of the hierarchy, returning each unit in order (most-local first). The
// depth guard bounds the walk so malformed data (a cycle) cannot loop forever.
func (g *GazetteerIndex) ResolveChain(ctx context.Context, layer string, fromFID int64) ([]output.AdminRow, error) {
	var b strings.Builder
	fmt.Fprintf(&b, `WITH RECURSIVE chain(fid, parent_id, lvl, name, country_iso, depth) AS (
		SELECT fid, COALESCE(parent_id, 0), CAST(admin_level AS INTEGER), name, country_iso, 0
		FROM "%s" WHERE fid = ?
		UNION ALL
		SELECT a.fid, COALESCE(a.parent_id, 0), CAST(a.admin_level AS INTEGER), a.name, a.country_iso, chain.depth + 1
		FROM "%s" a JOIN chain ON a.fid = chain.parent_id
		WHERE chain.parent_id <> 0 AND chain.depth < 32
	)
	SELECT fid, parent_id, lvl, name, country_iso FROM chain ORDER BY depth`, layer, layer)

	rows, err := g.db.QueryContext(ctx, b.String(), fromFID)
	if err != nil {
		return nil, &domain.QueryError{Layer: layer, Err: err}
	}
	defer func() { _ = rows.Close() }()

	var chain []output.AdminRow
	for rows.Next() {
		var (
			fid, parent int64
			lvl         sql.NullInt64
			name, iso   sql.NullString
		)
		if err := rows.Scan(&fid, &parent, &lvl, &name, &iso); err != nil {
			return nil, err
		}
		chain = append(chain, output.AdminRow{
			FID:        fid,
			ParentFID:  parent,
			Level:      int(lvl.Int64),
			Name:       name.String,
			CountryISO: iso.String,
		})
	}
	return chain, rows.Err()
}

// DistanceKM returns the ellipsoidal distance between two coordinates in km,
// using SpatiaLite's Distance(g1, g2, 1) so it matches the KNN ordering metric.
func (g *GazetteerIndex) DistanceKM(a, b domain.Coordinate) (float64, error) {
	var meters float64
	err := g.db.QueryRowContext(context.Background(),
		"SELECT Distance(MakePoint(?, ?, 4326), MakePoint(?, ?, 4326), 1)",
		a.X, a.Y, b.X, b.Y).Scan(&meters)
	if err != nil {
		return 0, err
	}
	return meters / 1000, nil
}

// Azimuth returns the initial bearing from one coordinate to another in degrees
// (0=N, 90=E, clockwise), via SpatiaLite's ST_Azimuth (which returns radians).
func (g *GazetteerIndex) Azimuth(from, to domain.Coordinate) (float64, error) {
	var rad float64
	err := g.db.QueryRowContext(context.Background(),
		"SELECT ST_Azimuth(MakePoint(?, ?, 4326), MakePoint(?, ?, 4326))",
		from.X, from.Y, to.X, to.Y).Scan(&rad)
	if err != nil {
		return 0, err
	}
	deg := math.Mod(rad*180/math.Pi, 360)
	if deg < 0 {
		deg += 360
	}
	return deg, nil
}

// runFeatureQuery executes a feature query and maps each row to a domain.Feature
// via the shared scanFeature (last selected column is the geometry WKT).
func (g *GazetteerIndex) runFeatureQuery(ctx context.Context, layer, geom, query string, args ...any) ([]domain.Feature, error) {
	rows, err := g.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, &domain.QueryError{Layer: layer, Err: err}
	}
	defer func() { _ = rows.Close() }()

	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var features []domain.Feature
	for rows.Next() {
		f, err := scanFeature(rows, columns, layer, geom)
		if err != nil {
			return nil, err
		}
		features = append(features, f)
	}
	return features, rows.Err()
}

// buildKNNQuery assembles the radius-search SQL and its ordered args. The bbox
// pre-filter is added only when an R-tree exists; the attribute filter and the
// exact-distance radius/order are always applied.
func buildKNNQuery(layer, geom string, hasRtree bool, p domain.Coordinate, k int, maxKM float64, f *output.Filter) (query string, args []any) {
	distExpr := fmt.Sprintf(`Distance(CastAutomagic(t.%q), MakePoint(?, ?, 4326), 1)`, geom)
	var b strings.Builder
	fmt.Fprintf(&b, `SELECT t.*, AsText(CastAutomagic(t."%s")) FROM "%s" t`, geom, layer)
	if hasRtree {
		minX, maxX, minY, maxY := knnBBox(p, maxKM)
		fmt.Fprintf(&b, ` JOIN "rtree_%s_%s" r ON t.rowid = r.id`, layer, geom)
		b.WriteString(` WHERE r.maxx >= ? AND r.minx <= ? AND r.maxy >= ? AND r.miny <= ?`)
		args = append(args, minX, maxX, minY, maxY)
	} else {
		b.WriteString(` WHERE 1 = 1`)
	}
	if f != nil && len(f.Values) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(f.Values)), ",")
		fmt.Fprintf(&b, ` AND t."%s" IN (%s)`, f.Column, placeholders)
		args = append(args, f.Values...)
	}
	fmt.Fprintf(&b, ` AND %s <= ?`, distExpr)
	args = append(args, p.X, p.Y, maxKM*1000)
	fmt.Fprintf(&b, ` ORDER BY %s ASC LIMIT ?`, distExpr)
	args = append(args, p.X, p.Y, k)
	return b.String(), args
}

// knnBBox returns a lon/lat bounding box of half-side maxKM around p, used as the
// R-tree pre-filter. Longitude degrees shrink with latitude; the cosine is
// floored so the box stays finite near the poles.
func knnBBox(p domain.Coordinate, maxKM float64) (minX, maxX, minY, maxY float64) {
	const kmPerDegree = 111.32
	dLat := maxKM / kmPerDegree
	cos := math.Cos(p.Y * math.Pi / 180)
	if cos < 0.01 {
		cos = 0.01
	}
	dLon := maxKM / (kmPerDegree * cos)
	return p.X - dLon, p.X + dLon, p.Y - dLat, p.Y + dLat
}

// geomColumn looks up a layer's geometry column from the GeoPackage catalog, so
// the index stays schema-agnostic about the column name.
func geomColumn(ctx context.Context, db *sql.DB, layer string) (string, error) {
	var col string
	err := db.QueryRowContext(ctx,
		"SELECT column_name FROM gpkg_geometry_columns WHERE table_name = ?", layer).Scan(&col)
	if err == sql.ErrNoRows {
		return "", domain.ErrLayerNotFound
	}
	if err != nil {
		return "", err
	}
	return col, nil
}

// tableExists reports whether a table (e.g. an R-tree index) is present.
func tableExists(ctx context.Context, db *sql.DB, name string) bool {
	var n int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type IN ('table','virtual') AND name = ?", name).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

// Compile-time assertion that the index satisfies the output port.
var _ output.SpatialIndex = (*GazetteerIndex)(nil)
