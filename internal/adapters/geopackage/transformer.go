package geopackage

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jobrunner/ortus/internal/domain"
)

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
