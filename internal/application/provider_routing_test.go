package application

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// extProvider is a SpatialSource that only claims paths with a given extension
// and tags every source/feature it produces, so tests can prove which adapter
// the registry routed a path to.
type extProvider struct {
	ext string
	tag string
}

func (p *extProvider) Supports(path string) bool { return strings.HasSuffix(path, p.ext) }

func (p *extProvider) Open(_ context.Context, path string) (*domain.Source, error) {
	return &domain.Source{
		ID:     derivePackageID(path),
		Name:   p.tag,
		Path:   path,
		Kind:   domain.SourceKindVector,
		Layers: []domain.Layer{{Name: "l", SRID: 4326}},
	}, nil
}

func (p *extProvider) Prepare(_ context.Context, _, _ string) error { return nil }

func (p *extProvider) QueryPoint(_ context.Context, _, _ string, _ domain.Coordinate) ([]domain.Feature, error) {
	return []domain.Feature{{Properties: map[string]interface{}{"provider": p.tag}}}, nil
}

func (p *extProvider) Close(_ context.Context, _ string) error { return nil }

func newRoutingRegistry(providers []output.SpatialSource) *PackageRegistry {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	return NewPackageRegistry(providers, &mockStorage{}, testMeter(), output.NoOpTracer{}, logger, "/tmp")
}

func TestRegistry_ProviderRoutingByExtension(t *testing.T) {
	gpkg := &extProvider{ext: ".gpkg", tag: "vector"}
	zip := &extProvider{ext: ".zip", tag: "raster"}
	reg := newRoutingRegistry([]output.SpatialSource{gpkg, zip})
	ctx := context.Background()

	if err := reg.LoadPackage(ctx, "/data/a.gpkg"); err != nil {
		t.Fatalf("load gpkg: %v", err)
	}
	if err := reg.LoadPackage(ctx, "/data/b.zip"); err != nil {
		t.Fatalf("load zip: %v", err)
	}

	// Each path must have been opened by the adapter that supports its extension.
	if src, _ := reg.GetPackage(ctx, "a"); src == nil || src.Name != "vector" {
		t.Errorf("a.gpkg routed to %v, want vector provider", src)
	}
	if src, _ := reg.GetPackage(ctx, "b"); src == nil || src.Name != "raster" {
		t.Errorf("b.zip routed to %v, want raster provider", src)
	}

	// Query must delegate to the owning adapter, not just any provider.
	feats, err := reg.Query(ctx, "b", "l", domain.NewWGS84Coordinate(1, 1))
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(feats) != 1 || feats[0].Properties["provider"] != "raster" {
		t.Errorf("query delegated to wrong provider: %+v", feats)
	}
}

func TestRegistry_ProviderOrderFirstMatchWins(t *testing.T) {
	first := &extProvider{ext: ".gpkg", tag: "first"}
	second := &extProvider{ext: ".gpkg", tag: "second"}
	reg := newRoutingRegistry([]output.SpatialSource{first, second})
	ctx := context.Background()

	if err := reg.LoadPackage(ctx, "/data/x.gpkg"); err != nil {
		t.Fatalf("load: %v", err)
	}
	src, _ := reg.GetPackage(ctx, "x")
	if src == nil || src.Name != "first" {
		t.Errorf("first matching provider should win, got %v", src)
	}
}

func TestRegistry_LoadUnsupportedSource(t *testing.T) {
	reg := newRoutingRegistry([]output.SpatialSource{&extProvider{ext: ".gpkg", tag: "vector"}})
	ctx := context.Background()

	err := reg.LoadPackage(ctx, "/data/elevation.tiff")
	if !errors.Is(err, domain.ErrUnsupportedSource) {
		t.Errorf("err = %v, want ErrUnsupportedSource", err)
	}
	if reg.PackageCount() != 0 {
		t.Errorf("unsupported source must not be registered, count = %d", reg.PackageCount())
	}
}

func TestRegistry_QueryUnknownSource(t *testing.T) {
	reg := newRoutingRegistry([]output.SpatialSource{&extProvider{ext: ".gpkg", tag: "vector"}})

	_, err := reg.Query(context.Background(), "missing", "l", domain.NewWGS84Coordinate(1, 1))
	if err != domain.ErrPackageNotFound {
		t.Errorf("err = %v, want ErrPackageNotFound", err)
	}
}

func TestRegistry_QueryEntryWithoutRepo(t *testing.T) {
	// A malformed entry (no owning adapter) must surface a clean error, not
	// a nil-pointer panic.
	reg := newRoutingRegistry(nil)
	reg.mu.Lock()
	reg.packages["broken"] = &packageEntry{
		Package: &domain.Source{ID: "broken"},
		Status:  domain.StatusReady,
		// Repo intentionally nil
	}
	reg.mu.Unlock()

	_, err := reg.Query(context.Background(), "broken", "l", domain.NewWGS84Coordinate(1, 1))
	if err != domain.ErrPackageNotFound {
		t.Errorf("err = %v, want ErrPackageNotFound", err)
	}
}

func TestRegistry_UnloadEntryWithoutRepo(t *testing.T) {
	// A malformed entry (no owning adapter) must be dropped by UnloadPackage,
	// not left stuck in StatusUnloading.
	reg := newRoutingRegistry(nil)
	reg.mu.Lock()
	reg.packages["broken"] = &packageEntry{
		Package: &domain.Source{ID: "broken"},
		Status:  domain.StatusReady,
		// Repo intentionally nil
	}
	reg.mu.Unlock()

	if err := reg.UnloadPackage(context.Background(), "broken"); err != nil {
		t.Fatalf("UnloadPackage: %v", err)
	}
	if reg.IsLoaded("broken") {
		t.Error("malformed entry should have been removed, not left stuck")
	}
}

func TestRegistry_NoProviders(t *testing.T) {
	reg := newRoutingRegistry(nil)
	err := reg.LoadPackage(context.Background(), "/data/a.gpkg")
	if !errors.Is(err, domain.ErrUnsupportedSource) {
		t.Errorf("empty provider set should reject all sources, got %v", err)
	}
}
