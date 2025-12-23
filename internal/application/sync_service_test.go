package application

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jobrunner/ortus/internal/ports/output"
)

func TestSyncService_RateLimiting(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create a mock registry
	registry := &PackageRegistry{
		packages:  make(map[string]*packageEntry),
		logger:    logger,
		localPath: "/tmp",
		storage:   &mockStorage{},
		metrics:   &output.NoOpMetrics{},
	}

	service := NewSyncService(registry, time.Hour, logger)

	ctx := context.Background()

	// First call should succeed (sync will return 0 added since storage is empty)
	result, err := service.TriggerSync(ctx)
	if err != nil {
		t.Errorf("first sync should succeed, got error: %v", err)
	}
	if result.PackagesAdded != 0 {
		t.Errorf("expected 0 packages added with empty storage, got %d", result.PackagesAdded)
	}

	// Immediate second call should be rate limited
	_, err = service.TriggerSync(ctx)
	if err != ErrRateLimited {
		t.Errorf("expected ErrRateLimited, got %v", err)
	}
}

func TestSyncService_StartStop(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	registry := &PackageRegistry{
		packages:  make(map[string]*packageEntry),
		logger:    logger,
		localPath: "/tmp",
		storage:   &mockStorage{},
		metrics:   &output.NoOpMetrics{},
	}

	// Use a short interval for testing
	service := NewSyncService(registry, 100*time.Millisecond, logger)

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

	registry := &PackageRegistry{
		packages:  make(map[string]*packageEntry),
		logger:    logger,
		localPath: "/tmp",
		storage:   &mockStorage{},
		metrics:   &output.NoOpMetrics{},
	}

	interval := 2 * time.Hour
	service := NewSyncService(registry, interval, logger)

	if service.Interval() != interval {
		t.Errorf("expected interval %v, got %v", interval, service.Interval())
	}
}

func TestSyncService_SyncAddsNewPackages(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create storage with some mock objects
	storage := &mockStorage{
		objects: []output.StorageObject{
			{Key: "test1.gpkg"},
			{Key: "test2.gpkg"},
		},
	}

	registry := &PackageRegistry{
		packages:  make(map[string]*packageEntry),
		repo:      &mockRepository{},
		logger:    logger,
		localPath: "/tmp",
		storage:   storage,
		metrics:   &output.NoOpMetrics{},
	}

	service := NewSyncService(registry, time.Hour, logger)

	ctx := context.Background()

	// First sync should add packages
	result, err := service.TriggerSync(ctx)
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	if result.PackagesAdded != 2 {
		t.Errorf("expected 2 packages added, got %d", result.PackagesAdded)
	}
	if result.PackagesTotal != 2 {
		t.Errorf("expected 2 total packages, got %d", result.PackagesTotal)
	}
}

func TestRegistry_IsLoaded(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	registry := &PackageRegistry{
		packages:  make(map[string]*packageEntry),
		logger:    logger,
		localPath: "/tmp",
		storage:   &mockStorage{},
		metrics:   &output.NoOpMetrics{},
	}

	// Initially not loaded
	if registry.IsLoaded("test-package") {
		t.Error("expected package to not be loaded initially")
	}

	// Add a package manually
	registry.packages["test-package"] = &packageEntry{}

	// Now it should be loaded
	if !registry.IsLoaded("test-package") {
		t.Error("expected package to be loaded after adding")
	}
}

func TestRegistry_PackageCount(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	registry := &PackageRegistry{
		packages:  make(map[string]*packageEntry),
		logger:    logger,
		localPath: "/tmp",
		storage:   &mockStorage{},
		metrics:   &output.NoOpMetrics{},
	}

	if registry.PackageCount() != 0 {
		t.Errorf("expected 0 packages, got %d", registry.PackageCount())
	}

	registry.packages["pkg1"] = &packageEntry{}
	registry.packages["pkg2"] = &packageEntry{}

	if registry.PackageCount() != 2 {
		t.Errorf("expected 2 packages, got %d", registry.PackageCount())
	}
}

func TestRegistry_SyncRemovesDeletedPackages(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create storage with two packages initially
	storage := &mockStorage{
		objects: []output.StorageObject{
			{Key: "test1.gpkg"},
			{Key: "test2.gpkg"},
		},
	}

	registry := &PackageRegistry{
		packages:  make(map[string]*packageEntry),
		repo:      &mockRepository{},
		logger:    logger,
		localPath: "/tmp",
		storage:   storage,
		metrics:   &output.NoOpMetrics{},
	}

	ctx := context.Background()

	// First sync should add both packages
	stats, err := registry.Sync(ctx)
	if err != nil {
		t.Fatalf("first sync failed: %v", err)
	}
	if stats.Added != 2 {
		t.Errorf("expected 2 packages added, got %d", stats.Added)
	}
	if stats.Removed != 0 {
		t.Errorf("expected 0 packages removed, got %d", stats.Removed)
	}

	// Remove one package from storage
	storage.objects = []output.StorageObject{
		{Key: "test1.gpkg"},
	}

	// Second sync should remove the deleted package
	stats, err = registry.Sync(ctx)
	if err != nil {
		t.Fatalf("second sync failed: %v", err)
	}
	if stats.Added != 0 {
		t.Errorf("expected 0 packages added, got %d", stats.Added)
	}
	if stats.Removed != 1 {
		t.Errorf("expected 1 package removed, got %d", stats.Removed)
	}
	if registry.PackageCount() != 1 {
		t.Errorf("expected 1 total package, got %d", registry.PackageCount())
	}
}

func TestRegistry_FindPackagesToRemove(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	registry := &PackageRegistry{
		packages:  make(map[string]*packageEntry),
		logger:    logger,
		localPath: "/tmp",
		storage:   &mockStorage{},
		metrics:   &output.NoOpMetrics{},
	}

	// Add some packages locally
	registry.packages["pkg1"] = &packageEntry{}
	registry.packages["pkg2"] = &packageEntry{}
	registry.packages["pkg3"] = &packageEntry{}

	// Only pkg1 and pkg3 are in remote
	remotePackages := map[string]string{
		"pkg1": "pkg1.gpkg",
		"pkg3": "pkg3.gpkg",
	}

	toRemove := registry.findPackagesToRemove(remotePackages)

	if len(toRemove) != 1 {
		t.Errorf("expected 1 package to remove, got %d", len(toRemove))
	}
	if len(toRemove) > 0 && toRemove[0].id != "pkg2" {
		t.Errorf("expected pkg2 to be removed, got %s", toRemove[0].id)
	}
}
