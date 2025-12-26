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

// Repository implements the GeoPackageRepository port using SpatiaLite.
type Repository struct {
	mu          sync.RWMutex
	connections map[string]*sql.DB
	packages    map[string]*domain.GeoPackage
}

// NewRepository creates a new GeoPackage repository.
func NewRepository() *Repository {
	return &Repository{
		connections: make(map[string]*sql.DB),
		packages:    make(map[string]*domain.GeoPackage),
	}
}

// Open opens a GeoPackage file and returns its metadata.
func (r *Repository) Open(ctx context.Context, path string) (*domain.GeoPackage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Derive package ID from filename
	packageID := DerivePackageID(path)

	// Check if already open
	if pkg, ok := r.packages[packageID]; ok {
		return pkg, nil
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
	pkg, err := r.readPackageMetadata(ctx, db, packageID, path)
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	// Store connection and package
	r.connections[packageID] = db
	r.packages[packageID] = pkg

	return pkg, nil
}

// Close closes a GeoPackage connection.
func (r *Repository) Close(_ context.Context, packageID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	db, ok := r.connections[packageID]
	if !ok {
		return nil
	}

	if err := db.Close(); err != nil {
		return err
	}

	delete(r.connections, packageID)
	delete(r.packages, packageID)
	return nil
}

// GetLayers returns all layers in a GeoPackage.
func (r *Repository) GetLayers(_ context.Context, packageID string) ([]domain.Layer, error) {
	r.mu.RLock()
	pkg, ok := r.packages[packageID]
	r.mu.RUnlock()

	if !ok {
		return nil, domain.ErrPackageNotFound
	}

	return pkg.Layers, nil
}

// QueryPoint performs a point query on a specific layer.
func (r *Repository) QueryPoint(ctx context.Context, packageID, layerName string, coord domain.Coordinate) ([]domain.Feature, error) {
	r.mu.RLock()
	db, ok := r.connections[packageID]
	pkg := r.packages[packageID]
	r.mu.RUnlock()

	if !ok {
		return nil, domain.ErrPackageNotFound
	}

	// Find layer
	layer, found := pkg.GetLayer(layerName)
	if !found {
		return nil, domain.ErrLayerNotFound
	}

	// Build and execute query
	return r.executePointQuery(ctx, db, layer, coord)
}

// CreateSpatialIndex creates a spatial index for a layer.
// This creates an R-tree virtual table directly, bypassing SpatiaLite's CreateSpatialIndex()
// which requires a geometry_columns table that GeoPackage files don't have.
func (r *Repository) CreateSpatialIndex(ctx context.Context, packageID, layerName string) error {
	r.mu.RLock()
	db, ok := r.connections[packageID]
	pkg := r.packages[packageID]
	r.mu.RUnlock()

	if !ok {
		return domain.ErrPackageNotFound
	}

	layer, found := pkg.GetLayer(layerName)
	if !found {
		return domain.ErrLayerNotFound
	}

	// Check if index already exists
	hasIndex, err := r.HasSpatialIndex(ctx, packageID, layerName)
	if err != nil {
		return err
	}
	if hasIndex {
		return nil
	}

	indexTable := fmt.Sprintf("rtree_%s_%s", layerName, layer.GeometryColumn)

	// Create R-tree virtual table
	createQuery := fmt.Sprintf(
		"CREATE VIRTUAL TABLE \"%s\" USING rtree(id, minx, maxx, miny, maxy)", //#nosec G201 -- table name derived from trusted database
		indexTable,
	)
	if _, err := db.ExecContext(ctx, createQuery); err != nil {
		return &domain.IndexError{
			PackageID: packageID,
			Layer:     layerName,
			Err:       fmt.Errorf("creating R-tree table: %w", err),
		}
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

	if _, err := db.ExecContext(ctx, populateQuery); err != nil {
		// Clean up the empty R-tree table on failure
		_, _ = db.ExecContext(ctx, fmt.Sprintf("DROP TABLE IF EXISTS \"%s\"", indexTable))
		return &domain.IndexError{
			PackageID: packageID,
			Layer:     layerName,
			Err:       fmt.Errorf("populating R-tree index: %w", err),
		}
	}

	// Update layer status
	r.mu.Lock()
	for i := range r.packages[packageID].Layers {
		if r.packages[packageID].Layers[i].Name == layerName {
			r.packages[packageID].Layers[i].HasIndex = true
			break
		}
	}
	r.mu.Unlock()

	return nil
}

// HasSpatialIndex checks if a layer has a spatial index.
func (r *Repository) HasSpatialIndex(ctx context.Context, packageID, layerName string) (bool, error) {
	r.mu.RLock()
	db, ok := r.connections[packageID]
	pkg := r.packages[packageID]
	r.mu.RUnlock()

	if !ok {
		return false, domain.ErrPackageNotFound
	}

	layer, found := pkg.GetLayer(layerName)
	if !found {
		return false, domain.ErrLayerNotFound
	}

	// Check for RTree index table
	indexTable := fmt.Sprintf("rtree_%s_%s", layerName, layer.GeometryColumn)
	query := `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`

	var count int
	err := db.QueryRowContext(ctx, query, indexTable).Scan(&count)
	if err != nil {
		return false, err
	}

	return count > 0, nil
}

// openDB opens the SQLite database with appropriate settings.
func (r *Repository) openDB(ctx context.Context, path string) (*sql.DB, error) {
	// Open in read-write mode to allow spatial index creation
	// GeoPackage data remains unmodified - only R-tree indexes are added
	dsn := fmt.Sprintf("file:%s?cache=shared", path)
	db, err := sql.Open("sqlite3_with_extensions", dsn)
	if err != nil {
		return nil, err
	}

	// Test connection
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}

	return db, nil
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

// readPackageMetadata reads metadata from a GeoPackage.
func (r *Repository) readPackageMetadata(ctx context.Context, db *sql.DB, packageID, path string) (*domain.GeoPackage, error) {
	pkg := &domain.GeoPackage{
		ID:   packageID,
		Name: packageID,
		Path: path,
	}

	// Read layers from gpkg_contents
	layers, err := r.readLayers(ctx, db)
	if err != nil {
		return nil, err
	}
	pkg.Layers = layers

	// Try to read metadata from gpkg_metadata if available
	_ = r.readMetadata(ctx, db, pkg)

	return pkg, nil
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
func (r *Repository) readMetadata(ctx context.Context, db *sql.DB, pkg *domain.GeoPackage) error {
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
	pkg.Metadata.Description = metadata
	return nil
}

// executePointQuery performs the actual point query using ST_Contains.
// The coordinate must already be transformed to the layer's SRID before calling this function.
// Uses R-tree spatial index for fast bounding box filtering when available.
func (r *Repository) executePointQuery(ctx context.Context, db *sql.DB, layer *domain.Layer, coord domain.Coordinate) ([]domain.Feature, error) {
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

	// Build query using ST_Contains for polygon layers, MbrContains for others
	// Note: GeoPackage uses GPKG binary format, so we use CastAutomagic() to convert
	// the geometry to SpatiaLite format before spatial operations
	var query string
	if indexExists > 0 {
		// Use R-tree index for fast bounding box pre-filtering
		if layer.IsPolygonLayer() {
			query = fmt.Sprintf(`
				SELECT t.fid, t.*, AsText(CastAutomagic(t."%s"))
				FROM "%s" t
				INNER JOIN "%s" r ON t.rowid = r.id
				WHERE r.minx <= ? AND r.maxx >= ? AND r.miny <= ? AND r.maxy >= ?
				  AND ST_Contains(CastAutomagic(t."%s"), GeomFromText(?, ?))
			`, layer.GeometryColumn, layer.Name, indexTable,
				layer.GeometryColumn,
			) //#nosec G201 -- table/column names from trusted database
		} else {
			query = fmt.Sprintf(`
				SELECT t.fid, t.*, AsText(CastAutomagic(t."%s"))
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
				SELECT fid, *, AsText(CastAutomagic("%s"))
				FROM "%s"
				WHERE ST_Contains(CastAutomagic("%s"), GeomFromText(?, ?))
			`, layer.GeometryColumn, layer.Name, layer.GeometryColumn) //#nosec G201
		} else {
			query = fmt.Sprintf(`
				SELECT fid, *, AsText(CastAutomagic("%s"))
				FROM "%s"
				WHERE MbrContains(CastAutomagic("%s"), GeomFromText(?, ?))
			`, layer.GeometryColumn, layer.Name, layer.GeometryColumn) //#nosec G201
		}
	}

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
		return nil, &domain.QueryError{
			Layer: layer.Name,
			Err:   err,
		}
	}
	defer func() { _ = rows.Close() }()

	// Get column names for property mapping
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var features []domain.Feature
	for rows.Next() {
		feature, err := r.scanFeature(rows, columns, layer.Name, layer.GeometryColumn)
		if err != nil {
			return nil, err
		}
		features = append(features, feature)
	}

	return features, rows.Err()
}

// scanFeature scans a row into a Feature.
func (r *Repository) scanFeature(rows *sql.Rows, columns []string, layerName, geomColumn string) (domain.Feature, error) {
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

// DerivePackageID derives a package ID from the file path.
// It extracts the filename without extension as the package identifier.
func DerivePackageID(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext)
}

// extractGeometryType extracts the geometry type from WKT.
func extractGeometryType(wkt string) string {
	if idx := strings.Index(wkt, "("); idx > 0 {
		return strings.TrimSpace(wkt[:idx])
	}
	return ""
}

// GetConnection returns the database connection for a specific package.
// This is used by the RepositoryTransformer for coordinate transformation.
func (r *Repository) GetConnection(packageID string) *sql.DB {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.connections[packageID]
}

// RepositoryTransformer implements CoordinateTransformer using an in-memory SpatiaLite database.
// We use a separate in-memory database because GeoPackage files are opened read-only
// and don't have the spatial_ref_sys table required by ST_Transform.
type RepositoryTransformer struct {
	db *sql.DB
}

// NewRepositoryTransformer creates a transformer with an in-memory SpatiaLite database.
func NewRepositoryTransformer(_ *Repository) *RepositoryTransformer {
	// Create in-memory database for coordinate transformations
	db, err := sql.Open("sqlite3_with_extensions", ":memory:")
	if err != nil {
		return nil
	}

	// Initialize SpatiaLite metadata tables WITH full SRID definitions (required for ST_Transform)
	// InitSpatialMetaDataFull populates spatial_ref_sys with standard EPSG definitions
	_, _ = db.ExecContext(context.Background(), "SELECT InitSpatialMetaDataFull(1)")

	return &RepositoryTransformer{db: db}
}

// Transform transforms a coordinate from one SRID to another.
func (t *RepositoryTransformer) Transform(ctx context.Context, coord domain.Coordinate, targetSRID int) (domain.Coordinate, error) {
	if coord.SRID == targetSRID {
		return coord, nil
	}

	if t.db == nil {
		return domain.Coordinate{}, fmt.Errorf("transformer database not initialized")
	}

	return TransformCoordinate(ctx, t.db, coord, targetSRID)
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
