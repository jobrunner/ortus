package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jobrunner/ortus/internal/config"
)

func TestSplitOrigin(t *testing.T) {
	tests := []struct {
		name       string
		origin     string
		wantScheme string
		wantHost   string
		wantPort   string
	}{
		{
			name:       "simple https URL",
			origin:     "https://example.com",
			wantScheme: "https",
			wantHost:   "example.com",
			wantPort:   "",
		},
		{
			name:       "https URL with port",
			origin:     "https://example.com:8080",
			wantScheme: "https",
			wantHost:   "example.com",
			wantPort:   "8080",
		},
		{
			name:       "http URL",
			origin:     "http://example.com",
			wantScheme: "http",
			wantHost:   "example.com",
			wantPort:   "",
		},
		{
			name:       "URL with path",
			origin:     "https://example.com/path/to/resource",
			wantScheme: "https",
			wantHost:   "example.com",
			wantPort:   "",
		},
		{
			name:       "URL with port and path",
			origin:     "https://example.com:443/path",
			wantScheme: "https",
			wantHost:   "example.com",
			wantPort:   "443",
		},
		{
			name:       "subdomain",
			origin:     "https://sub.example.com",
			wantScheme: "https",
			wantHost:   "sub.example.com",
			wantPort:   "",
		},
		{
			name:       "deep subdomain",
			origin:     "https://deep.sub.example.com",
			wantScheme: "https",
			wantHost:   "deep.sub.example.com",
			wantPort:   "",
		},
		{
			name:       "localhost",
			origin:     "http://localhost:3000",
			wantScheme: "http",
			wantHost:   "localhost",
			wantPort:   "3000",
		},
		{
			name:       "IP address",
			origin:     "http://192.168.1.1:8080",
			wantScheme: "http",
			wantHost:   "192.168.1.1",
			wantPort:   "8080",
		},
		{
			name:       "no protocol",
			origin:     "example.com",
			wantScheme: "",
			wantHost:   "example.com",
			wantPort:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme, host, port := splitOrigin(tt.origin)
			if scheme != tt.wantScheme || host != tt.wantHost || port != tt.wantPort {
				t.Errorf("splitOrigin(%q) = (%q, %q, %q); want (%q, %q, %q)",
					tt.origin, scheme, host, port, tt.wantScheme, tt.wantHost, tt.wantPort)
			}
		})
	}
}

func TestMatchOrigin(t *testing.T) {
	tests := []struct {
		name     string
		origin   string
		pattern  string
		expected bool
	}{
		// Exact matches
		{
			name:     "exact match https",
			origin:   "https://example.com",
			pattern:  "https://example.com",
			expected: true,
		},
		{
			name:     "exact match with port",
			origin:   "https://example.com:8080",
			pattern:  "https://example.com:8080",
			expected: true,
		},
		{
			name:     "exact match fails - different protocol",
			origin:   "http://example.com",
			pattern:  "https://example.com",
			expected: false,
		},
		{
			name:     "exact match fails - different domain",
			origin:   "https://other.com",
			pattern:  "https://example.com",
			expected: false,
		},
		{
			name:     "exact match fails - different port",
			origin:   "https://example.com:8080",
			pattern:  "https://example.com:9090",
			expected: false,
		},

		// Wildcard matches. An origin is a scheme/host/port triple; only the
		// host label is wildcarded, so scheme and port must still match exactly.
		{
			name:     "wildcard matches subdomain",
			origin:   "https://sub.example.com",
			pattern:  "https://*.example.com",
			expected: true,
		},
		{
			name:     "wildcard matches deep subdomain",
			origin:   "https://deep.sub.example.com",
			pattern:  "https://*.example.com",
			expected: true,
		},
		{
			name:     "wildcard does not match root domain",
			origin:   "https://example.com",
			pattern:  "https://*.example.com",
			expected: false,
		},
		{
			name:     "wildcard does not match different domain",
			origin:   "https://sub.other.com",
			pattern:  "https://*.example.com",
			expected: false,
		},
		{
			name:     "wildcard with subdomain pattern",
			origin:   "https://app.sub.domain.tld",
			pattern:  "https://*.sub.domain.tld",
			expected: true,
		},
		{
			name:     "wildcard does not match partial",
			origin:   "https://notexample.com",
			pattern:  "https://*.example.com",
			expected: false,
		},

		// Wildcards must not widen the scheme: admitting http where the
		// operator wrote https hands responses to a plaintext origin.
		{
			name:     "wildcard does not widen https to http",
			origin:   "http://sub.example.com",
			pattern:  "https://*.example.com",
			expected: false,
		},
		{
			name:     "wildcard does not widen http to https",
			origin:   "https://sub.example.com",
			pattern:  "http://*.example.com",
			expected: false,
		},

		// …nor the port: a different port is a different service on the same host.
		{
			name:     "wildcard does not widen to another port",
			origin:   "https://sub.example.com:8443",
			pattern:  "https://*.example.com",
			expected: false,
		},
		{
			name:     "wildcard with port does not match the default port",
			origin:   "https://sub.example.com",
			pattern:  "https://*.example.com:8443",
			expected: false,
		},
		{
			name:     "wildcard with port matches that port",
			origin:   "http://sub.localhost:3000",
			pattern:  "http://*.localhost:3000",
			expected: true,
		},

		// A scheme-less wildcard is rejected at config validation; if one ever
		// reaches the matcher it must not match a real (schemed) origin.
		{
			name:     "scheme-less wildcard does not match a schemed origin",
			origin:   "https://sub.example.com",
			pattern:  "*.example.com",
			expected: false,
		},

		// Edge cases
		{
			name:     "empty origin",
			origin:   "",
			pattern:  "https://example.com",
			expected: false,
		},
		{
			name:     "empty pattern",
			origin:   "https://example.com",
			pattern:  "",
			expected: false,
		},
		{
			name:     "localhost exact match",
			origin:   "http://localhost:3000",
			pattern:  "http://localhost:3000",
			expected: true,
		},
		{
			name:     "wildcard localhost",
			origin:   "http://sub.localhost",
			pattern:  "http://*.localhost",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchOrigin(tt.origin, tt.pattern)
			if result != tt.expected {
				t.Errorf("matchOrigin(%q, %q) = %v; want %v",
					tt.origin, tt.pattern, result, tt.expected)
			}
		})
	}
}

func TestServer_isOriginAllowed(t *testing.T) {
	tests := []struct {
		name           string
		allowedOrigins []string
		origin         string
		expected       bool
	}{
		{
			name:           "allowed - exact match",
			allowedOrigins: []string{"https://example.com"},
			origin:         "https://example.com",
			expected:       true,
		},
		{
			name:           "allowed - one of multiple",
			allowedOrigins: []string{"https://first.com", "https://second.com", "https://third.com"},
			origin:         "https://second.com",
			expected:       true,
		},
		{
			name:           "allowed - wildcard match",
			allowedOrigins: []string{"https://*.example.com"},
			origin:         "https://app.example.com",
			expected:       true,
		},
		{
			name:           "allowed - mixed patterns",
			allowedOrigins: []string{"https://exact.com", "https://*.wildcard.com"},
			origin:         "https://sub.wildcard.com",
			expected:       true,
		},
		{
			name:           "not allowed - no match",
			allowedOrigins: []string{"https://example.com"},
			origin:         "https://other.com",
			expected:       false,
		},
		{
			name:           "not allowed - empty list",
			allowedOrigins: []string{},
			origin:         "https://example.com",
			expected:       false,
		},
		{
			name:           "not allowed - nil list",
			allowedOrigins: nil,
			origin:         "https://example.com",
			expected:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{
				config: config.ServerConfig{
					CORS: config.CORSConfig{
						AllowedOrigins: tt.allowedOrigins,
					},
				},
			}

			result := s.isOriginAllowed(tt.origin)
			if result != tt.expected {
				t.Errorf("isOriginAllowed(%q) with origins %v = %v; want %v",
					tt.origin, tt.allowedOrigins, result, tt.expected)
			}
		})
	}
}

// corsTestCase defines a test case for origin matching on a simple (non-preflight)
// cross-origin request. Preflight behavior is pinned in cors_preflight_test.go.
type corsTestCase struct {
	name                string
	allowedOrigins      []string
	requestOrigin       string
	expectCORSHeaders   bool
	expectAllowedOrigin string
}

// runCORSTest executes a single CORS test case against the handler the server
// really serves, so the middleware is exercised in its actual position around
// the router rather than around a stub.
func runCORSTest(t *testing.T, tt *corsTestCase) {
	t.Helper()

	srv := newCORSServer(t, tt.allowedOrigins...)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sources", nil)
	if tt.requestOrigin != "" {
		req.Header.Set("Origin", tt.requestOrigin)
	}

	rr := httptest.NewRecorder()
	served(t, srv).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status code = %d; want %d", rr.Code, http.StatusOK)
	}

	allowOrigin := rr.Header().Get("Access-Control-Allow-Origin")
	switch {
	case tt.expectCORSHeaders:
		if allowOrigin != tt.expectAllowedOrigin {
			t.Errorf("Access-Control-Allow-Origin = %q; want %q", allowOrigin, tt.expectAllowedOrigin)
		}
		// A simple request carries Allow-Origin and Vary. Allow-Methods,
		// Allow-Headers and Max-Age are preflight-only per the CORS spec and
		// are deliberately absent here.
		if vary := rr.Header().Get("Vary"); vary != "Origin" {
			t.Errorf("Vary = %q; want %q", vary, "Origin")
		}
		for _, h := range []string{"Access-Control-Allow-Methods", "Access-Control-Allow-Headers", "Access-Control-Max-Age"} {
			if got := rr.Header().Get(h); got != "" {
				t.Errorf("%s = %q on a simple request; want empty (preflight-only header)", h, got)
			}
		}
	case allowOrigin != "":
		t.Errorf("expected no CORS headers, but got Access-Control-Allow-Origin = %q", allowOrigin)
	}
}

func TestCORSMiddleware(t *testing.T) {
	tests := []corsTestCase{
		{
			name:                "allowed origin - GET request",
			allowedOrigins:      []string{"https://example.com"},
			requestOrigin:       "https://example.com",
			expectCORSHeaders:   true,
			expectAllowedOrigin: "https://example.com",
		},
		{
			name:                "allowed wildcard origin",
			allowedOrigins:      []string{"https://*.example.com"},
			requestOrigin:       "https://app.example.com",
			expectCORSHeaders:   true,
			expectAllowedOrigin: "https://app.example.com",
		},
		{
			name:              "not allowed origin - no CORS headers",
			allowedOrigins:    []string{"https://example.com"},
			requestOrigin:     "https://evil.com",
			expectCORSHeaders: false,
		},
		{
			name:              "no origin header - no CORS headers",
			allowedOrigins:    []string{"https://example.com"},
			requestOrigin:     "",
			expectCORSHeaders: false,
		},
		{
			name:              "empty allowed origins - no CORS headers",
			allowedOrigins:    []string{},
			requestOrigin:     "https://example.com",
			expectCORSHeaders: false,
		},
	}

	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			runCORSTest(t, tt)
		})
	}
}

func TestCORSMiddleware_PreflightDoesNotReachTheHandler(t *testing.T) {
	srv := newCORSServer(t, "https://example.com")

	// /api/v1/sources would answer 200 with a JSON body; a preflight must be
	// short-circuited before it gets there.
	rec := preflight(t, srv, http.MethodGet, "/api/v1/sources", "https://example.com")

	if rec.Code != http.StatusNoContent {
		t.Errorf("status code = %d; want %d", rec.Code, http.StatusNoContent)
	}
	if body := rec.Body.String(); body != "" {
		t.Errorf("preflight response body = %q; want empty (handler must not run)", body)
	}
}

func TestCORSConfig_Enabled(t *testing.T) {
	tests := []struct {
		name           string
		allowedOrigins []string
		expected       bool
	}{
		{
			name:           "enabled with single origin",
			allowedOrigins: []string{"https://example.com"},
			expected:       true,
		},
		{
			name:           "enabled with multiple origins",
			allowedOrigins: []string{"https://example.com", "https://*.other.com"},
			expected:       true,
		},
		{
			name:           "disabled with empty slice",
			allowedOrigins: []string{},
			expected:       false,
		},
		{
			name:           "disabled with nil",
			allowedOrigins: nil,
			expected:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.CORSConfig{
				AllowedOrigins: tt.allowedOrigins,
			}

			result := cfg.Enabled()
			if result != tt.expected {
				t.Errorf("Enabled() = %v; want %v", result, tt.expected)
			}
		})
	}
}
