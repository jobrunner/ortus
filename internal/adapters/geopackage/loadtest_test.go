package geopackage

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jobrunner/ortus/internal/domain"
)

// Load-test harness for the SQLite/SpatiaLite read path.
//
// These benchmarks are ENV-GATED: they skip unless ORTUS_LOADTEST_GPKG points
// at a GeoPackage on disk. The file is intentionally NOT part of the repo —
// supply your own large .gpkg (big enough to stress the system) to measure
// query throughput and the effect of the query.sqlite.* tuning knobs under
// concurrency. See doc/load-test.md and `make load-test`.
//
// Example:
//
//	ORTUS_LOADTEST_GPKG=/data/big.gpkg \
//	ORTUS_LOADTEST_LAYER=parcels \
//	go test -run=^$ -bench=BenchmarkLoadTest -benchmem \
//	    ./internal/adapters/geopackage/
//
// Tuning knobs mirror the query.sqlite.* config keys:
//
//	ORTUS_LOADTEST_CACHE   cache mode: private (default) | shared
//	ORTUS_LOADTEST_BUSY_MS busy_timeout in ms (default 5000)
//	ORTUS_LOADTEST_JOURNAL journal_mode, e.g. WAL (default: leave file's mode)
//	ORTUS_LOADTEST_MAXOPEN max open connections (default 0 = unlimited)
//	ORTUS_LOADTEST_MAXIDLE max idle connections (default 4)
//
// Query point (default = center of the queried layer's extent):
//
//	ORTUS_LOADTEST_X / ORTUS_LOADTEST_Y / ORTUS_LOADTEST_SRID
const (
	envLoadTestGPKG    = "ORTUS_LOADTEST_GPKG"
	envLoadTestLayer   = "ORTUS_LOADTEST_LAYER"
	envLoadTestCache   = "ORTUS_LOADTEST_CACHE"
	envLoadTestBusyMS  = "ORTUS_LOADTEST_BUSY_MS"
	envLoadTestJournal = "ORTUS_LOADTEST_JOURNAL"
	envLoadTestMaxOpen = "ORTUS_LOADTEST_MAXOPEN"
	envLoadTestMaxIdle = "ORTUS_LOADTEST_MAXIDLE"
	envLoadTestX       = "ORTUS_LOADTEST_X"
	envLoadTestY       = "ORTUS_LOADTEST_Y"
	envLoadTestSRID    = "ORTUS_LOADTEST_SRID"
)

// loadTestFixture is a prepared, indexed source ready to be queried.
type loadTestFixture struct {
	repo     *Repository
	sourceID string
	layer    string
	coord    domain.Coordinate
}

// envInt reads an integer env var, returning def when unset/blank/unparsable.
func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// setupLoadTest opens the file named by ORTUS_LOADTEST_GPKG, builds the spatial
// index on the chosen layer (timed, reported via b.Logf), and resolves the
// query coordinate. It skips the benchmark when the env gate is unset or when
// SpatiaLite is unavailable so a plain `make test-bench` stays green.
func setupLoadTest(b *testing.B) loadTestFixture {
	b.Helper()

	path := os.Getenv(envLoadTestGPKG)
	if path == "" {
		b.Skipf("%s not set — skipping load test (supply a large .gpkg to run it)", envLoadTestGPKG)
	}
	if _, err := os.Stat(path); err != nil {
		b.Skipf("%s=%q not readable: %v", envLoadTestGPKG, path, err)
	}

	opts := Options{
		CacheMode:     os.Getenv(envLoadTestCache),
		BusyTimeoutMS: envInt(envLoadTestBusyMS, 5000),
		JournalMode:   os.Getenv(envLoadTestJournal),
		MaxOpenConns:  envInt(envLoadTestMaxOpen, 0),
		MaxIdleConns:  envInt(envLoadTestMaxIdle, 4),
	}

	repo := NewRepository(opts)
	ctx := context.Background()

	src, err := repo.Open(ctx, path)
	if err != nil {
		// A missing SpatiaLite extension surfaces here; treat as a skip rather
		// than a failure so the gate behaves like the integration tests.
		b.Skipf("open %q: %v", path, err)
	}
	b.Cleanup(func() { _ = repo.Close(ctx, src.ID) })

	if len(src.Layers) == 0 {
		b.Skipf("%q has no layers", path)
	}

	layer := os.Getenv(envLoadTestLayer)
	if layer == "" {
		layer = src.Layers[0].Name
	}
	target, found := src.GetLayer(layer)
	if !found {
		b.Fatalf("layer %q not found in %q", layer, path)
	}

	// Build the R-tree index once, outside the timed loop, mirroring what the
	// registry does at source bring-up. Report the cost — index build time is
	// itself a load-test signal for large files.
	start := time.Now()
	if err := repo.Prepare(ctx, src.ID, layer); err != nil {
		b.Fatalf("prepare index for %q/%q: %v", src.ID, layer, err)
	}
	b.Logf("indexed %q/%q (%d features) in %s", src.ID, layer, target.FeatureCount, time.Since(start))

	coord, err := resolveCoord(target)
	if err != nil {
		b.Skip(err.Error())
	}
	b.Logf("querying %q/%q at (%g, %g) SRID=%d", src.ID, layer, coord.X, coord.Y, coord.SRID)

	return loadTestFixture{repo: repo, sourceID: src.ID, layer: layer, coord: coord}
}

// resolveCoord picks the query point from ORTUS_LOADTEST_X/Y/SRID, falling back
// to the layer's extent center. It errors (→ skip) when neither is available.
func resolveCoord(layer *domain.Layer) (domain.Coordinate, error) {
	xs, ys := os.Getenv(envLoadTestX), os.Getenv(envLoadTestY)
	if xs != "" && ys != "" {
		x, errX := strconv.ParseFloat(xs, 64)
		y, errY := strconv.ParseFloat(ys, 64)
		if errX != nil || errY != nil {
			return domain.Coordinate{}, fmt.Errorf("invalid %s/%s: %q/%q", envLoadTestX, envLoadTestY, xs, ys)
		}
		srid := envInt(envLoadTestSRID, domain.SRIDWGS84)
		return domain.NewCoordinate(x, y, srid), nil
	}
	if layer.Extent != nil && layer.Extent.IsValid() {
		return layer.Extent.Center(), nil
	}
	return domain.Coordinate{}, fmt.Errorf(
		"no query point: set %s/%s (the layer has no usable extent)", envLoadTestX, envLoadTestY)
}

// BenchmarkLoadTestQueryPointSerial measures single-threaded query latency —
// the baseline against which the concurrent run is compared.
func BenchmarkLoadTestQueryPointSerial(b *testing.B) {
	f := setupLoadTest(b)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := f.repo.QueryPoint(ctx, f.sourceID, f.layer, f.coord); err != nil {
			b.Fatalf("query: %v", err)
		}
	}
}

// BenchmarkLoadTestQueryPointConcurrent stresses the read path with GOMAXPROCS
// goroutines sharing one repository, the way ortus serves concurrent HTTP
// clients. Vary parallelism with `-cpu` and connection limits with
// ORTUS_LOADTEST_MAXOPEN to find the SQLite contention knee.
func BenchmarkLoadTestQueryPointConcurrent(b *testing.B) {
	f := setupLoadTest(b)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := f.repo.QueryPoint(ctx, f.sourceID, f.layer, f.coord); err != nil {
				b.Errorf("query: %v", err)
				return
			}
		}
	})
}
