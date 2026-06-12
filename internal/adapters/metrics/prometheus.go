// Package metrics provides metrics collection using OpenTelemetry meters
// exported in Prometheus format. The Collector implements the
// output.MetricsCollector port so callers stay decoupled from this adapter.
package metrics

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/attribute"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// attrString builds a string attribute. Kept local to avoid importing
// attribute throughout this file's method bodies.
func attrString(key, value string) attribute.KeyValue {
	return attribute.String(key, value)
}

// instrumentationName groups all metrics emitted by this collector. Visible as
// the "otel_scope_name" label on /metrics output.
const instrumentationName = "github.com/jobrunner/ortus"

// Collector implements the MetricsCollector port using OpenTelemetry meters
// exported as Prometheus metrics.
type Collector struct {
	provider *sdkmetric.MeterProvider
	meter    metric.Meter

	queryCounter        metric.Int64Counter
	queryDuration       metric.Float64Histogram
	storageOperations   metric.Int64Counter
	storageDuration     metric.Float64Histogram
	httpRequestsTotal   metric.Int64Counter
	httpRequestDuration metric.Float64Histogram

	// Gauges are exposed via observable callbacks reading these counters.
	packagesLoaded atomic.Int64
	packagesReady  atomic.Int64
}

// NewCollector creates a new OTel-backed collector. The namespace is used as
// a metric-name prefix to keep the Prometheus output stable across this
// migration (ortus_queries_total etc.).
func NewCollector(namespace string) *Collector {
	if namespace == "" {
		namespace = "ortus"
	}

	exporter, err := otelprom.New()
	if err != nil {
		// otelprom.New only fails on duplicate registration with the default
		// registerer, which would indicate a programmer error during startup.
		// Returning a stub avoids a nil collector but keeps the rest of the
		// app running with a no-op metrics path.
		slog.Default().Error("creating prometheus exporter failed", "error", err)
		return &Collector{}
	}

	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter))
	meter := provider.Meter(instrumentationName)

	c := &Collector{provider: provider, meter: meter}

	prefix := namespace + "."

	c.queryCounter, _ = meter.Int64Counter(
		prefix+"queries",
		metric.WithDescription("Total number of point queries"),
	)
	c.queryDuration, _ = meter.Float64Histogram(
		prefix+"query.duration",
		metric.WithDescription("Query duration in seconds"),
		metric.WithUnit("s"),
	)
	c.storageOperations, _ = meter.Int64Counter(
		prefix+"storage.operations",
		metric.WithDescription("Total number of storage operations"),
	)
	c.storageDuration, _ = meter.Float64Histogram(
		prefix+"storage.duration",
		metric.WithDescription("Storage operation duration in seconds"),
		metric.WithUnit("s"),
	)
	c.httpRequestsTotal, _ = meter.Int64Counter(
		prefix+"http.requests",
		metric.WithDescription("Total number of HTTP requests"),
	)
	c.httpRequestDuration, _ = meter.Float64Histogram(
		prefix+"http.request.duration",
		metric.WithDescription("HTTP request duration in seconds"),
		metric.WithUnit("s"),
	)

	packagesLoadedGauge, _ := meter.Int64ObservableGauge(
		prefix+"packages.loaded",
		metric.WithDescription("Number of loaded GeoPackages"),
	)
	packagesReadyGauge, _ := meter.Int64ObservableGauge(
		prefix+"packages.ready",
		metric.WithDescription("Number of ready GeoPackages"),
	)
	_, _ = meter.RegisterCallback(
		func(_ context.Context, o metric.Observer) error {
			o.ObserveInt64(packagesLoadedGauge, c.packagesLoaded.Load())
			o.ObserveInt64(packagesReadyGauge, c.packagesReady.Load())
			return nil
		},
		packagesLoadedGauge,
		packagesReadyGauge,
	)

	return c
}

// MeterProvider returns the underlying OTel MeterProvider so it can be wired
// into otelhttp / other instrumentation libraries.
func (c *Collector) MeterProvider() metric.MeterProvider { return c.provider }

// Shutdown flushes and closes the meter provider. Safe to call on a zero
// collector.
func (c *Collector) Shutdown(ctx context.Context) error {
	if c == nil || c.provider == nil {
		return nil
	}
	return c.provider.Shutdown(ctx)
}

// IncQueryCount increments the query counter.
func (c *Collector) IncQueryCount(packageID string, success bool) {
	if c.queryCounter == nil {
		return
	}
	status := "success"
	if !success {
		status = "error"
	}
	c.queryCounter.Add(context.Background(), 1, metric.WithAttributes(
		attrString("package_id", packageID),
		attrString("status", status),
	))
}

// ObserveQueryDuration records query duration.
func (c *Collector) ObserveQueryDuration(packageID string, duration time.Duration) {
	if c.queryDuration == nil {
		return
	}
	c.queryDuration.Record(context.Background(), duration.Seconds(), metric.WithAttributes(
		attrString("package_id", packageID),
	))
}

// SetPackagesLoaded sets the number of loaded packages.
func (c *Collector) SetPackagesLoaded(count int) {
	c.packagesLoaded.Store(int64(count))
}

// SetPackagesReady sets the number of ready packages.
func (c *Collector) SetPackagesReady(count int) {
	c.packagesReady.Store(int64(count))
}

// IncStorageOperations increments storage operation counter.
func (c *Collector) IncStorageOperations(operation string, success bool) {
	if c.storageOperations == nil {
		return
	}
	status := "success"
	if !success {
		status = "error"
	}
	c.storageOperations.Add(context.Background(), 1, metric.WithAttributes(
		attrString("operation", operation),
		attrString("status", status),
	))
}

// ObserveStorageDuration records storage operation duration.
func (c *Collector) ObserveStorageDuration(operation string, duration time.Duration) {
	if c.storageDuration == nil {
		return
	}
	c.storageDuration.Record(context.Background(), duration.Seconds(), metric.WithAttributes(
		attrString("operation", operation),
	))
}

// IncHTTPRequests increments the HTTP request counter.
func (c *Collector) IncHTTPRequests(method, path, status string) {
	if c.httpRequestsTotal == nil {
		return
	}
	c.httpRequestsTotal.Add(context.Background(), 1, metric.WithAttributes(
		attrString("method", method),
		attrString("path", path),
		attrString("status", status),
	))
}

// ObserveHTTPDuration records HTTP request duration.
func (c *Collector) ObserveHTTPDuration(method, path string, duration time.Duration) {
	if c.httpRequestDuration == nil {
		return
	}
	c.httpRequestDuration.Record(context.Background(), duration.Seconds(), metric.WithAttributes(
		attrString("method", method),
		attrString("path", path),
	))
}

// Handler returns the Prometheus HTTP handler. The OTel prometheus exporter
// registers with the default registerer, which this handler serves.
func Handler() http.Handler {
	return promhttp.Handler()
}

// Middleware returns HTTP middleware for metrics collection.
func (c *Collector) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Wrap response writer to capture status code
		wrapped := &statusResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(wrapped, r)

		duration := time.Since(start)
		path := normalizePath(r.URL.Path)
		status := statusToString(wrapped.statusCode)

		c.IncHTTPRequests(r.Method, path, status)
		c.ObserveHTTPDuration(r.Method, path, duration)
	})
}

type statusResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *statusResponseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

// normalizePath normalizes the URL path for metrics. It prevents high
// cardinality by truncating long paths.
func normalizePath(path string) string {
	switch {
	case len(path) > 20:
		return path[:20] + "..."
	default:
		return path
	}
}

// statusToString converts HTTP status code to string category.
func statusToString(code int) string {
	switch {
	case code >= 200 && code < 300:
		return "2xx"
	case code >= 300 && code < 400:
		return "3xx"
	case code >= 400 && code < 500:
		return "4xx"
	case code >= 500:
		return "5xx"
	default:
		return "unknown"
	}
}

// Server provides a dedicated HTTP server for Prometheus metrics.
type Server struct {
	server *http.Server
	logger *slog.Logger
}

// NewServer creates a new metrics server.
func NewServer(port int, path string, logger *slog.Logger) *Server {
	mux := http.NewServeMux()
	mux.Handle(path, promhttp.Handler())

	return &Server{
		server: &http.Server{
			Addr:              fmt.Sprintf(":%d", port),
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
		},
		logger: logger,
	}
}

// Start starts the metrics server.
func (s *Server) Start() error {
	s.logger.Info("starting metrics server", "address", s.server.Addr)
	return s.server.ListenAndServe()
}

// Shutdown gracefully shuts down the metrics server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("shutting down metrics server")
	return s.server.Shutdown(ctx)
}
