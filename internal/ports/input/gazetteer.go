package input

import (
	"context"

	"github.com/jobrunner/ortus/internal/domain"
)

// Gazetteer is the primary port for reverse geocoding and bearing ("Peilung").
// It is a capability distinct from the generic point-query QueryService: it reads
// a dedicated places/admin GeoPackage, not the generic source pool, so the
// generic engine stays schema-agnostic.
type Gazetteer interface {
	// Locate reverse-geocodes a coordinate to its administrative hierarchy
	// (levels 2–8), each level labeled with its semantic meaning.
	Locate(ctx context.Context, p domain.Coordinate) (*domain.Locality, error)

	// Bearing returns the most salient nearby place as a bearing fix
	// ("4 km E Würzburg"), selected per the BearingPolicy.
	Bearing(ctx context.Context, p domain.Coordinate, pol domain.BearingPolicy) (*domain.Fix, error)

	// Islands returns the named island(s) whose polygon contains the point, or
	// nil when the point is on no island or the optional islands layer is not
	// configured — adapters render a null islands block in that case.
	Islands(ctx context.Context, p domain.Coordinate) ([]domain.Island, error)

	// Mountains returns the smallest containing mountain range and single-mountain
	// territory (independently, per landform), or nil when the point is on neither
	// or the optional mountains layer is not configured — adapters render a null
	// mountains block in that case.
	Mountains(ctx context.Context, p domain.Coordinate) (*domain.MountainResult, error)

	// Elevation returns the height above sea level at the point, or (nil, nil)
	// when the optional elevation feature is not wired — adapters render a null
	// elevation block in that case.
	Elevation(ctx context.Context, p domain.Coordinate) (*domain.Elevation, error)

	// Exposure returns the terrain slope + aspect at the point, derived from the
	// elevation DEM. It is (nil, nil) when the elevation feature is not wired or
	// the point (or a neighbor) has no DEM coverage — adapters render a null
	// exposure block in that case.
	Exposure(ctx context.Context, p domain.Coordinate) (*domain.Exposure, error)

	// Capabilities reports which optional blocks this deployment can answer at
	// all, so a consumer can tell a null block that means "not part of this
	// dataset" from one that means "no result here". Every method above returns
	// (nil, nil) for both, which made a package that quietly lost a layer
	// indistinguishable from correct behavior.
	Capabilities() domain.GazetteerCapabilities
}
