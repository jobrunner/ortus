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

// GeometryProjector is the secondary port for reprojecting a whole geometry and
// serializing it for a map client.
type GeometryProjector interface {
	// ProjectGeometry reprojects a WKB geometry from sourceSRID to targetSRID and
	// returns it as a GeoJSON geometry object together with its bounding box in
	// targetSRID. The box is computed from the reprojected geometry: projecting
	// the corners of the source box would not bound it.
	ProjectGeometry(ctx context.Context, wkb []byte, sourceSRID, targetSRID int) (geojson []byte, extent domain.Extent, err error)
}
