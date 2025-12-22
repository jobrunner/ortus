// Package application contains the application services.
package application

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// PackageRegistry manages loaded GeoPackages.
type PackageRegistry struct {
	mu        sync.RWMutex
	packages  map[string]*packageEntry
	repo      output.GeoPackageRepository
	storage   output.ObjectStorage
	metrics   output.MetricsCollector
	logger    *slog.Logger
	localPath string
}

type packageEntry struct {
	Package *domain.GeoPackage
	Status  domain.GeoPackageStatus
	Error   error
}

// NewPackageRegistry creates a new package registry.
func NewPackageRegistry(
	repo output.GeoPackageRepository,
	storage output.ObjectStorage,
	metrics output.MetricsCollector,
	logger *slog.Logger,
	localPath string,
) *PackageRegistry {
	return &PackageRegistry{
		packages:  make(map[string]*packageEntry),
		repo:      repo,
		storage:   storage,
		metrics:   metrics,
		logger:    logger,
		localPath: localPath,
	}
}

// LoadPackage loads a GeoPackage from the given path.
func (r *PackageRegistry) LoadPackage(ctx context.Context, path string) error {
	r.logger.Info("loading package", "path", path)

	// Open the package
	pkg, err := r.repo.Open(ctx, path)
	if err != nil {
		r.logger.Error("failed to open package", "path", path, "error", err)
		return err
	}

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

	return nil
}

// UnloadPackage unloads a GeoPackage.
func (r *PackageRegistry) UnloadPackage(ctx context.Context, packageID string) error {
	r.logger.Info("unloading package", "id", packageID)

	r.mu.Lock()
	if entry, ok := r.packages[packageID]; ok {
		entry.Status = domain.StatusUnloading
	}
	r.mu.Unlock()

	if err := r.repo.Close(ctx, packageID); err != nil {
		r.logger.Error("failed to close package", "id", packageID, "error", err)
		return err
	}

	r.mu.Lock()
	delete(r.packages, packageID)
	r.mu.Unlock()

	r.updateMetrics()
	return nil
}

// ListPackages returns all registered GeoPackages.
func (r *PackageRegistry) ListPackages(_ context.Context) ([]domain.GeoPackage, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	packages := make([]domain.GeoPackage, 0, len(r.packages))
	for _, entry := range r.packages {
		packages = append(packages, *entry.Package)
	}

	return packages, nil
}

// GetPackage returns a specific GeoPackage by ID.
func (r *PackageRegistry) GetPackage(_ context.Context, id string) (*domain.GeoPackage, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, ok := r.packages[id]
	if !ok {
		return nil, domain.ErrPackageNotFound
	}

	return entry.Package, nil
}

// GetPackageStatus returns the status of a GeoPackage.
func (r *PackageRegistry) GetPackageStatus(_ context.Context, id string) (domain.GeoPackageStatus, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, ok := r.packages[id]
	if !ok {
		return "", domain.ErrPackageNotFound
	}

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

// updateMetrics updates the metrics collector with current package counts.
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

	r.metrics.SetPackagesLoaded(total)
	r.metrics.SetPackagesReady(ready)
}

// LoadAll loads all GeoPackages from storage.
func (r *PackageRegistry) LoadAll(ctx context.Context) error {
	r.logger.Info("loading all packages from storage")

	objects, err := r.storage.List(ctx)
	if err != nil {
		return err
	}

	for _, obj := range objects {
		// Download to local path using filepath.Join for consistent path handling
		localPath := filepath.Join(r.localPath, obj.Key)
		if err := r.storage.Download(ctx, obj.Key, localPath); err != nil {
			r.logger.Error("failed to download package", "key", obj.Key, "error", err)
			continue
		}

		if err := r.LoadPackage(ctx, localPath); err != nil {
			r.logger.Error("failed to load package", "path", localPath, "error", err)
		}
	}

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
	r.logger.Info("syncing packages from storage")

	objects, err := r.storage.List(ctx)
	if err != nil {
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
	packagesToRemove := r.findPackagesToRemove(remotePackages)
	for _, packageID := range packagesToRemove {
		r.logger.Info("removing package not in remote storage", "id", packageID)

		// Get the package path before unloading
		localPath := r.getPackagePath(packageID)

		// Unload the package
		if err := r.UnloadPackage(ctx, packageID); err != nil {
			r.logger.Error("failed to unload removed package", "id", packageID, "error", err)
			continue
		}

		// Delete local cache file
		if localPath != "" {
			if err := os.Remove(localPath); err != nil && !os.IsNotExist(err) {
				r.logger.Warn("failed to delete local cache file", "path", localPath, "error", err)
			} else {
				r.logger.Debug("deleted local cache file", "path", localPath)
			}
		}

		stats.Removed++
	}

	r.logger.Info("sync completed", "added", stats.Added, "removed", stats.Removed, "total", r.PackageCount())
	return stats, nil
}

// findPackagesToRemove returns package IDs that are loaded but not in remote storage.
func (r *PackageRegistry) findPackagesToRemove(remotePackages map[string]string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var toRemove []string
	for packageID := range r.packages {
		if _, exists := remotePackages[packageID]; !exists {
			toRemove = append(toRemove, packageID)
		}
	}
	return toRemove
}

// getPackagePath returns the local file path for a loaded package.
func (r *PackageRegistry) getPackagePath(packageID string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if entry, ok := r.packages[packageID]; ok && entry.Package != nil {
		return entry.Package.Path
	}
	return ""
}

// derivePackageID extracts a package ID from a file path or object key.
func derivePackageID(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	return base[:len(base)-len(ext)]
}
