package geopackage

import (
	"context"
	"math"
	"sync"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/jobrunner/ortus/internal/domain"
)

// Reference point: Frankfurt am Main in WGS84 and ETRS89 / UTM 32N (EPSG:25832),
// as computed by PROJ. Shared by the transformer tests so every direction is
// checked against the same, independently known pair.
var (
	refWGS84 = domain.NewWGS84Coordinate(8.68, 50.1)
	refUTM   = domain.Coordinate{X: 477114.161254, Y: 5549798.285525, SRID: 25832}
)

// newTestTransformer builds a transformer and fails the test if it can't. A skip
// here would hide exactly the regression these tests exist for: CI installs
// SpatiaLite, so a transformer that doesn't come up is a failure, not a missing
// optional dependency.
func newTestTransformer(t *testing.T) *RepositoryTransformer {
	t.Helper()
	tr, err := NewRepositoryTransformer(nil)
	if err != nil {
		t.Fatalf("NewRepositoryTransformer: %v", err)
	}
	t.Cleanup(func() { _ = tr.Close() })
	return tr
}

func assertNear(t *testing.T, got, want domain.Coordinate, tol float64) {
	t.Helper()
	if got.SRID != want.SRID {
		t.Fatalf("SRID = %d, want %d", got.SRID, want.SRID)
	}
	if math.Abs(got.X-want.X) > tol || math.Abs(got.Y-want.Y) > tol {
		t.Fatalf("got %+v, want %+v (±%g)", got, want, tol)
	}
}

func TestIntegration_Transformer(t *testing.T) {
	tr := newTestTransformer(t)
	ctx := context.Background()

	// Same SRID is an identity no-op.
	out, err := tr.Transform(ctx, refWGS84, domain.SRIDWGS84)
	if err != nil {
		t.Fatalf("identity transform: %v", err)
	}
	if out != refWGS84 {
		t.Errorf("identity transform changed coord: %+v", out)
	}

	out, err = tr.Transform(ctx, refWGS84, refUTM.SRID)
	if err != nil {
		t.Fatalf("4326 -> 25832: %v", err)
	}
	assertNear(t, out, refUTM, 0.01)

	out, err = tr.Transform(ctx, refUTM, domain.SRIDWGS84)
	if err != nil {
		t.Fatalf("25832 -> 4326: %v", err)
	}
	assertNear(t, out, refWGS84, 1e-6)

	// MGRS input arrives as WGS84 / UTM (EPSG:326xx).
	out, err = tr.Transform(ctx, domain.Coordinate{X: refUTM.X, Y: refUTM.Y, SRID: 32632}, domain.SRIDWGS84)
	if err != nil {
		t.Fatalf("32632 -> 4326: %v", err)
	}
	assertNear(t, out, refWGS84, 1e-5)

	out, err = tr.Transform(ctx, refWGS84, domain.SRIDWebMercator)
	if err != nil {
		t.Fatalf("4326 -> 3857: %v", err)
	}
	if out.X < 900_000 || out.Y < 6_000_000 {
		t.Errorf("Web Mercator coords look untransformed: %+v", out)
	}
}

// TestIntegration_TransformerFreshConnections is the regression test for the
// pooled ":memory:" bug: every new pool connection is its own empty database, so
// SpatiaLite metadata initialized on one connection is missing on the next. With
// idle connections disabled, database/sql opens a fresh connection for every
// call — each one must be fully usable on its own.
func TestIntegration_TransformerFreshConnections(t *testing.T) {
	tr := newTestTransformer(t)
	tr.db.SetMaxIdleConns(0)
	ctx := context.Background()

	for i := 0; i < 20; i++ {
		out, err := tr.Transform(ctx, refUTM, domain.SRIDWGS84)
		if err != nil {
			t.Fatalf("call %d on a fresh connection: %v", i, err)
		}
		assertNear(t, out, refWGS84, 1e-6)
	}
}

// TestIntegration_TransformerConcurrent drives the transformer the way parallel
// HTTP requests do, then checks it is still healthy afterwards: in production a
// burst left the pool holding only uninitialized connections, and every later
// transform failed until restart.
func TestIntegration_TransformerConcurrent(t *testing.T) {
	tr := newTestTransformer(t)
	ctx := context.Background()

	const workers = 64
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		first error
		fails int
	)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := tr.Transform(ctx, refUTM, domain.SRIDWGS84); err != nil {
				mu.Lock()
				fails++
				if first == nil {
					first = err
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if fails > 0 {
		t.Fatalf("%d/%d concurrent transforms failed, first: %v", fails, workers, first)
	}

	for i := 0; i < 20; i++ {
		if _, err := tr.Transform(ctx, refUTM, domain.SRIDWGS84); err != nil {
			t.Fatalf("sequential call %d after the burst: %v", i, err)
		}
	}
}

// TestIntegration_TransformerInitFailure pins that a SpatiaLite init reporting
// failure through its return value (0, without an SQL error) is caught instead
// of yielding a transformer that looks healthy but can't transform anything.
func TestIntegration_TransformerInitFailure(t *testing.T) {
	orig := transformerInitSQL
	transformerInitSQL = "SELECT 0"
	t.Cleanup(func() { transformerInitSQL = orig })

	tr, err := NewRepositoryTransformer(nil)
	if err == nil {
		_ = tr.Close()
		t.Fatal("NewRepositoryTransformer succeeded although metadata init reported failure")
	}
}

func TestIntegration_TransformerCheck(t *testing.T) {
	tr := newTestTransformer(t)
	if err := tr.Check(context.Background()); err != nil {
		t.Fatalf("Check on a healthy transformer: %v", err)
	}
}

// TestIntegration_TransformerFailureMetric: every failed transform is counted,
// so a broken transformer shows up on a dashboard, not only in query answers.
func TestIntegration_TransformerFailureMetric(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	tr := newTestTransformer(t)
	tr.SetMeter(provider.Meter("test"))

	// EPSG:999999 doesn't exist → the transform fails inside SpatiaLite.
	if _, err := tr.Transform(context.Background(), refWGS84, 999999); err == nil {
		t.Fatal("transform to an unknown SRID should fail")
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	var total int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "ortus.coordinate.transform.failures" {
				continue
			}
			for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
				total += dp.Value
			}
		}
	}
	if total != 1 {
		t.Fatalf("ortus.coordinate.transform.failures = %d, want 1", total)
	}
}
