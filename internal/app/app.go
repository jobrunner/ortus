// Package app provides application initialization and wiring.
package app

import (
	"context"
	"fmt"
	"log/slog"

	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/jobrunner/ortus/internal/adapters/geopackage"
	httpAdapter "github.com/jobrunner/ortus/internal/adapters/http"
	"github.com/jobrunner/ortus/internal/adapters/metrics"
	"github.com/jobrunner/ortus/internal/adapters/storage"
	"github.com/jobrunner/ortus/internal/adapters/telemetry"
	tlsAdapter "github.com/jobrunner/ortus/internal/adapters/tls"
	"github.com/jobrunner/ortus/internal/adapters/watcher"
	"github.com/jobrunner/ortus/internal/application"
	"github.com/jobrunner/ortus/internal/config"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// App holds all application components.
type App struct {
	Config            *config.Config
	Logger            *slog.Logger
	Storage           output.ObjectStorage
	Repository        *geopackage.Repository
	Registry          *application.PackageRegistry
	QueryService      *application.QueryService
	HealthService     *application.HealthService
	SyncService       *application.SyncService
	HTTPServer        *httpAdapter.Server
	TLSServer         *tlsAdapter.Server
	Watcher           *watcher.Watcher
	Metrics           *metrics.Collector
	MetricsServer     *metrics.Server
	TelemetryProvider *telemetry.Provider // nil when tracing is disabled
	Tracer            output.Tracer       // never nil; NoOp when tracing is disabled
}

// tracerProvider returns the underlying OTel TracerProvider for instrumentation
// libraries (e.g. otelmux) that need it directly. Returns nil when tracing is
// disabled, signalling to those libraries that they should not install
// middleware.
func (a *App) tracerProvider() oteltrace.TracerProvider {
	if a.TelemetryProvider == nil {
		return nil
	}
	return a.TelemetryProvider.TracerProvider()
}

// New creates and initializes a new application.
func New(ctx context.Context, cfg *config.Config, logger *slog.Logger) (*App, error) {
	app := &App{
		Config: cfg,
		Logger: logger,
		Tracer: output.NoOpTracer{},
	}

	// Initialize tracing (must come before HTTP/middleware setup so the
	// TracerProvider is available downstream).
	if cfg.Tracing.Enabled {
		tp, err := telemetry.NewProvider(ctx, telemetry.ProviderOptions{
			ServiceName: cfg.Tracing.ServiceName,
			Environment: cfg.Tracing.Environment,
			Transport:   cfg.Tracing.Transport,
			Endpoint:    cfg.Tracing.Endpoint,
			Insecure:    cfg.Tracing.Insecure,
			Headers:     cfg.Tracing.Headers,
			SampleRatio: cfg.Tracing.SampleRatio,
			BufferSize:  cfg.Tracing.BufferSize,
			ExtraAttrs:  cfg.Tracing.Attributes,
		}, logger)
		if err != nil {
			return nil, fmt.Errorf("initializing tracing: %w", err)
		}
		app.TelemetryProvider = tp
		app.Tracer = telemetry.NewTracer(tp.TracerProvider())
		logger.Info("tracing enabled",
			"service", cfg.Tracing.ServiceName,
			"sample_ratio", cfg.Tracing.SampleRatio,
			"buffer_size", cfg.Tracing.BufferSize,
		)
	}

	// Initialize metrics
	if cfg.Metrics.Enabled {
		app.Metrics = metrics.NewCollector("ortus")
		app.MetricsServer = metrics.NewServer(
			cfg.Metrics.Port,
			cfg.Metrics.Path,
			logger,
		)
	}

	var metricsCollector output.MetricsCollector
	if app.Metrics != nil {
		metricsCollector = app.Metrics
	} else {
		metricsCollector = &output.NoOpMetrics{}
	}

	// Initialize storage adapter
	store, err := initStorage(ctx, cfg.Storage)
	if err != nil {
		return nil, fmt.Errorf("initializing storage: %w", err)
	}
	if cfg.Tracing.Enabled {
		store = storage.NewTracedStorage(store, app.Tracer, cfg.Storage.Type)
	}
	app.Storage = store

	// Initialize GeoPackage repository
	app.Repository = geopackage.NewRepository()
	app.Repository.SetTracer(app.Tracer)

	// Initialize package registry
	app.Registry = application.NewPackageRegistry(
		app.Repository,
		app.Storage,
		metricsCollector,
		app.Tracer,
		logger,
		cfg.Storage.LocalPath,
	)

	// Initialize coordinate transformer
	transformer := geopackage.NewRepositoryTransformer(app.Repository)
	transformer.SetTracer(app.Tracer)

	// Initialize query service
	app.QueryService = application.NewQueryService(
		app.Registry,
		app.Repository,
		transformer,
		metricsCollector,
		app.Tracer,
		logger,
		application.QueryServiceConfig{
			DefaultSRID: cfg.Query.DefaultSRID,
			MaxFeatures: cfg.Query.MaxFeatures,
		},
	)

	// Initialize health service
	app.HealthService = application.NewHealthService(app.Registry, app.Tracer)

	// Initialize sync service (only for remote storage)
	if cfg.Sync.Enabled && cfg.Storage.Type != config.StorageTypeLocal {
		app.SyncService = application.NewSyncService(
			app.Registry,
			cfg.Sync.Interval,
			app.Tracer,
			logger,
		)
		logger.Info("sync service configured",
			"interval", cfg.Sync.Interval,
			"storage_type", cfg.Storage.Type,
		)
	}

	// Initialize HTTP server
	app.HTTPServer = httpAdapter.NewServer(
		cfg.Server,
		app.QueryService,
		app.Registry,
		app.HealthService,
		app.SyncService, // May be nil if sync is disabled
		logger,
		cfg.Query.WithGeometry,
		httpAdapter.ServerOptions{
			TracerProvider: app.tracerProvider(),
			ServiceName:    cfg.Tracing.ServiceName,
		},
	)

	// Initialize TLS server if enabled
	if cfg.TLS.Enabled {
		tlsServer, err := tlsAdapter.NewServer(
			tlsAdapter.Config{
				Enabled:  cfg.TLS.Enabled,
				Domains:  cfg.TLS.Domains,
				Email:    cfg.TLS.Email,
				CacheDir: cfg.TLS.CacheDir,
				Staging:  cfg.TLS.Staging,
				DNS: tlsAdapter.DNSConfig{
					SubscriptionID:    cfg.TLS.DNS.SubscriptionID,
					ResourceGroupName: cfg.TLS.DNS.ResourceGroupName,
					ClientID:          cfg.TLS.DNS.ClientID,
				},
			},
			app.HTTPServer.Router(),
			logger,
		)
		if err != nil {
			return nil, fmt.Errorf("initializing TLS: %w", err)
		}
		app.TLSServer = tlsServer
	}

	// Initialize file watcher for hot-reload
	if cfg.Storage.Type == config.StorageTypeLocal {
		w, err := watcher.New(
			watcher.Config{
				Paths:  []string{cfg.Storage.LocalPath},
				Tracer: app.Tracer,
			},
			app.handleFileEvent,
			logger,
		)
		if err != nil {
			logger.Warn("failed to initialize file watcher", "error", err)
		} else {
			app.Watcher = w
		}
	}

	return app, nil
}

// Start starts all application components.
func (a *App) Start(ctx context.Context) error {
	startupCtx, startupSpan := a.Tracer.Start(ctx, "App.Startup")
	startupSpan.SetAttributes(
		output.String("ortus.storage.type", a.Config.Storage.Type),
		output.Bool("ortus.tls.enabled", a.Config.TLS.Enabled),
		output.Bool("ortus.tracing.enabled", a.Config.Tracing.Enabled),
		output.Bool("ortus.metrics.enabled", a.Config.Metrics.Enabled),
		output.Bool("ortus.sync.enabled", a.SyncService != nil),
		output.Bool("ortus.watcher.enabled", a.Watcher != nil),
	)

	// Load all packages from storage
	if err := a.Registry.LoadAll(startupCtx); err != nil {
		a.Logger.Warn("failed to load packages", "error", err)
		startupSpan.RecordError(err)
	}
	startupSpan.SetAttributes(output.Int("ortus.packages.loaded", a.Registry.PackageCount()))

	// Start file watcher
	if a.Watcher != nil {
		if err := a.Watcher.Start(startupCtx); err != nil {
			a.Logger.Warn("failed to start file watcher", "error", err)
			startupSpan.RecordError(err)
		}
	}

	// Start metrics server in background
	if a.MetricsServer != nil {
		go func() {
			defer func() {
				if rec := recover(); rec != nil {
					a.Logger.Error("metrics server panic recovered", "panic", rec)
				}
			}()
			if err := a.MetricsServer.Start(); err != nil && err.Error() != "http: Server closed" {
				a.Logger.Error("metrics server error", "error", err)
			}
		}()
	}

	// Start sync service in background
	if a.SyncService != nil {
		a.SyncService.Start(ctx)
	}

	startupSpan.SetStatus(output.StatusOK, "")
	startupSpan.End()

	// Start server (long-running — must run outside the startup span so it
	// doesn't keep the span open for the entire lifetime of the process).
	if a.Config.TLS.Enabled && a.TLSServer != nil {
		return a.TLSServer.ListenAndServe(a.Config.Server.Address())
	}
	return a.HTTPServer.Start()
}

// Shutdown gracefully shuts down all components.
func (a *App) Shutdown(ctx context.Context) error {
	ctx, span := a.Tracer.Start(ctx, "App.Shutdown")
	defer span.End()

	a.Logger.Info("shutting down application")

	// Stop sync service
	if a.SyncService != nil {
		a.SyncService.Stop()
	}

	// Stop watcher
	if a.Watcher != nil {
		_ = a.Watcher.Stop()
	}

	// Shutdown metrics server
	if a.MetricsServer != nil {
		if err := a.MetricsServer.Shutdown(ctx); err != nil {
			a.Logger.Error("metrics server shutdown error", "error", err)
		}
	}

	// Shutdown HTTP server
	if err := a.HTTPServer.Shutdown(ctx); err != nil {
		a.Logger.Error("HTTP server shutdown error", "error", err)
	}

	// Close all packages
	packages, _ := a.Registry.ListPackages(ctx)
	for _, pkg := range packages {
		if err := a.Registry.UnloadPackage(ctx, pkg.ID); err != nil {
			a.Logger.Error("failed to unload package", "id", pkg.ID, "error", err)
		}
	}

	// Shutdown metrics provider so the prometheus exporter unregisters.
	if a.Metrics != nil {
		if err := a.Metrics.Shutdown(ctx); err != nil {
			a.Logger.Error("metrics provider shutdown error", "error", err)
		}
	}

	// Shutdown telemetry last so spans emitted during shutdown above still
	// get flushed by the BatchSpanProcessor.
	if a.TelemetryProvider != nil {
		if err := a.TelemetryProvider.Shutdown(ctx); err != nil {
			a.Logger.Error("telemetry shutdown error", "error", err)
		}
	}

	return nil
}

// handleFileEvent handles file system events for hot-reload.
func (a *App) handleFileEvent(ctx context.Context, event watcher.Event) error {
	ctx, span := a.Tracer.Start(ctx, "App.handleFileEvent",
		output.WithAttributes(
			output.String("watcher.path", event.Path),
			output.String("watcher.operation", event.Operation.String()),
		),
	)
	defer span.End()

	a.Logger.Info("file event", "path", event.Path, "operation", event.Operation.String())

	switch event.Operation {
	case watcher.OpCreate, watcher.OpModify:
		// Reload the package
		if err := a.Registry.LoadPackage(ctx, event.Path); err != nil {
			span.RecordError(err)
			span.SetStatus(output.StatusError, "load failed")
			return err
		}
		return nil

	case watcher.OpDelete:
		// Unload the package by deriving the package ID from the file path
		packageID := geopackage.DerivePackageID(event.Path)
		span.SetAttributes(output.String("ortus.package.id", packageID))
		if err := a.Registry.UnloadPackage(ctx, packageID); err != nil {
			a.Logger.Warn("failed to unload deleted package", "id", packageID, "error", err)
			span.RecordError(err)
			span.SetStatus(output.StatusError, "unload failed")
		}
		return nil
	}

	return nil
}

// initStorage initializes the appropriate storage adapter.
func initStorage(ctx context.Context, cfg config.StorageConfig) (output.ObjectStorage, error) {
	switch cfg.Type {
	case config.StorageTypeLocal:
		return storage.NewLocalStorage(cfg.LocalPath), nil

	case config.StorageTypeS3:
		return storage.NewS3Storage(ctx, storage.S3Config{
			Bucket:          cfg.S3.Bucket,
			Region:          cfg.S3.Region,
			Prefix:          cfg.S3.Prefix,
			Endpoint:        cfg.S3.Endpoint,
			AccessKeyID:     cfg.S3.AccessKeyID,
			SecretAccessKey: cfg.S3.SecretAccessKey,
		})

	case config.StorageTypeAzure:
		return storage.NewAzureStorage(storage.AzureConfig{
			Container:        cfg.Azure.Container,
			AccountName:      cfg.Azure.AccountName,
			AccountKey:       cfg.Azure.AccountKey,
			ConnectionString: cfg.Azure.ConnectionString,
			Prefix:           cfg.Azure.Prefix,
		})

	case config.StorageTypeHTTP:
		return storage.NewHTTPStorage(storage.HTTPConfig{
			BaseURL:   cfg.HTTP.BaseURL,
			IndexFile: cfg.HTTP.IndexFile,
			Timeout:   cfg.HTTP.Timeout,
			Username:  cfg.HTTP.Username,
			Password:  cfg.HTTP.Password,
		}), nil

	default:
		return nil, fmt.Errorf("unknown storage type: %s", cfg.Type)
	}
}
