package application

import (
	"context"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/input"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// sourceInspector is the minimal registry surface the health service needs.
// Declared consumer-side so the service depends on an interface, not the
// concrete *SourceRegistry.
type sourceInspector interface {
	ListSources(ctx context.Context) ([]domain.Source, error)
	GetSourceStatus(ctx context.Context, id string) (domain.SourceStatus, error)
	InitialLoadComplete() bool
}

// HealthService provides health check functionality.
type HealthService struct {
	registry sourceInspector
	tracer   output.Tracer
	// readyWhenEmpty: when true (default), a fully-loaded service with no ready
	// source still reports ready ("no data today"). When false, readiness
	// additionally requires at least one ready source.
	readyWhenEmpty bool
}

// NewHealthService creates a new health service. readyWhenEmpty controls the
// no-source readiness policy (see HealthService.readyWhenEmpty).
func NewHealthService(registry sourceInspector, readyWhenEmpty bool, tracer output.Tracer) *HealthService {
	if tracer == nil {
		tracer = output.NoOpTracer{}
	}
	return &HealthService{
		registry:       registry,
		tracer:         tracer,
		readyWhenEmpty: readyWhenEmpty,
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

	sources, err := s.registry.ListSources(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "list sources failed")
		span.SetAttributes(output.Bool("health.ready", false))
		return false
	}
	span.SetAttributes(output.Int("health.sources_total", len(sources)))

	// A usable source means ready, regardless of the load latch — this also
	// covers sources brought online by sync after the initial pass (or after a
	// startup where storage was briefly unreachable).
	for _, src := range sources {
		if src.IsReady() {
			span.SetAttributes(output.Bool("health.ready", true), output.String("health.reason", "source_ready"))
			return true
		}
	}

	// No ready source. Until the initial load pass completes we're still
	// bringing data online → not ready, so clients retry. After it completes,
	// "no ready source" means no data (or all failed/reindexing): readyWhenEmpty
	// (default true) treats that as ready ("no data today"); false keeps the
	// instance out of the LB until a source is ready. A startup where storage
	// was unreachable stays not-ready (loud) until sync brings a source up.
	var ready bool
	var reason string
	switch {
	case !s.registry.InitialLoadComplete():
		ready, reason = false, "initial_load"
	case len(sources) == 0:
		ready, reason = s.readyWhenEmpty, "no_sources"
	default:
		ready, reason = s.readyWhenEmpty, "no_ready_sources"
	}
	span.SetAttributes(output.Bool("health.ready", ready), output.String("health.reason", reason))
	return ready
}

// GetHealthDetails returns detailed health information.
func (s *HealthService) GetHealthDetails(ctx context.Context) input.HealthDetails {
	ctx, span := s.tracer.Start(ctx, "HealthService.GetHealthDetails")
	defer span.End()

	sources, _ := s.registry.ListSources(ctx)

	loaded := len(sources)
	ready := 0
	states := make([]input.SourceState, 0, len(sources))
	for _, src := range sources {
		st, err := s.registry.GetSourceStatus(ctx, src.ID)
		if err != nil {
			// Source vanished between ListSources and here (concurrent unload) —
			// skip it rather than reporting an empty status.
			continue
		}
		isReady := src.IsReady()
		if isReady {
			ready++
		}
		states = append(states, input.SourceState{ID: src.ID, Status: string(st), Ready: isReady})
	}

	components := map[string]string{
		"storage": "ok",
	}

	span.SetAttributes(
		output.Int("health.sources_loaded", loaded),
		output.Int("health.sources_ready", ready),
	)

	return input.HealthDetails{
		Healthy:       s.IsHealthy(ctx),
		Ready:         s.IsReady(ctx),
		SourcesLoaded: loaded,
		SourcesReady:  ready,
		Components:    components,
		Sources:       states,
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

	sources, _ := s.registry.ListSources(ctx)

	health := make([]SourceHealth, len(sources))
	for i, src := range sources {
		status, _ := s.registry.GetSourceStatus(ctx, src.ID)
		health[i] = SourceHealth{
			ID:     src.ID,
			Status: status,
			Ready:  src.IsReady(),
		}
	}

	span.SetAttributes(output.Int("health.sources.count", len(health)))
	return health
}
