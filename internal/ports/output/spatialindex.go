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
	// QueryKNN returns up to k nearest features in a layer within maxKM of p,
	// each paired with its ellipsoidal distance from p (computed by the same query,
	// so callers need no follow-up DistanceKM round-trip). An optional Filter
	// restricts candidates by an attribute predicate — used both for the
	// place-class query and the admin boundary constraint.
	QueryKNN(ctx context.Context, layer string, p domain.Coordinate, k int, maxKM float64, f *Filter) ([]NearFeature, error)

	// PointInPolygon returns the features of a polygon layer that cover p
	// (boundary-inclusive: a point on a polygon edge is a match). Results are
	// deduplicated by attribute identity, so ST_Subdivide-tiled sources match
	// their un-tiled originals.
	PointInPolygon(ctx context.Context, layer string, p domain.Coordinate) ([]domain.Feature, error)

	// ResolveChain walks a layer's parent_id links from a starting feature id up
	// to the top of the hierarchy, returning each unit in order (most-local first).
	// cols names the columns to walk/select, so the walk stays manifest-driven
	// rather than assuming fixed column names.
	ResolveChain(ctx context.Context, layer string, fromFID int64, cols AdminColumns) ([]AdminRow, error)

	// DistanceKM returns the ellipsoidal distance between two coordinates in km.
	DistanceKM(ctx context.Context, a, b domain.Coordinate) (float64, error)

	// Azimuth returns the initial bearing from one coordinate to another in
	// degrees (0=N, 90=E, clockwise).
	Azimuth(ctx context.Context, from, to domain.Coordinate) (float64, error)
}

// NearFeature is a QueryKNN result: a feature paired with its ellipsoidal
// distance from the query point (km), computed by the KNN query itself.
type NearFeature struct {
	Feature    domain.Feature
	DistanceKM float64
}

// Filter is an optional attribute predicate for QueryKNN: Column IN Values.
type Filter struct {
	Column string
	Values []any
}

// AdminColumns names the admin-layer columns ResolveChain walks and selects, so
// the parent-chain query is driven by the manifest rather than fixed names.
type AdminColumns struct {
	ParentFK string // FK to the broader enclosing unit (walked)
	Level    string // admin level (text, cast to int)
	Name     string // native unit name
	Country  string // ISO 3166-1 alpha-2 code
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
