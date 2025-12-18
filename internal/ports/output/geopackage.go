package output

import (
	"context"

	"github.com/jobrunner/ortus/internal/domain"
)

// GeoPackageRepository defines the secondary port for GeoPackage data access.
type GeoPackageRepository interface {
	// Open opens a GeoPackage file and returns its metadata.
	Open(ctx context.Context, path string) (*domain.GeoPackage, error)

	// Close closes a GeoPackage connection.
	Close(ctx context.Context, packageID string) error

	// GetLayers returns all layers in a GeoPackage.
	GetLayers(ctx context.Context, packageID string) ([]domain.Layer, error)

	// QueryPoint performs a point query on a specific layer.
	QueryPoint(ctx context.Context, packageID string, layer string, coord domain.Coordinate) ([]domain.Feature, error)

	// CreateSpatialIndex creates a spatial index for a layer.
	CreateSpatialIndex(ctx context.Context, packageID string, layer string) error

	// HasSpatialIndex checks if a layer has a spatial index.
	HasSpatialIndex(ctx context.Context, packageID string, layer string) (bool, error)
}

// CoordinateTransformer defines the secondary port for coordinate transformations.
type CoordinateTransformer interface {
	// Transform transforms a coordinate from one SRID to another.
	Transform(ctx context.Context, coord domain.Coordinate, targetSRID int) (domain.Coordinate, error)

	// IsSupported checks if a transformation is supported.
	IsSupported(sourceSRID, targetSRID int) bool
}
