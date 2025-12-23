// Package watcher provides file system watching for hot-reload.
package watcher

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Event represents a file system event.
type Event struct {
	Path      string
	Operation Operation
}

// Operation represents the type of file operation.
type Operation int

// File operation types.
const (
	OpCreate Operation = iota
	OpModify
	OpDelete
)

// String returns the string representation of the operation.
func (o Operation) String() string {
	switch o {
	case OpCreate:
		return "create"
	case OpModify:
		return "modify"
	case OpDelete:
		return "delete"
	default:
		return "unknown"
	}
}

// Handler is called when a relevant file event occurs.
type Handler func(ctx context.Context, event Event) error

// pendingEvent holds a debounced event with its operation.
type pendingEvent struct {
	timestamp time.Time
	op        Operation
}

// Watcher watches directories for GeoPackage file changes.
type Watcher struct {
	fsWatcher *fsnotify.Watcher
	handler   Handler
	logger    *slog.Logger
	paths     []string
	debounce  time.Duration
	mu        sync.Mutex
	pending   map[string]*pendingEvent
}

// Config holds watcher configuration.
type Config struct {
	Paths    []string
	Debounce time.Duration
}

// New creates a new file watcher.
func New(cfg Config, handler Handler, logger *slog.Logger) (*Watcher, error) {
	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	if cfg.Debounce == 0 {
		cfg.Debounce = 500 * time.Millisecond
	}

	return &Watcher{
		fsWatcher: fsWatcher,
		handler:   handler,
		logger:    logger,
		paths:     cfg.Paths,
		debounce:  cfg.Debounce,
		pending:   make(map[string]*pendingEvent),
	}, nil
}

// Start starts watching the configured paths.
func (w *Watcher) Start(ctx context.Context) error {
	// Add paths to watch
	for _, path := range w.paths {
		absPath, err := filepath.Abs(path)
		if err != nil {
			w.logger.Warn("invalid watch path", "path", path, "error", err)
			continue
		}

		if err := w.fsWatcher.Add(absPath); err != nil {
			w.logger.Warn("failed to watch path", "path", absPath, "error", err)
			continue
		}

		w.logger.Info("watching directory", "path", absPath)
	}

	// Start event loop
	go w.eventLoop(ctx)

	// Start debounce processor
	go w.debounceLoop(ctx)

	return nil
}

// Stop stops the watcher.
func (w *Watcher) Stop() error {
	return w.fsWatcher.Close()
}

// eventLoop processes fsnotify events.
func (w *Watcher) eventLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return

		case event, ok := <-w.fsWatcher.Events:
			if !ok {
				return
			}
			w.handleFsEvent(event)

		case err, ok := <-w.fsWatcher.Errors:
			if !ok {
				return
			}
			w.logger.Error("watcher error", "error", err)
		}
	}
}

// handleFsEvent processes a single fsnotify event.
func (w *Watcher) handleFsEvent(event fsnotify.Event) {
	// Only process .gpkg files
	if !isGeoPackageFile(event.Name) {
		return
	}

	w.logger.Debug("file event", "path", event.Name, "op", event.Op.String())

	// Convert fsnotify operation to our operation type
	op := fsnotifyOpToOperation(event.Op)

	// Add to pending events for debouncing
	w.mu.Lock()
	defer w.mu.Unlock()

	existing, exists := w.pending[event.Name]
	if !exists {
		w.pending[event.Name] = &pendingEvent{
			timestamp: time.Now(),
			op:        op,
		}
		return
	}

	// Update pending event based on operation precedence
	w.updatePendingEvent(existing, op)
}

// updatePendingEvent updates an existing pending event based on the new operation.
func (w *Watcher) updatePendingEvent(existing *pendingEvent, newOp Operation) {
	existing.timestamp = time.Now()

	switch {
	case existing.op == OpDelete && newOp == OpCreate:
		// File was deleted then recreated - use create operation
		existing.op = OpCreate
	case newOp == OpDelete:
		// New delete event always takes precedence
		existing.op = OpDelete
		// For other cases (modify after modify, etc), just update timestamp
	}
}

// debounceLoop processes debounced events.
func (w *Watcher) debounceLoop(ctx context.Context) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			w.processPending(ctx)
		}
	}
}

// processPending processes pending debounced events.
func (w *Watcher) processPending(ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()

	now := time.Now()
	for path, pending := range w.pending {
		if now.Sub(pending.timestamp) < w.debounce {
			continue
		}

		delete(w.pending, path)

		event := Event{
			Path:      path,
			Operation: pending.op,
		}

		w.logger.Info("processing file event",
			"path", path,
			"operation", pending.op.String(),
		)

		// Call handler in goroutine to not block
		go func(e Event) {
			if err := w.handler(ctx, e); err != nil {
				w.logger.Error("handler error",
					"path", e.Path,
					"operation", e.Operation.String(),
					"error", err,
				)
			}
		}(event)
	}
}

// fsnotifyOpToOperation converts fsnotify.Op to our Operation type.
func fsnotifyOpToOperation(op fsnotify.Op) Operation {
	switch {
	case op.Has(fsnotify.Remove):
		return OpDelete
	case op.Has(fsnotify.Rename):
		// Rename is treated as delete (the file is gone from original location)
		return OpDelete
	case op.Has(fsnotify.Create):
		return OpCreate
	default:
		// Write, Chmod, etc. are treated as modify
		return OpModify
	}
}

// isGeoPackageFile checks if the path is a GeoPackage file.
func isGeoPackageFile(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".gpkg")
}

// AddPath adds a path to watch.
func (w *Watcher) AddPath(path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	if err := w.fsWatcher.Add(absPath); err != nil {
		return err
	}

	w.logger.Info("added watch path", "path", absPath)
	return nil
}

// RemovePath removes a path from watching.
func (w *Watcher) RemovePath(path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	if err := w.fsWatcher.Remove(absPath); err != nil {
		return err
	}

	w.logger.Info("removed watch path", "path", absPath)
	return nil
}
