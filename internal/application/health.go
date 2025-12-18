package application

import (
	"context"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/input"
)

// HealthService provides health check functionality.
type HealthService struct {
	registry *PackageRegistry
}

// NewHealthService creates a new health service.
func NewHealthService(registry *PackageRegistry) *HealthService {
	return &HealthService{
		registry: registry,
	}
}

// IsHealthy returns true if the service is healthy.
func (s *HealthService) IsHealthy(ctx context.Context) bool {
	return true // Basic health check
}

// IsReady returns true if the service is ready to accept requests.
func (s *HealthService) IsReady(ctx context.Context) bool {
	packages, err := s.registry.ListPackages(ctx)
	if err != nil {
		return false
	}

	// Ready if at least one package is ready
	for _, pkg := range packages {
		if pkg.IsReady() {
			return true
		}
	}

	// Also ready if no packages are configured (empty state)
	return len(packages) == 0
}

// GetHealthDetails returns detailed health information.
func (s *HealthService) GetHealthDetails(ctx context.Context) input.HealthDetails {
	packages, _ := s.registry.ListPackages(ctx)

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

	return input.HealthDetails{
		Healthy:        s.IsHealthy(ctx),
		Ready:          s.IsReady(ctx),
		PackagesLoaded: loaded,
		PackagesReady:  ready,
		Components:     components,
	}
}

// PackageHealth contains health info for a single package.
type PackageHealth struct {
	ID     string
	Status domain.GeoPackageStatus
	Ready  bool
}

// GetPackageHealth returns health info for all packages.
func (s *HealthService) GetPackageHealth(ctx context.Context) []PackageHealth {
	packages, _ := s.registry.ListPackages(ctx)

	health := make([]PackageHealth, len(packages))
	for i, pkg := range packages {
		status, _ := s.registry.GetPackageStatus(ctx, pkg.ID)
		health[i] = PackageHealth{
			ID:     pkg.ID,
			Status: status,
			Ready:  pkg.IsReady(),
		}
	}

	return health
}
