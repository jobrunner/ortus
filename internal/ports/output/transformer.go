package output

import (
	"context"

	"github.com/jobrunner/ortus/internal/domain"
)

// CoordinateTransformer defines the secondary port for coordinate transformations.
type CoordinateTransformer interface {
	// Transform transforms a coordinate from one SRID to another.
	Transform(ctx context.Context, coord domain.Coordinate, targetSRID int) (domain.Coordinate, error)

	// IsSupported checks if a transformation is supported.
	IsSupported(sourceSRID, targetSRID int) bool
}
