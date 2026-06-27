// Package http provides the HTTP server and handlers.
package http //nolint:revive // package name conflicts with stdlib but is acceptable in this context

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gorilla/mux/otelmux"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/jobrunner/ortus/internal/config"
	"github.com/jobrunner/ortus/internal/ports/input"
)

// Server wraps the HTTP server with application handlers. It depends only on
// the driving ports (input.*), not on concrete application services.
type Server struct {
	server         *http.Server
	router         *mux.Router
	queryService   input.QueryService
	registry       input.SourceRegistry
	health         input.HealthChecker
	syncService    input.Syncer
	logger         *slog.Logger
	config         config.ServerConfig
	withGeometry   bool                 // Include geometry in query results
	tracerProvider trace.TracerProvider // Used by otelmux middleware; may be nil
	serviceName    string               // Used as otelmux service name; defaults to "ortus"
	httpMetrics    *httpMetrics         // HTTP-level instruments; nil when metrics disabled
	rateLimiter    *ipRateLimiter       // per-IP limiter; nil unless server.rate_limit.enabled
	trustedProxies []*net.IPNet         // proxy CIDRs allowed to set X-Forwarded-For
}

// ServerOptions wraps optional dependencies the HTTP server can use, such as
// the OTel TracerProvider for request-level tracing and the MeterProvider
// for HTTP request metrics.
type ServerOptions struct {
	TracerProvider trace.TracerProvider
	MeterProvider  metric.MeterProvider
	ServiceName    string
}

// NewServer creates a new HTTP server.
func NewServer(
	cfg config.ServerConfig,
	queryService input.QueryService,
	registry input.SourceRegistry,
	health input.HealthChecker,
	syncService input.Syncer,
	logger *slog.Logger,
	withGeometry bool,
	opts ServerOptions,
) *Server {
	serviceName := opts.ServiceName
	if serviceName == "" {
		serviceName = "ortus"
	}

	var httpM *httpMetrics
	if opts.MeterProvider != nil {
		httpM = newHTTPMetrics(opts.MeterProvider.Meter("github.com/jobrunner/ortus/http"))
	}

	s := &Server{
		queryService:   queryService,
		registry:       registry,
		health:         health,
		syncService:    syncService,
		logger:         logger,
		config:         cfg,
		withGeometry:   withGeometry,
		tracerProvider: opts.TracerProvider,
		serviceName:    serviceName,
		httpMetrics:    httpM,
	}

	// Opt-in per-IP rate limiting (off by default). Only the /api/v1 surface is
	// limited; health/probe endpoints are never throttled.
	if cfg.RateLimit.Enabled {
		if cfg.RateLimit.Rate <= 0 {
			// Fail safe: a non-positive rate would deny all traffic after the
			// burst. Treat as a misconfiguration and leave limiting OFF.
			logger.Warn("rate limiting requested but rate <= 0 — leaving it DISABLED",
				"rate", cfg.RateLimit.Rate)
		} else {
			trusted, invalid := parseCIDRs(cfg.RateLimit.TrustedProxies)
			if len(invalid) > 0 {
				logger.Warn("ignoring invalid trusted_proxies CIDRs — X-Forwarded-For will not be trusted for these",
					"invalid", invalid)
			}
			s.rateLimiter = newIPRateLimiter(cfg.RateLimit.Rate, cfg.RateLimit.Burst)
			s.trustedProxies = trusted
			logger.Info("rate limiting enabled",
				"rate", cfg.RateLimit.Rate, "burst", cfg.RateLimit.Burst,
				"trusted_proxies", len(trusted))
		}
	}

	s.router = s.setupRoutes()

	s.server = &http.Server{
		Addr:         cfg.Address(),
		Handler:      s.router,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	}

	return s
}

// setupRoutes configures all HTTP routes.
func (s *Server) setupRoutes() *mux.Router {
	r := mux.NewRouter()

	// Tracing middleware MUST run first so subsequent middleware (logging,
	// CORS, handlers) sees the span context. The instrumentation uses the
	// matched mux route as span name (e.g. "GET /api/v1/query/{sourceId}"),
	// which keeps cardinality low.
	if s.tracerProvider != nil {
		r.Use(otelmux.Middleware(
			s.serviceName,
			otelmux.WithTracerProvider(s.tracerProvider),
		))
	}

	// HTTP metrics: must be OUTSIDE recoveryMiddleware so panics-turned-500
	// land in the status="5xx" series, and OUTSIDE traceIDHeader so the
	// duration includes header-write time. The matched mux route is
	// resolved inside the middleware via mux.CurrentRoute(r) — that's what
	// fixes the cardinality bug from issue #14.
	if s.httpMetrics != nil {
		r.Use(s.httpMetrics.middleware)
	}

	// Add middleware. traceIDHeader runs immediately after the tracing
	// middleware so every response — including errors from later middleware
	// or panics caught by recovery — carries an X-Trace-Id the user can
	// quote when reporting issues.
	if s.tracerProvider != nil {
		r.Use(s.traceIDHeaderMiddleware)
	}
	r.Use(s.loggingMiddleware)
	r.Use(s.recoveryMiddleware)

	// Note on 404/405 coverage: gorilla/mux invokes its NotFoundHandler /
	// MethodNotAllowedHandler outside the r.Use(...) middleware chain, so
	// unmatched routes don't currently flow through the metrics middleware.
	// That's acceptable — it keeps cardinality bounded with zero extra
	// code. If we ever want to count unmatched traffic, the fix is to wrap
	// those handlers with the same middleware chain manually.

	// Add CORS middleware if configured
	if s.config.CORS.Enabled() {
		r.Use(s.corsMiddleware)
	}

	// Health endpoints
	r.HandleFunc("/health", s.handleHealth).Methods(http.MethodGet)
	r.HandleFunc("/health/live", s.handleLiveness).Methods(http.MethodGet)
	r.HandleFunc("/health/ready", s.handleReadiness).Methods(http.MethodGet)

	// API v1
	api := r.PathPrefix("/api/v1").Subrouter()

	// Per-IP rate limiting on the API surface only (never on /health probes).
	if s.rateLimiter != nil {
		api.Use(s.rateLimitMiddleware)
	}

	// Query endpoints
	api.HandleFunc("/query", s.handleQuery).Methods(http.MethodGet)
	api.HandleFunc("/query/{sourceId}", s.handleQuerySource).Methods(http.MethodGet)

	// Source management endpoints
	api.HandleFunc("/sources", s.handleListSources).Methods(http.MethodGet)
	api.HandleFunc("/sources/{sourceId}", s.handleGetSource).Methods(http.MethodGet)
	api.HandleFunc("/sources/{sourceId}/layers", s.handleGetLayers).Methods(http.MethodGet)

	// Sync endpoint (only if sync service is configured)
	if s.syncService != nil {
		api.HandleFunc("/sync", s.handleSync).Methods(http.MethodPost)
	}

	// OpenAPI spec and Swagger UI
	r.HandleFunc("/openapi.json", s.handleOpenAPI).Methods(http.MethodGet)
	r.HandleFunc("/docs", s.handleSwaggerUI).Methods(http.MethodGet)
	r.HandleFunc("/swagger", s.handleSwaggerUI).Methods(http.MethodGet)

	// Frontend for coordinate queries (if enabled)
	if s.config.FrontendEnabled {
		r.HandleFunc("/", s.handleFrontend).Methods(http.MethodGet)
	}

	return r
}

// Router returns the mux router.
func (s *Server) Router() *mux.Router {
	return s.router
}

// Start starts the HTTP server.
func (s *Server) Start() error {
	s.logger.Info("starting HTTP server", "address", s.config.Address())
	return s.server.ListenAndServe()
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("shutting down HTTP server")
	return s.server.Shutdown(ctx)
}

// traceIDHeaderMiddleware writes the active trace id to X-Trace-Id on every
// response. Users reporting "GET /api/v1/query returned 500" can quote this
// id and the MCP server can pull the full trace from the ring buffer.
//
// The header must be written BEFORE the handler calls WriteHeader. We set
// it up-front (before calling next.ServeHTTP); since net/http only flushes
// the response headers on the first body write or explicit WriteHeader,
// any handler-set X-Trace-Id arrives at the client.
func (s *Server) traceIDHeaderMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sc := trace.SpanContextFromContext(r.Context())
		if sc.IsValid() {
			w.Header().Set("X-Trace-Id", sc.TraceID().String())
		}
		next.ServeHTTP(w, r)
	})
}

// loggingMiddleware logs incoming requests, enriched with trace_id when a
// span is present so log lines can be correlated with traces in the buffer
// or in the OTLP backend.
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Wrap response writer to capture status code
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(wrapped, r)

		fields := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", wrapped.statusCode,
			"duration", time.Since(start),
			"remote_addr", r.RemoteAddr,
		}
		if sc := trace.SpanContextFromContext(r.Context()); sc.IsValid() {
			fields = append(fields, "trace_id", sc.TraceID().String(), "span_id", sc.SpanID().String())
		}
		s.logger.Info("request", fields...)
	})
}

// recoveryMiddleware recovers from panics and records them on the active span
// so panics are visible in the trace timeline alongside the request.
func (s *Server) recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				fields := []any{"error", err, "path", r.URL.Path}
				if sc := trace.SpanContextFromContext(r.Context()); sc.IsValid() {
					fields = append(fields, "trace_id", sc.TraceID().String())
					span := trace.SpanFromContext(r.Context())
					span.RecordError(fmt.Errorf("panic: %v", err), trace.WithStackTrace(true))
					span.SetStatus(otelcodes.Error, "panic recovered")
				}
				s.logger.Error("panic recovered", fields...)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// responseWriter wraps http.ResponseWriter to capture status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}
