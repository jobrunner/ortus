package application

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

func TestPackageRegistryLoadUnload(t *testing.T) {
	repo := &mockRepository{
		packages: map[string]*domain.GeoPackage{
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

	registry := NewPackageRegistry(
		repo,
		&mockStorage{},
		&output.NoOpMetrics{},
		slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		"/tmp",
	)

	ctx := context.Background()

	// Load package
	err := registry.LoadPackage(ctx, "/data/test.gpkg")
	if err != nil {
		t.Fatalf("LoadPackage failed: %v", err)
	}

	// Verify package is loaded
	packages, err := registry.ListPackages(ctx)
	if err != nil {
		t.Fatalf("ListPackages failed: %v", err)
	}
	if len(packages) != 1 {
		t.Errorf("len(packages) = %d, want 1", len(packages))
	}

	// Get package
	pkg, err := registry.GetPackage(ctx, "test")
	if err != nil {
		t.Fatalf("GetPackage failed: %v", err)
	}
	if pkg.ID != "test" {
		t.Errorf("pkg.ID = %q, want %q", pkg.ID, "test")
	}

	// Unload package
	err = registry.UnloadPackage(ctx, "test")
	if err != nil {
		t.Fatalf("UnloadPackage failed: %v", err)
	}

	// Verify package is unloaded
	packages, _ = registry.ListPackages(ctx)
	if len(packages) != 0 {
		t.Errorf("len(packages) = %d, want 0", len(packages))
	}
}

func TestPackageRegistryGetPackageNotFound(t *testing.T) {
	registry := newTestRegistry()
	ctx := context.Background()

	_, err := registry.GetPackage(ctx, "nonexistent")
	if err != domain.ErrPackageNotFound {
		t.Errorf("err = %v, want %v", err, domain.ErrPackageNotFound)
	}
}

func TestPackageRegistryGetPackageStatus(t *testing.T) {
	registry := newTestRegistry()
	ctx := context.Background()

	registry.mu.Lock()
	registry.packages["test"] = &packageEntry{
		Package: &domain.GeoPackage{ID: "test"},
		Status:  domain.StatusReady,
	}
	registry.mu.Unlock()

	status, err := registry.GetPackageStatus(ctx, "test")
	if err != nil {
		t.Fatalf("GetPackageStatus failed: %v", err)
	}
	if status != domain.StatusReady {
		t.Errorf("status = %s, want %s", status, domain.StatusReady)
	}
}

func TestPackageRegistryGetPackageStatusNotFound(t *testing.T) {
	registry := newTestRegistry()
	ctx := context.Background()

	_, err := registry.GetPackageStatus(ctx, "nonexistent")
	if err != domain.ErrPackageNotFound {
		t.Errorf("err = %v, want %v", err, domain.ErrPackageNotFound)
	}
}

func TestPackageRegistryIsReady(t *testing.T) {
	registry := newTestRegistry()

	registry.mu.Lock()
	registry.packages["ready"] = &packageEntry{
		Package: &domain.GeoPackage{ID: "ready"},
		Status:  domain.StatusReady,
	}
	registry.packages["loading"] = &packageEntry{
		Package: &domain.GeoPackage{ID: "loading"},
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

func TestPackageRegistryReadyPackageIDs(t *testing.T) {
	registry := newTestRegistry()

	registry.mu.Lock()
	registry.packages["ready1"] = &packageEntry{
		Package: &domain.GeoPackage{ID: "ready1"},
		Status:  domain.StatusReady,
	}
	registry.packages["ready2"] = &packageEntry{
		Package: &domain.GeoPackage{ID: "ready2"},
		Status:  domain.StatusReady,
	}
	registry.packages["loading"] = &packageEntry{
		Package: &domain.GeoPackage{ID: "loading"},
		Status:  domain.StatusLoading,
	}
	registry.mu.Unlock()

	ids := registry.ReadyPackageIDs()
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

func TestPackageRegistryUnloadNonexistent(t *testing.T) {
	registry := newTestRegistry()
	ctx := context.Background()

	// Should not error when unloading nonexistent package
	err := registry.UnloadPackage(ctx, "nonexistent")
	if err != nil {
		t.Errorf("UnloadPackage for nonexistent should not error, got: %v", err)
	}
}
