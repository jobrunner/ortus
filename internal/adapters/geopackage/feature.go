package geopackage

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// origIDColumn is the column a package tiled with ST_Subdivide uses to tie the
// fragments of one feature together (see the build-ortus-package skill).
const origIDColumn = "orig_id"

// ReadFeature loads one feature of a polygon layer by fid, with its complete
// outline as a WKB MultiPolygon in the layer's SRID.
//
// A package tiled with ST_Subdivide stores a feature as several fragment rows,
// and the fid a point query returns addresses just one of them. The fragments
// are the rows sharing the addressed row's orig_id; a layer without that column
// is not tiled as far as ortus is concerned, and the row is the feature. The
// fragments are unioned (not merely collected) so the cut edges between tiles do
// not show up as lines on a map. Nothing is written: the union is computed per
// call.
func (r *Repository) ReadFeature(ctx context.Context, sourceID, layerName string, fid int64) (domain.Feature, error) {
	ctx, span := r.tracer.Start(ctx, "Repository.ReadFeature",
		output.WithSpanKind(output.SpanKindClient),
		output.WithAttributes(
			output.String("db.system", "sqlite"),
			output.String("ortus.source.id", sourceID),
			output.String("ortus.layer.name", layerName),
			output.Int("ortus.feature.id", int(fid)),
		),
	)
	defer span.End()

	fail := func(err error, status string) error {
		span.RecordError(err)
		span.SetStatus(output.StatusError, status)
		return err
	}

	r.mu.RLock()
	db, ok := r.connections[sourceID]
	src := r.sources[sourceID]
	r.mu.RUnlock()
	if !ok {
		return domain.Feature{}, fail(domain.ErrSourceNotFound, "source not found")
	}
	layer, found := src.GetLayer(layerName)
	if !found {
		return domain.Feature{}, fail(domain.ErrLayerNotFound, "layer not found")
	}
	if !layer.IsPolygonLayer() {
		return domain.Feature{}, fail(fmt.Errorf("layer %q is not a polygon layer: %w", layerName, domain.ErrUnsupported), "not a polygon layer")
	}

	columns, idColumn, err := tableColumnNames(ctx, db, layer.Name)
	if err != nil {
		return domain.Feature{}, fail(&domain.QueryError{Layer: layer.Name, Err: err}, "reading columns failed")
	}
	if idColumn == "" {
		return domain.Feature{}, fail(
			fmt.Errorf("layer %q has no integer primary key to address a feature by: %w", layerName, domain.ErrUnsupported),
			"no feature id column")
	}
	query := buildFeatureQuery(layer, columns, idColumn)
	span.SetAttributes(
		output.String("db.statement", query),
		output.String("ortus.feature.id_column", idColumn),
	)

	feature, err := scanFeatureOutline(ctx, db, query, layer, idColumn, fid)
	if err != nil {
		return domain.Feature{}, fail(err, "query failed")
	}
	span.SetAttributes(output.Int("ortus.geometry.wkb_bytes", len(feature.Geometry.WKB)))
	span.SetStatus(output.StatusOK, "")
	return feature, nil
}

// tableColumnNames returns the column names of a table in declaration order, and
// the name of its integer primary key — empty when the table has none or a
// composite one. The key is usually "fid" (the ogr default), but a feature is
// addressed by it whatever its name: packages exported from a scratch SpatiaLite
// table (the ST_Subdivide recipe) key their rows "rowid". An INTEGER PRIMARY KEY
// is SQLite's rowid alias, so looking a row up by it is a direct B-tree seek.
func tableColumnNames(ctx context.Context, db *sql.DB, table string) (cols []string, idColumn string, err error) {
	rows, err := db.QueryContext(ctx, `SELECT name, type, pk FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = rows.Close() }()
	keyColumns := 0
	for rows.Next() {
		var name, typ string
		var pk int
		if err := rows.Scan(&name, &typ, &pk); err != nil {
			return nil, "", err
		}
		cols = append(cols, name)
		if pk > 0 {
			keyColumns++
			if strings.Contains(strings.ToUpper(typ), "INT") {
				idColumn = name
			}
		}
	}
	if keyColumns != 1 {
		idColumn = ""
	}
	return cols, idColumn, rows.Err()
}

// defaultFeatureIDColumn is the feature-id column by ogr convention, used when a
// layer's primary key is not known.
const defaultFeatureIDColumn = "fid"

// idColumnOrDefault returns the column whose value is a feature's id: the
// layer's primary key, or the "fid" convention for a layer without one.
func idColumnOrDefault(idColumn string) string {
	if idColumn == "" {
		return defaultFeatureIDColumn
	}
	return idColumn
}

// primaryKeyColumn returns the layer's integer primary key column, empty when it
// has none (or the table cannot be inspected).
func primaryKeyColumn(ctx context.Context, db *sql.DB, table string) string {
	_, idColumn, err := tableColumnNames(ctx, db, table)
	if err != nil {
		return ""
	}
	return idColumn
}

// quoteIdent quotes an SQL identifier, doubling embedded quotes.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// buildFeatureQuery builds the statement that returns the addressed row (alias
// a) followed by one extra column: the union of all rows (alias t) belonging to
// the same feature, as a WKB MultiPolygon. idColumn is the layer's integer
// primary key.
//
// Rows belong together only through orig_id. Matching by identical attributes
// instead would be a guess: on an untiled layer whose features share their
// attributes (two forest parcels with nothing but landuse='forest') it unions
// unrelated features into one outline. A layer without orig_id is therefore
// served row by row, and tiling a package without carrying orig_id is a
// packaging fault the build-ortus-package checks catch — not something to paper
// over here.
//
// Both sides of the OR are indexable (orig_id by the index the package ships,
// the key by the table itself), so SQLite resolves the fragments by lookup
// rather than by scanning the layer; the second term also keeps the addressed
// row when its orig_id is NULL.
func buildFeatureQuery(layer *domain.Layer, columns []string, idColumn string) string {
	fidCol := quoteIdent(idColumn)
	siblings := fmt.Sprintf("t.%[1]s = a.%[1]s", fidCol)
	if slices.Contains(columns, origIDColumn) {
		siblings = fmt.Sprintf("t.%[1]s = a.%[1]s OR %[2]s", quoteIdent(origIDColumn), siblings)
	}

	table := quoteIdent(layer.Name)
	return fmt.Sprintf(`
		SELECT a.*, (
			SELECT AsBinary(CastToMultiPolygon(ST_Union(CastAutomagic(t.%[1]s))))
			FROM %[2]s t
			WHERE %[3]s
		)
		FROM %[2]s a
		WHERE a.%[4]s = ?
	`, quoteIdent(layer.GeometryColumn), table, siblings, fidCol)
}

// scanFeatureOutline runs the buildFeatureQuery statement and maps its single
// row: the layer's own columns become the feature, the trailing column its
// outline.
func scanFeatureOutline(ctx context.Context, db *sql.DB, query string, layer *domain.Layer, idColumn string, fid int64) (domain.Feature, error) {
	rows, err := db.QueryContext(ctx, query, fid)
	if err != nil {
		return domain.Feature{}, &domain.QueryError{Layer: layer.Name, Err: err}
	}
	defer func() { _ = rows.Close() }()

	columns, err := rows.Columns()
	if err != nil {
		return domain.Feature{}, err
	}
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return domain.Feature{}, &domain.QueryError{Layer: layer.Name, Err: err}
		}
		return domain.Feature{}, domain.ErrFeatureNotFound
	}

	values := make([]interface{}, len(columns))
	ptrs := make([]interface{}, len(columns))
	for i := range values {
		ptrs[i] = &values[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return domain.Feature{}, err
	}

	last := len(columns) - 1
	feature := buildFeature(columns[:last], values[:last], layer.Name, layer.GeometryColumn, idColumn, false)
	wkb, _ := values[last].([]byte)
	if len(wkb) == 0 {
		// NULL: the row has no geometry, or the union is not polygonal.
		return domain.Feature{}, &domain.QueryError{
			Layer: layer.Name,
			Err:   fmt.Errorf("feature %d has no polygon geometry", fid),
		}
	}
	feature.Geometry = domain.Geometry{
		Type: string(domain.GeomMultiPolygon),
		WKB:  wkb,
		SRID: layer.SRID,
	}
	return feature, rows.Err()
}

// Compile-time assertion that the repository offers the optional capability.
var _ output.FeatureReader = (*Repository)(nil)
