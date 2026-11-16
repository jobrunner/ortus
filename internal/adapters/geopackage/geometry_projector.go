package geopackage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// Whole-geometry projection of the RepositoryTransformer. In its own file to
// keep transformer.go (the point path) under its complexity cap.

// projectGeometrySQL reprojects a WKB geometry and serializes it. The CTE is
// MATERIALIZED so the (expensive) transform runs once rather than once per
// column that reads it. 7 decimals are about a centimeter in degrees.
const (
	projectGeometrySQL = `
		WITH x(g) AS MATERIALIZED (SELECT Transform(GeomFromWKB(?, ?), ?))
		SELECT AsGeoJSON(g, 7), MbrMinX(g), MbrMinY(g), MbrMaxX(g), MbrMaxY(g) FROM x`
	serializeGeometrySQL = `
		WITH x(g) AS MATERIALIZED (SELECT GeomFromWKB(?, ?))
		SELECT AsGeoJSON(g, 7), MbrMinX(g), MbrMinY(g), MbrMaxX(g), MbrMaxY(g) FROM x`
)

// ProjectGeometry reprojects a WKB geometry from sourceSRID to targetSRID and
// returns it as a GeoJSON geometry object with its bounding box in targetSRID.
// It runs on the transformer's pool, the only connections that carry the
// spatial_ref_sys Transform needs. With equal SRIDs the geometry is only
// serialized.
func (t *RepositoryTransformer) ProjectGeometry(ctx context.Context, wkb []byte, sourceSRID, targetSRID int) ([]byte, domain.Extent, error) {
	query, args := projectGeometrySQL, []any{wkb, sourceSRID, targetSRID}
	if sourceSRID == targetSRID {
		query, args = serializeGeometrySQL, []any{wkb, sourceSRID}
	}

	ctx, span := t.tracer.Start(ctx, "RepositoryTransformer.ProjectGeometry",
		output.WithSpanKind(output.SpanKindClient),
		output.WithAttributes(
			output.String("db.system", "sqlite"),
			output.String("db.statement", query),
			output.Int("ortus.coordinate.from_srid", sourceSRID),
			output.Int("ortus.coordinate.to_srid", targetSRID),
			output.Int("ortus.geometry.wkb_bytes", len(wkb)),
		),
	)
	defer span.End()

	fail := func(err error) error {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "projection failed")
		if !errors.Is(err, context.Canceled) {
			t.failures.Add(ctx, 1, metric.WithAttributes(
				attribute.Int("from_srid", sourceSRID),
				attribute.Int("to_srid", targetSRID),
			))
		}
		return err
	}

	if t.db == nil {
		return nil, domain.Extent{}, fail(fmt.Errorf("transformer database not initialized"))
	}

	// SpatiaLite reports an unparsable geometry or a failed transform as NULL,
	// not as an SQL error.
	var geojson sql.NullString
	var minX, minY, maxX, maxY sql.NullFloat64
	if err := t.db.QueryRowContext(ctx, query, args...).Scan(&geojson, &minX, &minY, &maxX, &maxY); err != nil {
		return nil, domain.Extent{}, fail(fmt.Errorf("projecting geometry: %w", err))
	}
	if !geojson.Valid || !minX.Valid || !minY.Valid || !maxX.Valid || !maxY.Valid {
		return nil, domain.Extent{}, fail(fmt.Errorf("projecting geometry from EPSG:%d to EPSG:%d: no result", sourceSRID, targetSRID))
	}

	span.SetAttributes(output.Int("ortus.geometry.geojson_bytes", len(geojson.String)))
	return []byte(geojson.String), domain.Extent{
		MinX: minX.Float64, MinY: minY.Float64,
		MaxX: maxX.Float64, MaxY: maxY.Float64,
		SRID: targetSRID,
	}, nil
}

// Compile-time assertion that the transformer also projects whole geometries.
var _ output.GeometryProjector = (*RepositoryTransformer)(nil)
