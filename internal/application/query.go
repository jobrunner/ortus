package application

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// sourceQuerier is the minimal registry surface the query service needs —
// declared here (consumer side) so the service depends on an interface, not the
// concrete *SourceRegistry. *SourceRegistry satisfies it.
type sourceQuerier interface {
	ReadySourceIDs() []string
	GetSource(ctx context.Context, id string) (*domain.Source, error)
	Query(ctx context.Context, sourceID, layer string, coord domain.Coordinate) ([]domain.Feature, error)
}

// QueryService handles point queries across registered sources.
type QueryService struct {
	registry      sourceQuerier
	transformer   output.CoordinateTransformer
	tracer        output.Tracer
	queryCount    metric.Int64Counter
	queryDuration metric.Float64Histogram
	logger        *slog.Logger
	maxFeatures   int
	queryTimeout  time.Duration
}

// QueryServiceConfig holds configuration for the query service.
type QueryServiceConfig struct {
	MaxFeatures  int
	QueryTimeout time.Duration // per-query deadline; 0 disables
}

// NewQueryService creates a new query service. The meter is used directly
// to define query-level instruments — no MetricsCollector indirection. Pass
// noop.NewMeterProvider().Meter("test") to disable metrics in tests.
func NewQueryService(
	registry sourceQuerier,
	transformer output.CoordinateTransformer,
	meter metric.Meter,
	tracer output.Tracer,
	logger *slog.Logger,
	cfg QueryServiceConfig,
) *QueryService {
	if cfg.MaxFeatures == 0 {
		cfg.MaxFeatures = 1000
	}
	if tracer == nil {
		tracer = output.NoOpTracer{}
	}
	if meter == nil {
		meter = noop.NewMeterProvider().Meter("ortus/application")
	}

	queryCount, _ := meter.Int64Counter(
		"ortus.queries",
		metric.WithDescription("Total number of point queries"),
	)
	queryDuration, _ := meter.Float64Histogram(
		"ortus.query.duration",
		metric.WithDescription("Query duration in seconds"),
		metric.WithUnit("s"),
	)

	return &QueryService{
		registry:      registry,
		transformer:   transformer,
		tracer:        tracer,
		queryCount:    queryCount,
		queryDuration: queryDuration,
		logger:        logger,
		maxFeatures:   cfg.MaxFeatures,
		queryTimeout:  cfg.QueryTimeout,
	}
}

// QueryPoint performs a point query across all registered GeoPackages.
func (s *QueryService) QueryPoint(ctx context.Context, req domain.QueryRequest) (*domain.QueryResponse, error) {
	start := time.Now()

	// Enforce the configured per-query deadline so an expensive or hung adapter
	// query can't pin a goroutine/connection indefinitely. Respect a caller's
	// existing deadline if it already set one.
	if s.queryTimeout > 0 {
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, s.queryTimeout)
			defer cancel()
		}
	}

	ctx, span := s.tracer.Start(ctx, "QueryService.QueryPoint",
		output.WithAttributes(
			output.Float64("ortus.coordinate.x", req.Coordinate.X),
			output.Float64("ortus.coordinate.y", req.Coordinate.Y),
			output.Int("ortus.coordinate.srid", req.Coordinate.SRID),
			output.String("ortus.source.id", req.SourceID),
			output.Int("ortus.properties.count", len(req.Properties)),
		),
	)
	defer span.End()

	response := &domain.QueryResponse{
		Coordinate: req.Coordinate,
	}

	// Validate coordinate
	if err := req.Coordinate.Validate(); err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "invalid coordinate")
		return nil, err
	}

	// Get all ready sources
	sourceIDs := s.registry.ReadySourceIDs()

	// Filter by specific source if requested
	if req.SourceID != "" {
		found := false
		for _, id := range sourceIDs {
			if id == req.SourceID {
				sourceIDs = []string{req.SourceID}
				found = true
				break
			}
		}
		if !found {
			span.RecordError(domain.ErrSourceNotFound)
			span.SetStatus(output.StatusError, "source not found")
			return nil, domain.ErrSourceNotFound
		}
	}

	span.SetAttributes(output.Int("ortus.sources.queried", len(sourceIDs)))

	// Query each source
	for _, sid := range sourceIDs {
		result, err := s.QueryPointInSource(ctx, sid, req)
		if err != nil {
			s.logger.Warn("query failed for source", "source", sid, "error", err)
			s.queryCount.Add(ctx, 1, metric.WithAttributes(
				attribute.String("source_id", sid),
				attribute.String("status", "error"),
			))
			continue
		}

		if result.HasFeatures() {
			response.AddResult(*result)
		}
		s.queryCount.Add(ctx, 1, metric.WithAttributes(
			attribute.String("source_id", sid),
			attribute.String("status", "success"),
		))
	}

	response.ProcessingTime = time.Since(start)
	span.SetAttributes(
		output.Int("ortus.features.total", response.TotalFeatures),
		output.Float64("ortus.duration_ms", float64(response.ProcessingTime.Microseconds())/1000.0),
	)
	span.SetStatus(output.StatusOK, "")
	return response, nil
}

// QueryPointInSource performs a point query in a specific source.
func (s *QueryService) QueryPointInSource(ctx context.Context, sourceID string, req domain.QueryRequest) (*domain.QueryResult, error) {
	start := time.Now()

	ctx, span := s.tracer.Start(ctx, "QueryService.QueryPointInSource",
		output.WithAttributes(
			output.String("ortus.source.id", sourceID),
		),
	)
	defer span.End()

	// Get source info
	pkg, err := s.registry.GetSource(ctx, sourceID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "get source")
		return nil, err
	}

	result := &domain.QueryResult{
		SourceID:   pkg.ID,
		SourceName: pkg.Name,
		License:    pkg.License,
	}

	span.SetAttributes(
		output.String("ortus.source.name", pkg.Name),
		output.Int("ortus.layers.count", len(pkg.Layers)),
	)

	// Query each layer
	maxReached := false
	for _, layer := range pkg.Layers {
		if s.queryLayer(ctx, sourceID, &layer, &req, result) {
			maxReached = true
			break // max features reached
		}
	}

	result.QueryTime = time.Since(start)
	s.queryDuration.Record(ctx, result.QueryTime.Seconds(), metric.WithAttributes(
		attribute.String("source_id", sourceID),
	))

	span.SetAttributes(
		output.Int("ortus.features.count", result.FeatureCount()),
		output.Bool("ortus.max_features_reached", maxReached),
		output.Float64("ortus.duration_ms", float64(result.QueryTime.Microseconds())/1000.0),
	)
	span.SetStatus(output.StatusOK, "")

	return result, nil
}

// queryLayer queries a single layer and appends results. Returns true if max features reached.
func (s *QueryService) queryLayer(ctx context.Context, sourceID string, layer *domain.Layer, req *domain.QueryRequest, result *domain.QueryResult) bool {
	ctx, span := s.tracer.Start(ctx, "QueryService.queryLayer",
		output.WithAttributes(
			output.String("ortus.source.id", sourceID),
			output.String("ortus.layer.name", layer.Name),
			output.Int("ortus.layer.srid", layer.SRID),
			output.String("ortus.layer.geometry_type", layer.GeometryType),
		),
	)
	defer span.End()

	queryCoord, ok := s.transformCoordinate(ctx, req.Coordinate, layer)
	if !ok {
		// ok=false covers an unsupported SRID mismatch (no transformer) and a
		// failed/canceled transform; transformCoordinate logs the specific reason.
		span.AddEvent("layer skipped (coordinate not transformable)")
		return false
	}

	features, err := s.registry.Query(ctx, sourceID, layer.Name, queryCoord)
	if err != nil {
		if isCanceled(err) {
			// Expected when the client aborts the request (e.g. the map UI
			// cancels the previous in-flight query) — not a server failure.
			s.logger.Debug("layer query canceled", "source", sourceID, "layer", layer.Name, "error", err)
			return false
		}
		s.logger.Warn("layer query failed", "source", sourceID, "layer", layer.Name, "error", err)
		span.RecordError(err)
		span.SetStatus(output.StatusError, "layer query failed")
		return false
	}

	if len(req.Properties) > 0 {
		features = s.filterProperties(features, req.Properties)
	}

	features, maxReached := s.applyMaxFeaturesLimit(features, result)
	result.Features = append(result.Features, features...)

	span.SetAttributes(
		output.Int("ortus.layer.features.count", len(features)),
		output.Bool("ortus.max_features_reached", maxReached),
	)
	return maxReached
}

// transformCoordinate transforms the coordinate to the layer's SRID if needed.
func (s *QueryService) transformCoordinate(ctx context.Context, coord domain.Coordinate, layer *domain.Layer) (domain.Coordinate, bool) {
	if coord.SRID == layer.SRID {
		return coord, true
	}

	if s.transformer == nil {
		s.logger.Debug("skipping layer due to SRID mismatch", "layer", layer.Name, "layer_srid", layer.SRID, "query_srid", coord.SRID)
		return coord, false
	}

	ctx, span := s.tracer.Start(ctx, "QueryService.transformCoordinate",
		output.WithAttributes(
			output.Int("ortus.coordinate.from_srid", coord.SRID),
			output.Int("ortus.coordinate.to_srid", layer.SRID),
		),
	)
	defer span.End()

	transformed, err := s.transformer.Transform(ctx, coord, layer.SRID)
	if err != nil {
		if isCanceled(err) {
			s.logger.Debug("coordinate transformation canceled", "from_srid", coord.SRID, "to_srid", layer.SRID, "error", err)
			return coord, false
		}
		s.logger.Warn("coordinate transformation failed", "from_srid", coord.SRID, "to_srid", layer.SRID, "error", err)
		span.RecordError(err)
		span.SetStatus(output.StatusError, "transform failed")
		return coord, false
	}
	return transformed, true
}

// isCanceled reports whether err is a client-side context cancellation — an
// expected outcome when the caller aborts the request (e.g. the map UI cancels
// the previous in-flight query), not a failure worth warning about.
//
// context.DeadlineExceeded is deliberately NOT treated as expected: the server
// applies its own query.timeout via context.WithTimeout, so a deadline is a real
// "query too slow" signal that should keep warning.
func isCanceled(err error) bool {
	return errors.Is(err, context.Canceled)
}

// applyMaxFeaturesLimit limits features to not exceed maxFeatures. Returns true if limit reached.
func (s *QueryService) applyMaxFeaturesLimit(features []domain.Feature, result *domain.QueryResult) ([]domain.Feature, bool) {
	total := len(result.Features) + len(features)
	if total <= s.maxFeatures {
		return features, false
	}

	remaining := s.maxFeatures - len(result.Features)
	if remaining > 0 {
		return features[:remaining], true
	}
	return nil, true
}

// filterProperties filters feature properties to only include requested ones.
func (s *QueryService) filterProperties(features []domain.Feature, properties []string) []domain.Feature {
	propSet := make(map[string]bool, len(properties))
	for _, p := range properties {
		propSet[p] = true
	}

	for i := range features {
		filtered := make(map[string]interface{})
		for key, value := range features[i].Properties {
			if propSet[key] {
				filtered[key] = value
			}
		}
		features[i].Properties = filtered //#nosec G602 -- i is the loop index over features, always in range
	}

	return features
}
