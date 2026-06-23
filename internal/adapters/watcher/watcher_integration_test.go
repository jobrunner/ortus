package watcher

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUpdatePendingEvent(t *testing.T) {
	w := &Watcher{}
	cases := []struct {
		name     string
		existing Operation
		incoming Operation
		want     Operation
	}{
		{"delete then create => create", OpDelete, OpCreate, OpCreate},
		{"create then delete => delete", OpCreate, OpDelete, OpDelete},
		{"modify then delete => delete", OpModify, OpDelete, OpDelete},
		{"create then modify => create", OpCreate, OpModify, OpCreate},
		{"modify then modify => modify", OpModify, OpModify, OpModify},
		{"create then create => create", OpCreate, OpCreate, OpCreate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pe := &pendingEvent{op: tc.existing, timestamp: time.Now()}
			w.updatePendingEvent(pe, tc.incoming)
			if pe.op != tc.want {
				t.Errorf("op = %v, want %v", pe.op, tc.want)
			}
		})
	}
}

// TestWatcherHotReload drives the watcher end to end against a real directory:
// creating a .gpkg must fire OpCreate, deleting it must fire OpDelete, and a
// non-.gpkg file must be ignored.
func TestWatcherHotReload(t *testing.T) {
	if testing.Short() {
		t.Skip("filesystem-timing test; skipped in -short mode")
	}

	dir := t.TempDir()
	events := make(chan Event, 16)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	w, err := New(Config{Paths: []string{dir}, Debounce: 50 * time.Millisecond}, func(_ context.Context, e Event) error {
		events <- e
		return nil
	}, logger)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = w.Stop() }()

	gpkg := filepath.Join(dir, "regions.gpkg")
	if err := os.WriteFile(gpkg, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A non-.gpkg file must never surface an event.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	waitFor := func(op Operation) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case e := <-events:
				if filepath.Base(e.Path) != "regions.gpkg" {
					t.Errorf("event for unexpected file: %s", e.Path)
					continue
				}
				if e.Operation == op {
					return
				}
				// Tolerate create→modify coalescing noise; keep waiting for op.
			case <-deadline:
				t.Fatalf("timed out waiting for %v event", op)
			}
		}
	}

	waitFor(OpCreate)

	if err := os.Remove(gpkg); err != nil {
		t.Fatal(err)
	}
	waitFor(OpDelete)
}

func TestWatcherAddRemovePath(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	w, err := New(Config{Paths: nil, Debounce: 50 * time.Millisecond}, func(_ context.Context, _ Event) error { return nil }, logger)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = w.Stop() }()

	if err := w.AddPath(dir); err != nil {
		t.Errorf("AddPath: %v", err)
	}
	if err := w.RemovePath(dir); err != nil {
		t.Errorf("RemovePath: %v", err)
	}
	if err := w.AddPath(filepath.Join(dir, "does-not-exist")); err == nil {
		t.Error("AddPath on nonexistent dir should error")
	}
}
