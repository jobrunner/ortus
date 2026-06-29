// Package app provides application initialization and wiring.
package app

import (
	"context"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel/metric"
	otelmetricnoop "go.opentelemetry.io/otel/metric/noop"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/jobrunner/ortus/internal/adapters/geopackage"
	httpAdapter "github.com/jobrunner/ortus/internal/adapters/http"
	"github.com/jobrunner/ortus/internal/adapters/mcp"
	"github.com/jobrunner/ortus/internal/adapters/metrics"
	"github.com/jobrunner/ortus/internal/adapters/raster"
	"github.com/jobrunner/ortus/internal/adapters/storage"
	"github.com/jobrunner/ortus/internal/adapters/telemetry"
	tlsAdapter "github.com/jobrunner/ortus/internal/adapters/tls"
	"github.com/jobrunner/ortus/internal/adapters/watcher"
	"github.com/jobrunner/ortus/internal/application"
	"github.com/jobrunner/ortus/internal/config"
	"github.com/jobrunner/ortus/internal/ports/input"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// App holds all application components.
type App struct {
	Config            *config.Config
	Logger            *slog.Logger
	Storage           output.ObjectStorage
	Repository        *geopackage.Repository
	RasterRepository  *raster.Repository
	Registry          *application.SourceRegistry
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
	MCPServer         *mcp.Server         // nil when MCP is disabled
}

// tracerProvider returns the underlying OTel TracerProvider for instrumentation
// libraries (e.g. otelmux) that need it directly. Returns nil when tracing is
// disabled, signaling to those libraries that they should not install
// middleware.
func (a *App) tracerProvider() oteltrace.TracerProvider {
	if a.TelemetryProvider == nil {
		return nil
	}
	return a.TelemetryProvider.TracerProvider()
}

// meterProvider returns the MeterProvider for HTTP instrumentation. nil
// when metrics are disabled, signaling to the HTTP layer to skip metric
// middleware installation.
func (a *App) meterProvider() metric.MeterProvider {
	if a.Metrics == nil {
		return nil
	}
	return a.Metrics.MeterProvider()
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

	// Initialize metrics provider. Combines a Prometheus reader (for the
	// existing /metrics scrape endpoint) with an optional OTLP push reader
	// configured via metrics.otlp.*. Falls back to a no-op meter when
	// metrics are disabled entirely so service code never has to nil-check.
	var meter metric.Meter
	if cfg.Metrics.Enabled {
		mc, err := metrics.New(ctx, metrics.Options{
			OTLPEnabled:   cfg.Metrics.OTLP.Enabled,
			OTLPEndpoint:  cfg.MetricsOTLPEndpoint(),
			OTLPTransport: cfg.Metrics.OTLP.Transport,
			OTLPInsecure:  cfg.Metrics.OTLP.Insecure,
			OTLPHeaders:   cfg.Metrics.OTLP.Headers,
			OTLPInterval:  cfg.Metrics.OTLP.Interval,
		}, logger)
		if err != nil {
			return nil, fmt.Errorf("initializing metrics: %w", err)
		}
		app.Metrics = mc
		meter = mc.MeterProvider().Meter("github.com/jobrunner/ortus")
		app.MetricsServer = metrics.NewServer(cfg.Metrics.Port, cfg.Metrics.Path, logger)
	} else {
		meter = otelmetricnoop.NewMeterProvider().Meter("github.com/jobrunner/ortus")
	}

	// Initialize storage adapter
	store, err := buildStorage(ctx, cfg, app.Tracer)
	if err != nil {
		return nil, err
	}
	app.Storage = store

	// Initialize GeoPackage (vector) repository
	app.Repository = geopackage.NewRepository(geopackage.Options{
		CacheMode:     cfg.Query.SQLite.CacheMode,
		BusyTimeoutMS: cfg.Query.SQLite.BusyTimeoutMS,
		JournalMode:   cfg.Query.SQLite.JournalMode,
		MaxOpenConns:  cfg.Query.SQLite.MaxOpenConns,
		MaxIdleConns:  cfg.Query.SQLite.MaxIdleConns,
	})
	app.Repository.SetTracer(app.Tracer)

	// Initialize raster bundle repository. Bundles are unpacked into OS temp
	// dirs (not the watched storage path, so unpacked files don't re-trigger
	// the watcher) and cleaned up on unload.
	app.RasterRepository = raster.NewRepository("")
	app.RasterRepository.SetTracer(app.Tracer)

	// Initialize source registry with the available source adapters. The
	// registry routes each file to the first adapter whose Supports matches
	// (geopackage: *.gpkg, raster: *.zip).
	app.Registry = application.NewSourceRegistry(
		[]output.SpatialSource{app.Repository, app.RasterRepository},
		app.Storage,
		meter,
		app.Tracer,
		logger,
		cfg.Storage.LocalPath,
	)

	// Initialize coordinate transformer
	transformer, err := geopackage.NewRepositoryTransformer(app.Repository)
	if err != nil {
		return nil, fmt.Errorf("initializing coordinate transformer: %w", err)
	}
	transformer.SetTracer(app.Tracer)

	// Initialize query service
	app.QueryService = application.NewQueryService(
		app.Registry,
		transformer,
		meter,
		app.Tracer,
		logger,
		application.QueryServiceConfig{
			MaxFeatures:  cfg.Query.MaxFeatures,
			QueryTimeout: cfg.Query.Timeout,
		},
	)

	// Initialize health service
	app.HealthService = application.NewHealthService(app.Registry, cfg.Server.ReadyWhenEmpty, app.Tracer)

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

	// Initialize HTTP server. Guard the syncer against the typed-nil trap: a
	// nil *application.SyncService stuffed into an input.Syncer interface is
	// NOT == nil, which would defeat the handler's nil check.
	var syncer input.Syncer
	if app.SyncService != nil {
		syncer = app.SyncService
	}
	app.HTTPServer = httpAdapter.NewServer(
		cfg.Server,
		app.QueryService,
		app.Registry,
		app.HealthService,
		syncer, // nil interface when sync is disabled
		logger,
		cfg.Query.WithGeometry,
		httpAdapter.ServerOptions{
			TracerProvider: app.tracerProvider(),
			MeterProvider:  app.meterProvider(),
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

	// Initialize MCP server (optional, off by default). Lives on its own
	// port so a NetworkPolicy can isolate it from the public REST API.
	if cfg.MCP.Enabled {
		app.MCPServer = mcp.New(
			mcp.Options{
				Host:  cfg.MCP.Host,
				Port:  cfg.MCP.Port,
				Path:  cfg.MCP.Path,
				Token: cfg.MCP.Token,
			},
			app.MCPDeps(),
			logger,
		)
		logger.Info("MCP server configured",
			"host", cfg.MCP.Host,
			"port", cfg.MCP.Port,
			"path", cfg.MCP.Path,
			"token_set", cfg.MCP.Token != "",
		)
	}

	return app, nil
}

// MCPDeps bundles the dependencies the MCP adapter needs. Exported so the
// stdio-mode subcommand (cmd/ortus) builds the exact same Deps struct via this
// one definition instead of duplicating the field-by-field wiring.
func (a *App) MCPDeps() mcp.Deps {
	// Keep the interface nil (not a typed-nil) when tracing is off, so the MCP
	// tools' `deps.Telemetry == nil` checks degrade gracefully.
	var tq input.TelemetryQuery
	if a.TelemetryProvider != nil {
		tq = a.TelemetryProvider.Buffer()
	}
	version := a.Config.Build.Version
	if version == "" {
		version = "dev"
	}
	return mcp.Deps{
		Telemetry:     tq,
		QueryService:  a.QueryService,
		Registry:      a.Registry,
		HealthService: a.HealthService,
		Version:       version,
		Tracer:        a.Tracer,
	}
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

	// Track whether any startup step failed so the span status reflects
	// real outcome rather than always claiming OK after RecordError.
	startupOK := true

	// Remove raster unpack directories orphaned by a previous crash before they
	// accumulate and exhaust disk.
	if a.RasterRepository != nil {
		if n, err := a.RasterRepository.CleanupOrphaned(); err != nil {
			a.Logger.Warn("failed to clean orphaned raster temp dirs", "error", err)
		} else if n > 0 {
			a.Logger.Info("removed orphaned raster temp dirs", "count", n)
		}
	}

	// Load all sources from storage
	if err := a.Registry.LoadAll(startupCtx); err != nil {
		a.Logger.Warn("failed to load sources", "error", err)
		startupSpan.RecordError(err)
		startupOK = false
	}
	startupSpan.SetAttributes(output.Int("ortus.sources.loaded", a.Registry.SourceCount()))

	// Start file watcher. Pass the parent ctx (NOT startupCtx) — the
	// watcher keeps the ctx for the life of the process and uses it as the
	// parent of every Watcher.handle span. Tying file-event spans to the
	// startup trace would (a) keep the startup trace eternally unfinalized
	// in the ring buffer and (b) misleadingly group hot-reload events
	// under the startup root. File events deserve their own trace roots.
	if a.Watcher != nil {
		if err := a.Watcher.Start(ctx); err != nil {
			a.Logger.Warn("failed to start file watcher", "error", err)
			startupSpan.RecordError(err)
			startupOK = false
		}
	}

	a.startBackgroundServers(ctx)

	if startupOK {
		startupSpan.SetStatus(output.StatusOK, "")
	} else {
		startupSpan.SetStatus(output.StatusError, "one or more startup steps failed")
	}
	startupSpan.End()

	// Start server (long-running — must run outside the startup span so it
	// doesn't keep the span open for the entire lifetime of the process).
	if a.Config.TLS.Enabled && a.TLSServer != nil {
		return a.TLSServer.ListenAndServe(a.Config.Server.Address())
	}
	return a.HTTPServer.Start()
}

// startBackgroundServers spins up the long-running goroutines (metrics
// scrape endpoint, sync ticker, MCP server). Each one has its own panic
// recovery so a runaway in one doesn't take the others down. Extracted
// from Start() for cognitive-complexity reasons — Start was at gocognit 26.
func (a *App) startBackgroundServers(ctx context.Context) {
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

	if a.SyncService != nil {
		a.SyncService.Start(ctx)
	}

	// MCP server has its own port + its own panic guard, so a runaway
	// MCP client can't take the main HTTP server with it.
	if a.MCPServer != nil {
		go func() {
			defer func() {
				if rec := recover(); rec != nil {
					a.Logger.Error("MCP server panic recovered", "panic", rec)
				}
			}()
			if err := a.MCPServer.Run(); err != nil {
				a.Logger.Error("MCP server error", "error", err)
			}
		}()
	}
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

	// Shutdown MCP server first — block new MCP requests before we tear
	// down the things they would access.
	if a.MCPServer != nil {
		if err := a.MCPServer.Shutdown(ctx); err != nil {
			a.Logger.Error("MCP server shutdown error", "error", err)
		}
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

	// Close all sources
	packages, _ := a.Registry.ListSources(ctx)
	for _, pkg := range packages {
		if err := a.Registry.UnloadSource(ctx, pkg.ID); err != nil {
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
		if err := a.Registry.LoadSource(ctx, event.Path); err != nil {
			span.RecordError(err)
			span.SetStatus(output.StatusError, "load failed")
			return err
		}
		return nil

	case watcher.OpDelete:
		// Unload the source by deriving its id from the file path. Use the
		// registry's derivation (not an adapter's) so it stays correct for any
		// source kind (.gpkg, .zip, …).
		sourceID := a.Registry.DeriveSourceID(event.Path)
		span.SetAttributes(output.String("ortus.source.id", sourceID))
		if err := a.Registry.UnloadSource(ctx, sourceID); err != nil {
			a.Logger.Warn("failed to unload deleted source", "id", sourceID, "error", err)
			span.RecordError(err)
			span.SetStatus(output.StatusError, "unload failed")
		}
		return nil
	}

	return nil
}

// buildStorage assembles the object-storage stack: the configured backend,
// error normalization (so all backends surface *domain.StorageError), and
// optional tracing. Error wrapping is innermost so tracing and every caller
// see the typed error.
func buildStorage(ctx context.Context, cfg *config.Config, tracer output.Tracer) (output.ObjectStorage, error) {
	store, err := initStorage(ctx, cfg.Storage)
	if err != nil {
		return nil, fmt.Errorf("initializing storage: %w", err)
	}
	store = storage.NewErrorWrappingStorage(store)
	if cfg.Tracing.Enabled {
		store = storage.NewTracedStorage(store, tracer, cfg.Storage.Type)
	}
	return store, nil
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
