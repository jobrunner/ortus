package http

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.opentelemetry.io/otel/metric/noop"

	"github.com/jobrunner/ortus/internal/application"
	"github.com/jobrunner/ortus/internal/config"
	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// These benchmarks isolate the PER-REQUEST HTTP transport tax — the cost a client
// pays for firing one REST request per coordinate (the "10 000 points = 10 000
// requests" problem). The source pool is empty (mockStorage lists nothing), so a
// /query does the full HTTP + handler + query-service work with ~zero spatial cost.
// The gap between the in-process baseline and the HTTP variants is exactly what a
// batch endpoint (one request, N points processed in-process) would reclaim.
//
// Run: go test -run=^$ -bench=BenchmarkQueryPath -benchmem ./internal/adapters/http/

func benchQueryStack(tb testing.TB) (*Server, *application.QueryService) {
	tb.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	reg := application.NewSourceRegistry(
		[]output.SpatialSource{&mockRepository{}}, &mockStorage{},
		noop.NewMeterProvider().Meter("bench"), output.NoOpTracer{}, logger, "/tmp")
	_ = reg.LoadAll(context.Background())
	health := application.NewHealthService(reg, true, output.NoOpTracer{})
	query := application.NewQueryService(reg, nil, noop.NewMeterProvider().Meter("bench"),
		output.NoOpTracer{}, logger, application.QueryServiceConfig{})
	srv := NewServer(
		config.ServerConfig{Host: "localhost", Port: 8080, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second},
		query, reg, health, nil, logger, false, ServerOptions{})
	return srv, query
}

// BenchmarkQueryPathInProcess is the floor: the query service called directly, no
// HTTP at all — what a batch handler's inner loop approaches per point.
func BenchmarkQueryPathInProcess(b *testing.B) {
	_, query := benchQueryStack(b)
	ctx := context.Background()
	req := domain.QueryRequest{Coordinate: domain.NewWGS84Coordinate(10, 50), SourceSRID: domain.SRIDWGS84}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := query.QueryPoint(ctx, req); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkQueryPathHTTPNoKeepAlive is the worst case: a fresh connection per
// request (client opens+closes TCP each time — no TLS here, so this is the lower
// bound of the no-reuse cost).
func BenchmarkQueryPathHTTPNoKeepAlive(b *testing.B) {
	srv, _ := benchQueryStack(b)
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()
	url := ts.URL + "/api/v1/query?lon=10&lat=50"
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		drain(b, client, url)
	}
}

// BenchmarkQueryPathHTTPKeepAlive: serial requests over a reused connection
// (keep-alive) — the realistic single-client-loop cost.
func BenchmarkQueryPathHTTPKeepAlive(b *testing.B) {
	srv, _ := benchQueryStack(b)
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()
	url := ts.URL + "/api/v1/query?lon=10&lat=50"
	client := ts.Client() // keep-alive on

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		drain(b, client, url)
	}
}

// BenchmarkQueryPathHTTPParallel: many clients hammering over keep-alive — the
// throughput ceiling a client reaches today only by parallelising N requests.
func BenchmarkQueryPathHTTPParallel(b *testing.B) {
	srv, _ := benchQueryStack(b)
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()
	url := ts.URL + "/api/v1/query?lon=10&lat=50"
	client := ts.Client()

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			drain(b, client, url)
		}
	})
}

func drain(b *testing.B, c *http.Client, url string) {
	resp, err := c.Get(url)
	if err != nil {
		b.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b.Fatalf("status %d", resp.StatusCode)
	}
}
