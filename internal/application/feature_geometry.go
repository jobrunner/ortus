package application

import (
	"context"
	"fmt"
	"slices"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// FeatureGeometry returns the complete outline of one feature of a vector
// source, reprojected to ref.SRID (WGS84 when 0), with its bounding box and the
// license of its package — what a map client needs to draw a feature a point
// query found.
//
// Only polygon layers of vector sources are served: a raster source or another
// geometry type is domain.ErrUnsupported.
func (s *QueryService) FeatureGeometry(ctx context.Context, ref domain.FeatureRef) (*domain.FeatureGeometry, error) {
	if s.queryTimeout > 0 {
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, s.queryTimeout)
			defer cancel()
		}
	}

	targetSRID := ref.SRID
	if targetSRID == 0 {
		targetSRID = domain.SRIDWGS84
	}

	ctx, span := s.tracer.Start(ctx, "QueryService.FeatureGeometry",
		output.WithAttributes(
			output.String("ortus.source.id", ref.SourceID),
			output.String("ortus.layer.name", ref.Layer),
			output.Int("ortus.feature.id", int(ref.ID)),
			output.Int("ortus.coordinate.to_srid", targetSRID),
		),
	)
	defer span.End()

	fail := func(err error, status string) error {
		span.RecordError(err)
		span.SetStatus(output.StatusError, status)
		return err
	}

	src, layer, err := s.featureLayer(ctx, ref, targetSRID)
	if err != nil {
		return nil, fail(err, "invalid feature reference")
	}

	feature, err := s.registry.ReadFeature(ctx, ref.SourceID, layer.Name, ref.ID)
	if err != nil {
		return nil, fail(err, "reading feature failed")
	}

	geojson, extent, err := s.projector.ProjectGeometry(ctx, feature.Geometry.WKB, layer.SRID, targetSRID)
	if err != nil {
		return nil, fail(fmt.Errorf("%w: EPSG:%d -> EPSG:%d: %w", domain.ErrTransformFailed, layer.SRID, targetSRID, err),
			"projection failed")
	}

	span.SetAttributes(output.Int("ortus.geometry.geojson_bytes", len(geojson)))
	span.SetStatus(output.StatusOK, "")
	return &domain.FeatureGeometry{
		SourceID:   src.ID,
		Layer:      layer.Name,
		ID:         feature.ID,
		Properties: feature.Properties,
		GeoJSON:    geojson,
		Extent:     extent,
		License:    src.License,
	}, nil
}

// featureLayer resolves and validates what a feature reference points at: a
// ready vector source, one of its polygon layers and a target SRID the server
// can project to. The id is not judged here: an integer primary key may hold any
// signed 64-bit value, so whether a feature exists is for the lookup to say.
func (s *QueryService) featureLayer(ctx context.Context, ref domain.FeatureRef, targetSRID int) (*domain.Source, *domain.Layer, error) {
	if err := s.checkSRID(targetSRID); err != nil {
		return nil, nil, err
	}

	// Like the point queries, only ready sources are served.
	if !slices.Contains(s.registry.ReadySourceIDs(), ref.SourceID) {
		return nil, nil, domain.ErrSourceNotFound
	}
	src, err := s.registry.GetSource(ctx, ref.SourceID)
	if err != nil {
		return nil, nil, err
	}
	if src.Kind == domain.SourceKindRaster {
		return nil, nil, fmt.Errorf("source %q is a raster source and has no feature geometries: %w",
			src.ID, domain.ErrUnsupported)
	}
	layer, found := src.GetLayer(ref.Layer)
	if !found {
		return nil, nil, domain.ErrLayerNotFound
	}
	if !layer.IsPolygonLayer() {
		return nil, nil, fmt.Errorf("layer %q is not a polygon layer: %w", layer.Name, domain.ErrUnsupported)
	}
	if s.projector == nil {
		return nil, nil, fmt.Errorf("geometry projection is not available: %w", domain.ErrUnsupported)
	}
	return src, layer, nil
}
