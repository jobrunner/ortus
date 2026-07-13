package raster

import (
	"context"
	"fmt"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// ElevationSource adapts one continuous raster layer to the ElevationSampler
// port, so the gazetteer can sample elevation at the query point without
// depending on the raster adapter's internals.
type ElevationSource struct {
	repo       *Repository
	sourceID   string
	layerName  string
	outputProp string
	license    domain.License
}

// NewElevationSource binds an elevation sampler to a continuous raster layer of
// an already-loaded bundle. It fails when the source or layer is absent or the
// layer is not continuous, so a misconfiguration surfaces at startup rather than
// as silent zero elevations at query time. Call it after the registry has loaded
// the DEM bundle (i.e. after LoadAll).
func (r *Repository) NewElevationSource(sourceID, layerName string) (*ElevationSource, error) {
	r.mu.RLock()
	b, ok := r.sources[sourceID]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("elevation source %q not found", sourceID)
	}
	layer, ok := b.layers[layerName]
	if !ok {
		return nil, fmt.Errorf("elevation source %q: layer %q not found", sourceID, layerName)
	}
	if !layer.continuous {
		return nil, fmt.Errorf("elevation source %q: layer %q is not continuous (value_type must be continuous)", sourceID, layerName)
	}
	var lic domain.License
	if b.source != nil {
		lic = b.source.License
	}
	return &ElevationSource{
		repo:       r,
		sourceID:   sourceID,
		layerName:  layerName,
		outputProp: layer.outputProp,
		license:    lic,
	}, nil
}

// License returns the DEM bundle's license/attribution captured at bind time.
func (e *ElevationSource) License() domain.License { return e.license }

// ElevationAt samples the continuous layer at coord. A point with no data (ocean
// / outside the raster extent) returns ok=false, meters=0 — the sea-level
// convention — rather than an error.
func (e *ElevationSource) ElevationAt(ctx context.Context, coord domain.Coordinate) (meters float64, ok bool, err error) {
	feats, err := e.repo.QueryPoint(ctx, e.sourceID, e.layerName, coord)
	if err != nil {
		return 0, false, err
	}
	if len(feats) == 0 {
		return 0, false, nil // no tile / nodata → sea level by convention
	}
	v, ok := feats[0].Properties[e.outputProp].(float64)
	if !ok {
		return 0, false, fmt.Errorf("elevation source %q: property %q missing or not a float", e.sourceID, e.outputProp)
	}
	return v, true, nil
}

// Compile-time assertion that the adapter satisfies its output port.
var _ output.ElevationSampler = (*ElevationSource)(nil)
