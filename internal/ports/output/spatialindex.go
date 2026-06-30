package output

import (
	"context"

	"github.com/jobrunner/ortus/internal/domain"
)

// SpatialIndex is the secondary port for the cgo-backed spatial primitives the
// gazetteer needs: nearest-neighbor search, point-in-polygon, ellipsoidal
// distance, azimuth, and the admin parent-chain walk. One adapter owns the
// cgo/SpatiaLite dependency behind this contract so the application core stays
// cgo-free.
type SpatialIndex interface {
	// QueryKNN returns up to k nearest features in a layer within maxKM of p.
	// An optional Filter restricts candidates by an attribute predicate — used
	// both for the place-class query and the admin boundary constraint.
	QueryKNN(ctx context.Context, layer string, p domain.Coordinate, k int, maxKM float64, f *Filter) ([]domain.Feature, error)

	// PointInPolygon returns the features of a polygon layer that contain p.
	PointInPolygon(ctx context.Context, layer string, p domain.Coordinate) ([]domain.Feature, error)

	// ResolveChain walks a layer's parent_id links from a starting feature id up
	// to the top of the hierarchy, returning each unit in order (most-local first).
	ResolveChain(ctx context.Context, layer string, fromFID int64) ([]AdminRow, error)

	// DistanceKM returns the ellipsoidal distance between two coordinates in km.
	DistanceKM(a, b domain.Coordinate) (float64, error)

	// Azimuth returns the initial bearing from one coordinate to another in
	// degrees (0=N, 90=E, clockwise).
	Azimuth(from, to domain.Coordinate) (float64, error)
}

// Filter is an optional attribute predicate for QueryKNN: Column IN Values.
type Filter struct {
	Column string
	Values []any
}

// AdminRow is a raw administrative-unit row from the spatial store, used to build
// the domain admin hierarchy. ParentFID is 0 when the unit has no parent (the top
// of the chain, e.g. a country with no imported super-unit).
type AdminRow struct {
	FID        int64
	ParentFID  int64
	Level      int
	Name       string
	CountryISO string
}
