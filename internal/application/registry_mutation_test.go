package application

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// newRegistryWithStorage builds a registry wired to a specific storage mock so
// the LoadAll/Sync paths can be exercised end-to-end.
func newRegistryWithStorage(storage output.ObjectStorage, providers ...output.SpatialSource) *SourceRegistry {
	if len(providers) == 0 {
		providers = []output.SpatialSource{&mockRepository{}}
	}
	return NewSourceRegistry(
		providers,
		storage,
		testMeter(),
		output.NoOpTracer{},
		slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		"/tmp",
	)
}

// TestNewSourceRegistryNilMeterAndTracer pins the nil-guard defaulting in the
// constructor: with a nil meter and nil tracer it must substitute the no-op
// implementations rather than storing nil. Flipping either guard leaves a nil
// field, so constructing the gauges (meter) or the first traced call (tracer)
// would panic — asserted here by building with nils and running a traced load.
func TestNewSourceRegistryNilMeterAndTracer(t *testing.T) {
	reg := NewSourceRegistry(
		[]output.SpatialSource{&mockRepository{}},
		&mockStorage{},
		nil, // meter — must default to a no-op meter (else gauge setup panics)
		nil, // tracer — must default to NoOpTracer (else the first Start panics)
		slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		"/tmp",
	)
	// LoadSource opens a span via r.tracer — a nil tracer would panic here.
	if err := reg.LoadSource(context.Background(), "/data/nilguard.gpkg"); err != nil {
		t.Fatalf("LoadSource with defaulted meter/tracer: %v", err)
	}
	if reg.SourceCount() != 1 {
		t.Errorf("SourceCount = %d, want 1", reg.SourceCount())
	}
}

// TestLoadAllCountsLoadedAndFailed drives LoadAll over a listing that mixes
// loadable objects with an unsafe (traversal) key. It pins the loaded/failed
// tallies AND the latch, killing the increment/store/latch mutants in LoadAll
// that survive because no test called LoadAll at all.
func TestLoadAllCountsLoadedAndFailed(t *testing.T) {
	reg := newRegistryWithStorage(&mockStorage{
		objects: []output.StorageObject{
			{Key: "a.gpkg"},
			{Key: "b.gpkg"},
			{Key: "../evil.gpkg"}, // rejected by safeLocalPath → counts as failed
		},
	})
	ctx := context.Background()

	if reg.InitialLoadComplete() {
		t.Fatal("initial load latch should start false")
	}
	if err := reg.LoadAll(ctx); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	// Two safe keys loaded, the traversal key failed.
	if got := reg.SourceCount(); got != 2 {
		t.Errorf("SourceCount = %d, want 2 (loaded++ mutated?)", got)
	}
	if got := reg.loadedCount.Load(); got != 2 {
		t.Errorf("loadedCount = %d, want 2", got)
	}
	if got := reg.failedCount.Load(); got != 1 {
		t.Errorf("failedCount = %d, want 1 (failed++ mutated?)", got)
	}
	// The latch flips true even with a partial failure.
	if !reg.InitialLoadComplete() {
		t.Error("initialLoadDone should latch true after LoadAll")
	}
}

// TestLoadAllPropagatesListError verifies a storage.List failure aborts LoadAll
// (returned verbatim) and the latch stays down — the pass never completed.
func TestLoadAllPropagatesListError(t *testing.T) {
	sentinel := errors.New("list boom")
	reg := newRegistryWithStorage(&mockStorage{listErr: sentinel})

	err := reg.LoadAll(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("LoadAll error = %v, want %v", err, sentinel)
	}
	if reg.InitialLoadComplete() {
		t.Error("latch must stay false when the pass aborts before completing")
	}
	if got := reg.SourceCount(); got != 0 {
		t.Errorf("SourceCount = %d, want 0", got)
	}
}

// TestLoadAllLatchesWhenEverySourceFails uses a download that always errors so
// every object fails. loaded stays 0, failed equals the object count, and the
// latch still flips — a fully-failed initial pass is still "done".
func TestLoadAllLatchesWhenEverySourceFails(t *testing.T) {
	reg := newRegistryWithStorage(&mockStorage{
		objects:     []output.StorageObject{{Key: "a.gpkg"}, {Key: "b.gpkg"}},
		downloadErr: errors.New("download boom"),
	})

	if err := reg.LoadAll(context.Background()); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if got := reg.SourceCount(); got != 0 {
		t.Errorf("SourceCount = %d, want 0 (nothing should load)", got)
	}
	if got := reg.failedCount.Load(); got != 2 {
		t.Errorf("failedCount = %d, want 2", got)
	}
	if !reg.InitialLoadComplete() {
		t.Error("initialLoadDone should latch true even when all sources fail")
	}
}

// TestLoadAllCountsLoadFailure exercises the branch where a download succeeds
// but LoadSource fails (provider Open errors). That path's failed++ was NOT
// COVERED because the other LoadAll tests fail earlier (unsafe key / download).
func TestLoadAllCountsLoadFailure(t *testing.T) {
	reg := newRegistryWithStorage(
		&mockStorage{objects: []output.StorageObject{{Key: "a.gpkg"}}},
		&mockRepository{openErr: errors.New("open boom")},
	)

	if err := reg.LoadAll(context.Background()); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if got := reg.SourceCount(); got != 0 {
		t.Errorf("SourceCount = %d, want 0 (Open failed, nothing loads)", got)
	}
	if got := reg.failedCount.Load(); got != 1 {
		t.Errorf("failedCount = %d, want 1 (load-failure failed++ mutated?)", got)
	}
}

// TestSafeLocalPathAcceptsBaseDir covers the boundary where the key resolves to
// the cache dir itself (joined == base). That case must be accepted, pinning the
// `joined != base` guard so its negation can't be flipped silently.
func TestSafeLocalPathAcceptsBaseDir(t *testing.T) {
	reg := newTestRegistry() // localPath "/tmp"
	got, err := reg.safeLocalPath(".")
	if err != nil {
		t.Fatalf("safeLocalPath(\".\") should be accepted (resolves to the base dir), got %v", err)
	}
	if got != "/tmp" {
		t.Errorf("safeLocalPath(\".\") = %q, want /tmp (the base dir itself)", got)
	}
}

// TestSyncDeletesLocalCacheFile pins the `src.path != ""` guard in Sync's
// removal loop by asserting the on-disk cache file is actually deleted when a
// source disappears from remote storage. Without the guard-driven os.Remove the
// file would linger, so a flipped guard fails the test.
func TestSyncDeletesLocalCacheFile(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// A real file on disk that Sync should remove once the source leaves remote.
	cacheFile := dir + "/gone.gpkg"
	if err := os.WriteFile(cacheFile, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed cache file: %v", err)
	}

	storage := &mockStorage{objects: []output.StorageObject{{Key: "gone.gpkg"}}}
	reg := NewSourceRegistry(
		[]output.SpatialSource{&mockRepository{}},
		storage,
		testMeter(),
		output.NoOpTracer{},
		logger,
		dir,
	)
	ctx := context.Background()

	// First sync loads "gone" with Path == <dir>/gone.gpkg (set by the adapter).
	if _, err := reg.Sync(ctx); err != nil {
		t.Fatalf("initial sync: %v", err)
	}
	// Drop it from remote, then sync again → it must be unloaded AND its cache
	// file deleted.
	storage.objects = nil
	stats, err := reg.Sync(ctx)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if stats.Removed != 1 {
		t.Fatalf("Removed = %d, want 1", stats.Removed)
	}
	if _, err := os.Stat(cacheFile); !os.IsNotExist(err) {
		t.Errorf("cache file still present after Sync removal (src.path guard mutated?): stat err = %v", err)
	}
}

// TestUpdateMetricsCounters pins the loaded/ready tallies against a crafted
// source map so the `ready++`, `== StatusReady`, `len(r.sources)` and the two
// atomic Store calls in updateMetrics all have observable effects. The map is
// deliberately asymmetric (1 ready, 2 not) so flipping `==` to `!=` changes the
// ready count.
func TestUpdateMetricsCounters(t *testing.T) {
	reg := newTestRegistry()
	setSources(reg, map[string]*sourceEntry{
		"r": {Source: &domain.Source{ID: "r"}, Status: domain.StatusReady},
		"i": {Source: &domain.Source{ID: "i"}, Status: domain.StatusIndexing},
		"u": {Source: &domain.Source{ID: "u"}, Status: domain.StatusUnloading},
	})

	reg.updateMetrics()

	if got := reg.loadedCount.Load(); got != 3 {
		t.Errorf("loadedCount = %d, want 3 (len mutated?)", got)
	}
	if got := reg.readyCount.Load(); got != 1 {
		t.Errorf("readyCount = %d, want 1 (ready++ or ==StatusReady mutated?)", got)
	}
}

// TestAllLayersIndexed nails the vacuous-true, all-indexed and one-unindexed
// cases so the loop's `!HasIndex` guard and its early return can't be flipped
// without a failure.
func TestAllLayersIndexed(t *testing.T) {
	cases := []struct {
		name   string
		layers []domain.Layer
		want   bool
	}{
		{"empty is vacuously indexed", nil, true},
		{"all indexed", []domain.Layer{{HasIndex: true}, {HasIndex: true}}, true},
		{"one unindexed", []domain.Layer{{HasIndex: true}, {HasIndex: false}}, false},
		{"first unindexed", []domain.Layer{{HasIndex: false}, {HasIndex: true}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := allLayersIndexed(tc.layers); got != tc.want {
				t.Errorf("allLayersIndexed = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestLoadedSourcePath covers all three branches of loadedSourcePath: a present
// entry with a Source (reports its path + true), a malformed entry whose Source
// is nil (must report false, not a collision), and a missing id.
func TestLoadedSourcePath(t *testing.T) {
	reg := newTestRegistry()
	setSources(reg, map[string]*sourceEntry{
		"good":      {Source: &domain.Source{ID: "good", Path: "/data/good.gpkg"}},
		"malformed": {Source: nil},
	})

	if path, ok := reg.loadedSourcePath("good"); !ok || path != "/data/good.gpkg" {
		t.Errorf("loadedSourcePath(good) = %q,%v; want /data/good.gpkg,true", path, ok)
	}
	if path, ok := reg.loadedSourcePath("malformed"); ok || path != "" {
		t.Errorf("loadedSourcePath(malformed) = %q,%v; want \"\",false (nil Source is not a collision)", path, ok)
	}
	if _, ok := reg.loadedSourcePath("missing"); ok {
		t.Error("loadedSourcePath(missing) should report false")
	}
}
