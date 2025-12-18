// Package tls provides TLS configuration using CertMagic.
package tls

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/caddyserver/certmagic"
)

// Config holds TLS configuration.
type Config struct {
	Enabled  bool
	Domains  []string
	Email    string
	CacheDir string
	Staging  bool // Use Let's Encrypt staging environment
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
		return http.ListenAndServe(addr, s.handler)
	}

	s.logger.Info("starting HTTPS server",
		"address", addr,
		"domains", s.config.Domains,
	)

	server := &http.Server{
		Addr:      addr,
		Handler:   s.handler,
		TLSConfig: s.tlsConfig,
	}

	// Start HTTP-01 challenge handler on port 80
	go func() {
		s.logger.Info("starting HTTP-01 challenge handler on :80")
		if err := http.ListenAndServe(":80", certmagic.DefaultACME.HTTPChallengeHandler(http.HandlerFunc(redirectHTTPS))); err != nil {
			s.logger.Error("HTTP-01 handler error", "error", err)
		}
	}()

	return server.ListenAndServeTLS("", "")
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	// CertMagic handles its own cleanup
	return nil
}

// TLSConfig returns the TLS configuration.
func (s *Server) TLSConfig() *tls.Config {
	return s.tlsConfig
}

// redirectHTTPS redirects HTTP requests to HTTPS.
func redirectHTTPS(w http.ResponseWriter, r *http.Request) {
	target := "https://" + r.Host + r.URL.RequestURI()
	http.Redirect(w, r, target, http.StatusPermanentRedirect)
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
