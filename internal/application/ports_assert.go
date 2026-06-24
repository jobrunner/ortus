package application

import "github.com/jobrunner/ortus/internal/ports/input"

// Compile-time assertions that the application services satisfy the driving
// ports. Adapters depend on these interfaces; these checks fail the build if a
// service ever drifts from its port contract.
var (
	_ input.QueryService   = (*QueryService)(nil)
	_ input.SourceRegistry = (*SourceRegistry)(nil)
	_ input.HealthChecker  = (*HealthService)(nil)
	_ input.Syncer         = (*SyncService)(nil)
)
