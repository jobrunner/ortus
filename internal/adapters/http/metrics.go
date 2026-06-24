package http

import (
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

// httpMetrics owns the HTTP-layer instruments. They live in the http
// adapter (not the metrics package) because the label values come from a
// gorilla/mux primitive — keeping `metrics` mux-free preserves the
// hexagonal split.
type httpMetrics struct {
	requests metric.Int64Counter
	duration metric.Float64Histogram
}

func newHTTPMetrics(meter metric.Meter) *httpMetrics {
	if meter == nil {
		meter = noop.NewMeterProvider().Meter("github.com/jobrunner/ortus/http")
	}
	reqs, _ := meter.Int64Counter(
		"ortus.http.requests",
		metric.WithDescription("Total number of HTTP requests"),
	)
	dur, _ := meter.Float64Histogram(
		"ortus.http.request.duration",
		metric.WithDescription("HTTP request duration in seconds"),
		metric.WithUnit("s"),
	)
	return &httpMetrics{requests: reqs, duration: dur}
}

// middleware returns the gorilla/mux middleware that records counter +
// histogram for every request that flows through it. The `path` label is
// the MATCHED ROUTE TEMPLATE ("/api/v1/sources/{sourceId}"), not the
// raw URL — so 100 distinct source IDs collapse to one label
// combination rather than 100.
//
// Note: gorilla/mux invokes NotFoundHandler / MethodNotAllowedHandler
// outside the r.Use(...) chain, so unmatched requests do NOT currently
// flow through this middleware. routePath's "unknown" fallback only
// kicks in if a caller manually wraps an unmatched-route handler with
// this middleware.
func (m *httpMetrics) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapped := &statusCaptureWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(wrapped, r)

		duration := time.Since(start).Seconds()
		path := routePath(r)
		status := statusBucket(wrapped.statusCode)

		attrs := metric.WithAttributes(
			attribute.String("method", r.Method),
			attribute.String("path", path),
			attribute.String("status", status),
		)
		m.requests.Add(r.Context(), 1, attrs)
		m.duration.Record(r.Context(), duration, metric.WithAttributes(
			attribute.String("method", r.Method),
			attribute.String("path", path),
		))
	})
}

// routePath returns the gorilla/mux route template for the matched route
// (e.g. "/api/v1/sources/{sourceId}") so that high-cardinality path
// segments collapse into a single Prometheus label combination. Returns
// "unknown" when no route matched (404/405) or the matched route was
// registered without a path template.
func routePath(r *http.Request) string {
	route := mux.CurrentRoute(r)
	if route == nil {
		return "unknown"
	}
	if t, err := route.GetPathTemplate(); err == nil && t != "" {
		return t
	}
	return "unknown"
}

// statusBucket maps an HTTP status code to its xx-bucket. Bounded
// cardinality (max 5 values) keeps Prometheus happy.
func statusBucket(code int) string {
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

// statusCaptureWriter wraps http.ResponseWriter to capture the status
// code so the middleware can label metrics with the actual response code.
type statusCaptureWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *statusCaptureWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}
