package geopackage

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jobrunner/ortus/internal/domain"
)

// Transformer implements coordinate transformation using SpatiaLite.
type Transformer struct {
	db *sql.DB
}

// NewTransformer creates a new coordinate transformer.
func NewTransformer(db *sql.DB) *Transformer {
	return &Transformer{db: db}
}

// Transform transforms a coordinate from one SRID to another.
func (t *Transformer) Transform(ctx context.Context, coord domain.Coordinate, targetSRID int) (domain.Coordinate, error) {
	if coord.SRID == targetSRID {
		return coord, nil
	}

	// Use SpatiaLite's Transform function
	query := `SELECT X(Transform(GeomFromText(?, ?), ?)), Y(Transform(GeomFromText(?, ?), ?))`

	wkt := coord.WKT()
	var x, y float64
	err := t.db.QueryRowContext(ctx, query,
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

// IsSupported checks if a transformation is supported.
func (t *Transformer) IsSupported(ctx context.Context, sourceSRID, targetSRID int) bool {
	// Check if both SRIDs are in the spatial_ref_sys table
	query := `
		SELECT COUNT(*)
		FROM spatial_ref_sys
		WHERE srid IN (?, ?)
	`
	var count int
	err := t.db.QueryRowContext(ctx, query, sourceSRID, targetSRID).Scan(&count)
	if err != nil {
		return false
	}
	return count == 2
}

// TransformDB provides a transformer that uses a specific database connection.
type TransformDB struct{}

// TransformCoordinate transforms a coordinate using a provided database.
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
