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

	// Elevation returns the height above sea level at the point, or (nil, nil)
	// when the optional elevation feature is not wired (so the caller omits it).
	Elevation(ctx context.Context, p domain.Coordinate) (*domain.Elevation, error)
}
