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

// markLoaded simulates the initial LoadAll pass having completed.
func markLoaded(r *SourceRegistry) { r.initialLoadDone.Store(true) }

func setSources(r *SourceRegistry, srcs map[string]*sourceEntry) {
	r.mu.Lock()
	r.sources = srcs
	r.mu.Unlock()
}

func readyEntry(id string) *sourceEntry {
	return &sourceEntry{
		Source: &domain.Source{ID: id, Indexed: true, Layers: []domain.Layer{{Name: "l", HasIndex: true}}},
		Status: domain.StatusReady,
	}
}

func loadingEntry(id string) *sourceEntry {
	return &sourceEntry{Source: &domain.Source{ID: id, Indexed: false}, Status: domain.StatusLoading}
}

func TestHealthServiceIsHealthy(t *testing.T) {
	service := NewHealthService(newTestRegistry(), true, output.NoOpTracer{})
	if !service.IsHealthy(context.Background()) {
		t.Error("IsHealthy should return true")
	}
}

func TestHealthServiceIsReady(t *testing.T) {
	tests := []struct {
		name           string
		initialDone    bool
		readyWhenEmpty bool
		sources        map[string]*sourceEntry
		want           bool
	}{
		{
			name:        "initial load not done → not ready (even with a ready source)",
			initialDone: false, readyWhenEmpty: true,
			sources: map[string]*sourceEntry{"a": readyEntry("a")},
			want:    false,
		},
		{
			name:        "empty + ready_when_empty=true → ready",
			initialDone: true, readyWhenEmpty: true,
			sources: map[string]*sourceEntry{},
			want:    true,
		},
		{
			name:        "empty + ready_when_empty=false → not ready",
			initialDone: true, readyWhenEmpty: false,
			sources: map[string]*sourceEntry{},
			want:    false,
		},
		{
			name:        "ready source → ready",
			initialDone: true, readyWhenEmpty: false,
			sources: map[string]*sourceEntry{"a": readyEntry("a")},
			want:    true,
		},
		{
			name:        "sources present but none ready, ready_when_empty=true → ready",
			initialDone: true, readyWhenEmpty: true,
			sources: map[string]*sourceEntry{"a": loadingEntry("a")},
			want:    true,
		},
		{
			name:        "sources present but none ready, ready_when_empty=false → not ready",
			initialDone: true, readyWhenEmpty: false,
			sources: map[string]*sourceEntry{"a": loadingEntry("a")},
			want:    false,
		},
		{
			name:        "mixed — one ready → ready",
			initialDone: true, readyWhenEmpty: false,
			sources: map[string]*sourceEntry{"a": loadingEntry("a"), "b": readyEntry("b")},
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := newTestRegistry()
			if tt.initialDone {
				markLoaded(registry)
			}
			setSources(registry, tt.sources)
			service := NewHealthService(registry, tt.readyWhenEmpty, output.NoOpTracer{})

			if got := service.IsReady(context.Background()); got != tt.want {
				t.Errorf("IsReady() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHealthServiceGetHealthDetails(t *testing.T) {
	registry := newTestRegistry()
	markLoaded(registry)
	service := NewHealthService(registry, true, output.NoOpTracer{})

	setSources(registry, map[string]*sourceEntry{
		"ready1":  readyEntry("ready1"),
		"ready2":  readyEntry("ready2"),
		"loading": loadingEntry("loading"),
	})

	details := service.GetHealthDetails(context.Background())

	if !details.Healthy {
		t.Error("Healthy should be true")
	}
	if !details.Ready {
		t.Error("Ready should be true")
	}
	if details.SourcesLoaded != 3 {
		t.Errorf("SourcesLoaded = %d, want 3", details.SourcesLoaded)
	}
	if details.SourcesReady != 2 {
		t.Errorf("SourcesReady = %d, want 2", details.SourcesReady)
	}
	if details.Components["storage"] != "ok" {
		t.Errorf("Components[storage] = %q, want %q", details.Components["storage"], "ok")
	}
}

func TestHealthServiceGetSourceHealth(t *testing.T) {
	registry := newTestRegistry()
	service := NewHealthService(registry, true, output.NoOpTracer{})

	setSources(registry, map[string]*sourceEntry{
		"pkg1": readyEntry("pkg1"),
		"pkg2": {Source: &domain.Source{ID: "pkg2", Indexed: false}, Status: domain.StatusIndexing},
	})

	health := service.GetSourceHealth(context.Background())
	if len(health) != 2 {
		t.Errorf("len(health) = %d, want 2", len(health))
	}

	var pkg1 *SourceHealth
	for i := range health {
		if health[i].ID == "pkg1" {
			pkg1 = &health[i]
			break
		}
	}
	if pkg1 == nil {
		t.Fatal("pkg1 not found in health results")
	}
	if pkg1.Status != domain.StatusReady {
		t.Errorf("pkg1.Status = %s, want %s", pkg1.Status, domain.StatusReady)
	}
	if !pkg1.Ready {
		t.Error("pkg1.Ready should be true")
	}
}
