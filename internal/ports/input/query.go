// Package input defines the primary/driving ports of the application.
package input

import (
	"context"

	"github.com/jobrunner/ortus/internal/domain"
)

// QueryService defines the primary port for spatial queries.
type QueryService interface {
	// QueryPoint performs a point query across all registered GeoPackages.
	QueryPoint(ctx context.Context, req domain.QueryRequest) (*domain.QueryResponse, error)

	// QueryPointInPackage performs a point query in a specific GeoPackage.
	QueryPointInPackage(ctx context.Context, packageID string, req domain.QueryRequest) (*domain.QueryResult, error)
}

// PackageRegistry defines the primary port for GeoPackage management.
type PackageRegistry interface {
	// ListPackages returns all registered GeoPackages.
	ListPackages(ctx context.Context) ([]domain.GeoPackage, error)

	// GetPackage returns a specific GeoPackage by ID.
	GetPackage(ctx context.Context, id string) (*domain.GeoPackage, error)

	// GetPackageStatus returns the status of a GeoPackage.
	GetPackageStatus(ctx context.Context, id string) (domain.GeoPackageStatus, error)
}

// HealthChecker defines the primary port for health checks.
type HealthChecker interface {
	// IsHealthy returns true if the service is healthy.
	IsHealthy(ctx context.Context) bool

	// IsReady returns true if the service is ready to accept requests.
	IsReady(ctx context.Context) bool

	// GetHealthDetails returns detailed health information.
	GetHealthDetails(ctx context.Context) HealthDetails
}

// HealthDetails contains detailed health information.
type HealthDetails struct {
	Healthy        bool              // Overall health status
	Ready          bool              // Ready to accept requests
	PackagesLoaded int               // Number of loaded packages
	PackagesReady  int               // Number of ready packages
	Components     map[string]string // Component statuses
}
