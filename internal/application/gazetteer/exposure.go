package gazetteer

import (
	"context"
	"errors"
	"math"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

const (
	// exposureSampleSpacingM is the horizontal distance between adjacent samples
	// of the 3×3 gradient window, in meters. It is tuned to the Copernicus GLO-30
	// DEM (~30 m native resolution): sampling closer than one pixel would just
	// re-read the same pixel (zero gradient); the Horn baseline is 2× this (60 m).
	exposureSampleSpacingM = 30.0

	// exposureCompassPoints quantises the aspect to the 8-point rose (N…NW), the
	// conventional resolution for terrain aspect.
	exposureCompassPoints = 8

	// exposureFlatThresholdDeg is the slope below which the aspect is treated as
	// undefined. On gentle terrain the gradient is dominated by DEM noise: for
	// GLO-30 (~4 m LE90 absolute, better relative) over the 60 m Horn baseline,
	// a couple of meters of noise already produces a few degrees of spurious
	// slope — so a facing direction below this is not trustworthy.
	exposureFlatThresholdDeg = 2.0

	// metersPerDegLat is the length of one degree of latitude, ~constant. One
	// degree of longitude is this scaled by cos(latitude).
	metersPerDegLat = 111320.0
)

// Exposure derives the terrain slope + aspect at the query point from the
// elevation DEM. It samples a 3×3 window at exposureSampleSpacingM around the
// point (offsetting lon/lat by the equivalent metric distance) and runs Horn's
// finite-difference method. It returns (nil, nil) when no elevation sampler is
// wired, or when the point or any neighbor has no DEM coverage (a reliable
// gradient needs the full window) — adapters then render a null exposure block
// (best-effort, no error), mirroring Elevation.
func (s *Service) Exposure(ctx context.Context, p domain.Coordinate) (*domain.Exposure, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := requireWGS84(p); err != nil {
		return nil, err
	}
	if s.elevation == nil {
		return nil, nil // feature not wired — omit from the response
	}
	w, ok, err := sampleWindow(ctx, s.elevation, p, exposureSampleSpacingM)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil // the point or a neighbor lacks coverage — exposure undefined
	}
	exp := computeExposure(w, exposureSampleSpacingM)
	exp.License = s.elevation.License()
	return &exp, nil
}

// horn3x3 is a 3×3 window of elevations (meters) laid out by compass position,
// row 1 = north. It is the input to Horn's slope/aspect finite differences.
type horn3x3 struct {
	nw, n, ne float64
	w, c, e   float64
	sw, s, se float64
}

// computeExposure derives slope + aspect from a 3×3 elevation window using Horn's
// (1981) method — the finite-difference scheme GDAL and ESRI use. spacingM is the
// horizontal distance between adjacent samples in meters (equal in x and y, since
// the samples sit on a metric offset grid). Aspect is the downslope azimuth
// (0=N, 90=E, clockwise); below the flat threshold it is undefined and Flat is
// set. The center cell (w.c) is unused by Horn but kept for a complete window.
func computeExposure(w horn3x3, spacingM float64) domain.Exposure {
	// Horn gradients over an 8·spacing baseline. dzdx is east-positive, dzdy is
	// north-positive (row 1 = north).
	dzdx := ((w.ne + 2*w.e + w.se) - (w.nw + 2*w.w + w.sw)) / (8 * spacingM)
	dzdy := ((w.nw + 2*w.n + w.ne) - (w.sw + 2*w.s + w.se)) / (8 * spacingM)

	rise := math.Hypot(dzdx, dzdy) // = tan(slope)
	slopeDeg := math.Atan(rise) * 180 / math.Pi

	exp := domain.Exposure{
		SlopeDeg:       slopeDeg,
		SlopePercent:   rise * 100,
		SampleSpacingM: spacingM,
	}
	if slopeDeg < exposureFlatThresholdDeg {
		exp.Flat = true
		return exp
	}

	// The gradient (dzdx, dzdy) points uphill; the aspect is the downhill facing,
	// i.e. the azimuth of (-dzdx east, -dzdy north), clockwise from north.
	az := math.Atan2(-dzdx, -dzdy) * 180 / math.Pi
	if az < 0 {
		az += 360
	}
	exp.AspectDeg = az
	exp.AspectCompass = domain.Compass(az, exposureCompassPoints)
	return exp
}

// sampleWindow reads the 3×3 elevation window (row 1 = north) around p at the
// given metric spacing, via the sampler. Longitude offsets shrink with latitude
// (cos-scaled, guarded near the poles) so the window stays roughly square on the
// ground. ok is false — with a nil error — when the point or any neighbor has no
// DEM coverage (a reliable gradient needs the whole window); a missing layer
// (ErrNotFound) is likewise treated as no coverage, mirroring Elevation.
func sampleWindow(ctx context.Context, sampler output.ElevationSampler, p domain.Coordinate, spacingM float64) (w horn3x3, ok bool, err error) {
	dLat := spacingM / metersPerDegLat
	cosLat := math.Cos(p.Y * math.Pi / 180)
	if cosLat < 1e-6 {
		cosLat = 1e-6
	}
	dLon := spacingM / (metersPerDegLat * cosLat)

	// row 1 = north: nw, n, ne, w, c, e, sw, s, se
	offsets := [9][2]float64{
		{-dLon, +dLat}, {0, +dLat}, {+dLon, +dLat},
		{-dLon, 0}, {0, 0}, {+dLon, 0},
		{-dLon, -dLat}, {0, -dLat}, {+dLon, -dLat},
	}
	var z [9]float64
	for i, o := range offsets {
		r, present, e := sampler.ElevationAt(ctx, domain.NewWGS84Coordinate(p.X+o[0], p.Y+o[1]))
		if e != nil {
			if errors.Is(e, domain.ErrNotFound) {
				return horn3x3{}, false, nil
			}
			return horn3x3{}, false, e
		}
		if !present {
			return horn3x3{}, false, nil
		}
		z[i] = r.Meters
	}
	return horn3x3{
		nw: z[0], n: z[1], ne: z[2],
		w: z[3], c: z[4], e: z[5],
		sw: z[6], s: z[7], se: z[8],
	}, true, nil
}
