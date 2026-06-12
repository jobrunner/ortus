package telemetry_test

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/jobrunner/ortus/internal/adapters/telemetry"
	"github.com/jobrunner/ortus/internal/application"
	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// stubRepo is a minimal GeoPackageRepository: it reports one package + layer
// and returns no features. Enough to exercise the query span tree.
type stubRepo struct{}

func (stubRepo) Open(_ context.Context, _ string) (*domain.GeoPackage, error) {
	return nil, domain.ErrPackageNotFound
}
func (stubRepo) Close(_ context.Context, _ string) error { return nil }
func (stubRepo) GetLayers(_ context.Context, _ string) ([]domain.Layer, error) {
	return nil, nil
}
func (stubRepo) QueryPoint(_ context.Context, _, _ string, _ domain.Coordinate) ([]domain.Feature, error) {
	return nil, nil
}
func (stubRepo) CreateSpatialIndex(_ context.Context, _, _ string) error { return nil }
func (stubRepo) HasSpatialIndex(_ context.Context, _, _ string) (bool, error) {
	return true, nil
}

// TestEndToEnd_QueryServiceProducesSpansInBuffer asserts that the full
// QueryService → Registry → Repo chain, when given a tracer wired to the
// in-memory buffer, captures the expected span tree. This is the contract
// the future MCP server depends on.
func TestEndToEnd_QueryServiceProducesSpansInBuffer(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	provider, err := telemetry.NewProvider(context.Background(), telemetry.ProviderOptions{
		ServiceName: "ortus-test",
		SampleRatio: 1.0,
		BufferSize:  16,
	}, logger)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	tracer := telemetry.NewTracer(provider.TracerProvider())

	registry := application.NewPackageRegistry(
		stubRepo{},
		nil, // storage unused in this path
		&output.NoOpMetrics{},
		tracer,
		logger,
		"/tmp",
	)
	qs := application.NewQueryService(registry, stubRepo{}, nil, &output.NoOpMetrics{}, tracer, logger, application.QueryServiceConfig{})

	req := domain.QueryRequest{Coordinate: domain.NewCoordinate(13.4, 52.5, domain.SRIDWGS84)}
	if _, err := qs.QueryPoint(context.Background(), req); err != nil {
		t.Fatalf("QueryPoint: %v", err)
	}

	traces := provider.Buffer().ListTraces(telemetry.TraceFilter{})
	if len(traces) == 0 {
		t.Fatal("expected at least one trace in buffer")
	}
	root := traces[0]
	if root.RootName != "QueryService.QueryPoint" {
		t.Errorf("RootName = %q, want %q", root.RootName, "QueryService.QueryPoint")
	}

	// Verify expected attributes survived the capture round-trip.
	if root.Spans[0].Attributes["ortus.coordinate.srid"] == nil {
		t.Errorf("expected ortus.coordinate.srid attribute on root span; got %v", root.Spans[0].Attributes)
	}
}
