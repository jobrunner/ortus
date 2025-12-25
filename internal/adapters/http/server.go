// Package http provides the HTTP server and handlers.
package http //nolint:revive // package name conflicts with stdlib but is acceptable in this context

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	"github.com/jobrunner/ortus/internal/application"
	"github.com/jobrunner/ortus/internal/config"
)

// Server wraps the HTTP server with application handlers.
type Server struct {
	server       *http.Server
	router       *mux.Router
	queryService *application.QueryService
	registry     *application.PackageRegistry
	health       *application.HealthService
	syncService  *application.SyncService
	logger       *slog.Logger
	config       config.ServerConfig
	withGeometry bool // Include geometry in query results
}

// NewServer creates a new HTTP server.
func NewServer(
	cfg config.ServerConfig,
	queryService *application.QueryService,
	registry *application.PackageRegistry,
	health *application.HealthService,
	syncService *application.SyncService,
	logger *slog.Logger,
	withGeometry bool,
) *Server {
	s := &Server{
		queryService: queryService,
		registry:     registry,
		health:       health,
		syncService:  syncService,
		logger:       logger,
		config:       cfg,
		withGeometry: withGeometry,
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

	// Add middleware
	r.Use(s.loggingMiddleware)
	r.Use(s.recoveryMiddleware)

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

	// Query endpoints
	api.HandleFunc("/query", s.handleQuery).Methods(http.MethodGet)
	api.HandleFunc("/query/{packageId}", s.handleQueryPackage).Methods(http.MethodGet)

	// Package management endpoints
	api.HandleFunc("/packages", s.handleListPackages).Methods(http.MethodGet)
	api.HandleFunc("/packages/{packageId}", s.handleGetPackage).Methods(http.MethodGet)
	api.HandleFunc("/packages/{packageId}/layers", s.handleGetLayers).Methods(http.MethodGet)

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

// loggingMiddleware logs incoming requests.
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Wrap response writer to capture status code
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(wrapped, r)

		s.logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", wrapped.statusCode,
			"duration", time.Since(start),
			"remote_addr", r.RemoteAddr,
		)
	})
}

// recoveryMiddleware recovers from panics.
func (s *Server) recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				s.logger.Error("panic recovered", "error", err, "path", r.URL.Path)
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
