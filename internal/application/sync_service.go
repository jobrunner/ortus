// Package application contains the application services.
package application

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// ErrRateLimited is returned when the sync API rate limit is exceeded.
var ErrRateLimited = errors.New("rate limit exceeded")

// SyncResult contains the result of a sync operation.
type SyncResult struct {
	PackagesAdded   int       `json:"packages_added"`
	PackagesRemoved int       `json:"packages_removed"`
	PackagesTotal   int       `json:"packages_total"`
	SyncedAt        time.Time `json:"synced_at"`
	NextScheduledAt time.Time `json:"next_scheduled_at,omitempty"`
}

// SyncService manages periodic synchronization with remote storage.
type SyncService struct {
	registry *PackageRegistry
	interval time.Duration
	logger   *slog.Logger

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
func NewSyncService(registry *PackageRegistry, interval time.Duration, logger *slog.Logger) *SyncService {
	return &SyncService{
		registry: registry,
		interval: interval,
		logger:   logger,
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

// run is the main sync loop.
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
		return SyncResult{}, ErrRateLimited
	}
	s.lastAPISync = time.Now()

	return s.doSyncWithResult(ctx)
}

// doSync performs the sync operation without returning detailed results.
func (s *SyncService) doSync(ctx context.Context) {
	// Prevent concurrent sync operations
	s.syncOpMutex.Lock()
	defer s.syncOpMutex.Unlock()

	stats, err := s.registry.Sync(ctx)
	if err != nil {
		s.logger.Error("sync failed", "error", err)
		return
	}
	s.logger.Info("sync completed",
		"added", stats.Added,
		"removed", stats.Removed,
		"total", s.registry.PackageCount(),
	)
}

// doSyncWithResult performs the sync operation and returns detailed results.
func (s *SyncService) doSyncWithResult(ctx context.Context) (SyncResult, error) {
	// Prevent concurrent sync operations
	s.syncOpMutex.Lock()
	defer s.syncOpMutex.Unlock()

	stats, err := s.registry.Sync(ctx)
	if err != nil {
		return SyncResult{}, err
	}

	return SyncResult{
		PackagesAdded:   stats.Added,
		PackagesRemoved: stats.Removed,
		PackagesTotal:   s.registry.PackageCount(),
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
