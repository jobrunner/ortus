// Package application contains the application services.
package application

import (
	"context"
	"fmt"
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

// SourceRegistry manages loaded spatial sources (GeoPackages, raster bundles).
type SourceRegistry struct {
	mu        sync.RWMutex
	sources   map[string]*sourceEntry
	providers []output.SpatialSource
	storage   output.ObjectStorage
	tracer    output.Tracer
	logger    *slog.Logger
	localPath string

	// Observable gauge state. Atomic so the OTel callback (which can fire
	// from a metric-export goroutine) doesn't race with mutations under
	// r.mu. Updated by updateMetrics() after every load/unload.
	loadedCount atomic.Int64
	readyCount  atomic.Int64
	// failedCount reflects how many sources failed in the last LoadAll pass.
	failedCount atomic.Int64

	// initialLoadDone latches true once the first LoadAll pass completes (even
	// with zero or partially-failed sources). Readiness uses it so the service
	// reports not-ready only during the initial bring-up, not when later sync
	// activity adds sources in the background.
	initialLoadDone atomic.Bool
}

// InitialLoadComplete reports whether the first LoadAll pass has finished.
func (r *SourceRegistry) InitialLoadComplete() bool { return r.initialLoadDone.Load() }

// loadedSourcePath returns the on-disk path of an already-loaded source, if any.
func (r *SourceRegistry) loadedSourcePath(id string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.sources[id]
	if !ok || entry.Source == nil {
		// No entry, or a malformed placeholder without a Source: report "not a
		// known-path load" so the caller doesn't treat it as a collision.
		return "", false
	}
	return entry.Source.Path, true
}

type sourceEntry struct {
	Source *domain.Source
	Repo   output.SpatialSource // adapter that opened this source
	Status domain.SourceStatus
	Error  error
}

// NewSourceRegistry creates a new source registry. providers are the source
// adapters consulted (in order) to open a file; the first whose Supports
// reports true for a path owns that source.
func NewSourceRegistry(
	providers []output.SpatialSource,
	storage output.ObjectStorage,
	meter metric.Meter,
	tracer output.Tracer,
	logger *slog.Logger,
	localPath string,
) *SourceRegistry {
	if tracer == nil {
		tracer = output.NoOpTracer{}
	}
	if meter == nil {
		meter = noop.NewMeterProvider().Meter("ortus/application")
	}

	r := &SourceRegistry{
		sources:   make(map[string]*sourceEntry),
		providers: providers,
		storage:   storage,
		tracer:    tracer,
		logger:    logger,
		localPath: localPath,
	}

	// Register observable gauges for sources.loaded / sources.ready.
	// The callback reads from atomic counters maintained by updateMetrics()
	// so the read is lock-free and safe from any goroutine the SDK uses.
	loaded, _ := meter.Int64ObservableGauge(
		"ortus.sources.loaded",
		metric.WithDescription("Number of loaded sources"),
	)
	ready, _ := meter.Int64ObservableGauge(
		"ortus.sources.ready",
		metric.WithDescription("Number of sources ready to serve queries"),
	)
	failed, _ := meter.Int64ObservableGauge(
		"ortus.sources.failed",
		metric.WithDescription("Number of sources that failed to load in the last LoadAll pass"),
	)
	_, _ = meter.RegisterCallback(
		func(_ context.Context, o metric.Observer) error {
			o.ObserveInt64(loaded, r.loadedCount.Load())
			o.ObserveInt64(ready, r.readyCount.Load())
			o.ObserveInt64(failed, r.failedCount.Load())
			return nil
		},
		loaded, ready, failed,
	)

	return r
}

// LoadSource loads a GeoPackage from the given path.
func (r *SourceRegistry) LoadSource(ctx context.Context, path string) error {
	ctx, span := r.tracer.Start(ctx, "SourceRegistry.LoadSource",
		output.WithAttributes(output.String("ortus.source.path", path)),
	)
	defer span.End()

	r.logger.Info("loading source", "path", path)

	// Reload vs collision: a source id is the filename stem, so two different
	// files can derive the same id (e.g. "foo.gpkg" and "foo.zip").
	id := domain.DeriveSourceID(path)
	if existingPath, loaded := r.loadedSourcePath(id); loaded {
		if existingPath != path {
			// Different file, same id — reject rather than silently evicting the
			// already-loaded source. The operator must rename one (ids must be
			// unique across all source files, regardless of extension).
			err := fmt.Errorf("%w: %q is already loaded as id %q, refusing %q",
				domain.ErrSourceIDCollision, existingPath, id, path)
			r.logger.Error("source id collision", "id", id, "existing", existingPath, "incoming", path)
			span.RecordError(err)
			span.SetStatus(output.StatusError, "id collision")
			return err
		}
		// Same file already loaded (e.g. a file-watcher modify event): unload
		// first so the adapter re-reads it instead of returning its cached,
		// pre-modification instance.
		r.logger.Info("reloading source — unloading stale instance first", "id", id)
		if err := r.UnloadSource(ctx, id); err != nil {
			r.logger.Warn("failed to unload before reload", "id", id, "error", err)
		}
	}

	// Resolve the adapter that owns this file kind.
	provider, err := r.providerFor(path)
	if err != nil {
		r.logger.Error("no adapter for source", "path", path, "error", err)
		span.RecordError(err)
		span.SetStatus(output.StatusError, "no adapter")
		return err
	}

	// Open the source
	src, err := provider.Open(ctx, path)
	if err != nil {
		r.logger.Error("failed to open source", "path", path, "error", err)
		span.RecordError(err)
		span.SetStatus(output.StatusError, "open failed")
		return err
	}

	span.SetAttributes(
		output.String("ortus.source.id", src.ID),
		output.String("ortus.source.kind", string(src.Kind)),
		output.Int("ortus.layers.count", len(src.Layers)),
	)

	// License/attribution should travel with every source so it can be surfaced
	// in query responses and the sources listing. Missing it is not fatal, but
	// warn loudly so operators notice a package that will show no attribution.
	if src.License.IsEmpty() {
		r.logger.Warn("source has no license/attribution metadata — it will show none in responses",
			"id", src.ID, "kind", string(src.Kind), "path", path)
	}

	// Register the source
	r.mu.Lock()
	r.sources[src.ID] = &sourceEntry{
		Source: src,
		Repo:   provider,
		Status: domain.StatusIndexing,
	}
	r.mu.Unlock()

	// Prepare all layers (builds spatial indices for vector sources; a no-op
	// for sources that are ready on open).
	for _, layer := range src.Layers {
		r.logger.Debug("preparing layer", "source", src.ID, "layer", layer.Name)
		if err := provider.Prepare(ctx, src.ID, layer.Name); err != nil {
			r.logger.Warn("failed to prepare layer", "source", src.ID, "layer", layer.Name, "error", err)
			span.AddEvent("layer preparation failed",
				output.String("ortus.layer.name", layer.Name),
				output.String("error", err.Error()),
			)
		}
	}

	// Update status. Indexed reflects the actual post-Prepare per-layer state
	// (Prepare updates each layer's HasIndex), not an unconditional assumption —
	// a failed Prepare leaves its layer unindexed and the source not fully ready.
	r.mu.Lock()
	if entry, ok := r.sources[src.ID]; ok {
		entry.Status = domain.StatusReady
		entry.Source.LoadedAt = time.Now()
		entry.Source.Indexed = allLayersIndexed(entry.Source.Layers)
	}
	r.mu.Unlock()

	r.updateMetrics()
	r.logger.Info("source loaded", "id", src.ID, "layers", len(src.Layers))
	span.SetStatus(output.StatusOK, "")

	return nil
}

// UnloadSource unloads a GeoPackage.
func (r *SourceRegistry) UnloadSource(ctx context.Context, sourceID string) error {
	ctx, span := r.tracer.Start(ctx, "SourceRegistry.UnloadSource",
		output.WithAttributes(output.String("ortus.source.id", sourceID)),
	)
	defer span.End()

	r.logger.Info("unloading source", "id", sourceID)

	r.mu.Lock()
	entry, ok := r.sources[sourceID]
	if !ok {
		r.mu.Unlock()
		return nil // not loaded — nothing to do
	}
	entry.Status = domain.StatusUnloading
	repo := entry.Repo
	if repo == nil {
		// Malformed entry with no owning adapter: nothing to close, but it
		// must not be left stuck in StatusUnloading — drop it.
		delete(r.sources, sourceID)
		r.mu.Unlock()
		r.updateMetrics()
		return nil
	}
	r.mu.Unlock()

	if err := repo.Close(ctx, sourceID); err != nil {
		r.logger.Error("failed to close source", "id", sourceID, "error", err)
		span.RecordError(err)
		span.SetStatus(output.StatusError, "close failed")
		return err
	}

	r.mu.Lock()
	delete(r.sources, sourceID)
	r.mu.Unlock()

	r.updateMetrics()
	span.SetStatus(output.StatusOK, "")
	return nil
}

// allLayersIndexed reports whether every layer has its index/preparation done.
// An empty layer set is vacuously indexed.
func allLayersIndexed(layers []domain.Layer) bool {
	for i := range layers {
		if !layers[i].HasIndex {
			return false
		}
	}
	return true
}

// providerFor returns the first registered adapter that supports the given
// path, or ErrUnsupportedSource if none do.
func (r *SourceRegistry) providerFor(path string) (output.SpatialSource, error) {
	for _, p := range r.providers {
		if p.Supports(path) {
			return p, nil
		}
	}
	return nil, domain.ErrUnsupportedSource
}

// Query samples/queries a single layer of a loaded source, delegating to the
// adapter that owns it. This is the seam the query service uses so it stays
// agnostic of the source kind.
func (r *SourceRegistry) Query(ctx context.Context, sourceID, layer string, coord domain.Coordinate) ([]domain.Feature, error) {
	r.mu.RLock()
	entry, ok := r.sources[sourceID]
	r.mu.RUnlock()
	if !ok || entry.Repo == nil {
		// entry.Repo is always set by LoadSource; guard anyway so a
		// malformed entry surfaces a clean error instead of a nil panic.
		return nil, domain.ErrSourceNotFound
	}
	return entry.Repo.QueryPoint(ctx, sourceID, layer, coord)
}

// ListSources returns all registered sources.
func (r *SourceRegistry) ListSources(ctx context.Context) ([]domain.Source, error) {
	_, span := r.tracer.Start(ctx, "SourceRegistry.ListSources")
	defer span.End()

	r.mu.RLock()
	defer r.mu.RUnlock()

	sources := make([]domain.Source, 0, len(r.sources))
	for _, entry := range r.sources {
		sources = append(sources, *entry.Source)
	}

	span.SetAttributes(output.Int("ortus.sources.count", len(sources)))
	return sources, nil
}

// GetSource returns a specific GeoPackage by ID.
func (r *SourceRegistry) GetSource(ctx context.Context, id string) (*domain.Source, error) {
	_, span := r.tracer.Start(ctx, "SourceRegistry.GetSource",
		output.WithAttributes(output.String("ortus.source.id", id)),
	)
	defer span.End()

	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, ok := r.sources[id]
	if !ok {
		span.RecordError(domain.ErrSourceNotFound)
		span.SetStatus(output.StatusError, "source not found")
		return nil, domain.ErrSourceNotFound
	}

	return entry.Source, nil
}

// GetSourceStatus returns the status of a GeoPackage.
func (r *SourceRegistry) GetSourceStatus(ctx context.Context, id string) (domain.SourceStatus, error) {
	_, span := r.tracer.Start(ctx, "SourceRegistry.GetSourceStatus",
		output.WithAttributes(output.String("ortus.source.id", id)),
	)
	defer span.End()

	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, ok := r.sources[id]
	if !ok {
		span.RecordError(domain.ErrSourceNotFound)
		span.SetStatus(output.StatusError, "source not found")
		return "", domain.ErrSourceNotFound
	}

	span.SetAttributes(output.String("ortus.source.status", string(entry.Status)))
	return entry.Status, nil
}

// IsReady returns true if a source is ready for queries.
func (r *SourceRegistry) IsReady(sourceID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, ok := r.sources[sourceID]
	if !ok {
		return false
	}

	return entry.Status == domain.StatusReady
}

// ReadySourceIDs returns IDs of all ready sources.
func (r *SourceRegistry) ReadySourceIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := make([]string, 0)
	for id, entry := range r.sources {
		if entry.Status == domain.StatusReady {
			ids = append(ids, id)
		}
	}
	return ids
}

// updateMetrics refreshes the atomic counters that back the
// sources.loaded / sources.ready observable gauges. Called after every
// load/unload so the gauge callback (which can fire at any time) reads
// a current value without needing r.mu.
func (r *SourceRegistry) updateMetrics() {
	r.mu.RLock()
	total := len(r.sources)
	ready := 0
	for _, entry := range r.sources {
		if entry.Status == domain.StatusReady {
			ready++
		}
	}
	r.mu.RUnlock()

	r.loadedCount.Store(int64(total))
	r.readyCount.Store(int64(ready))
}

// LoadAll loads all sources from storage.
func (r *SourceRegistry) LoadAll(ctx context.Context) error {
	ctx, span := r.tracer.Start(ctx, "SourceRegistry.LoadAll")
	defer span.End()

	r.logger.Info("loading all sources from storage")

	objects, err := r.storage.List(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "storage list failed")
		return err
	}

	span.SetAttributes(output.Int("ortus.storage.objects", len(objects)))

	loaded, failed := 0, 0
	for _, obj := range objects {
		// Reject keys that would escape the local cache dir (a hostile remote
		// store could return "../../etc/..." object keys → arbitrary write).
		localPath, err := r.safeLocalPath(obj.Key)
		if err != nil {
			r.logger.Error("rejecting unsafe storage key", "key", obj.Key, "error", err)
			failed++
			continue
		}
		if err := r.storage.Download(ctx, obj.Key, localPath); err != nil {
			r.logger.Error("failed to download source", "key", obj.Key, "error", err)
			failed++
			continue
		}

		if err := r.LoadSource(ctx, localPath); err != nil {
			r.logger.Error("failed to load source", "path", localPath, "error", err)
			failed++
			continue
		}
		loaded++
	}

	r.failedCount.Store(int64(failed))
	span.SetAttributes(
		output.Int("ortus.sources.loaded", loaded),
		output.Int("ortus.sources.failed", failed),
	)
	// Operator-visible summary (the per-source failures were logged at ERROR
	// above). A partial load is valid: ortus keeps serving what loaded.
	if failed > 0 {
		r.logger.Warn("source load completed with failures — serving the sources that loaded",
			"loaded", loaded, "failed", failed, "total", len(objects))
	} else {
		r.logger.Info("source load complete", "loaded", loaded, "total", len(objects))
	}
	// Latch readiness: the initial bring-up pass is done (even if zero or
	// partially-failed). Subsequent sync activity won't flip readiness off.
	r.initialLoadDone.Store(true)
	span.SetStatus(output.StatusOK, "")
	return nil
}

// IsLoaded returns true if a source with the given ID is already loaded.
func (r *SourceRegistry) IsLoaded(sourceID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.sources[sourceID]
	return ok
}

// SourceCount returns the number of loaded sources.
func (r *SourceRegistry) SourceCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.sources)
}

// SyncStats contains statistics from a sync operation.
type SyncStats struct {
	Added   int
	Removed int
}

// Sync synchronizes with remote storage, downloading new sources and removing
// sources that no longer exist in remote storage.
// Returns statistics about added and removed sources.
func (r *SourceRegistry) Sync(ctx context.Context) (SyncStats, error) {
	ctx, span := r.tracer.Start(ctx, "SourceRegistry.Sync")
	defer span.End()

	r.logger.Info("syncing sources from storage")

	objects, err := r.storage.List(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "storage list failed")
		return SyncStats{}, err
	}

	// Build set of remote source IDs
	remoteSources := make(map[string]string) // sourceID -> objectKey
	for _, obj := range objects {
		sourceID := domain.DeriveSourceID(obj.Key)
		remoteSources[sourceID] = obj.Key
	}

	stats := SyncStats{}
	stats.Added = r.syncAddNew(ctx, remoteSources)

	// Remove sources that no longer exist in remote storage
	// We capture both ID and path in findSourcesToRemove to avoid race conditions
	sourcesToRemove := r.findSourcesToRemove(remoteSources)
	for _, src := range sourcesToRemove {
		r.logger.Info("removing source not in remote storage", "id", src.id)

		// Unload the source
		if err := r.UnloadSource(ctx, src.id); err != nil {
			r.logger.Error("failed to unload removed source", "id", src.id, "error", err)
			continue
		}

		// Delete local cache file
		if src.path != "" {
			if err := os.Remove(src.path); err != nil && !os.IsNotExist(err) {
				r.logger.Warn("failed to delete local cache file", "path", src.path, "error", err)
			} else {
				r.logger.Debug("deleted local cache file", "path", src.path)
			}
		}

		stats.Removed++
	}

	r.logger.Info("sync completed", "added", stats.Added, "removed", stats.Removed, "total", r.SourceCount())
	span.SetAttributes(
		output.Int("ortus.sync.added", stats.Added),
		output.Int("ortus.sync.removed", stats.Removed),
		output.Int("ortus.sources.total", r.SourceCount()),
	)
	span.SetStatus(output.StatusOK, "")
	return stats, nil
}

// syncAddNew downloads and loads every remote source not already loaded,
// returning the number added. Unsafe object keys and download/load failures are
// logged and skipped (one bad source must not abort the whole sync).
func (r *SourceRegistry) syncAddNew(ctx context.Context, remoteSources map[string]string) int {
	added := 0
	for sourceID, objectKey := range remoteSources {
		if r.IsLoaded(sourceID) {
			r.logger.Debug("source already loaded, skipping", "id", sourceID)
			continue
		}
		localPath, err := r.safeLocalPath(objectKey)
		if err != nil {
			r.logger.Error("rejecting unsafe storage key", "key", objectKey, "error", err)
			continue
		}
		if err := r.storage.Download(ctx, objectKey, localPath); err != nil {
			r.logger.Error("failed to download source", "key", objectKey, "error", err)
			continue
		}
		if err := r.LoadSource(ctx, localPath); err != nil {
			r.logger.Error("failed to load source", "path", localPath, "error", err)
			continue
		}
		added++
		r.logger.Info("new source synced", "id", sourceID)
	}
	return added
}

// sourceToRemove holds information about a source that should be removed.
type sourceToRemove struct {
	id   string
	path string
}

// findSourcesToRemove returns sources that are loaded but not in remote storage.
// This captures both ID and path in a single lock acquisition to avoid race conditions.
func (r *SourceRegistry) findSourcesToRemove(remoteSources map[string]string) []sourceToRemove {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var toRemove []sourceToRemove
	for sourceID, entry := range r.sources {
		if _, exists := remoteSources[sourceID]; !exists {
			path := ""
			if entry.Source != nil {
				path = entry.Source.Path
			}
			toRemove = append(toRemove, sourceToRemove{id: sourceID, path: path})
		}
	}
	return toRemove
}

// safeLocalPath joins a storage object key onto the local cache dir, rejecting
// absolute paths and parent-traversal that would escape it (a hostile remote
// store must not be able to make ortus write outside its data directory).
func (r *SourceRegistry) safeLocalPath(key string) (string, error) {
	clean := filepath.Clean(key)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("object key %q escapes the local cache dir", key)
	}
	joined := filepath.Join(r.localPath, clean)
	base := filepath.Clean(r.localPath)
	if joined != base && !strings.HasPrefix(joined, base+string(filepath.Separator)) {
		return "", fmt.Errorf("object key %q escapes the local cache dir", key)
	}
	return joined, nil
}

// DeriveSourceID derives a source id from a file path or object key (the
// filename stem), matching the id every adapter assigns. Callers that need to
// unload/route by path (e.g. the file watcher) should use this rather than an
// adapter-specific derivation, so the registry stays the single source of truth.
func (r *SourceRegistry) DeriveSourceID(path string) string {
	return domain.DeriveSourceID(path)
}
