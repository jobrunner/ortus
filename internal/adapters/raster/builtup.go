package raster

import (
	"context"
	"fmt"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// BuiltUpSource adapts one continuous raster layer to the BuiltUpSampler port, so
// the gazetteer can sample built-up surface at the query point without depending on
// the raster adapter's internals (mirrors ElevationSource, minus the accuracy layer).
type BuiltUpSource struct {
	repo       *Repository
	sourceID   string
	layerName  string
	outputProp string
}

// NewBuiltUpSource binds a built-up sampler to a continuous raster layer of an
// already-loaded bundle. It fails when the source/layer is absent or not continuous,
// so a misconfiguration surfaces at startup rather than as silent zeros. Call it
// after the registry has loaded the bundle (i.e. after LoadAll).
func (r *Repository) NewBuiltUpSource(sourceID, layerName string) (*BuiltUpSource, error) {
	r.mu.RLock()
	b, ok := r.sources[sourceID]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("built-up source %q not found", sourceID)
	}
	layer, err := continuousLayer(b, sourceID, layerName)
	if err != nil {
		return nil, err
	}
	return &BuiltUpSource{repo: r, sourceID: sourceID, layerName: layerName, outputProp: layer.outputProp}, nil
}

// BuiltUpAt samples the built-up value at coord. A point with no data (outside the
// raster extent / nodata) returns ok=false rather than an error, so the caller falls
// back to the distance-only "in" decision.
func (b *BuiltUpSource) BuiltUpAt(ctx context.Context, coord domain.Coordinate) (value float64, ok bool, err error) {
	feats, err := b.repo.QueryPoint(ctx, b.sourceID, b.layerName, coord)
	if err != nil {
		return 0, false, err
	}
	if len(feats) == 0 {
		return 0, false, nil
	}
	if _, present := feats[0].GetProperty(b.outputProp); !present {
		return 0, false, fmt.Errorf("built-up source %q: property %q missing", b.sourceID, b.outputProp)
	}
	return feats[0].GetFloatProperty(b.outputProp), true, nil
}

// Compile-time assertion that the adapter satisfies its output port.
var _ output.BuiltUpSampler = (*BuiltUpSource)(nil)
