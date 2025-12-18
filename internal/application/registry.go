// Package application contains the application services.
package application

import (
	"context"
	"log/slog"
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
func (r *PackageRegistry) ListPackages(ctx context.Context) ([]domain.GeoPackage, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	packages := make([]domain.GeoPackage, 0, len(r.packages))
	for _, entry := range r.packages {
		packages = append(packages, *entry.Package)
	}

	return packages, nil
}

// GetPackage returns a specific GeoPackage by ID.
func (r *PackageRegistry) GetPackage(ctx context.Context, id string) (*domain.GeoPackage, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, ok := r.packages[id]
	if !ok {
		return nil, domain.ErrPackageNotFound
	}

	return entry.Package, nil
}

// GetPackageStatus returns the status of a GeoPackage.
func (r *PackageRegistry) GetPackageStatus(ctx context.Context, id string) (domain.GeoPackageStatus, error) {
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
		// Download to local path
		localPath := r.localPath + "/" + obj.Key
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
