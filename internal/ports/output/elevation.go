package output

import (
	"context"

	"github.com/jobrunner/ortus/internal/domain"
)

// ElevationReading is one elevation sample plus an optional per-point vertical
// accuracy (e.g. from a Copernicus Height Error Mask). HasAccuracy is false when
// no accuracy layer is bound, in which case the caller falls back to a dataset
// accuracy constant.
type ElevationReading struct {
	Meters      float64
	AccuracyM   float64
	HasAccuracy bool
}

// ElevationSampler samples the elevation (meters above sea level) at a WGS84
// coordinate from a continuous raster DEM source. It is an optional output port:
// when the gazetteer is not wired with one, it simply omits elevation from its
// response. Keeping it a port (rather than reaching into the raster adapter)
// keeps the gazetteer decoupled, mirroring SpatialIndex.
type ElevationSampler interface {
	// ElevationAt returns the elevation reading at coord. ok is false when no
	// data covers the point (ocean / outside coverage) — this is not an error;
	// callers treat it as sea level by convention (0 m).
	ElevationAt(ctx context.Context, coord domain.Coordinate) (reading ElevationReading, ok bool, err error)

	// License returns the DEM source's license/attribution, distinct from the
	// gazetteer's own dataset license, so both provenances can be surfaced.
	License() domain.License
}
