package application

import (
	"context"
	"fmt"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// ReadFeature loads one feature of a source by fid with its complete outline,
// delegating to the owning adapter when it implements output.FeatureReader (the
// GeoPackage adapter). An adapter without features to address (e.g. raster)
// yields domain.ErrUnsupported.
func (r *SourceRegistry) ReadFeature(ctx context.Context, sourceID, layer string, fid int64) (domain.Feature, error) {
	ctx, span := r.tracer.Start(ctx, "SourceRegistry.ReadFeature",
		output.WithAttributes(
			output.String("ortus.source.id", sourceID),
			output.String("ortus.layer.name", layer),
		),
	)
	defer span.End()

	fail := func(err error, status string) error {
		span.RecordError(err)
		span.SetStatus(output.StatusError, status)
		return err
	}

	r.mu.RLock()
	entry, ok := r.sources[sourceID]
	r.mu.RUnlock()
	if !ok || entry.Repo == nil {
		return domain.Feature{}, fail(domain.ErrSourceNotFound, "source not found")
	}
	fr, isReader := entry.Repo.(output.FeatureReader)
	if !isReader {
		return domain.Feature{}, fail(
			fmt.Errorf("source %q cannot address single features: %w", sourceID, domain.ErrUnsupported),
			"adapter has no features")
	}
	feature, err := fr.ReadFeature(ctx, sourceID, layer, fid)
	if err != nil {
		return domain.Feature{}, fail(err, "reading feature failed")
	}
	return feature, nil
}
