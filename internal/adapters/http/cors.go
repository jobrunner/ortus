package http

import (
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

// corsMaxAgeSeconds is how long a browser may cache a preflight result.
const corsMaxAgeSeconds = "86400" // 24 hours

// corsMiddleware answers CORS preflights and decorates cross-origin responses.
//
// It wraps the router (see NewServer) and is deliberately NOT registered with
// r.Use: gorilla/mux runs Use-middleware only for requests that MATCH a route,
// and a preflight is `OPTIONS <path>` while our routes are registered GET/POST.
// As an r.Use middleware it therefore never ran for a preflight, and the
// browser got a bare 404 with no Access-Control-* headers — which is what made
// POST /api/v1/query/batch unusable cross-origin. The same mux property is why
// the metrics middleware misses 404s (see the note in setupRoutes).
//
// Only "non-simple" requests preflight, which is why this stayed invisible: the
// embedded frontend is same-origin, and a cross-origin GET needs no preflight.
// What broke was exactly POST with Content-Type: application/json, and any
// request carrying an Authorization header.
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			// Not a cross-origin request — leave the response untouched.
			next.ServeHTTP(w, r)
			return
		}

		requestedMethod := r.Header.Get("Access-Control-Request-Method")
		isPreflight := r.Method == http.MethodOptions && requestedMethod != ""

		// Vary goes on every cross-origin response, allowed or not: the response
		// depends on these headers, so a shared cache must not serve one origin's
		// response to another.
		w.Header().Add("Vary", "Origin")
		if isPreflight {
			w.Header().Add("Vary", "Access-Control-Request-Method")
			w.Header().Add("Vary", "Access-Control-Request-Headers")
		}

		if s.isOriginAllowed(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			if isPreflight {
				// Advertise the method the ROUTER accepts for this path rather
				// than a hand-written list. The previous literal "GET, OPTIONS"
				// was correct until /query/batch and /sync became POST routes,
				// and nothing failed at build time when they did.
				if s.routeAllowsMethod(r, requestedMethod) {
					w.Header().Set("Access-Control-Allow-Methods", requestedMethod+", "+http.MethodOptions)
				}
				w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Authorization")
				w.Header().Set("Access-Control-Max-Age", corsMaxAgeSeconds)
			}
		}

		// A preflight is answered here and never reaches a handler. It is
		// answered uniformly even for a disallowed origin: without the
		// Allow-Origin header set above the browser rejects it anyway, and a
		// uniform answer avoids leaking which origins are configured.
		//
		// A bare OPTIONS (no Access-Control-Request-Method) is not a preflight
		// and keeps falling through to the router, so enabling CORS does not
		// silently turn every OPTIONS into a 204.
		if isPreflight {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// routeAllowsMethod asks the router whether method+path would match a route, so
// the preflight answer is derived from the route table and cannot drift from it.
// mux signals "path exists, method doesn't" via RouteMatch.MatchErr, which is
// exactly the case that must not be advertised as allowed.
func (s *Server) routeAllowsMethod(r *http.Request, method string) bool {
	probe := r.Clone(r.Context())
	probe.Method = method

	var match mux.RouteMatch
	return s.router.Match(probe, &match) && match.MatchErr == nil
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
