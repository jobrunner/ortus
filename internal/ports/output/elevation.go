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

// ElevationCoverage says why a sample did or did not yield a height, so a caller
// can tell "there is nothing here" from "we have nothing here".
//
// This used to be one boolean, and everything falsy was reported as sea level.
// That was a deliberate convention and it does not hold: a DEM is finite, and a
// point beyond its edge is unknown, not at 0 m. Reporting it as sea level turns a
// gap in the data into a confident wrong answer — worst exactly at the coverage
// boundary, where land is most likely.
type ElevationCoverage int

const (
	// ElevationUnknown means the point lies outside the DEM's footprint, so no
	// claim can be made about its height.
	ElevationUnknown ElevationCoverage = iota
	// ElevationNoData means the DEM covers the point but has no value there. For
	// a terrain model that is water, so sea level is a sound reading.
	ElevationNoData
	// ElevationMeasured means a real sample.
	ElevationMeasured
)

// ElevationSampler samples the elevation (meters above sea level) at a WGS84
// coordinate from a continuous raster DEM source. It is an optional output port:
// when the gazetteer is not wired with one, it simply omits elevation from its
// response. Keeping it a port (rather than reaching into the raster adapter)
// keeps the gazetteer decoupled, mirroring SpatialIndex.
type ElevationSampler interface {
	// ElevationAt returns the elevation reading at coord together with why it is
	// or is not present. Absence is not an error.
	ElevationAt(ctx context.Context, coord domain.Coordinate) (reading ElevationReading, cov ElevationCoverage, err error)

	// CoveredAt reports whether the DEM has data coverage at coord, independent of
	// whether that pixel holds a value.
	CoveredAt(ctx context.Context, coord domain.Coordinate) (bool, error)

	// License returns the DEM source's license/attribution, distinct from the
	// gazetteer's own dataset license, so both provenances can be surfaced.
	License() domain.License
}
