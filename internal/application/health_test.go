package application

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

func newTestRegistry() *SourceRegistry {
	return NewSourceRegistry(
		[]output.SpatialSource{&mockRepository{}},
		&mockStorage{},
		testMeter(),
		output.NoOpTracer{},
		slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		"/tmp",
	)
}

func TestHealthServiceIsHealthy(t *testing.T) {
	registry := newTestRegistry()
	service := NewHealthService(registry, output.NoOpTracer{})

	if !service.IsHealthy(context.Background()) {
		t.Error("IsHealthy should return true")
	}
}

func TestHealthServiceIsReady(t *testing.T) {
	registry := newTestRegistry()
	service := NewHealthService(registry, output.NoOpTracer{})

	tests := []struct {
		name     string
		packages map[string]*sourceEntry
		want     bool
	}{
		{
			name:     "empty registry is ready",
			packages: map[string]*sourceEntry{},
			want:     true,
		},
		{
			name: "ready package",
			packages: map[string]*sourceEntry{
				"test": {
					Source: &domain.Source{
						ID:      "test",
						Indexed: true,
						Layers:  []domain.Layer{{Name: "layer1", HasIndex: true}},
					},
					Status: domain.StatusReady,
				},
			},
			want: true,
		},
		{
			name: "no ready packages",
			packages: map[string]*sourceEntry{
				"test": {
					Source: &domain.Source{
						ID:      "test",
						Indexed: false,
					},
					Status: domain.StatusLoading,
				},
			},
			want: false,
		},
		{
			name: "mixed packages - one ready",
			packages: map[string]*sourceEntry{
				"loading": {
					Source: &domain.Source{ID: "loading", Indexed: false},
					Status: domain.StatusLoading,
				},
				"ready": {
					Source: &domain.Source{
						ID:      "ready",
						Indexed: true,
						Layers:  []domain.Layer{{Name: "layer1", HasIndex: true}},
					},
					Status: domain.StatusReady,
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry.mu.Lock()
			registry.sources = tt.packages
			registry.mu.Unlock()

			if got := service.IsReady(context.Background()); got != tt.want {
				t.Errorf("IsReady() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHealthServiceGetHealthDetails(t *testing.T) {
	registry := newTestRegistry()
	service := NewHealthService(registry, output.NoOpTracer{})

	// Add some packages
	registry.mu.Lock()
	registry.sources = map[string]*sourceEntry{
		"ready1": {
			Source: &domain.Source{
				ID:      "ready1",
				Indexed: true,
				Layers:  []domain.Layer{{Name: "l1", HasIndex: true}},
			},
			Status: domain.StatusReady,
		},
		"ready2": {
			Source: &domain.Source{
				ID:      "ready2",
				Indexed: true,
				Layers:  []domain.Layer{{Name: "l2", HasIndex: true}},
			},
			Status: domain.StatusReady,
		},
		"loading": {
			Source: &domain.Source{ID: "loading", Indexed: false},
			Status: domain.StatusLoading,
		},
	}
	registry.mu.Unlock()

	details := service.GetHealthDetails(context.Background())

	if !details.Healthy {
		t.Error("Healthy should be true")
	}
	if !details.Ready {
		t.Error("Ready should be true")
	}
	if details.PackagesLoaded != 3 {
		t.Errorf("PackagesLoaded = %d, want 3", details.PackagesLoaded)
	}
	if details.PackagesReady != 2 {
		t.Errorf("PackagesReady = %d, want 2", details.PackagesReady)
	}
	if details.Components["storage"] != "ok" {
		t.Errorf("Components[storage] = %q, want %q", details.Components["storage"], "ok")
	}
}

func TestHealthServiceGetSourceHealth(t *testing.T) {
	registry := newTestRegistry()
	service := NewHealthService(registry, output.NoOpTracer{})

	registry.mu.Lock()
	registry.sources = map[string]*sourceEntry{
		"pkg1": {
			Source: &domain.Source{
				ID:      "pkg1",
				Indexed: true,
				Layers:  []domain.Layer{{Name: "l1", HasIndex: true}},
			},
			Status: domain.StatusReady,
		},
		"pkg2": {
			Source: &domain.Source{ID: "pkg2", Indexed: false},
			Status: domain.StatusIndexing,
		},
	}
	registry.mu.Unlock()

	health := service.GetSourceHealth(context.Background())

	if len(health) != 2 {
		t.Errorf("len(health) = %d, want 2", len(health))
	}

	// Find pkg1
	var pkg1Health *SourceHealth
	for i := range health {
		if health[i].ID == "pkg1" {
			pkg1Health = &health[i]
			break
		}
	}

	if pkg1Health == nil {
		t.Fatal("pkg1 not found in health results")
	}

	if pkg1Health.Status != domain.StatusReady {
		t.Errorf("pkg1.Status = %s, want %s", pkg1Health.Status, domain.StatusReady)
	}
	if !pkg1Health.Ready {
		t.Error("pkg1.Ready should be true")
	}
}
