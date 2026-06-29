package telemetry_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/metric/noop"

	"github.com/jobrunner/ortus/internal/adapters/storage"
	"github.com/jobrunner/ortus/internal/adapters/telemetry"
	"github.com/jobrunner/ortus/internal/application"
	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// coverageRepo is a fake SpatialSource that lets every traced method
// run to completion without hitting SQLite.
type coverageRepo struct{}

func (coverageRepo) Open(_ context.Context, path string) (*domain.Source, error) {
	return &domain.Source{
		ID:   "fake",
		Name: "fake.gpkg",
		Path: path,
		Layers: []domain.Layer{
			{Name: "regions", GeometryColumn: "geom", GeometryType: "POLYGON", SRID: 4326, HasIndex: true},
		},
	}, nil
}
func (coverageRepo) Close(_ context.Context, _ string) error { return nil }
func (coverageRepo) GetLayers(_ context.Context, _ string) ([]domain.Layer, error) {
	return []domain.Layer{{Name: "regions"}}, nil
}
func (coverageRepo) QueryPoint(_ context.Context, _, _ string, _ domain.Coordinate) ([]domain.Feature, error) {
	return nil, nil
}
func (coverageRepo) CreateSpatialIndex(_ context.Context, _, _ string) error { return nil }
func (coverageRepo) HasSpatialIndex(_ context.Context, _, _ string) (bool, error) {
	return true, nil
}

// coverageStorage satisfies output.ObjectStorage with no real I/O.
type coverageStorage struct{}

func (coverageStorage) List(_ context.Context) ([]output.StorageObject, error) {
	return []output.StorageObject{{Key: "fake.gpkg", Size: 1024}}, nil
}
func (coverageStorage) Download(_ context.Context, _, _ string) error { return nil }
func (coverageStorage) GetReader(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, errors.New("unused")
}
func (coverageStorage) Exists(_ context.Context, _ string) (bool, error) { return true, nil }

// TestTracingCoverage_AllPathsProduceSpans is the contract enforced for the
// MCP server: every named operation in the application MUST produce a span
// the MCP can later return to Claude. If you add a new traced operation, add
// it to wantSpans and exercise it from this test.
// (Naturally branchy; gocyclo is already excluded for _test.go files.)
func TestTracingCoverage_AllPathsProduceSpans(t *testing.T) {
	wantSpans := []string{
		// App / startup
		"App.Startup",
		// Storage decorator
		"ObjectStorage.List",
		"ObjectStorage.Download",
		// Registry
		"SourceRegistry.LoadAll",
		"SourceRegistry.LoadSource",
		"SourceRegistry.UnloadSource",
		"SourceRegistry.ListSources",
		"SourceRegistry.GetSource",
		"SourceRegistry.GetSourceStatus",
		// Repository (via fake — won't hit SQL paths, but every interface method must trace)
		"Repository.Open",
		"Repository.Close",
		"Repository.GetLayers",
		"Repository.HasSpatialIndex",
		"Repository.CreateSpatialIndex",
		// QueryService
		"QueryService.QueryPoint",
		"QueryService.QueryPointInSource",
		"QueryService.queryLayer",
		// HealthService
		"HealthService.IsHealthy",
		"HealthService.IsReady",
		"HealthService.GetHealthDetails",
		"HealthService.GetSourceHealth",
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	provider, err := telemetry.NewProvider(context.Background(), telemetry.ProviderOptions{
		ServiceName: "ortus-coverage",
		SampleRatio: 1.0,
		BufferSize:  512,
	}, logger)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	tr := telemetry.NewTracer(provider.TracerProvider())

	repo := &tracedFakeRepo{inner: coverageRepo{}, tracer: tr}
	store := storage.NewTracedStorage(coverageStorage{}, tr, "local")
	reg := application.NewSourceRegistry([]output.SpatialSource{repo}, store, noop.NewMeterProvider().Meter("test"), tr, logger, "/tmp")
	qs := application.NewQueryService(reg, nil, noop.NewMeterProvider().Meter("test"), tr, logger, application.QueryServiceConfig{})
	hs := application.NewHealthService(reg, true, tr)

	// Each "request" runs in its own root context — this mirrors how
	// otelmux creates a fresh root span per incoming HTTP request in prod.
	asRequest := func(name string, fn func(ctx context.Context)) {
		ctx, span := tr.Start(context.Background(), name)
		defer span.End()
		fn(ctx)
	}

	asRequest("App.Startup", func(ctx context.Context) {
		if err := reg.LoadAll(ctx); err != nil {
			t.Fatalf("LoadAll: %v", err)
		}
	})

	asRequest("HTTP GET /api/v1/query", func(ctx context.Context) {
		req := domain.QueryRequest{Coordinate: domain.NewCoordinate(13.4, 52.5, domain.SRIDWGS84)}
		if _, err := qs.QueryPoint(ctx, req); err != nil {
			t.Fatalf("QueryPoint: %v", err)
		}
	})

	asRequest("HTTP GET /api/v1/sources", func(ctx context.Context) {
		if _, err := reg.ListSources(ctx); err != nil {
			t.Fatalf("ListSources: %v", err)
		}
	})
	asRequest("HTTP GET /api/v1/sources/{id}", func(ctx context.Context) {
		if _, err := reg.GetSource(ctx, "fake"); err != nil {
			t.Fatalf("GetSource: %v", err)
		}
		if _, err := reg.GetSourceStatus(ctx, "fake"); err != nil {
			t.Fatalf("GetSourceStatus: %v", err)
		}
	})

	asRequest("HTTP GET /health", func(ctx context.Context) {
		_ = hs.IsHealthy(ctx)
		_ = hs.IsReady(ctx)
		_ = hs.GetHealthDetails(ctx)
		_ = hs.GetSourceHealth(ctx)
	})

	// GetLayers and HasSpatialIndex aren't reached through the higher-level
	// paths above, exercise them directly through the same traced wrapper.
	asRequest("RepoOps", func(ctx context.Context) {
		if _, err := repo.GetLayers(ctx, "fake"); err != nil {
			t.Fatalf("GetLayers: %v", err)
		}
		if _, err := repo.HasSpatialIndex(ctx, "fake", "regions"); err != nil {
			t.Fatalf("HasSpatialIndex: %v", err)
		}
	})

	asRequest("watcher delete event", func(ctx context.Context) {
		if err := reg.UnloadSource(ctx, "fake"); err != nil {
			t.Fatalf("UnloadSource: %v", err)
		}
	})

	// Collect span names from the ring buffer.
	traces := provider.Buffer().ListTraces(telemetry.TraceFilter{})
	seen := map[string]bool{}
	for _, ct := range traces {
		for _, sp := range ct.Spans {
			seen[sp.Name] = true
		}
	}

	var missing []string
	for _, name := range wantSpans {
		if !seen[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		all := make([]string, 0, len(seen))
		for n := range seen {
			all = append(all, n)
		}
		t.Fatalf("missing spans: %v\nspans seen (%d): %s", missing, len(all), strings.Join(all, ", "))
	}
}

// tracedFakeRepo wraps coverageRepo with manual spans for the methods the
// real *geopackage.Repository instruments. The real one needs SQLite + a
// GeoPackage file; this wrapper mirrors the span names so the coverage
// contract test can run hermetically.
type tracedFakeRepo struct {
	inner  coverageRepo
	tracer output.Tracer
}

func (r *tracedFakeRepo) Open(ctx context.Context, path string) (*domain.Source, error) {
	_, span := r.tracer.Start(ctx, "Repository.Open")
	defer span.End()
	return r.inner.Open(ctx, path)
}
func (r *tracedFakeRepo) Supports(_ string) bool { return true }
func (r *tracedFakeRepo) Prepare(ctx context.Context, packageID, layerName string) error {
	return r.CreateSpatialIndex(ctx, packageID, layerName)
}
func (r *tracedFakeRepo) CreateSpatialIndex(ctx context.Context, packageID, layerName string) error {
	_, span := r.tracer.Start(ctx, "Repository.CreateSpatialIndex")
	defer span.End()
	return r.inner.CreateSpatialIndex(ctx, packageID, layerName)
}
func (r *tracedFakeRepo) GetLayers(ctx context.Context, packageID string) ([]domain.Layer, error) {
	_, span := r.tracer.Start(ctx, "Repository.GetLayers")
	defer span.End()
	return r.inner.GetLayers(ctx, packageID)
}
func (r *tracedFakeRepo) HasSpatialIndex(ctx context.Context, packageID, layerName string) (bool, error) {
	_, span := r.tracer.Start(ctx, "Repository.HasSpatialIndex")
	defer span.End()
	return r.inner.HasSpatialIndex(ctx, packageID, layerName)
}
func (r *tracedFakeRepo) Close(ctx context.Context, packageID string) error {
	_, span := r.tracer.Start(ctx, "Repository.Close")
	defer span.End()
	return r.inner.Close(ctx, packageID)
}
func (r *tracedFakeRepo) QueryPoint(ctx context.Context, packageID, layerName string, coord domain.Coordinate) ([]domain.Feature, error) {
	ctx, span := r.tracer.Start(ctx, "Repository.QueryPoint")
	defer span.End()
	return r.inner.QueryPoint(ctx, packageID, layerName, coord)
}
