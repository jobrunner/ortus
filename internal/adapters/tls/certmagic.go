// Package tls provides TLS configuration using CertMagic.
package tls

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/libdns/azure"
)

// Config holds TLS configuration.
type Config struct {
	Enabled  bool
	Domains  []string
	Email    string
	CacheDir string
	Staging  bool // Use Let's Encrypt staging environment
	DNS      DNSConfig
}

// DNSConfig holds Azure DNS provider configuration for DNS-01 challenges.
type DNSConfig struct {
	SubscriptionID    string
	ResourceGroupName string
	ClientID          string // User Assigned Managed Identity client ID (optional)
}

// Server wraps an HTTP server with automatic TLS.
type Server struct {
	config    Config
	handler   http.Handler
	logger    *slog.Logger
	tlsConfig *tls.Config
}

// NewServer creates a new TLS-enabled server.
func NewServer(cfg Config, handler http.Handler, logger *slog.Logger) (*Server, error) {
	if !cfg.Enabled {
		return &Server{
			config:  cfg,
			handler: handler,
			logger:  logger,
		}, nil
	}

	if len(cfg.Domains) == 0 {
		return nil, fmt.Errorf("TLS enabled but no domains specified")
	}

	if cfg.Email == "" {
		return nil, fmt.Errorf("TLS enabled but no email specified")
	}

	// Configure CertMagic
	certmagic.DefaultACME.Agreed = true
	certmagic.DefaultACME.Email = cfg.Email

	if cfg.Staging {
		certmagic.DefaultACME.CA = certmagic.LetsEncryptStagingCA
	}

	if cfg.CacheDir != "" {
		certmagic.Default.Storage = &certmagic.FileStorage{Path: cfg.CacheDir}
	}

	// Configure DNS-01 challenge solver with Azure DNS
	provider := &azure.Provider{
		SubscriptionId:    cfg.DNS.SubscriptionID,
		ResourceGroupName: cfg.DNS.ResourceGroupName,
		ClientId:          cfg.DNS.ClientID, // Empty = System Assigned Managed Identity
	}
	certmagic.DefaultACME.DNS01Solver = &certmagic.DNS01Solver{
		DNSManager: certmagic.DNSManager{
			DNSProvider: provider,
		},
	}

	// Get TLS config
	tlsConfig, err := certmagic.TLS(cfg.Domains)
	if err != nil {
		return nil, fmt.Errorf("configuring TLS: %w", err)
	}

	return &Server{
		config:    cfg,
		handler:   handler,
		logger:    logger,
		tlsConfig: tlsConfig,
	}, nil
}

// ListenAndServe starts the server with TLS if enabled.
func (s *Server) ListenAndServe(addr string) error {
	if !s.config.Enabled {
		s.logger.Info("starting HTTP server (TLS disabled)", "address", addr)
		server := &http.Server{
			Addr:              addr,
			Handler:           s.handler,
			ReadHeaderTimeout: 10 * time.Second,
		}
		return server.ListenAndServe()
	}

	s.logger.Info("starting HTTPS server with DNS-01 challenge",
		"address", addr,
		"domains", s.config.Domains,
	)

	server := &http.Server{
		Addr:              addr,
		Handler:           s.handler,
		TLSConfig:         s.tlsConfig,
		ReadHeaderTimeout: 10 * time.Second,
	}

	return server.ListenAndServeTLS("", "")
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(_ context.Context) error {
	// CertMagic handles its own cleanup
	return nil
}

// TLSConfig returns the TLS configuration.
func (s *Server) TLSConfig() *tls.Config {
	return s.tlsConfig
}

// ManageCertificates pre-obtains certificates for the configured domains.
func (s *Server) ManageCertificates(ctx context.Context) error {
	if !s.config.Enabled {
		return nil
	}

	s.logger.Info("obtaining certificates", "domains", s.config.Domains)

	err := certmagic.ManageSync(ctx, s.config.Domains)
	if err != nil {
		return fmt.Errorf("managing certificates: %w", err)
	}

	s.logger.Info("certificates obtained successfully")
	return nil
}
