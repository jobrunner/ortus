package output

import (
	"context"

	"github.com/jobrunner/ortus/internal/domain"
)

// ElevationSampler samples the elevation (meters above sea level) at a WGS84
// coordinate from a continuous raster DEM source. It is an optional output port:
// when the gazetteer is not wired with one, it simply omits elevation from its
// response. Keeping it a port (rather than reaching into the raster adapter)
// keeps the gazetteer decoupled, mirroring SpatialIndex.
type ElevationSampler interface {
	// ElevationAt returns the orthometric elevation in meters at coord. ok is
	// false when no data covers the point (ocean / outside coverage) — this is
	// not an error; callers treat it as sea level by convention (0 m).
	ElevationAt(ctx context.Context, coord domain.Coordinate) (meters float64, ok bool, err error)

	// License returns the DEM source's license/attribution, distinct from the
	// gazetteer's own dataset license, so both provenances can be surfaced.
	License() domain.License
}
