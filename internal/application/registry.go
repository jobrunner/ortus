// Package application contains the application services.
package application

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// PackageRegistry manages loaded GeoPackages.
type PackageRegistry struct {
	mu        sync.RWMutex
	packages  map[string]*packageEntry
	repo      output.GeoPackageRepository
	storage   output.ObjectStorage
	tracer    output.Tracer
	logger    *slog.Logger
	localPath string

	// Observable gauge state. Atomic so the OTel callback (which can fire
	// from a metric-export goroutine) doesn't race with mutations under
	// r.mu. Updated by updateMetrics() after every load/unload.
	loadedCount atomic.Int64
	readyCount  atomic.Int64
}

type packageEntry struct {
	Package *domain.Source
	Status  domain.SourceStatus
	Error   error
}

// NewPackageRegistry creates a new package registry.
func NewPackageRegistry(
	repo output.GeoPackageRepository,
	storage output.ObjectStorage,
	meter metric.Meter,
	tracer output.Tracer,
	logger *slog.Logger,
	localPath string,
) *PackageRegistry {
	if tracer == nil {
		tracer = output.NoOpTracer{}
	}
	if meter == nil {
		meter = noop.NewMeterProvider().Meter("ortus/application")
	}

	r := &PackageRegistry{
		packages:  make(map[string]*packageEntry),
		repo:      repo,
		storage:   storage,
		tracer:    tracer,
		logger:    logger,
		localPath: localPath,
	}

	// Register observable gauges for packages.loaded / packages.ready.
	// The callback reads from atomic counters maintained by updateMetrics()
	// so the read is lock-free and safe from any goroutine the SDK uses.
	loaded, _ := meter.Int64ObservableGauge(
		"ortus.packages.loaded",
		metric.WithDescription("Number of loaded GeoPackages"),
	)
	ready, _ := meter.Int64ObservableGauge(
		"ortus.packages.ready",
		metric.WithDescription("Number of GeoPackages ready to serve queries"),
	)
	_, _ = meter.RegisterCallback(
		func(_ context.Context, o metric.Observer) error {
			o.ObserveInt64(loaded, r.loadedCount.Load())
			o.ObserveInt64(ready, r.readyCount.Load())
			return nil
		},
		loaded, ready,
	)

	return r
}

// LoadPackage loads a GeoPackage from the given path.
func (r *PackageRegistry) LoadPackage(ctx context.Context, path string) error {
	ctx, span := r.tracer.Start(ctx, "PackageRegistry.LoadPackage",
		output.WithAttributes(output.String("ortus.package.path", path)),
	)
	defer span.End()

	r.logger.Info("loading package", "path", path)

	// Open the package
	pkg, err := r.repo.Open(ctx, path)
	if err != nil {
		r.logger.Error("failed to open package", "path", path, "error", err)
		span.RecordError(err)
		span.SetStatus(output.StatusError, "open failed")
		return err
	}

	span.SetAttributes(
		output.String("ortus.package.id", pkg.ID),
		output.Int("ortus.layers.count", len(pkg.Layers)),
	)

	// Register the package
	r.mu.Lock()
	r.packages[pkg.ID] = &packageEntry{
		Package: pkg,
		Status:  domain.StatusIndexing,
	}
	r.mu.Unlock()

	// Create spatial indices for all layers
	for _, layer := range pkg.Layers {
		r.logger.Debug("creating spatial index", "package", pkg.ID, "layer", layer.Name)
		if err := r.repo.CreateSpatialIndex(ctx, pkg.ID, layer.Name); err != nil {
			r.logger.Warn("failed to create spatial index", "package", pkg.ID, "layer", layer.Name, "error", err)
			span.AddEvent("spatial index creation failed",
				output.String("ortus.layer.name", layer.Name),
				output.String("error", err.Error()),
			)
		}
	}

	// Update status
	r.mu.Lock()
	if entry, ok := r.packages[pkg.ID]; ok {
		entry.Status = domain.StatusReady
		entry.Package.Indexed = true
		entry.Package.LoadedAt = time.Now()
	}
	r.mu.Unlock()

	r.updateMetrics()
	r.logger.Info("package loaded", "id", pkg.ID, "layers", len(pkg.Layers))
	span.SetStatus(output.StatusOK, "")

	return nil
}

// UnloadPackage unloads a GeoPackage.
func (r *PackageRegistry) UnloadPackage(ctx context.Context, packageID string) error {
	ctx, span := r.tracer.Start(ctx, "PackageRegistry.UnloadPackage",
		output.WithAttributes(output.String("ortus.package.id", packageID)),
	)
	defer span.End()

	r.logger.Info("unloading package", "id", packageID)

	r.mu.Lock()
	if entry, ok := r.packages[packageID]; ok {
		entry.Status = domain.StatusUnloading
	}
	r.mu.Unlock()

	if err := r.repo.Close(ctx, packageID); err != nil {
		r.logger.Error("failed to close package", "id", packageID, "error", err)
		span.RecordError(err)
		span.SetStatus(output.StatusError, "close failed")
		return err
	}

	r.mu.Lock()
	delete(r.packages, packageID)
	r.mu.Unlock()

	r.updateMetrics()
	span.SetStatus(output.StatusOK, "")
	return nil
}

// ListPackages returns all registered GeoPackages.
func (r *PackageRegistry) ListPackages(ctx context.Context) ([]domain.Source, error) {
	_, span := r.tracer.Start(ctx, "PackageRegistry.ListPackages")
	defer span.End()

	r.mu.RLock()
	defer r.mu.RUnlock()

	packages := make([]domain.Source, 0, len(r.packages))
	for _, entry := range r.packages {
		packages = append(packages, *entry.Package)
	}

	span.SetAttributes(output.Int("ortus.packages.count", len(packages)))
	return packages, nil
}

// GetPackage returns a specific GeoPackage by ID.
func (r *PackageRegistry) GetPackage(ctx context.Context, id string) (*domain.Source, error) {
	_, span := r.tracer.Start(ctx, "PackageRegistry.GetPackage",
		output.WithAttributes(output.String("ortus.package.id", id)),
	)
	defer span.End()

	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, ok := r.packages[id]
	if !ok {
		span.RecordError(domain.ErrPackageNotFound)
		span.SetStatus(output.StatusError, "package not found")
		return nil, domain.ErrPackageNotFound
	}

	return entry.Package, nil
}

// GetPackageStatus returns the status of a GeoPackage.
func (r *PackageRegistry) GetPackageStatus(ctx context.Context, id string) (domain.SourceStatus, error) {
	_, span := r.tracer.Start(ctx, "PackageRegistry.GetPackageStatus",
		output.WithAttributes(output.String("ortus.package.id", id)),
	)
	defer span.End()

	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, ok := r.packages[id]
	if !ok {
		span.RecordError(domain.ErrPackageNotFound)
		span.SetStatus(output.StatusError, "package not found")
		return "", domain.ErrPackageNotFound
	}

	span.SetAttributes(output.String("ortus.package.status", string(entry.Status)))
	return entry.Status, nil
}

// IsReady returns true if a package is ready for queries.
func (r *PackageRegistry) IsReady(packageID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, ok := r.packages[packageID]
	if !ok {
		return false
	}

	return entry.Status == domain.StatusReady
}

// ReadyPackageIDs returns IDs of all ready packages.
func (r *PackageRegistry) ReadyPackageIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := make([]string, 0)
	for id, entry := range r.packages {
		if entry.Status == domain.StatusReady {
			ids = append(ids, id)
		}
	}
	return ids
}

// updateMetrics refreshes the atomic counters that back the
// packages.loaded / packages.ready observable gauges. Called after every
// load/unload so the gauge callback (which can fire at any time) reads
// a current value without needing r.mu.
func (r *PackageRegistry) updateMetrics() {
	r.mu.RLock()
	total := len(r.packages)
	ready := 0
	for _, entry := range r.packages {
		if entry.Status == domain.StatusReady {
			ready++
		}
	}
	r.mu.RUnlock()

	r.loadedCount.Store(int64(total))
	r.readyCount.Store(int64(ready))
}

// LoadAll loads all GeoPackages from storage.
func (r *PackageRegistry) LoadAll(ctx context.Context) error {
	ctx, span := r.tracer.Start(ctx, "PackageRegistry.LoadAll")
	defer span.End()

	r.logger.Info("loading all packages from storage")

	objects, err := r.storage.List(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "storage list failed")
		return err
	}

	span.SetAttributes(output.Int("ortus.storage.objects", len(objects)))

	loaded, failed := 0, 0
	for _, obj := range objects {
		// Download to local path using filepath.Join for consistent path handling
		localPath := filepath.Join(r.localPath, obj.Key)
		if err := r.storage.Download(ctx, obj.Key, localPath); err != nil {
			r.logger.Error("failed to download package", "key", obj.Key, "error", err)
			failed++
			continue
		}

		if err := r.LoadPackage(ctx, localPath); err != nil {
			r.logger.Error("failed to load package", "path", localPath, "error", err)
			failed++
			continue
		}
		loaded++
	}

	span.SetAttributes(
		output.Int("ortus.packages.loaded", loaded),
		output.Int("ortus.packages.failed", failed),
	)
	span.SetStatus(output.StatusOK, "")
	return nil
}

// IsLoaded returns true if a package with the given ID is already loaded.
func (r *PackageRegistry) IsLoaded(packageID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.packages[packageID]
	return ok
}

// PackageCount returns the number of loaded packages.
func (r *PackageRegistry) PackageCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.packages)
}

// SyncStats contains statistics from a sync operation.
type SyncStats struct {
	Added   int
	Removed int
}

// Sync synchronizes with remote storage, downloading new packages and removing
// packages that no longer exist in remote storage.
// Returns statistics about added and removed packages.
func (r *PackageRegistry) Sync(ctx context.Context) (SyncStats, error) {
	ctx, span := r.tracer.Start(ctx, "PackageRegistry.Sync")
	defer span.End()

	r.logger.Info("syncing packages from storage")

	objects, err := r.storage.List(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "storage list failed")
		return SyncStats{}, err
	}

	// Build set of remote package IDs
	remotePackages := make(map[string]string) // packageID -> objectKey
	for _, obj := range objects {
		packageID := derivePackageID(obj.Key)
		remotePackages[packageID] = obj.Key
	}

	stats := SyncStats{}

	// Add new packages
	for packageID, objectKey := range remotePackages {
		if r.IsLoaded(packageID) {
			r.logger.Debug("package already loaded, skipping", "id", packageID)
			continue
		}

		// Download to local path
		localPath := filepath.Join(r.localPath, objectKey)
		if err := r.storage.Download(ctx, objectKey, localPath); err != nil {
			r.logger.Error("failed to download package", "key", objectKey, "error", err)
			continue
		}

		// Load the package
		if err := r.LoadPackage(ctx, localPath); err != nil {
			r.logger.Error("failed to load package", "path", localPath, "error", err)
			continue
		}

		stats.Added++
		r.logger.Info("new package synced", "id", packageID)
	}

	// Remove packages that no longer exist in remote storage
	// We capture both ID and path in findPackagesToRemove to avoid race conditions
	packagesToRemove := r.findPackagesToRemove(remotePackages)
	for _, pkg := range packagesToRemove {
		r.logger.Info("removing package not in remote storage", "id", pkg.id)

		// Unload the package
		if err := r.UnloadPackage(ctx, pkg.id); err != nil {
			r.logger.Error("failed to unload removed package", "id", pkg.id, "error", err)
			continue
		}

		// Delete local cache file
		if pkg.path != "" {
			if err := os.Remove(pkg.path); err != nil && !os.IsNotExist(err) {
				r.logger.Warn("failed to delete local cache file", "path", pkg.path, "error", err)
			} else {
				r.logger.Debug("deleted local cache file", "path", pkg.path)
			}
		}

		stats.Removed++
	}

	r.logger.Info("sync completed", "added", stats.Added, "removed", stats.Removed, "total", r.PackageCount())
	span.SetAttributes(
		output.Int("ortus.sync.added", stats.Added),
		output.Int("ortus.sync.removed", stats.Removed),
		output.Int("ortus.packages.total", r.PackageCount()),
	)
	span.SetStatus(output.StatusOK, "")
	return stats, nil
}

// packageToRemove holds information about a package that should be removed.
type packageToRemove struct {
	id   string
	path string
}

// findPackagesToRemove returns packages that are loaded but not in remote storage.
// This captures both ID and path in a single lock acquisition to avoid race conditions.
func (r *PackageRegistry) findPackagesToRemove(remotePackages map[string]string) []packageToRemove {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var toRemove []packageToRemove
	for packageID, entry := range r.packages {
		if _, exists := remotePackages[packageID]; !exists {
			path := ""
			if entry.Package != nil {
				path = entry.Package.Path
			}
			toRemove = append(toRemove, packageToRemove{id: packageID, path: path})
		}
	}
	return toRemove
}

// derivePackageID extracts a package ID from a file path or object key.
func derivePackageID(path string) string {
	base := filepath.Base(path)
	if base == "" || base == "." {
		return ""
	}

	ext := filepath.Ext(base)
	// Handle edge case where basename is only the extension (e.g., ".gpkg")
	if ext == "" || len(base) == len(ext) {
		return base
	}

	return strings.TrimSuffix(base, ext)
}
