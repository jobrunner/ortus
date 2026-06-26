// Package main provides the entry point for the Ortus GeoPackage service.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	mcpAdapter "github.com/jobrunner/ortus/internal/adapters/mcp"
	"github.com/jobrunner/ortus/internal/adapters/telemetry"
	"github.com/jobrunner/ortus/internal/app"

	"github.com/jobrunner/ortus/internal/config"
)

var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

var cfgFile string

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "ortus",
	Short: "Ortus - GeoPackage Point Query Service",
	Long: `Ortus is a high-performance GeoPackage point query service.

It provides a REST API for querying geographic features from GeoPackage files
using point coordinates with automatic coordinate transformation support.

Features:
  - Point queries with ST_Contains
  - Automatic spatial indexing
  - Coordinate transformation (SRID support)
  - Multiple storage backends (local, AWS S3, Azure, HTTP)
  - Hot-reload of GeoPackages
  - TLS with automatic certificate management
  - Prometheus metrics`,
	RunE: runServer,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Printf("Ortus %s\n", version)
		fmt.Printf("  Commit:     %s\n", commit)
		fmt.Printf("  Build Date: %s\n", buildDate)
	},
}

// mcpCmd starts ortus in MCP-stdio mode: same tool surface as the HTTP
// MCP endpoint, but over stdin/stdout. Use this from Claude Desktop's
// mcpServers config to talk to a local instance without exposing an HTTP
// port. Storage + tracing are initialized exactly as in serve mode so
// the agent gets the same view.
var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Run ortus as a stdio MCP server (for Claude Desktop / IDE integration)",
	RunE:  runMCPStdio,
}

func init() {
	cobra.OnInitialize(initConfig)

	// Global flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: ./config.yaml)")
	rootCmd.PersistentFlags().String("log-level", "info", "log level (debug, info, warn, error)")
	rootCmd.PersistentFlags().String("log-format", "json", "log format (json, text)")

	// Server flags
	rootCmd.Flags().String("host", "0.0.0.0", "server host")
	rootCmd.Flags().Int("port", 8080, "server port")
	rootCmd.Flags().Bool("tls", false, "enable TLS")
	rootCmd.Flags().StringSlice("tls-domains", nil, "TLS domains")
	rootCmd.Flags().String("tls-email", "", "TLS email for Let's Encrypt")

	// Storage flags
	rootCmd.Flags().String("storage-type", "local", "storage type (local, s3, azure, http)")
	rootCmd.Flags().String("storage-path", "./data", "local storage path")

	// CORS flags
	rootCmd.Flags().StringSlice("cors", nil, "allowed CORS origins (e.g., https://example.com,*.sub.domain.tld)")

	// Query flags
	rootCmd.Flags().Bool("with-geometry", false, "include geometry in query results")

	// Frontend flags
	rootCmd.Flags().Bool("disable-frontend", false, "disable web frontend at /")

	// Tracing flags
	rootCmd.Flags().Bool("tracing", false, "enable OpenTelemetry tracing")
	rootCmd.Flags().String("tracing-endpoint", "", "OTLP collector endpoint (host:port — used as-is by both http and grpc transports)")
	rootCmd.Flags().String("tracing-transport", "http", "OTLP transport (http|grpc)")
	rootCmd.Flags().Float64("tracing-sample-ratio", 1.0, "tracing sample ratio (0.0..1.0)")

	// Bind flags to viper
	_ = viper.BindPFlag("logging.level", rootCmd.PersistentFlags().Lookup("log-level"))
	_ = viper.BindPFlag("logging.format", rootCmd.PersistentFlags().Lookup("log-format"))
	_ = viper.BindPFlag("server.host", rootCmd.Flags().Lookup("host"))
	_ = viper.BindPFlag("server.port", rootCmd.Flags().Lookup("port"))
	_ = viper.BindPFlag("tls.enabled", rootCmd.Flags().Lookup("tls"))
	_ = viper.BindPFlag("tls.domains", rootCmd.Flags().Lookup("tls-domains"))
	_ = viper.BindPFlag("tls.email", rootCmd.Flags().Lookup("tls-email"))
	_ = viper.BindPFlag("storage.type", rootCmd.Flags().Lookup("storage-type"))
	_ = viper.BindPFlag("storage.local_path", rootCmd.Flags().Lookup("storage-path"))
	_ = viper.BindPFlag("server.cors.allowed_origins", rootCmd.Flags().Lookup("cors"))
	_ = viper.BindPFlag("query.with_geometry", rootCmd.Flags().Lookup("with-geometry"))
	_ = viper.BindPFlag("tracing.enabled", rootCmd.Flags().Lookup("tracing"))
	_ = viper.BindPFlag("tracing.endpoint", rootCmd.Flags().Lookup("tracing-endpoint"))
	_ = viper.BindPFlag("tracing.transport", rootCmd.Flags().Lookup("tracing-transport"))
	_ = viper.BindPFlag("tracing.sample_ratio", rootCmd.Flags().Lookup("tracing-sample-ratio"))
	// Note: --disable-frontend uses inverted logic and is handled in runServer().
	// The env var ORTUS_SERVER_FRONTEND_ENABLED works via viper's AutomaticEnv()
	// binding to server.frontend_enabled (set in config.Defaults()).

	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(mcpCmd)
}

func initConfig() {
	config.Defaults()

	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	}
}

func runServer(cmd *cobra.Command, _ []string) error {
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	cfg.Build = config.BuildInfo{Version: version, Commit: commit, BuildDate: buildDate}

	// Handle --disable-frontend flag (inverts frontend_enabled)
	if disableFrontend, _ := cmd.Flags().GetBool("disable-frontend"); disableFrontend {
		cfg.Server.FrontendEnabled = false
	}

	// Setup logger
	logger := setupLogger(cfg.Logging)
	slog.SetDefault(logger)

	logger.Info("starting Ortus",
		"version", version,
		"host", cfg.Server.Host,
		"port", cfg.Server.Port,
		"storage_type", cfg.Storage.Type,
	)

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Initialize application
	application, err := app.New(ctx, cfg, logger)
	if err != nil {
		return fmt.Errorf("initializing application: %w", err)
	}

	// Start server in background
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("server listening", "address", cfg.Server.Address())
		if err := application.Start(ctx); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	// Wait for shutdown signal or server error
	select {
	case sig := <-sigChan:
		logger.Info("received shutdown signal", "signal", sig)
	case err := <-serverErr:
		logger.Error("server error", "error", err)
		cancel()
	case <-ctx.Done():
	}

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer shutdownCancel()

	logger.Info("shutting down server")
	if err := application.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown error", "error", err)
		return err
	}

	logger.Info("server stopped")
	return nil
}

// setupLogger constructs the default stdout-writing logger used by
// `serve` mode. The handler is wrapped with a span-context injector so
// any slog.*Context call carrying a traced ctx auto-includes
// trace_id/span_id (cheap no-op when ctx has no span).
func setupLogger(cfg config.LoggingConfig) *slog.Logger {
	return slog.New(telemetry.NewSpanContextHandler(buildHandler(cfg, os.Stdout)))
}

// runMCPStdio boots the same application stack as `serve` (storage,
// repository, registry, tracing, …) but speaks MCP over stdin/stdout
// instead of starting any HTTP listener. This is the mode Claude Desktop
// will spawn ortus in. Anything we log goes to stderr — stdout is
// reserved for the JSON-RPC protocol.
func runMCPStdio(_ *cobra.Command, _ []string) error {
	// Stdio mode does not start the MCP HTTP listener, so the HTTP-only
	// validation (host/port/token) shouldn't fail it. Force-disable MCP
	// before Load via env var — env beats config file in Viper's
	// precedence chain, so even a config with `mcp.enabled: true` and
	// no token still loads cleanly here.
	_ = os.Setenv("ORTUS_MCP_ENABLED", "false")

	cfg, err := config.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	cfg.Build = config.BuildInfo{Version: version, Commit: commit, BuildDate: buildDate}

	// Force log output to stderr — stdout belongs to MCP.
	logger := setupStderrLogger(cfg.Logging)
	slog.SetDefault(logger)

	// Disable the HTTP listeners regardless of config; they would race
	// for stdio's purposes and break the protocol stream.
	cfg.Server.FrontendEnabled = false
	cfg.MCP.Enabled = false // we run MCP ourselves in stdio mode

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	application, err := app.New(ctx, cfg, logger)
	if err != nil {
		return fmt.Errorf("initializing application: %w", err)
	}
	// Graceful shutdown on exit (incl. SIGINT/SIGTERM). Without this, the
	// tracing BatchSpanProcessor and metrics PeriodicReader wouldn't flush
	// their final batch and SQLite connections would leak.
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
		defer shutdownCancel()
		if err := application.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown error", "error", err)
		}
	}()

	// Load packages so query tools see real data. We deliberately do NOT
	// call application.Start() — that would also start the HTTP server.
	if err := application.Registry.LoadAll(ctx); err != nil {
		logger.Warn("LoadAll failed during MCP startup", "error", err)
	}

	deps := application.MCPDeps()

	// Cancel on signal — RunStdio blocks on stdin.
	go func() {
		<-sigChan
		cancel()
	}()

	logger.Info("MCP stdio mode active")
	if err := mcpAdapter.RunStdio(ctx, deps, logger); err != nil {
		return fmt.Errorf("mcp stdio: %w", err)
	}
	return nil
}

// setupStderrLogger mirrors setupLogger exactly — same level, same
// UTC RFC3339 timestamp formatting via ReplaceAttr — but writes to
// stderr. Used by stdio-mode (`./ortus mcp`) where stdout belongs to
// the JSON-RPC protocol.
func setupStderrLogger(cfg config.LoggingConfig) *slog.Logger {
	return slog.New(telemetry.NewSpanContextHandler(buildHandler(cfg, os.Stderr)))
}

// buildHandler centralizes the slog.Handler construction shared by
// setupLogger (stdout) and setupStderrLogger (stderr) so they never
// drift on level parsing or timestamp formatting.
func buildHandler(cfg config.LoggingConfig, w io.Writer) slog.Handler {
	var level slog.Level
	switch cfg.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				a.Value = slog.StringValue(time.Now().UTC().Format(time.RFC3339))
			}
			return a
		},
	}
	if cfg.Format == "text" {
		return slog.NewTextHandler(w, opts)
	}
	return slog.NewJSONHandler(w, opts)
}
