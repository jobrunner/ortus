package output

import (
	"context"

	"github.com/jobrunner/ortus/internal/domain"
)

// BuiltUpSampler samples the built-up surface value at a WGS84 coordinate from a
// continuous raster (e.g. GHS-BUILT-S: square meters of built-up surface per cell).
// It is an OPTIONAL output port used only to refine the gazetteer's "in <place>"
// decision: when a point lies within a settlement's radius but on no built-up
// fabric (a field or park), the "in" label is suppressed. When no sampler is wired
// the decision uses distance alone. Keeping it a port (rather than reaching into
// the raster adapter) keeps the gazetteer decoupled, mirroring ElevationSampler.
type BuiltUpSampler interface {
	// BuiltUpAt returns the built-up value at coord. ok is false when no data
	// covers the point (outside the bundle's extent) — this is not an error; the
	// caller then falls back to the distance-only "in" decision rather than
	// suppressing it.
	BuiltUpAt(ctx context.Context, coord domain.Coordinate) (value float64, ok bool, err error)
}
