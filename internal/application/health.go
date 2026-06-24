package application

import (
	"context"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/input"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// HealthService provides health check functionality.
type HealthService struct {
	registry *SourceRegistry
	tracer   output.Tracer
}

// NewHealthService creates a new health service.
func NewHealthService(registry *SourceRegistry, tracer output.Tracer) *HealthService {
	if tracer == nil {
		tracer = output.NoOpTracer{}
	}
	return &HealthService{
		registry: registry,
		tracer:   tracer,
	}
}

// IsHealthy returns true if the service is healthy.
func (s *HealthService) IsHealthy(ctx context.Context) bool {
	_, span := s.tracer.Start(ctx, "HealthService.IsHealthy")
	defer span.End()
	span.SetAttributes(output.Bool("health.healthy", true))
	return true // Basic health check
}

// IsReady returns true if the service is ready to accept requests.
func (s *HealthService) IsReady(ctx context.Context) bool {
	ctx, span := s.tracer.Start(ctx, "HealthService.IsReady")
	defer span.End()

	packages, err := s.registry.ListSources(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "list packages failed")
		span.SetAttributes(output.Bool("health.ready", false))
		return false
	}

	span.SetAttributes(output.Int("health.packages_total", len(packages)))

	// Ready if at least one package is ready
	for _, pkg := range packages {
		if pkg.IsReady() {
			span.SetAttributes(output.Bool("health.ready", true), output.String("health.reason", "package_ready"))
			return true
		}
	}

	// Also ready if no packages are configured (empty state)
	ready := len(packages) == 0
	reason := "no_packages"
	if !ready {
		reason = "no_ready_packages"
	}
	span.SetAttributes(output.Bool("health.ready", ready), output.String("health.reason", reason))
	return ready
}

// GetHealthDetails returns detailed health information.
func (s *HealthService) GetHealthDetails(ctx context.Context) input.HealthDetails {
	ctx, span := s.tracer.Start(ctx, "HealthService.GetHealthDetails")
	defer span.End()

	packages, _ := s.registry.ListSources(ctx)

	loaded := len(packages)
	ready := 0
	for _, pkg := range packages {
		if pkg.IsReady() {
			ready++
		}
	}

	components := map[string]string{
		"storage": "ok",
	}

	span.SetAttributes(
		output.Int("health.packages_loaded", loaded),
		output.Int("health.packages_ready", ready),
	)

	return input.HealthDetails{
		Healthy:        s.IsHealthy(ctx),
		Ready:          s.IsReady(ctx),
		PackagesLoaded: loaded,
		PackagesReady:  ready,
		Components:     components,
	}
}

// SourceHealth contains health info for a single source.
type SourceHealth struct {
	ID     string
	Status domain.SourceStatus
	Ready  bool
}

// GetSourceHealth returns health info for all sources.
func (s *HealthService) GetSourceHealth(ctx context.Context) []SourceHealth {
	ctx, span := s.tracer.Start(ctx, "HealthService.GetSourceHealth")
	defer span.End()

	packages, _ := s.registry.ListSources(ctx)

	health := make([]SourceHealth, len(packages))
	for i, pkg := range packages {
		status, _ := s.registry.GetSourceStatus(ctx, pkg.ID)
		health[i] = SourceHealth{
			ID:     pkg.ID,
			Status: status,
			Ready:  pkg.IsReady(),
		}
	}

	span.SetAttributes(output.Int("health.packages.count", len(health)))
	return health
}
