package application

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// countingProvider tracks Open/Close calls and tags each opened source with a
// version, so a reload can be observed.
type countingProvider struct {
	ext     string
	version int
	opens   int
	closes  int
}

func (p *countingProvider) Supports(path string) bool { return strings.HasSuffix(path, p.ext) }
func (p *countingProvider) Open(_ context.Context, path string) (*domain.Source, error) {
	p.opens++
	return &domain.Source{
		ID:      deriveSourceID(path),
		Name:    fmt.Sprintf("v%d", p.version),
		Kind:    domain.SourceKindVector,
		Indexed: true,
		Layers:  []domain.Layer{{Name: "l", SRID: 4326, HasIndex: true}},
	}, nil
}
func (p *countingProvider) Prepare(_ context.Context, _, _ string) error { return nil }
func (p *countingProvider) QueryPoint(_ context.Context, _, _ string, _ domain.Coordinate) ([]domain.Feature, error) {
	return nil, nil
}
func (p *countingProvider) Close(_ context.Context, _ string) error { p.closes++; return nil }

// TestLoadSourceReloadsModifiedSource is the regression test for the hot-reload
// stale-data bug: re-loading an already-loaded source must unload the stale
// instance first (so the adapter re-reads the file), not return the cached one.
func TestLoadSourceReloadsModifiedSource(t *testing.T) {
	p := &countingProvider{ext: ".gpkg", version: 1}
	reg := newRoutingRegistry([]output.SpatialSource{p})
	ctx := context.Background()

	if err := reg.LoadSource(ctx, "/data/a.gpkg"); err != nil {
		t.Fatalf("initial load: %v", err)
	}
	// File "changes" and the watcher fires a modify → reload.
	p.version = 2
	if err := reg.LoadSource(ctx, "/data/a.gpkg"); err != nil {
		t.Fatalf("reload: %v", err)
	}

	if p.opens != 2 {
		t.Errorf("opens = %d, want 2 (reload must re-open)", p.opens)
	}
	if p.closes != 1 {
		t.Errorf("closes = %d, want 1 (reload must unload the stale instance)", p.closes)
	}
	if reg.SourceCount() != 1 {
		t.Errorf("count = %d, want 1 (reload must not duplicate)", reg.SourceCount())
	}
	if src, _ := reg.GetSource(ctx, "a"); src == nil || src.Name != "v2" {
		t.Errorf("after reload GetSource = %v, want the v2 instance", src)
	}
}

// blockingProvider's QueryPoint blocks until the context is canceled.
type blockingProvider struct{}

func (blockingProvider) Supports(path string) bool { return strings.HasSuffix(path, ".gpkg") }
func (blockingProvider) Open(_ context.Context, path string) (*domain.Source, error) {
	return &domain.Source{
		ID: deriveSourceID(path), Kind: domain.SourceKindVector, Indexed: true,
		Layers: []domain.Layer{{Name: "l", SRID: 4326, HasIndex: true}},
	}, nil
}
func (blockingProvider) Prepare(_ context.Context, _, _ string) error { return nil }
func (blockingProvider) QueryPoint(ctx context.Context, _, _ string, _ domain.Coordinate) ([]domain.Feature, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (blockingProvider) Close(_ context.Context, _ string) error { return nil }

// TestQueryTimeoutIsEnforced verifies the configured per-query deadline bounds a
// hung adapter query instead of blocking forever.
func TestQueryTimeoutIsEnforced(t *testing.T) {
	reg := newRoutingRegistry([]output.SpatialSource{blockingProvider{}})
	if err := reg.LoadSource(context.Background(), "/data/a.gpkg"); err != nil {
		t.Fatalf("load: %v", err)
	}
	svc := NewQueryService(reg, nil, testMeter(), output.NoOpTracer{}, testLogger(),
		QueryServiceConfig{DefaultSRID: domain.SRIDWGS84, MaxFeatures: 100, QueryTimeout: 50 * time.Millisecond})

	done := make(chan struct{})
	var elapsed time.Duration
	go func() {
		start := time.Now()
		_, _ = svc.QueryPoint(context.Background(), domain.QueryRequest{Coordinate: domain.NewWGS84Coordinate(1, 1)})
		elapsed = time.Since(start)
		close(done)
	}()
	select {
	case <-done:
		if elapsed > 2*time.Second {
			t.Errorf("query took %s — timeout not enforced", elapsed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("QueryPoint blocked past 5s — timeout not enforced")
	}
}

func TestSafeLocalPath(t *testing.T) {
	reg := newRoutingRegistry(nil) // localPath is "/tmp"
	ok := []string{"a.gpkg", "sub/a.zip", "./b.gpkg"}
	for _, k := range ok {
		if _, err := reg.safeLocalPath(k); err != nil {
			t.Errorf("safeLocalPath(%q) unexpected error: %v", k, err)
		}
	}
	bad := []string{"../escape.gpkg", "../../etc/passwd", "/abs/x.gpkg", "sub/../../escape"}
	for _, k := range bad {
		if _, err := reg.safeLocalPath(k); err == nil {
			t.Errorf("safeLocalPath(%q) should have been rejected", k)
		}
	}
}
