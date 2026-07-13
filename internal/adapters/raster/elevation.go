package raster

import (
	"context"
	"fmt"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// ElevationSource adapts continuous raster layers to the ElevationSampler port,
// so the gazetteer can sample elevation at the query point without depending on
// the raster adapter's internals. An optional second continuous layer supplies a
// per-point vertical accuracy (e.g. a Copernicus Height Error Mask).
type ElevationSource struct {
	repo         *Repository
	sourceID     string
	layerName    string
	outputProp   string
	accuracyName string // "" when no accuracy layer is bound
	accuracyProp string
	license      domain.License
}

// NewElevationSource binds an elevation sampler to a continuous raster layer of
// an already-loaded bundle. accuracyLayer (optional; "" to disable) names a
// second continuous layer in the same source that yields a per-point vertical
// accuracy. It fails when a named source/layer is absent or not continuous, so a
// misconfiguration surfaces at startup rather than as silent zeros at query time.
// Call it after the registry has loaded the DEM bundle (i.e. after LoadAll).
func (r *Repository) NewElevationSource(sourceID, layerName, accuracyLayer string) (*ElevationSource, error) {
	r.mu.RLock()
	b, ok := r.sources[sourceID]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("elevation source %q not found", sourceID)
	}
	layer, err := continuousLayer(b, sourceID, layerName)
	if err != nil {
		return nil, err
	}
	es := &ElevationSource{
		repo:       r,
		sourceID:   sourceID,
		layerName:  layerName,
		outputProp: layer.outputProp,
	}
	if accuracyLayer != "" {
		acc, err := continuousLayer(b, sourceID, accuracyLayer)
		if err != nil {
			return nil, err
		}
		es.accuracyName = accuracyLayer
		es.accuracyProp = acc.outputProp
	}
	if b.source != nil {
		es.license = b.source.License
	}
	return es, nil
}

// continuousLayer looks up a layer and asserts it is continuous.
func continuousLayer(b *bundle, sourceID, layerName string) (*rasterLayer, error) {
	layer, ok := b.layers[layerName]
	if !ok {
		return nil, fmt.Errorf("elevation source %q: layer %q not found", sourceID, layerName)
	}
	if !layer.continuous {
		return nil, fmt.Errorf("elevation source %q: layer %q is not continuous (value_type must be continuous)", sourceID, layerName)
	}
	return layer, nil
}

// License returns the DEM bundle's license/attribution captured at bind time.
func (e *ElevationSource) License() domain.License { return e.license }

// ElevationAt samples the elevation (and, if bound, the per-point accuracy) at
// coord. A point with no elevation data (ocean / outside the raster extent)
// returns ok=false — the sea-level convention — rather than an error. A missing
// accuracy sample (e.g. accuracy layer covers less than the DEM) simply leaves
// HasAccuracy false.
func (e *ElevationSource) ElevationAt(ctx context.Context, coord domain.Coordinate) (reading output.ElevationReading, ok bool, err error) {
	m, has, err := e.sampleLayer(ctx, e.layerName, e.outputProp, coord)
	if err != nil {
		return output.ElevationReading{}, false, err
	}
	if !has {
		return output.ElevationReading{}, false, nil // no tile / nodata → sea level
	}
	reading = output.ElevationReading{Meters: m}
	if e.accuracyName != "" {
		acc, accHas, err := e.sampleLayer(ctx, e.accuracyName, e.accuracyProp, coord)
		if err != nil {
			return output.ElevationReading{}, false, err
		}
		reading.AccuracyM, reading.HasAccuracy = acc, accHas
	}
	return reading, true, nil
}

// sampleLayer samples one continuous layer and extracts its float property.
func (e *ElevationSource) sampleLayer(ctx context.Context, layer, prop string, coord domain.Coordinate) (value float64, ok bool, err error) {
	feats, err := e.repo.QueryPoint(ctx, e.sourceID, layer, coord)
	if err != nil {
		return 0, false, err
	}
	if len(feats) == 0 {
		return 0, false, nil
	}
	if _, present := feats[0].GetProperty(prop); !present {
		return 0, false, fmt.Errorf("elevation source %q: property %q missing", e.sourceID, prop)
	}
	// GetFloatProperty coerces float32/int/int64/float64 (matches the codebase's
	// KNN path); the continuous QueryPoint emits float64 today, but stay robust.
	return feats[0].GetFloatProperty(prop), true, nil
}

// Compile-time assertion that the adapter satisfies its output port.
var _ output.ElevationSampler = (*ElevationSource)(nil)
