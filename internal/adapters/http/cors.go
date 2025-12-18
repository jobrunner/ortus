package http //nolint:revive // package name conflicts with stdlib but is acceptable in this context

import (
	"net/http"
	"strings"
)

// corsMiddleware handles CORS headers based on configuration.
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		// Check if origin is allowed
		if origin != "" && s.isOriginAllowed(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Authorization")
			w.Header().Set("Access-Control-Max-Age", "86400") // 24 hours
			w.Header().Set("Vary", "Origin")
		}

		// Handle preflight requests
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// isOriginAllowed checks if the given origin matches any allowed pattern.
func (s *Server) isOriginAllowed(origin string) bool {
	for _, pattern := range s.config.CORS.AllowedOrigins {
		if matchOrigin(origin, pattern) {
			return true
		}
	}
	return false
}

// matchOrigin checks if an origin matches a pattern.
// Supports exact matches and wildcard patterns like "*.example.com".
func matchOrigin(origin, pattern string) bool {
	// Exact match
	if origin == pattern {
		return true
	}

	// Wildcard match (e.g., "*.example.com")
	if strings.HasPrefix(pattern, "*.") {
		// Extract the domain suffix from pattern (e.g., ".example.com")
		suffix := pattern[1:] // Remove the "*" to get ".example.com"

		// Parse origin to get just the host
		originHost := extractHost(origin)

		// Check if the origin host ends with the suffix
		// For "*.example.com", we match "sub.example.com" but not "example.com"
		if strings.HasSuffix(originHost, suffix) && len(originHost) > len(suffix) {
			return true
		}
	}

	return false
}

// extractHost extracts the host from an origin URL.
// Example: "https://example.com:8080" returns "example.com".
func extractHost(origin string) string {
	// Remove protocol
	host := origin
	if idx := strings.Index(host, "://"); idx != -1 {
		host = host[idx+3:]
	}

	// Remove port
	if idx := strings.Index(host, ":"); idx != -1 {
		host = host[:idx]
	}

	// Remove path
	if idx := strings.Index(host, "/"); idx != -1 {
		host = host[:idx]
	}

	return host
}
