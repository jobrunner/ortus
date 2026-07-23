package output

import (
	"context"

	"github.com/jobrunner/ortus/internal/domain"
)

// SpatialSource is the secondary port for a spatial data source adapter — a
// GeoPackage vector store, a raster bundle, etc. The registry routes each
// source file to the adapter whose Supports reports true, then drives the
// rest of the lifecycle through this interface, staying agnostic of the
// concrete source kind.
type SpatialSource interface {
	// Supports reports whether this adapter can open the given path
	// (typically by file extension, e.g. *.gpkg vs *.zip).
	Supports(path string) bool

	// Open opens a source file and returns its domain representation.
	Open(ctx context.Context, path string) (*domain.Source, error)

	// Prepare performs post-open readiness work for a single layer
	// (e.g. building a spatial index). It is a no-op for sources that need
	// none (a raster source is ready as soon as it is opened).
	Prepare(ctx context.Context, sourceID string, layer string) error

	// QueryPoint queries or samples a single layer at a coordinate.
	QueryPoint(ctx context.Context, sourceID string, layer string, coord domain.Coordinate) ([]domain.Feature, error)

	// Close releases resources held for a source.
	Close(ctx context.Context, sourceID string) error
}

// BatchQuerier is an OPTIONAL capability a SpatialSource may also implement to
// resolve many points against one layer in a single set-based operation (one SQL
// per source instead of N point queries — measured ~4–8× faster with far fewer
// allocations, and it avoids the reader contention naive per-point fan-out causes).
// The registry type-asserts for it and falls back to looping QueryPoint when a
// source (e.g. raster) does not implement it.
type BatchQuerier interface {
	// QueryPoints resolves each coordinate against the layer and returns one
	// result slice PER INPUT coordinate, in input order (a point with no hit gets
	// an empty slice). Coordinates must already be in the layer's SRID, matching
	// the QueryPoint contract.
	QueryPoints(ctx context.Context, sourceID string, layer string, coords []domain.Coordinate) ([][]domain.Feature, error)
}
