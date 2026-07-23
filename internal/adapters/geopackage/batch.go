package geopackage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// Repository implements output.BatchQuerier.
var _ output.BatchQuerier = (*Repository)(nil)

// QueryPoints resolves many coordinates against one layer in a SINGLE set-based
// query (one SQL per source, not N point queries), returning one feature slice per
// input coordinate in input order. It reproduces QueryPoint's semantics —
// R-tree prefilter, ST_Covers for polygon layers (boundary-inclusive) / MbrContains
// bbox for others, and per-point ST_Subdivide fragment dedup — for a whole batch.
//
// Coordinates must already be in the layer's SRID (same contract as QueryPoint).
// When the layer has no R-tree index it falls back to per-point executePointQuery
// (correctness over speed on the rare un-indexed layer).
func (r *Repository) QueryPoints(ctx context.Context, sourceID, layerName string, coords []domain.Coordinate) ([][]domain.Feature, error) {
	ctx, span := r.tracer.Start(ctx, "Repository.QueryPoints",
		output.WithSpanKind(output.SpanKindClient),
		output.WithAttributes(
			output.String("db.system", "sqlite"),
			output.String("ortus.source.id", sourceID),
			output.String("ortus.layer.name", layerName),
			output.Int("ortus.batch.points", len(coords)),
		),
	)
	defer span.End()

	out := make([][]domain.Feature, len(coords))
	if len(coords) == 0 {
		return out, nil
	}

	r.mu.RLock()
	db, ok := r.connections[sourceID]
	src := r.sources[sourceID]
	r.mu.RUnlock()
	if !ok {
		span.RecordError(domain.ErrSourceNotFound)
		span.SetStatus(output.StatusError, "source not found")
		return nil, domain.ErrSourceNotFound
	}
	layer, found := src.GetLayer(layerName)
	if !found {
		span.RecordError(domain.ErrLayerNotFound)
		span.SetStatus(output.StatusError, "layer not found")
		return nil, domain.ErrLayerNotFound
	}

	indexTable := fmt.Sprintf("rtree_%s_%s", layer.Name, layer.GeometryColumn)
	if !tableExists(ctx, db, indexTable) {
		span.AddEvent("no_rtree_fallback")
		return r.queryPointsUnindexed(ctx, db, layer, coords, out)
	}

	jsonPts, err := marshalPointsJSON(coords)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "marshal points failed")
		return nil, err
	}
	query := buildBatchPointQuery(layer, indexTable)
	span.SetAttributes(output.String("db.statement", query))

	// Polygon layers bind the layer SRID for the ST_Covers MakePoint; non-polygon
	// (bbox-only) layers don't. Build the args once so there's no extra branch here.
	args := []interface{}{jsonPts}
	if layer.IsPolygonLayer() {
		args = append(args, layer.SRID)
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "query failed")
		return nil, &domain.QueryError{Layer: layer.Name, Err: err}
	}
	defer func() { _ = rows.Close() }()

	if err := scanBatchRows(rows, layer, out); err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "scan failed")
		return nil, err
	}
	dedupBuckets(out, layer.IsPolygonLayer())
	span.SetStatus(output.StatusOK, "")
	return out, nil
}

// dedupBuckets applies the per-point ST_Subdivide fragment dedup (polygon layers
// only), mirroring QueryPoint's single-point dedup.
func dedupBuckets(out [][]domain.Feature, polygon bool) {
	if !polygon {
		return
	}
	for i := range out {
		out[i] = dedupFeaturesByProperties(out[i])
	}
}

// queryPointsUnindexed is the no-R-tree fallback: loop the per-point path so
// results are identical to QueryPoint, just without the set-based speedup.
func (r *Repository) queryPointsUnindexed(ctx context.Context, db *sql.DB, layer *domain.Layer, coords []domain.Coordinate, out [][]domain.Feature) ([][]domain.Feature, error) {
	for i, c := range coords {
		// Abort promptly if the caller canceled or the deadline elapsed, rather than
		// running every remaining per-point query for a large un-indexed batch.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		feats, err := r.executePointQuery(ctx, db, layer, c)
		if err != nil {
			return nil, err
		}
		out[i] = feats
	}
	return out, nil
}

// marshalPointsJSON encodes coords as [{"x":..,"y":..},...]; json_each's key is
// then the 0-based input index.
func marshalPointsJSON(coords []domain.Coordinate) (string, error) {
	pts := make([]struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	}, len(coords))
	for i, c := range coords {
		pts[i].X, pts[i].Y = c.X, c.Y
	}
	b, err := json.Marshal(pts)
	return string(b), err
}

// buildBatchPointQuery builds the set-based query: json_each unrolls the points,
// the R-tree bbox-prefilters candidates per point, then ST_Covers (polygon layers)
// confirms. The leading je.idx column maps each row back to its input coordinate.
func buildBatchPointQuery(layer *domain.Layer, indexTable string) string {
	// %[1]$s = geom column, %[2]$s = rtree table, %[3]$s = layer table, %[4]$s = the
	// polygon-only ST_Covers predicate (empty for non-polygon = bbox match only).
	covers := ""
	if layer.IsPolygonLayer() {
		covers = `WHERE ST_Covers(CastAutomagic(t."%[1]s"), MakePoint(je.x, je.y, ?))`
	}
	return fmt.Sprintf(`
		SELECT je.idx, t.*, AsText(CastAutomagic(t."%[1]s"))
		FROM (SELECT key AS idx, CAST(value->>'x' AS REAL) AS x, CAST(value->>'y' AS REAL) AS y FROM json_each(?)) je
		INNER JOIN "%[2]s" r ON r.minx <= je.x AND r.maxx >= je.x AND r.miny <= je.y AND r.maxy >= je.y
		INNER JOIN "%[3]s" t ON t.rowid = r.id
		`+covers+`
		ORDER BY je.idx
	`, layer.GeometryColumn, indexTable, layer.Name) //#nosec G201 -- identifiers from gpkg catalog, double-quoted; SQLite can't parameterize identifiers
}

// scanBatchRows scans the (idx, feature…) rows and buckets each feature into
// out[idx], reusing buildFeature for the per-row mapping.
func scanBatchRows(rows *sql.Rows, layer *domain.Layer, out [][]domain.Feature) error {
	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	featCols := columns[1:] // drop the leading idx column; rest is what buildFeature expects
	// Allocate the scan buffers once and reuse them per row: buildFeature copies each
	// value into the feature's property map before the next Scan overwrites the
	// buffer, so sharing is safe and drops a per-row allocation from the hot batch
	// path. Scanning into *interface{} (not *sql.RawBytes) means database/sql clones
	// any []byte value with bytes.Clone (see database/sql convert.go), so a BLOB
	// attribute stored in the map is independent of the reused buffer.
	var idx int64
	vals := make([]interface{}, len(columns))
	ptrs := make([]interface{}, len(columns))
	ptrs[0] = &idx
	for i := 1; i < len(columns); i++ {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		if idx < 0 || int(idx) >= len(out) {
			continue // defensive: json_each key out of range shouldn't happen
		}
		out[idx] = append(out[idx], buildFeature(featCols, vals[1:], layer.Name, layer.GeometryColumn))
	}
	return rows.Err()
}
