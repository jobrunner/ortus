// Package application contains the application services.
package application

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/input"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// SyncResult is the outcome of a sync run; defined on the driving port so
// adapters depend on input.SyncResult, not an application type.
type SyncResult = input.SyncResult

// SyncService manages periodic synchronization with remote storage.
type SyncService struct {
	registry *SourceRegistry
	interval time.Duration
	logger   *slog.Logger
	tracer   output.Tracer

	// Lifecycle management
	stopCh chan struct{}
	wg     sync.WaitGroup

	// Rate limiting for API triggers
	lastAPISync time.Time
	apiMutex    sync.Mutex

	// Prevents concurrent sync operations
	syncOpMutex sync.Mutex

	// Track next scheduled sync for reporting
	nextSync time.Time
	syncMu   sync.RWMutex
}

// NewSyncService creates a new sync service.
func NewSyncService(registry *SourceRegistry, interval time.Duration, tracer output.Tracer, logger *slog.Logger) *SyncService {
	if tracer == nil {
		tracer = output.NoOpTracer{}
	}
	return &SyncService{
		registry: registry,
		interval: interval,
		logger:   logger,
		tracer:   tracer,
		stopCh:   make(chan struct{}),
		// Initialize to past time to allow immediate first API call
		lastAPISync: time.Now().Add(-31 * time.Second),
	}
}

// Start begins the periodic sync scheduler.
func (s *SyncService) Start(ctx context.Context) {
	s.logger.Info("starting sync service", "interval", s.interval)

	s.wg.Add(1)
	go s.run(ctx)
}

// run is the main sync loop. Wrapped with panic recovery so a single tick's
// failure can't take down the whole process — instead the panic is recorded
// on a span and the loop continues.
func (s *SyncService) run(ctx context.Context) {
	defer s.wg.Done()

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	// Set initial next sync time
	s.setNextSync(time.Now().Add(s.interval))

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("sync service stopped: context canceled")
			return
		case <-s.stopCh:
			s.logger.Info("sync service stopped")
			return
		case <-ticker.C:
			s.logger.Debug("scheduled sync triggered")
			s.doSync(ctx)
			s.setNextSync(time.Now().Add(s.interval))
		}
	}
}

// Stop gracefully stops the sync service.
func (s *SyncService) Stop() {
	s.logger.Info("stopping sync service")
	close(s.stopCh)
	s.wg.Wait()
}

// TriggerSync manually triggers a sync operation with rate limiting.
// Returns ErrRateLimited if called more than 2 times per minute.
func (s *SyncService) TriggerSync(ctx context.Context) (SyncResult, error) {
	s.apiMutex.Lock()
	defer s.apiMutex.Unlock()

	// Rate limit: 30 seconds cooldown (allows ~2 requests per minute)
	if time.Since(s.lastAPISync) < 30*time.Second {
		return SyncResult{}, domain.ErrRateLimited
	}
	s.lastAPISync = time.Now()

	return s.doSyncWithResult(ctx)
}

// doSync performs the sync operation without returning detailed results.
// It is called from the scheduled-tick goroutine and includes panic recovery
// so a single tick's failure can't take down the loop. defer order matters:
// span.End() is registered first (runs last); the recover defer is registered
// second (runs first) so it can write the panic onto the span before End().
func (s *SyncService) doSync(ctx context.Context) {
	ctx, span := s.tracer.Start(ctx, "SyncService.doSync",
		output.WithAttributes(output.String("sync.trigger", "scheduled")),
	)
	defer span.End()
	defer func() {
		if rec := recover(); rec != nil {
			s.logger.Error("sync panic recovered", "panic", rec)
			span.RecordError(fmt.Errorf("panic: %v", rec))
			span.SetStatus(output.StatusError, "sync panicked")
		}
	}()

	// Prevent concurrent sync operations
	s.syncOpMutex.Lock()
	defer s.syncOpMutex.Unlock()

	stats, err := s.registry.Sync(ctx)
	if err != nil {
		s.logger.Error("sync failed", "error", err)
		span.RecordError(err)
		span.SetStatus(output.StatusError, "registry sync failed")
		return
	}
	s.logger.Info("sync completed",
		"added", stats.Added,
		"removed", stats.Removed,
		"total", s.registry.SourceCount(),
	)
	span.SetAttributes(
		output.Int("sync.added", stats.Added),
		output.Int("sync.removed", stats.Removed),
		output.Int("sync.total", s.registry.SourceCount()),
	)
	span.SetStatus(output.StatusOK, "")
}

// doSyncWithResult performs the sync operation and returns detailed results.
func (s *SyncService) doSyncWithResult(ctx context.Context) (SyncResult, error) {
	ctx, span := s.tracer.Start(ctx, "SyncService.doSyncWithResult",
		output.WithAttributes(output.String("sync.trigger", "manual")),
	)
	defer span.End()

	// Prevent concurrent sync operations
	s.syncOpMutex.Lock()
	defer s.syncOpMutex.Unlock()

	stats, err := s.registry.Sync(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "registry sync failed")
		return SyncResult{}, err
	}

	span.SetAttributes(
		output.Int("sync.added", stats.Added),
		output.Int("sync.removed", stats.Removed),
		output.Int("sync.total", s.registry.SourceCount()),
	)
	span.SetStatus(output.StatusOK, "")

	return SyncResult{
		PackagesAdded:   stats.Added,
		PackagesRemoved: stats.Removed,
		PackagesTotal:   s.registry.SourceCount(),
		SyncedAt:        time.Now(),
		NextScheduledAt: s.getNextSync(),
	}, nil
}

// setNextSync updates the next scheduled sync time.
func (s *SyncService) setNextSync(t time.Time) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	s.nextSync = t
}

// getNextSync returns the next scheduled sync time.
func (s *SyncService) getNextSync() time.Time {
	s.syncMu.RLock()
	defer s.syncMu.RUnlock()
	return s.nextSync
}

// Interval returns the sync interval.
func (s *SyncService) Interval() time.Duration {
	return s.interval
}
