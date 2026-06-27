// Package input defines the primary/driving ports of the application. Driving
// adapters (HTTP, MCP) depend on these interfaces, not on the concrete
// application services, so the application core stays replaceable behind its
// left-hand edge.
package input

import (
	"context"
	"time"

	"github.com/jobrunner/ortus/internal/domain"
)

// QueryService defines the primary port for spatial queries across sources.
type QueryService interface {
	// QueryPoint performs a point query across all registered sources.
	QueryPoint(ctx context.Context, req domain.QueryRequest) (*domain.QueryResponse, error)

	// QueryPointInSource performs a point query in a specific source.
	QueryPointInSource(ctx context.Context, sourceID string, req domain.QueryRequest) (*domain.QueryResult, error)
}

// SourceRegistry defines the primary port for source management.
type SourceRegistry interface {
	// ListSources returns all registered sources.
	ListSources(ctx context.Context) ([]domain.Source, error)

	// GetSource returns a specific source by ID.
	GetSource(ctx context.Context, id string) (*domain.Source, error)

	// GetSourceStatus returns the status of a source.
	GetSourceStatus(ctx context.Context, id string) (domain.SourceStatus, error)
}

// Syncer defines the primary port for triggering storage synchronization.
type Syncer interface {
	// TriggerSync runs a synchronization with remote storage on demand,
	// returning what changed. May return domain.ErrRateLimited.
	TriggerSync(ctx context.Context) (SyncResult, error)
}

// SyncResult contains the outcome of a synchronization run. It is a driving-port
// DTO (like HealthDetails) returned to adapters that expose sync.
type SyncResult struct {
	SourcesAdded    int       `json:"sources_added"`
	SourcesRemoved  int       `json:"sources_removed"`
	SourcesTotal    int       `json:"sources_total"`
	SyncedAt        time.Time `json:"synced_at"`
	NextScheduledAt time.Time `json:"next_scheduled_at,omitempty"`
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
	Healthy       bool              // Overall health status
	Ready         bool              // Ready to accept requests
	SourcesLoaded int               // Number of loaded sources
	SourcesReady  int               // Number of ready sources
	Components    map[string]string // Component statuses
	Sources       []SourceState     // Per-source status (lets a client see which source is still indexing)
}

// SourceState is the per-source status exposed via /health, so a client can
// tell a specific source apart (e.g. a large one still "indexing") without the
// whole instance being marked not-ready.
type SourceState struct {
	ID     string `json:"id"`
	Status string `json:"status"` // loading | indexing | ready | error | unloading
	Ready  bool   `json:"ready"`
}
