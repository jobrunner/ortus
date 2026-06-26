package application

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

func TestSyncService_RateLimiting(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create a mock registry
	registry := &SourceRegistry{
		sources:   make(map[string]*sourceEntry),
		logger:    logger,
		localPath: "/tmp",
		storage:   &mockStorage{},
		tracer:    output.NoOpTracer{},
	}

	service := NewSyncService(registry, time.Hour, output.NoOpTracer{}, logger)

	ctx := context.Background()

	// First call should succeed (sync will return 0 added since storage is empty)
	result, err := service.TriggerSync(ctx)
	if err != nil {
		t.Errorf("first sync should succeed, got error: %v", err)
	}
	if result.SourcesAdded != 0 {
		t.Errorf("expected 0 sources added with empty storage, got %d", result.SourcesAdded)
	}

	// Immediate second call should be rate limited
	_, err = service.TriggerSync(ctx)
	if !errors.Is(err, domain.ErrRateLimited) {
		t.Errorf("expected ErrRateLimited, got %v", err)
	}
}

func TestSyncService_StartStop(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	registry := &SourceRegistry{
		sources:   make(map[string]*sourceEntry),
		logger:    logger,
		localPath: "/tmp",
		storage:   &mockStorage{},
		tracer:    output.NoOpTracer{},
	}

	// Use a short interval for testing
	service := NewSyncService(registry, 100*time.Millisecond, output.NoOpTracer{}, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the service
	service.Start(ctx)

	// Give it a moment to start
	time.Sleep(50 * time.Millisecond)

	// Stop the service
	service.Stop()

	// Should complete without hanging
}

func TestSyncService_Interval(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	registry := &SourceRegistry{
		sources:   make(map[string]*sourceEntry),
		logger:    logger,
		localPath: "/tmp",
		storage:   &mockStorage{},
		tracer:    output.NoOpTracer{},
	}

	interval := 2 * time.Hour
	service := NewSyncService(registry, interval, output.NoOpTracer{}, logger)

	if service.Interval() != interval {
		t.Errorf("expected interval %v, got %v", interval, service.Interval())
	}
}

func TestSyncService_SyncAddsNewSources(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create storage with some mock objects
	storage := &mockStorage{
		objects: []output.StorageObject{
			{Key: "test1.gpkg"},
			{Key: "test2.gpkg"},
		},
	}

	registry := &SourceRegistry{
		sources:   make(map[string]*sourceEntry),
		providers: []output.SpatialSource{&mockRepository{}},
		logger:    logger,
		localPath: "/tmp",
		storage:   storage,
		tracer:    output.NoOpTracer{},
	}

	service := NewSyncService(registry, time.Hour, output.NoOpTracer{}, logger)

	ctx := context.Background()

	// First sync should add sources
	result, err := service.TriggerSync(ctx)
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	if result.SourcesAdded != 2 {
		t.Errorf("expected 2 sources added, got %d", result.SourcesAdded)
	}
	if result.SourcesTotal != 2 {
		t.Errorf("expected 2 total sources, got %d", result.SourcesTotal)
	}
}

func TestRegistry_IsLoaded(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	registry := &SourceRegistry{
		sources:   make(map[string]*sourceEntry),
		logger:    logger,
		localPath: "/tmp",
		storage:   &mockStorage{},
		tracer:    output.NoOpTracer{},
	}

	// Initially not loaded
	if registry.IsLoaded("test-source") {
		t.Error("expected source to not be loaded initially")
	}

	// Add a source manually
	registry.sources["test-source"] = &sourceEntry{}

	// Now it should be loaded
	if !registry.IsLoaded("test-source") {
		t.Error("expected source to be loaded after adding")
	}
}

func TestRegistry_SourceCount(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	registry := &SourceRegistry{
		sources:   make(map[string]*sourceEntry),
		logger:    logger,
		localPath: "/tmp",
		storage:   &mockStorage{},
		tracer:    output.NoOpTracer{},
	}

	if registry.SourceCount() != 0 {
		t.Errorf("expected 0 sources, got %d", registry.SourceCount())
	}

	registry.sources["pkg1"] = &sourceEntry{}
	registry.sources["pkg2"] = &sourceEntry{}

	if registry.SourceCount() != 2 {
		t.Errorf("expected 2 sources, got %d", registry.SourceCount())
	}
}

func TestRegistry_SyncRemovesDeletedSources(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create storage with two sources initially
	storage := &mockStorage{
		objects: []output.StorageObject{
			{Key: "test1.gpkg"},
			{Key: "test2.gpkg"},
		},
	}

	registry := &SourceRegistry{
		sources:   make(map[string]*sourceEntry),
		providers: []output.SpatialSource{&mockRepository{}},
		logger:    logger,
		localPath: "/tmp",
		storage:   storage,
		tracer:    output.NoOpTracer{},
	}

	ctx := context.Background()

	// First sync should add both sources
	stats, err := registry.Sync(ctx)
	if err != nil {
		t.Fatalf("first sync failed: %v", err)
	}
	if stats.Added != 2 {
		t.Errorf("expected 2 sources added, got %d", stats.Added)
	}
	if stats.Removed != 0 {
		t.Errorf("expected 0 sources removed, got %d", stats.Removed)
	}

	// Remove one source from storage
	storage.objects = []output.StorageObject{
		{Key: "test1.gpkg"},
	}

	// Second sync should remove the deleted source
	stats, err = registry.Sync(ctx)
	if err != nil {
		t.Fatalf("second sync failed: %v", err)
	}
	if stats.Added != 0 {
		t.Errorf("expected 0 sources added, got %d", stats.Added)
	}
	if stats.Removed != 1 {
		t.Errorf("expected 1 source removed, got %d", stats.Removed)
	}
	if registry.SourceCount() != 1 {
		t.Errorf("expected 1 total source, got %d", registry.SourceCount())
	}
}

func TestRegistry_FindSourcesToRemove(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	registry := &SourceRegistry{
		sources:   make(map[string]*sourceEntry),
		logger:    logger,
		localPath: "/tmp",
		storage:   &mockStorage{},
		tracer:    output.NoOpTracer{},
	}

	// Add some sources locally
	registry.sources["pkg1"] = &sourceEntry{}
	registry.sources["pkg2"] = &sourceEntry{}
	registry.sources["pkg3"] = &sourceEntry{}

	// Only pkg1 and pkg3 are in remote
	remoteSources := map[string]string{
		"pkg1": "pkg1.gpkg",
		"pkg3": "pkg3.gpkg",
	}

	toRemove := registry.findSourcesToRemove(remoteSources)

	if len(toRemove) != 1 {
		t.Errorf("expected 1 source to remove, got %d", len(toRemove))
	}
	if len(toRemove) > 0 && toRemove[0].id != "pkg2" {
		t.Errorf("expected pkg2 to be removed, got %s", toRemove[0].id)
	}
}
