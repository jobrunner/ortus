// Package metrics provides Prometheus metrics collection.
package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Collector implements the MetricsCollector port using Prometheus.
type Collector struct {
	queryCounter        *prometheus.CounterVec
	queryDuration       *prometheus.HistogramVec
	packagesLoaded      prometheus.Gauge
	packagesReady       prometheus.Gauge
	storageOperations   *prometheus.CounterVec
	storageDuration     *prometheus.HistogramVec
	httpRequestsTotal   *prometheus.CounterVec
	httpRequestDuration *prometheus.HistogramVec
}

// NewCollector creates a new Prometheus metrics collector.
func NewCollector(namespace string) *Collector {
	if namespace == "" {
		namespace = "ortus"
	}

	return &Collector{
		queryCounter: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "queries_total",
				Help:      "Total number of point queries",
			},
			[]string{"package_id", "status"},
		),

		queryDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "query_duration_seconds",
				Help:      "Query duration in seconds",
				Buckets:   prometheus.DefBuckets,
			},
			[]string{"package_id"},
		),

		packagesLoaded: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "packages_loaded",
				Help:      "Number of loaded GeoPackages",
			},
		),

		packagesReady: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "packages_ready",
				Help:      "Number of ready GeoPackages",
			},
		),

		storageOperations: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "storage_operations_total",
				Help:      "Total number of storage operations",
			},
			[]string{"operation", "status"},
		),

		storageDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "storage_duration_seconds",
				Help:      "Storage operation duration in seconds",
				Buckets:   prometheus.DefBuckets,
			},
			[]string{"operation"},
		),

		httpRequestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "http_requests_total",
				Help:      "Total number of HTTP requests",
			},
			[]string{"method", "path", "status"},
		),

		httpRequestDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "http_request_duration_seconds",
				Help:      "HTTP request duration in seconds",
				Buckets:   prometheus.DefBuckets,
			},
			[]string{"method", "path"},
		),
	}
}

// IncQueryCount increments the query counter.
func (c *Collector) IncQueryCount(packageID string, success bool) {
	status := "success"
	if !success {
		status = "error"
	}
	c.queryCounter.WithLabelValues(packageID, status).Inc()
}

// ObserveQueryDuration records query duration.
func (c *Collector) ObserveQueryDuration(packageID string, duration time.Duration) {
	c.queryDuration.WithLabelValues(packageID).Observe(duration.Seconds())
}

// SetPackagesLoaded sets the number of loaded packages.
func (c *Collector) SetPackagesLoaded(count int) {
	c.packagesLoaded.Set(float64(count))
}

// SetPackagesReady sets the number of ready packages.
func (c *Collector) SetPackagesReady(count int) {
	c.packagesReady.Set(float64(count))
}

// IncStorageOperations increments storage operation counter.
func (c *Collector) IncStorageOperations(operation string, success bool) {
	status := "success"
	if !success {
		status = "error"
	}
	c.storageOperations.WithLabelValues(operation, status).Inc()
}

// ObserveStorageDuration records storage operation duration.
func (c *Collector) ObserveStorageDuration(operation string, duration time.Duration) {
	c.storageDuration.WithLabelValues(operation).Observe(duration.Seconds())
}

// IncHTTPRequests increments the HTTP request counter.
func (c *Collector) IncHTTPRequests(method, path, status string) {
	c.httpRequestsTotal.WithLabelValues(method, path, status).Inc()
}

// ObserveHTTPDuration records HTTP request duration.
func (c *Collector) ObserveHTTPDuration(method, path string, duration time.Duration) {
	c.httpRequestDuration.WithLabelValues(method, path).Observe(duration.Seconds())
}

// Handler returns the Prometheus HTTP handler.
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

// normalizePath normalizes the URL path for metrics.
func normalizePath(path string) string {
	// Replace dynamic segments with placeholders
	// This prevents high cardinality metrics
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
