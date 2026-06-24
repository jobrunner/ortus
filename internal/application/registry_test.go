package application

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

func TestSourceRegistryLoadUnload(t *testing.T) {
	repo := &mockRepository{
		packages: map[string]*domain.Source{
			"/data/test.gpkg": {
				ID:   "test",
				Name: "Test Package",
				Path: "/data/test.gpkg",
				Layers: []domain.Layer{
					{Name: "layer1", GeometryType: "POLYGON", SRID: 4326, HasIndex: true},
				},
			},
		},
	}

	registry := NewSourceRegistry(
		[]output.SpatialSource{repo},
		&mockStorage{},
		testMeter(),
		output.NoOpTracer{},
		slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		"/tmp",
	)

	ctx := context.Background()

	// Load package
	err := registry.LoadSource(ctx, "/data/test.gpkg")
	if err != nil {
		t.Fatalf("LoadSource failed: %v", err)
	}

	// Verify package is loaded
	packages, err := registry.ListSources(ctx)
	if err != nil {
		t.Fatalf("ListSources failed: %v", err)
	}
	if len(packages) != 1 {
		t.Errorf("len(packages) = %d, want 1", len(packages))
	}

	// Get package
	pkg, err := registry.GetSource(ctx, "test")
	if err != nil {
		t.Fatalf("GetSource failed: %v", err)
	}
	if pkg.ID != "test" {
		t.Errorf("pkg.ID = %q, want %q", pkg.ID, "test")
	}

	// Unload package
	err = registry.UnloadSource(ctx, "test")
	if err != nil {
		t.Fatalf("UnloadSource failed: %v", err)
	}

	// Verify package is unloaded
	packages, _ = registry.ListSources(ctx)
	if len(packages) != 0 {
		t.Errorf("len(packages) = %d, want 0", len(packages))
	}
}

func TestSourceRegistryGetSourceNotFound(t *testing.T) {
	registry := newTestRegistry()
	ctx := context.Background()

	_, err := registry.GetSource(ctx, "nonexistent")
	if err != domain.ErrPackageNotFound {
		t.Errorf("err = %v, want %v", err, domain.ErrPackageNotFound)
	}
}

func TestSourceRegistryGetSourceStatus(t *testing.T) {
	registry := newTestRegistry()
	ctx := context.Background()

	registry.mu.Lock()
	registry.packages["test"] = &sourceEntry{
		Package: &domain.Source{ID: "test"},
		Status:  domain.StatusReady,
	}
	registry.mu.Unlock()

	status, err := registry.GetSourceStatus(ctx, "test")
	if err != nil {
		t.Fatalf("GetSourceStatus failed: %v", err)
	}
	if status != domain.StatusReady {
		t.Errorf("status = %s, want %s", status, domain.StatusReady)
	}
}

func TestSourceRegistryGetSourceStatusNotFound(t *testing.T) {
	registry := newTestRegistry()
	ctx := context.Background()

	_, err := registry.GetSourceStatus(ctx, "nonexistent")
	if err != domain.ErrPackageNotFound {
		t.Errorf("err = %v, want %v", err, domain.ErrPackageNotFound)
	}
}

func TestSourceRegistryIsReady(t *testing.T) {
	registry := newTestRegistry()

	registry.mu.Lock()
	registry.packages["ready"] = &sourceEntry{
		Package: &domain.Source{ID: "ready"},
		Status:  domain.StatusReady,
	}
	registry.packages["loading"] = &sourceEntry{
		Package: &domain.Source{ID: "loading"},
		Status:  domain.StatusLoading,
	}
	registry.mu.Unlock()

	tests := []struct {
		pkgID string
		want  bool
	}{
		{"ready", true},
		{"loading", false},
		{"nonexistent", false},
	}

	for _, tt := range tests {
		t.Run(tt.pkgID, func(t *testing.T) {
			if got := registry.IsReady(tt.pkgID); got != tt.want {
				t.Errorf("IsReady(%q) = %v, want %v", tt.pkgID, got, tt.want)
			}
		})
	}
}

func TestSourceRegistryReadySourceIDs(t *testing.T) {
	registry := newTestRegistry()

	registry.mu.Lock()
	registry.packages["ready1"] = &sourceEntry{
		Package: &domain.Source{ID: "ready1"},
		Status:  domain.StatusReady,
	}
	registry.packages["ready2"] = &sourceEntry{
		Package: &domain.Source{ID: "ready2"},
		Status:  domain.StatusReady,
	}
	registry.packages["loading"] = &sourceEntry{
		Package: &domain.Source{ID: "loading"},
		Status:  domain.StatusLoading,
	}
	registry.mu.Unlock()

	ids := registry.ReadySourceIDs()
	if len(ids) != 2 {
		t.Errorf("len(ids) = %d, want 2", len(ids))
	}

	// Check that only ready packages are returned
	for _, id := range ids {
		if id != "ready1" && id != "ready2" {
			t.Errorf("unexpected package ID: %s", id)
		}
	}
}

func TestSourceRegistryUnloadNonexistent(t *testing.T) {
	registry := newTestRegistry()
	ctx := context.Background()

	// Should not error when unloading nonexistent package
	err := registry.UnloadSource(ctx, "nonexistent")
	if err != nil {
		t.Errorf("UnloadSource for nonexistent should not error, got: %v", err)
	}
}
