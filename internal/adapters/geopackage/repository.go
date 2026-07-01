// Package geopackage provides the SpatiaLite-based GeoPackage repository.
package geopackage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mattn/go-sqlite3"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// Ensure sqlite3 driver is registered with extension support.
func init() {
	sql.Register("sqlite3_with_extensions", &sqlite3.SQLiteDriver{
		Extensions: getSpatiaLiteLibraryPaths(),
	})
}

// getSpatiaLiteLibraryPaths returns a list of paths to try for loading SpatiaLite.
// The order is important: environment variable first, then platform-specific paths.
func getSpatiaLiteLibraryPaths() []string {
	var paths []string

	// First, check environment variable (set by Nix shell or Docker)
	// The env var should point to the exact library path
	if envPath := os.Getenv("SPATIALITE_LIBRARY_PATH"); envPath != "" {
		paths = append(paths, envPath)
		return paths
	}

	// Fallback: Platform-specific paths
	// Order matters - more specific paths first, then generic names
	paths = append(paths,
		// Alpine Linux (Docker containers)
		"/usr/lib/mod_spatialite.so",
		"/usr/lib/mod_spatialite.so.8",

		// Debian/Ubuntu amd64
		"/usr/lib/x86_64-linux-gnu/mod_spatialite.so",
		"/usr/lib/x86_64-linux-gnu/mod_spatialite.so.8",

		// Debian/Ubuntu arm64
		"/usr/lib/aarch64-linux-gnu/mod_spatialite.so",
		"/usr/lib/aarch64-linux-gnu/mod_spatialite.so.8",

		// macOS Homebrew (Intel)
		"/usr/local/lib/mod_spatialite.dylib",

		// macOS Homebrew (Apple Silicon)
		"/opt/homebrew/lib/mod_spatialite.dylib",

		// Generic names (let the system find them via LD_LIBRARY_PATH)
		"mod_spatialite.so",    // Linux
		"mod_spatialite",       // System default
		"mod_spatialite.dylib", // macOS
	)

	return paths
}

// Options tunes how SQLite databases are opened. The zero value is valid and
// yields safe defaults (private cache, no busy timeout, unlimited connections).
// The composition root maps config.SQLiteConfig onto this, so the adapter does
// not import the config package.
type Options struct {
	CacheMode     string // "private" (default) | "shared"
	BusyTimeoutMS int    // 0 = none
	JournalMode   string // "" = leave file's mode; e.g. "WAL"
	MaxOpenConns  int    // 0 = unlimited
	MaxIdleConns  int    // <=0 = database/sql default
}

// Repository implements the output.SpatialSource port using SpatiaLite.
// It serves vector GeoPackages.
type Repository struct {
	mu          sync.RWMutex
	connections map[string]*sql.DB
	sources     map[string]*domain.Source
	tracer      output.Tracer
	opts        Options
}

// Supports reports whether this adapter can open the given path. The
// GeoPackage adapter handles *.gpkg files.
func (r *Repository) Supports(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".gpkg")
}

// Prepare builds the spatial index for a layer (the GeoPackage adapter's
// readiness work). It satisfies the output.SpatialSource port by delegating
// to CreateSpatialIndex.
func (r *Repository) Prepare(ctx context.Context, sourceID string, layer string) error {
	return r.CreateSpatialIndex(ctx, sourceID, layer)
}

// NewRepository creates a new GeoPackage repository with the given SQLite
// options. Pass Options{} for safe defaults.
func NewRepository(opts Options) *Repository {
	return &Repository{
		connections: make(map[string]*sql.DB),
		sources:     make(map[string]*domain.Source),
		tracer:      output.NoOpTracer{},
		opts:        opts,
	}
}

// SetTracer wires a tracer into the repository. Pass output.NoOpTracer{} to
// disable. Safe to call once at startup before queries flow.
func (r *Repository) SetTracer(t output.Tracer) {
	if t == nil {
		t = output.NoOpTracer{}
	}
	r.tracer = t
}

// Open opens a GeoPackage file and returns its metadata.
func (r *Repository) Open(ctx context.Context, path string) (*domain.Source, error) {
	ctx, span := r.tracer.Start(ctx, "Repository.Open",
		output.WithAttributes(output.String("ortus.source.path", path)),
	)
	defer span.End()

	r.mu.Lock()
	defer r.mu.Unlock()

	// Derive source ID from filename
	sourceID := domain.DeriveSourceID(path)
	span.SetAttributes(output.String("ortus.source.id", sourceID))

	// Check if already open
	if src, ok := r.sources[sourceID]; ok {
		span.AddEvent("already_open")
		return src, nil
	}

	// Open database with SpatiaLite extension
	db, err := r.openDB(ctx, path)
	if err != nil {
		return nil, &domain.StorageError{
			Operation: "open",
			Key:       path,
			Err:       err,
		}
	}

	// Load SpatiaLite extension
	if err := r.loadSpatiaLite(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("loading SpatiaLite: %w", err)
	}

	// Read GeoPackage metadata
	src, err := r.readSourceMetadata(ctx, db, sourceID, path)
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	// Store connection and source
	r.connections[sourceID] = db
	r.sources[sourceID] = src

	return src, nil
}

// Close closes a GeoPackage connection.
func (r *Repository) Close(ctx context.Context, sourceID string) error {
	_, span := r.tracer.Start(ctx, "Repository.Close",
		output.WithSpanKind(output.SpanKindClient),
		output.WithAttributes(
			output.String("db.system", "sqlite"),
			output.String("ortus.source.id", sourceID),
		),
	)
	defer span.End()

	r.mu.Lock()
	defer r.mu.Unlock()

	db, ok := r.connections[sourceID]
	if !ok {
		span.AddEvent("not_open")
		return nil
	}

	if err := db.Close(); err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "close failed")
		return err
	}

	delete(r.connections, sourceID)
	delete(r.sources, sourceID)
	return nil
}

// GetLayers returns all layers in a GeoPackage.
func (r *Repository) GetLayers(ctx context.Context, sourceID string) ([]domain.Layer, error) {
	_, span := r.tracer.Start(ctx, "Repository.GetLayers",
		output.WithAttributes(output.String("ortus.source.id", sourceID)),
	)
	defer span.End()

	r.mu.RLock()
	src, ok := r.sources[sourceID]
	r.mu.RUnlock()

	if !ok {
		span.RecordError(domain.ErrSourceNotFound)
		span.SetStatus(output.StatusError, "source not found")
		return nil, domain.ErrSourceNotFound
	}

	span.SetAttributes(output.Int("ortus.layers.count", len(src.Layers)))
	return src.Layers, nil
}

// QueryPoint performs a point query on a specific layer.
func (r *Repository) QueryPoint(ctx context.Context, sourceID, layerName string, coord domain.Coordinate) ([]domain.Feature, error) {
	ctx, span := r.tracer.Start(ctx, "Repository.QueryPoint",
		output.WithSpanKind(output.SpanKindClient),
		output.WithAttributes(
			output.String("db.system", "sqlite"),
			output.String("ortus.source.id", sourceID),
			output.String("ortus.layer.name", layerName),
			output.Float64("ortus.coordinate.x", coord.X),
			output.Float64("ortus.coordinate.y", coord.Y),
			output.Int("ortus.coordinate.srid", coord.SRID),
		),
	)
	defer span.End()

	r.mu.RLock()
	db, ok := r.connections[sourceID]
	src := r.sources[sourceID]
	r.mu.RUnlock()

	if !ok {
		span.RecordError(domain.ErrSourceNotFound)
		span.SetStatus(output.StatusError, "source not found")
		return nil, domain.ErrSourceNotFound
	}

	// Find layer
	layer, found := src.GetLayer(layerName)
	if !found {
		span.RecordError(domain.ErrLayerNotFound)
		span.SetStatus(output.StatusError, "layer not found")
		return nil, domain.ErrLayerNotFound
	}

	span.SetAttributes(
		output.String("ortus.layer.geometry_type", layer.GeometryType),
		output.Int("ortus.layer.srid", layer.SRID),
		output.Bool("ortus.layer.has_index", layer.HasIndex),
	)

	features, err := r.executePointQuery(ctx, db, layer, coord)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "query failed")
		return nil, err
	}
	span.SetAttributes(output.Int("ortus.features.count", len(features)))
	span.SetStatus(output.StatusOK, "")
	return features, nil
}

// CreateSpatialIndex creates a spatial index for a layer.
// This creates an R-tree virtual table directly, bypassing SpatiaLite's CreateSpatialIndex()
// which requires a geometry_columns table that GeoPackage files don't have.
func (r *Repository) CreateSpatialIndex(ctx context.Context, sourceID, layerName string) error {
	ctx, span := r.tracer.Start(ctx, "Repository.CreateSpatialIndex",
		output.WithSpanKind(output.SpanKindClient),
		output.WithAttributes(
			output.String("db.system", "sqlite"),
			output.String("ortus.source.id", sourceID),
			output.String("ortus.layer.name", layerName),
		),
	)
	defer span.End()

	r.mu.RLock()
	db, ok := r.connections[sourceID]
	src := r.sources[sourceID]
	r.mu.RUnlock()

	if !ok {
		span.RecordError(domain.ErrSourceNotFound)
		span.SetStatus(output.StatusError, "source not found")
		return domain.ErrSourceNotFound
	}

	layer, found := src.GetLayer(layerName)
	if !found {
		span.RecordError(domain.ErrLayerNotFound)
		span.SetStatus(output.StatusError, "layer not found")
		return domain.ErrLayerNotFound
	}

	// Check if index already exists
	hasIndex, err := r.HasSpatialIndex(ctx, sourceID, layerName)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "index probe failed")
		return err
	}
	if hasIndex {
		// Index already exists, just update the layer status
		span.SetAttributes(output.Bool("ortus.index.preexisting", true))
		if err := r.setLayerIndexStatus(sourceID, layerName, true); err != nil {
			span.RecordError(err)
			span.SetStatus(output.StatusError, "status update failed")
			return err
		}
		return nil
	}

	indexTable := fmt.Sprintf("rtree_%s_%s", layerName, layer.GeometryColumn)

	// Create R-tree virtual table
	//nolint:gocritic // sprintfQuotedString: SQL identifiers need double quotes, not Go's %q
	createQuery := fmt.Sprintf(
		`CREATE VIRTUAL TABLE "%s" USING rtree(id, minx, maxx, miny, maxy)`, //#nosec G201 -- table name derived from trusted database
		indexTable,
	)
	if _, err := db.ExecContext(ctx, createQuery); err != nil { //#nosec G701 -- identifier from layer validated via GetLayer, double-quoted; SQLite DDL identifiers cannot be parameterized
		idxErr := &domain.IndexError{
			SourceID: sourceID,
			Layer:    layerName,
			Err:      fmt.Errorf("creating R-tree table: %w", err),
		}
		span.RecordError(idxErr)
		span.SetStatus(output.StatusError, "create R-tree table failed")
		return idxErr
	}

	// Populate R-tree with bounding boxes from all geometries
	// Using CastAutomagic to convert GeoPackage binary geometry to SpatiaLite format
	populateQuery := fmt.Sprintf(`
		INSERT INTO "%s" (id, minx, maxx, miny, maxy)
		SELECT rowid,
			MbrMinX(CastAutomagic("%s")),
			MbrMaxX(CastAutomagic("%s")),
			MbrMinY(CastAutomagic("%s")),
			MbrMaxY(CastAutomagic("%s"))
		FROM "%s"
		WHERE "%s" IS NOT NULL
	`, indexTable,
		layer.GeometryColumn, layer.GeometryColumn,
		layer.GeometryColumn, layer.GeometryColumn,
		layerName, layer.GeometryColumn,
	) //#nosec G201 -- table/column names from trusted database source

	if _, err := db.ExecContext(ctx, populateQuery); err != nil { //#nosec G701 -- identifiers from layer validated via GetLayer, double-quoted; SQLite DDL identifiers cannot be parameterized
		// Clean up the empty R-tree table on failure
		//nolint:gocritic // sprintfQuotedString: SQL identifiers need double quotes, not Go's %q
		_, _ = db.ExecContext(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS "%s"`, indexTable)) //#nosec G701 -- table name derived from validated layer metadata, double-quoted
		idxErr := &domain.IndexError{
			SourceID: sourceID,
			Layer:    layerName,
			Err:      fmt.Errorf("populating R-tree index: %w", err),
		}
		span.RecordError(idxErr)
		span.SetStatus(output.StatusError, "populate R-tree failed")
		return idxErr
	}

	// Update layer status
	if err := r.setLayerIndexStatus(sourceID, layerName, true); err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "status update failed")
		return err
	}
	return nil
}

// setLayerIndexStatus safely updates the HasIndex status for a layer.
// It handles concurrent access and checks if the source still exists.
func (r *Repository) setLayerIndexStatus(sourceID, layerName string, hasIndex bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	src, ok := r.sources[sourceID]
	if !ok {
		return domain.ErrSourceNotFound
	}

	for i := range src.Layers {
		if src.Layers[i].Name == layerName {
			src.Layers[i].HasIndex = hasIndex
			return nil
		}
	}

	return domain.ErrLayerNotFound
}

// HasSpatialIndex checks if a layer has a spatial index.
func (r *Repository) HasSpatialIndex(ctx context.Context, sourceID, layerName string) (bool, error) {
	ctx, span := r.tracer.Start(ctx, "Repository.HasSpatialIndex",
		output.WithSpanKind(output.SpanKindClient),
		output.WithAttributes(
			output.String("db.system", "sqlite"),
			output.String("ortus.source.id", sourceID),
			output.String("ortus.layer.name", layerName),
		),
	)
	defer span.End()

	r.mu.RLock()
	db, ok := r.connections[sourceID]
	src := r.sources[sourceID]
	r.mu.RUnlock()

	if !ok {
		span.RecordError(domain.ErrSourceNotFound)
		span.SetStatus(output.StatusError, "source not found")
		return false, domain.ErrSourceNotFound
	}

	layer, found := src.GetLayer(layerName)
	if !found {
		span.RecordError(domain.ErrLayerNotFound)
		span.SetStatus(output.StatusError, "layer not found")
		return false, domain.ErrLayerNotFound
	}

	// Check for RTree index table
	indexTable := fmt.Sprintf("rtree_%s_%s", layerName, layer.GeometryColumn)
	query := `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`
	span.SetAttributes(
		output.String("db.statement", query),
		output.String("ortus.index.table", indexTable),
	)

	var count int
	err := db.QueryRowContext(ctx, query, indexTable).Scan(&count)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "query failed")
		return false, err
	}

	span.SetAttributes(output.Bool("ortus.index.exists", count > 0))
	return count > 0, nil
}

// openDB opens the SQLite database with appropriate settings.
func (r *Repository) openDB(ctx context.Context, path string) (*sql.DB, error) {
	// Open read-write so the one-off spatial-index build can run; the GeoPackage
	// data itself is never modified (only R-tree indexes are added).
	return openSpatiaLite(ctx, path, r.opts)
}

// validJournalModes is the set of SQLite journal modes accepted in the DSN.
var validJournalModes = map[string]bool{
	"DELETE": true, "TRUNCATE": true, "PERSIST": true,
	"MEMORY": true, "WAL": true, "OFF": true,
}

// normalizeCacheMode constrains the cache mode to the two SQLite values. Any
// other input (including "" or an attempt to smuggle extra DSN params via '&')
// falls back to the safe read-concurrency default.
func normalizeCacheMode(m string) string {
	if strings.EqualFold(strings.TrimSpace(m), "shared") {
		return "shared"
	}
	return "private"
}

// normalizeJournalMode returns a valid upper-case journal mode, or "" to leave
// the file's existing mode untouched (also when the input is unrecognized, so a
// typo can't break DB open or inject DSN parameters).
func normalizeJournalMode(m string) string {
	up := strings.ToUpper(strings.TrimSpace(m))
	if validJournalModes[up] {
		return up
	}
	return ""
}

// loadSpatiaLite verifies that SpatiaLite extension is loaded.
// The extension is loaded automatically by the sqlite3_with_extensions driver.
func (r *Repository) loadSpatiaLite(ctx context.Context, db *sql.DB) error {
	// Verify SpatiaLite is loaded by checking its version
	var version string
	err := db.QueryRowContext(ctx, "SELECT spatialite_version()").Scan(&version)
	if err != nil {
		return fmt.Errorf("SpatiaLite extension not available: %w", err)
	}
	return nil
}

// readSourceMetadata reads metadata from a GeoPackage.
func (r *Repository) readSourceMetadata(ctx context.Context, db *sql.DB, sourceID, path string) (*domain.Source, error) {
	src := &domain.Source{
		ID:   sourceID,
		Name: sourceID,
		Path: path,
		Kind: domain.SourceKindVector,
	}

	// Read layers from gpkg_contents
	layers, err := r.readLayers(ctx, db)
	if err != nil {
		return nil, err
	}
	src.Layers = layers

	// Try to read metadata from gpkg_metadata if available
	_ = r.readMetadata(ctx, db, src)

	return src, nil
}

// readLayers reads layer information from gpkg_contents.
func (r *Repository) readLayers(ctx context.Context, db *sql.DB) ([]domain.Layer, error) {
	query := `
		SELECT
			c.table_name,
			COALESCE(c.description, ''),
			g.column_name,
			g.geometry_type_name,
			g.srs_id,
			COALESCE(c.min_x, 0), COALESCE(c.min_y, 0),
			COALESCE(c.max_x, 0), COALESCE(c.max_y, 0)
		FROM gpkg_contents c
		JOIN gpkg_geometry_columns g ON c.table_name = g.table_name
		WHERE c.data_type = 'features'
	`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("reading layers: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var layers []domain.Layer
	for rows.Next() {
		var l domain.Layer
		var minX, minY, maxX, maxY float64

		err := rows.Scan(
			&l.Name, &l.Description, &l.GeometryColumn,
			&l.GeometryType, &l.SRID,
			&minX, &minY, &maxX, &maxY,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning layer: %w", err)
		}

		if minX != 0 || minY != 0 || maxX != 0 || maxY != 0 {
			l.Extent = &domain.Extent{
				MinX: minX, MinY: minY,
				MaxX: maxX, MaxY: maxY,
				SRID: l.SRID,
			}
		}

		// Count features
		countQuery := fmt.Sprintf("SELECT COUNT(*) FROM %s", l.Name) //#nosec G201 -- table name from trusted database source
		var count int64
		if err := db.QueryRowContext(ctx, countQuery).Scan(&count); err == nil {
			l.FeatureCount = count
		}

		layers = append(layers, l)
	}

	return layers, rows.Err()
}

// readMetadata reads optional metadata from gpkg_metadata.
func (r *Repository) readMetadata(ctx context.Context, db *sql.DB, src *domain.Source) error {
	// Check if metadata table exists
	var exists int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='gpkg_metadata'",
	).Scan(&exists)
	if err != nil || exists == 0 {
		return nil //nolint:nilerr // intentionally returning nil for optional metadata
	}

	// Read first metadata entry
	query := `SELECT metadata FROM gpkg_metadata LIMIT 1`
	var metadata string
	if err := db.QueryRowContext(ctx, query).Scan(&metadata); err != nil {
		return err
	}

	// Parse metadata (simplified - would need proper XML parsing for full support)
	src.Metadata.Description = metadata
	return nil
}

// executePointQuery performs the actual point query using ST_Contains.
// The coordinate must already be transformed to the layer's SRID before calling this function.
// Uses R-tree spatial index for fast bounding box filtering when available.
func (r *Repository) executePointQuery(ctx context.Context, db *sql.DB, layer *domain.Layer, coord domain.Coordinate) ([]domain.Feature, error) {
	ctx, span := r.tracer.Start(ctx, "Repository.executePointQuery",
		output.WithSpanKind(output.SpanKindClient),
		output.WithAttributes(
			output.String("db.system", "sqlite"),
			output.String("ortus.layer.name", layer.Name),
			output.Bool("ortus.layer.is_polygon", layer.IsPolygonLayer()),
		),
	)
	defer span.End()

	pointWKT := coord.WKT()
	indexTable := fmt.Sprintf("rtree_%s_%s", layer.Name, layer.GeometryColumn)

	// Check if R-tree index exists
	var indexExists int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?",
		indexTable,
	).Scan(&indexExists)
	if err != nil {
		indexExists = 0
	}
	span.SetAttributes(
		output.Bool("ortus.rtree.used", indexExists > 0),
		output.String("ortus.index.table", indexTable),
	)

	// Build query using ST_Contains for polygon layers, MbrContains for others
	// Note: GeoPackage uses GPKG binary format, so we use CastAutomagic() to convert
	// the geometry to SpatiaLite format before spatial operations
	var query string
	if indexExists > 0 {
		// Use R-tree index for fast bounding box pre-filtering
		if layer.IsPolygonLayer() {
			query = fmt.Sprintf(`
				SELECT t.*, AsText(CastAutomagic(t."%s"))
				FROM "%s" t
				INNER JOIN "%s" r ON t.rowid = r.id
				WHERE r.minx <= ? AND r.maxx >= ? AND r.miny <= ? AND r.maxy >= ?
				  AND ST_Contains(CastAutomagic(t."%s"), GeomFromText(?, ?))
			`, layer.GeometryColumn, layer.Name, indexTable,
				layer.GeometryColumn,
			) //#nosec G201 -- table/column names from trusted database
		} else {
			query = fmt.Sprintf(`
				SELECT t.*, AsText(CastAutomagic(t."%s"))
				FROM "%s" t
				INNER JOIN "%s" r ON t.rowid = r.id
				WHERE r.minx <= ? AND r.maxx >= ? AND r.miny <= ? AND r.maxy >= ?
			`, layer.GeometryColumn, layer.Name, indexTable,
			) //#nosec G201 -- table/column names from trusted database
		}
	} else {
		// Fallback: no R-tree index, full table scan
		if layer.IsPolygonLayer() {
			query = fmt.Sprintf(`
				SELECT *, AsText(CastAutomagic("%s"))
				FROM "%s"
				WHERE ST_Contains(CastAutomagic("%s"), GeomFromText(?, ?))
			`, layer.GeometryColumn, layer.Name, layer.GeometryColumn) //#nosec G201 -- identifiers from layer metadata read from the gpkg catalog, double-quoted; SQLite can't parameterize identifiers
		} else {
			query = fmt.Sprintf(`
				SELECT *, AsText(CastAutomagic("%s"))
				FROM "%s"
				WHERE MbrContains(CastAutomagic("%s"), GeomFromText(?, ?))
			`, layer.GeometryColumn, layer.Name, layer.GeometryColumn) //#nosec G201 -- identifiers from layer metadata read from the gpkg catalog, double-quoted; SQLite can't parameterize identifiers
		}
	}

	span.SetAttributes(output.String("db.statement", query))

	var rows *sql.Rows
	if indexExists > 0 {
		// R-tree query: pass point coordinates for bounding box filter, then WKT and SRID
		if layer.IsPolygonLayer() {
			rows, err = db.QueryContext(ctx, query,
				coord.X, coord.X, coord.Y, coord.Y, // R-tree bounds (point = minx=maxx, miny=maxy)
				pointWKT, coord.SRID, // ST_Contains parameters
			)
		} else {
			rows, err = db.QueryContext(ctx, query,
				coord.X, coord.X, coord.Y, coord.Y, // R-tree bounds only
			)
		}
	} else {
		rows, err = db.QueryContext(ctx, query, pointWKT, coord.SRID)
	}

	if err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "query failed")
		return nil, &domain.QueryError{
			Layer: layer.Name,
			Err:   err,
		}
	}
	defer func() { _ = rows.Close() }()

	// Get column names for property mapping
	columns, err := rows.Columns()
	if err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "columns failed")
		return nil, err
	}

	var features []domain.Feature
	for rows.Next() {
		feature, err := scanFeature(rows, columns, layer.Name, layer.GeometryColumn)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(output.StatusError, "scan failed")
			return nil, err
		}
		features = append(features, feature)
	}

	if err := rows.Err(); err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "rows iteration failed")
		return nil, err
	}

	span.SetAttributes(output.Int("ortus.features.count", len(features)))
	return features, nil
}

// scanFeature scans a row into a Feature.
func scanFeature(rows *sql.Rows, columns []string, layerName, geomColumn string) (domain.Feature, error) {
	// Create scan destinations
	values := make([]interface{}, len(columns))
	valuePtrs := make([]interface{}, len(columns))
	for i := range values {
		valuePtrs[i] = &values[i]
	}

	if err := rows.Scan(valuePtrs...); err != nil {
		return domain.Feature{}, err
	}

	feature := domain.Feature{
		LayerName:  layerName,
		Properties: make(map[string]interface{}),
	}

	for i, col := range columns {
		switch col {
		case "fid":
			if v, ok := values[i].(int64); ok {
				feature.ID = v
			}
		case geomColumn:
			// Skip raw geometry column
		default:
			// Skip the AsText result column (last column) - it contains geometry WKT
			// This is identified by checking if this is the last column and contains WKT-like string
			if i == len(columns)-1 {
				// Last column is the AsText result, skip it from properties
				continue
			}
			if values[i] != nil {
				feature.Properties[col] = values[i]
			}
		}
	}

	// Get WKT from the last column (AsText result)
	if len(values) > 0 {
		if wkt, ok := values[len(values)-1].(string); ok {
			feature.Geometry.WKT = wkt
			feature.Geometry.Type = extractGeometryType(wkt)
		}
	}

	return feature, nil
}

// extractGeometryType extracts the geometry type from WKT.
func extractGeometryType(wkt string) string {
	if idx := strings.Index(wkt, "("); idx > 0 {
		return strings.TrimSpace(wkt[:idx])
	}
	return ""
}

// RepositoryTransformer implements CoordinateTransformer using an in-memory SpatiaLite database.
// We use a separate in-memory database because GeoPackage files are opened read-only
// and don't have the spatial_ref_sys table required by ST_Transform.
type RepositoryTransformer struct {
	db     *sql.DB
	tracer output.Tracer
}

// NewRepositoryTransformer creates a transformer with an in-memory SpatiaLite
// database. It returns an error if the database can't be opened or the
// SpatiaLite metadata can't be initialized — otherwise the transformer would
// look healthy but fail every later ST_Transform.
func NewRepositoryTransformer(_ *Repository) (*RepositoryTransformer, error) {
	// Create in-memory database for coordinate transformations
	db, err := sql.Open("sqlite3_with_extensions", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("opening in-memory SpatiaLite db for transformer: %w", err)
	}

	// Initialize SpatiaLite metadata tables WITH full SRID definitions (required for ST_Transform).
	// InitSpatialMetaDataFull populates spatial_ref_sys with standard EPSG definitions.
	if _, err := db.ExecContext(context.Background(), "SELECT InitSpatialMetaDataFull(1)"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initializing SpatiaLite metadata for transformer: %w", err)
	}

	return &RepositoryTransformer{db: db, tracer: output.NoOpTracer{}}, nil
}

// SetTracer wires a tracer into the repository transformer.
func (t *RepositoryTransformer) SetTracer(tr output.Tracer) {
	if tr == nil {
		tr = output.NoOpTracer{}
	}
	t.tracer = tr
}

// Transform transforms a coordinate from one SRID to another.
func (t *RepositoryTransformer) Transform(ctx context.Context, coord domain.Coordinate, targetSRID int) (domain.Coordinate, error) {
	if coord.SRID == targetSRID {
		return coord, nil
	}

	ctx, span := t.tracer.Start(ctx, "RepositoryTransformer.Transform",
		output.WithSpanKind(output.SpanKindClient),
		output.WithAttributes(
			output.String("db.system", "sqlite"),
			output.String("db.statement", "SELECT X(Transform(GeomFromText(?, ?), ?)), Y(Transform(GeomFromText(?, ?), ?))"),
			output.Int("ortus.coordinate.from_srid", coord.SRID),
			output.Int("ortus.coordinate.to_srid", targetSRID),
		),
	)
	defer span.End()

	if t.db == nil {
		err := fmt.Errorf("transformer database not initialized")
		span.RecordError(err)
		span.SetStatus(output.StatusError, "db not initialized")
		return domain.Coordinate{}, err
	}

	result, err := TransformCoordinate(ctx, t.db, coord, targetSRID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "transform failed")
		return result, err
	}
	return result, nil
}

// IsSupported checks if a transformation between two SRIDs is supported.
func (t *RepositoryTransformer) IsSupported(sourceSRID, targetSRID int) bool {
	return sourceSRID > 0 && targetSRID > 0
}

// Close closes the transformer's database connection.
func (t *RepositoryTransformer) Close() error {
	if t.db != nil {
		return t.db.Close()
	}
	return nil
}
