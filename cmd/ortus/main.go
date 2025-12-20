// Package main provides the entry point for the Ortus GeoPackage service.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

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

	rootCmd.AddCommand(versionCmd)
}

func initConfig() {
	config.Defaults()

	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	}
}

func runServer(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
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

func setupLogger(cfg config.LoggingConfig) *slog.Logger {
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

	var handler slog.Handler
	if cfg.Format == "text" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	return slog.New(handler)
}
