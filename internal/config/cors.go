package config

import (
	"fmt"

	"github.com/jobrunner/ortus/internal/domain"
)

// CORSConfig holds CORS configuration: the browser origins allowed to call the
// API from another site. Empty (the default) means no CORS headers are sent at
// all, which is right when the only browser client is served same-origin.
type CORSConfig struct {
	// AllowedOrigins holds exact origins ("https://example.com") and wildcard
	// patterns ("https://*.example.com"). A wildcard covers the subdomains of a
	// host; scheme and port still have to match exactly, so it must be written
	// with a scheme. validateCORS enforces that at startup.
	AllowedOrigins []string `mapstructure:"allowed_origins"`
}

// Enabled returns true if CORS is configured with at least one allowed origin.
func (c *CORSConfig) Enabled() bool {
	return len(c.AllowedOrigins) > 0
}

// validateCORS rejects allow-list entries that are not well-formed origins.
//
// The rules live in domain.ParseOriginPattern, which is also what the HTTP
// adapter matches with — one definition, so a pattern accepted here cannot mean
// something else at runtime. Matching is strict about scheme and port, which
// means a pattern like "*.example.com" (no scheme) can never match; accepting it
// would leave CORS silently switched off, so it fails at startup instead.
func (c *Config) validateCORS() error {
	for _, origin := range c.Server.CORS.AllowedOrigins {
		if _, err := domain.ParseOriginPattern(origin); err != nil {
			return fmt.Errorf("server.cors.allowed_origins: %w", err)
		}
	}
	return nil
}
