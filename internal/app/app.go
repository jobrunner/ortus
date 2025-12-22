// Package app provides application initialization and wiring.
package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jobrunner/ortus/internal/adapters/geopackage"
	httpAdapter "github.com/jobrunner/ortus/internal/adapters/http"
	"github.com/jobrunner/ortus/internal/adapters/metrics"
	"github.com/jobrunner/ortus/internal/adapters/storage"
	tlsAdapter "github.com/jobrunner/ortus/internal/adapters/tls"
	"github.com/jobrunner/ortus/internal/adapters/watcher"
	"github.com/jobrunner/ortus/internal/application"
	"github.com/jobrunner/ortus/internal/config"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// App holds all application components.
type App struct {
	Config        *config.Config
	Logger        *slog.Logger
	Storage       output.ObjectStorage
	Repository    *geopackage.Repository
	Registry      *application.PackageRegistry
	QueryService  *application.QueryService
	HealthService *application.HealthService
	HTTPServer    *httpAdapter.Server
	TLSServer     *tlsAdapter.Server
	Watcher       *watcher.Watcher
	Metrics       *metrics.Collector
	MetricsServer *metrics.Server
}

// New creates and initializes a new application.
func New(ctx context.Context, cfg *config.Config, logger *slog.Logger) (*App, error) {
	app := &App{
		Config: cfg,
		Logger: logger,
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
	app.Storage = store

	// Initialize GeoPackage repository
	app.Repository = geopackage.NewRepository()

	// Initialize package registry
	app.Registry = application.NewPackageRegistry(
		app.Repository,
		app.Storage,
		metricsCollector,
		logger,
		cfg.Storage.LocalPath,
	)

	// Initialize coordinate transformer
	transformer := geopackage.NewRepositoryTransformer(app.Repository)

	// Initialize query service
	app.QueryService = application.NewQueryService(
		app.Registry,
		app.Repository,
		transformer,
		metricsCollector,
		logger,
		application.QueryServiceConfig{
			DefaultSRID: cfg.Query.DefaultSRID,
			MaxFeatures: cfg.Query.MaxFeatures,
		},
	)

	// Initialize health service
	app.HealthService = application.NewHealthService(app.Registry)

	// Initialize HTTP server
	app.HTTPServer = httpAdapter.NewServer(
		cfg.Server,
		app.QueryService,
		app.Registry,
		app.HealthService,
		logger,
		cfg.Query.WithGeometry,
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
	if cfg.Storage.Type == "local" {
		w, err := watcher.New(
			watcher.Config{
				Paths: []string{cfg.Storage.LocalPath},
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
	// Load all packages from storage
	if err := a.Registry.LoadAll(ctx); err != nil {
		a.Logger.Warn("failed to load packages", "error", err)
	}

	// Start file watcher
	if a.Watcher != nil {
		if err := a.Watcher.Start(ctx); err != nil {
			a.Logger.Warn("failed to start file watcher", "error", err)
		}
	}

	// Start metrics server in background
	if a.MetricsServer != nil {
		go func() {
			if err := a.MetricsServer.Start(); err != nil && err.Error() != "http: Server closed" {
				a.Logger.Error("metrics server error", "error", err)
			}
		}()
	}

	// Start server
	if a.Config.TLS.Enabled && a.TLSServer != nil {
		return a.TLSServer.ListenAndServe(a.Config.Server.Address())
	}
	return a.HTTPServer.Start()
}

// Shutdown gracefully shuts down all components.
func (a *App) Shutdown(ctx context.Context) error {
	a.Logger.Info("shutting down application")

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

	return nil
}

// handleFileEvent handles file system events for hot-reload.
func (a *App) handleFileEvent(ctx context.Context, event watcher.Event) error {
	a.Logger.Info("file event", "path", event.Path, "operation", event.Operation.String())

	switch event.Operation {
	case watcher.OpCreate, watcher.OpModify:
		// Reload the package
		return a.Registry.LoadPackage(ctx, event.Path)

	case watcher.OpDelete:
		// Unload the package by deriving the package ID from the file path
		packageID := geopackage.DerivePackageID(event.Path)
		if err := a.Registry.UnloadPackage(ctx, packageID); err != nil {
			a.Logger.Warn("failed to unload deleted package", "id", packageID, "error", err)
		}
		return nil
	}

	return nil
}

// initStorage initializes the appropriate storage adapter.
func initStorage(ctx context.Context, cfg config.StorageConfig) (output.ObjectStorage, error) {
	switch cfg.Type {
	case "local":
		return storage.NewLocalStorage(cfg.LocalPath), nil

	case "s3":
		return storage.NewS3Storage(ctx, storage.S3Config{
			Bucket:          cfg.S3.Bucket,
			Region:          cfg.S3.Region,
			Prefix:          cfg.S3.Prefix,
			Endpoint:        cfg.S3.Endpoint,
			AccessKeyID:     cfg.S3.AccessKeyID,
			SecretAccessKey: cfg.S3.SecretAccessKey,
		})

	case "azure":
		return storage.NewAzureStorage(storage.AzureConfig{
			Container:        cfg.Azure.Container,
			AccountName:      cfg.Azure.AccountName,
			AccountKey:       cfg.Azure.AccountKey,
			ConnectionString: cfg.Azure.ConnectionString,
			Prefix:           cfg.Azure.Prefix,
		})

	case "http":
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
