package application

import (
	"context"
	"log/slog"
	"time"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// QueryService handles point queries across GeoPackages.
type QueryService struct {
	registry    *PackageRegistry
	repo        output.GeoPackageRepository
	transformer output.CoordinateTransformer
	metrics     output.MetricsCollector
	logger      *slog.Logger
	defaultSRID int
	maxFeatures int
}

// QueryServiceConfig holds configuration for the query service.
type QueryServiceConfig struct {
	DefaultSRID int
	MaxFeatures int
}

// NewQueryService creates a new query service.
func NewQueryService(
	registry *PackageRegistry,
	repo output.GeoPackageRepository,
	transformer output.CoordinateTransformer,
	metrics output.MetricsCollector,
	logger *slog.Logger,
	cfg QueryServiceConfig,
) *QueryService {
	if cfg.DefaultSRID == 0 {
		cfg.DefaultSRID = domain.SRIDWGS84
	}
	if cfg.MaxFeatures == 0 {
		cfg.MaxFeatures = 1000
	}

	return &QueryService{
		registry:    registry,
		repo:        repo,
		transformer: transformer,
		metrics:     metrics,
		logger:      logger,
		defaultSRID: cfg.DefaultSRID,
		maxFeatures: cfg.MaxFeatures,
	}
}

// QueryPoint performs a point query across all registered GeoPackages.
func (s *QueryService) QueryPoint(ctx context.Context, req domain.QueryRequest) (*domain.QueryResponse, error) {
	start := time.Now()

	response := &domain.QueryResponse{
		Coordinate: req.Coordinate,
	}

	// Validate coordinate
	if err := req.Coordinate.Validate(); err != nil {
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
			return nil, domain.ErrPackageNotFound
		}
	}

	// Query each package
	for _, pkgID := range packageIDs {
		result, err := s.QueryPointInPackage(ctx, pkgID, req)
		if err != nil {
			s.logger.Warn("query failed for package", "package", pkgID, "error", err)
			s.metrics.IncQueryCount(pkgID, false)
			continue
		}

		if result.HasFeatures() {
			response.AddResult(*result)
		}
		s.metrics.IncQueryCount(pkgID, true)
	}

	response.ProcessingTime = time.Since(start)
	return response, nil
}

// QueryPointInPackage performs a point query in a specific GeoPackage.
func (s *QueryService) QueryPointInPackage(ctx context.Context, packageID string, req domain.QueryRequest) (*domain.QueryResult, error) {
	start := time.Now()

	// Get package info
	pkg, err := s.registry.GetPackage(ctx, packageID)
	if err != nil {
		return nil, err
	}

	result := &domain.QueryResult{
		PackageID:   pkg.ID,
		PackageName: pkg.Name,
		License:     pkg.License,
	}

	// Query each layer
	for _, layer := range pkg.Layers {
		if s.queryLayer(ctx, packageID, &layer, &req, result) {
			break // max features reached
		}
	}

	result.QueryTime = time.Since(start)
	s.metrics.ObserveQueryDuration(packageID, result.QueryTime)

	return result, nil
}

// queryLayer queries a single layer and appends results. Returns true if max features reached.
func (s *QueryService) queryLayer(ctx context.Context, packageID string, layer *domain.Layer, req *domain.QueryRequest, result *domain.QueryResult) bool {
	queryCoord, ok := s.transformCoordinate(ctx, req.Coordinate, layer)
	if !ok {
		return false
	}

	features, err := s.repo.QueryPoint(ctx, packageID, layer.Name, queryCoord)
	if err != nil {
		s.logger.Warn("layer query failed", "package", packageID, "layer", layer.Name, "error", err)
		return false
	}

	if len(req.Properties) > 0 {
		features = s.filterProperties(features, req.Properties)
	}

	features, maxReached := s.applyMaxFeaturesLimit(features, result)
	result.Features = append(result.Features, features...)
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

	transformed, err := s.transformer.Transform(ctx, coord, layer.SRID)
	if err != nil {
		s.logger.Warn("coordinate transformation failed", "from_srid", coord.SRID, "to_srid", layer.SRID, "error", err)
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
		features[i].Properties = filtered
	}

	return features
}
