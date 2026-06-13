package geopackage

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// Transformer implements coordinate transformation using SpatiaLite.
type Transformer struct {
	db     *sql.DB
	tracer output.Tracer
}

// NewTransformer creates a new coordinate transformer.
func NewTransformer(db *sql.DB) *Transformer {
	return &Transformer{db: db, tracer: output.NoOpTracer{}}
}

// SetTracer wires a tracer into the transformer.
func (t *Transformer) SetTracer(tr output.Tracer) {
	if tr == nil {
		tr = output.NoOpTracer{}
	}
	t.tracer = tr
}

// Transform transforms a coordinate from one SRID to another.
func (t *Transformer) Transform(ctx context.Context, coord domain.Coordinate, targetSRID int) (domain.Coordinate, error) {
	if coord.SRID == targetSRID {
		return coord, nil
	}

	ctx, span := t.tracer.Start(ctx, "Transformer.Transform",
		output.WithSpanKind(output.SpanKindClient),
		output.WithAttributes(
			output.String("db.system", "sqlite"),
			output.Int("ortus.coordinate.from_srid", coord.SRID),
			output.Int("ortus.coordinate.to_srid", targetSRID),
		),
	)
	defer span.End()

	// Use SpatiaLite's Transform function
	query := `SELECT X(Transform(GeomFromText(?, ?), ?)), Y(Transform(GeomFromText(?, ?), ?))`
	span.SetAttributes(output.String("db.statement", query))

	wkt := coord.WKT()
	var x, y float64
	err := t.db.QueryRowContext(ctx, query,
		wkt, coord.SRID, targetSRID,
		wkt, coord.SRID, targetSRID,
	).Scan(&x, &y)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "transform query failed")
		return domain.Coordinate{}, fmt.Errorf("transforming coordinate: %w", err)
	}

	return domain.Coordinate{
		X:    x,
		Y:    y,
		SRID: targetSRID,
	}, nil
}

// IsSupported checks if a transformation is supported.
func (t *Transformer) IsSupported(ctx context.Context, sourceSRID, targetSRID int) bool {
	ctx, span := t.tracer.Start(ctx, "Transformer.IsSupported",
		output.WithSpanKind(output.SpanKindClient),
		output.WithAttributes(
			output.String("db.system", "sqlite"),
			output.Int("ortus.coordinate.from_srid", sourceSRID),
			output.Int("ortus.coordinate.to_srid", targetSRID),
		),
	)
	defer span.End()

	// Check if both SRIDs are in the spatial_ref_sys table
	query := `
		SELECT COUNT(*)
		FROM spatial_ref_sys
		WHERE srid IN (?, ?)
	`
	var count int
	err := t.db.QueryRowContext(ctx, query, sourceSRID, targetSRID).Scan(&count)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "lookup failed")
		return false
	}
	supported := count == 2
	span.SetAttributes(output.Bool("ortus.transform.supported", supported))
	return supported
}

// TransformDB provides a transformer that uses a specific database connection.
type TransformDB struct{}

// TransformCoordinate transforms a coordinate using a provided database. This
// helper is used by RepositoryTransformer; tracing is added by the calling
// code (RepositoryTransformer.Transform) so this stays a thin SQL helper.
func TransformCoordinate(ctx context.Context, db *sql.DB, coord domain.Coordinate, targetSRID int) (domain.Coordinate, error) {
	if coord.SRID == targetSRID {
		return coord, nil
	}

	query := `SELECT X(Transform(GeomFromText(?, ?), ?)), Y(Transform(GeomFromText(?, ?), ?))`
	wkt := coord.WKT()

	var x, y float64
	err := db.QueryRowContext(ctx, query,
		wkt, coord.SRID, targetSRID,
		wkt, coord.SRID, targetSRID,
	).Scan(&x, &y)
	if err != nil {
		return domain.Coordinate{}, fmt.Errorf("transforming coordinate: %w", err)
	}

	return domain.Coordinate{
		X:    x,
		Y:    y,
		SRID: targetSRID,
	}, nil
}
