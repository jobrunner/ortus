package application

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// QueryService handles point queries across GeoPackages.
type QueryService struct {
	registry      *PackageRegistry
	repo          output.GeoPackageRepository
	transformer   output.CoordinateTransformer
	tracer        output.Tracer
	queryCount    metric.Int64Counter
	queryDuration metric.Float64Histogram
	logger        *slog.Logger
	defaultSRID   int
	maxFeatures   int
}

// QueryServiceConfig holds configuration for the query service.
type QueryServiceConfig struct {
	DefaultSRID int
	MaxFeatures int
}

// NewQueryService creates a new query service. The meter is used directly
// to define query-level instruments — no MetricsCollector indirection. Pass
// noop.NewMeterProvider().Meter("test") to disable metrics in tests.
func NewQueryService(
	registry *PackageRegistry,
	repo output.GeoPackageRepository,
	transformer output.CoordinateTransformer,
	meter metric.Meter,
	tracer output.Tracer,
	logger *slog.Logger,
	cfg QueryServiceConfig,
) *QueryService {
	if cfg.DefaultSRID == 0 {
		cfg.DefaultSRID = domain.SRIDWGS84
	}
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
		repo:          repo,
		transformer:   transformer,
		tracer:        tracer,
		queryCount:    queryCount,
		queryDuration: queryDuration,
		logger:        logger,
		defaultSRID:   cfg.DefaultSRID,
		maxFeatures:   cfg.MaxFeatures,
	}
}

// QueryPoint performs a point query across all registered GeoPackages.
func (s *QueryService) QueryPoint(ctx context.Context, req domain.QueryRequest) (*domain.QueryResponse, error) {
	start := time.Now()

	ctx, span := s.tracer.Start(ctx, "QueryService.QueryPoint",
		output.WithAttributes(
			output.Float64("ortus.coordinate.x", req.Coordinate.X),
			output.Float64("ortus.coordinate.y", req.Coordinate.Y),
			output.Int("ortus.coordinate.srid", req.Coordinate.SRID),
			output.String("ortus.package.id", req.PackageID),
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

	// Get all ready packages
	packageIDs := s.registry.ReadyPackageIDs()

	// Filter by specific package if requested
	if req.PackageID != "" {
		found := false
		for _, id := range packageIDs {
			if id == req.PackageID {
				packageIDs = []string{req.PackageID}
				found = true
				break
			}
		}
		if !found {
			span.RecordError(domain.ErrPackageNotFound)
			span.SetStatus(output.StatusError, "package not found")
			return nil, domain.ErrPackageNotFound
		}
	}

	span.SetAttributes(output.Int("ortus.packages.queried", len(packageIDs)))

	// Query each package
	for _, pkgID := range packageIDs {
		result, err := s.QueryPointInPackage(ctx, pkgID, req)
		if err != nil {
			s.logger.Warn("query failed for package", "package", pkgID, "error", err)
			s.queryCount.Add(ctx, 1, metric.WithAttributes(
				attribute.String("package_id", pkgID),
				attribute.String("status", "error"),
			))
			continue
		}

		if result.HasFeatures() {
			response.AddResult(*result)
		}
		s.queryCount.Add(ctx, 1, metric.WithAttributes(
			attribute.String("package_id", pkgID),
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

// QueryPointInPackage performs a point query in a specific GeoPackage.
func (s *QueryService) QueryPointInPackage(ctx context.Context, packageID string, req domain.QueryRequest) (*domain.QueryResult, error) {
	start := time.Now()

	ctx, span := s.tracer.Start(ctx, "QueryService.QueryPointInPackage",
		output.WithAttributes(
			output.String("ortus.package.id", packageID),
		),
	)
	defer span.End()

	// Get package info
	pkg, err := s.registry.GetPackage(ctx, packageID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "get package")
		return nil, err
	}

	result := &domain.QueryResult{
		PackageID:   pkg.ID,
		PackageName: pkg.Name,
		License:     pkg.License,
	}

	span.SetAttributes(
		output.String("ortus.package.name", pkg.Name),
		output.Int("ortus.layers.count", len(pkg.Layers)),
	)

	// Query each layer
	maxReached := false
	for _, layer := range pkg.Layers {
		if s.queryLayer(ctx, packageID, &layer, &req, result) {
			maxReached = true
			break // max features reached
		}
	}

	result.QueryTime = time.Since(start)
	s.queryDuration.Record(ctx, result.QueryTime.Seconds(), metric.WithAttributes(
		attribute.String("package_id", packageID),
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
func (s *QueryService) queryLayer(ctx context.Context, packageID string, layer *domain.Layer, req *domain.QueryRequest, result *domain.QueryResult) bool {
	ctx, span := s.tracer.Start(ctx, "QueryService.queryLayer",
		output.WithAttributes(
			output.String("ortus.package.id", packageID),
			output.String("ortus.layer.name", layer.Name),
			output.Int("ortus.layer.srid", layer.SRID),
			output.String("ortus.layer.geometry_type", layer.GeometryType),
		),
	)
	defer span.End()

	queryCoord, ok := s.transformCoordinate(ctx, req.Coordinate, layer)
	if !ok {
		span.AddEvent("layer skipped due to SRID mismatch")
		return false
	}

	features, err := s.repo.QueryPoint(ctx, packageID, layer.Name, queryCoord)
	if err != nil {
		s.logger.Warn("layer query failed", "package", packageID, "layer", layer.Name, "error", err)
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
		s.logger.Warn("coordinate transformation failed", "from_srid", coord.SRID, "to_srid", layer.SRID, "error", err)
		span.RecordError(err)
		span.SetStatus(output.StatusError, "transform failed")
		return coord, false
	}
	return transformed, true
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
