package app

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jobrunner/ortus/internal/adapters/watcher"
	"github.com/jobrunner/ortus/internal/application"
	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// fakeProvider is an in-memory SpatialSource for testing the file-event routing.
type fakeProvider struct {
	ext    string
	opens  int
	closes int
}

func (p *fakeProvider) Supports(path string) bool { return strings.HasSuffix(path, p.ext) }
func (p *fakeProvider) Open(_ context.Context, path string) (*domain.Source, error) {
	p.opens++
	base := filepath.Base(path)
	id := strings.TrimSuffix(base, filepath.Ext(base))
	return &domain.Source{
		ID: id, Kind: domain.SourceKindVector, Indexed: true,
		Layers: []domain.Layer{{Name: "l", SRID: 4326, HasIndex: true}},
	}, nil
}
func (p *fakeProvider) Prepare(_ context.Context, _, _ string) error { return nil }
func (p *fakeProvider) QueryPoint(_ context.Context, _, _ string, _ domain.Coordinate) ([]domain.Feature, error) {
	return nil, nil
}
func (p *fakeProvider) Close(_ context.Context, _ string) error { p.closes++; return nil }

// TestHandleFileEvent covers the hot-reload routing seam: create routes to the
// adapter that Supports the extension (.gpkg vs .zip), modify reloads, delete
// unloads via the registry's id derivation (not an adapter-specific one).
func TestHandleFileEvent(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	gpkg := &fakeProvider{ext: ".gpkg"}
	zip := &fakeProvider{ext: ".zip"}
	reg := application.NewSourceRegistry(
		[]output.SpatialSource{gpkg, zip}, nil, nil, output.NoOpTracer{}, logger, "/tmp")
	a := &App{Tracer: output.NoOpTracer{}, Logger: logger, Registry: reg}
	ctx := context.Background()

	ev := func(path string, op watcher.Operation) error {
		return a.handleFileEvent(ctx, watcher.Event{Path: path, Operation: op})
	}

	// Create a .gpkg → routed to the gpkg adapter.
	if err := ev("/data/regions.gpkg", watcher.OpCreate); err != nil {
		t.Fatalf("create gpkg: %v", err)
	}
	if !reg.IsLoaded("regions") || gpkg.opens != 1 {
		t.Fatalf("gpkg not loaded via gpkg adapter (loaded=%v opens=%d)", reg.IsLoaded("regions"), gpkg.opens)
	}

	// Create a .zip → routed to the raster (zip) adapter.
	if err := ev("/data/koeppen.zip", watcher.OpCreate); err != nil {
		t.Fatalf("create zip: %v", err)
	}
	if !reg.IsLoaded("koeppen") || zip.opens != 1 {
		t.Fatalf("zip not loaded via zip adapter (loaded=%v opens=%d)", reg.IsLoaded("koeppen"), zip.opens)
	}

	// Modify the .gpkg → reload (unload stale + re-open), no duplicate.
	if err := ev("/data/regions.gpkg", watcher.OpModify); err != nil {
		t.Fatalf("modify gpkg: %v", err)
	}
	if gpkg.opens != 2 || gpkg.closes != 1 {
		t.Errorf("modify should reload: opens=%d closes=%d, want 2/1", gpkg.opens, gpkg.closes)
	}
	if reg.SourceCount() != 2 {
		t.Errorf("count = %d, want 2 (reload must not duplicate)", reg.SourceCount())
	}

	// Delete the .gpkg → unloaded via registry id derivation.
	if err := ev("/data/regions.gpkg", watcher.OpDelete); err != nil {
		t.Fatalf("delete gpkg: %v", err)
	}
	if reg.IsLoaded("regions") {
		t.Error("gpkg should be unloaded after delete")
	}
	if !reg.IsLoaded("koeppen") {
		t.Error("deleting the gpkg must not unload the zip source")
	}
}
