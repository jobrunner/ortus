package http //nolint:revive // package name conflicts with stdlib but is acceptable in this context

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jobrunner/ortus/internal/config"
)

func TestExtractHost(t *testing.T) {
	tests := []struct {
		name     string
		origin   string
		expected string
	}{
		{
			name:     "simple https URL",
			origin:   "https://example.com",
			expected: "example.com",
		},
		{
			name:     "https URL with port",
			origin:   "https://example.com:8080",
			expected: "example.com",
		},
		{
			name:     "http URL",
			origin:   "http://example.com",
			expected: "example.com",
		},
		{
			name:     "URL with path",
			origin:   "https://example.com/path/to/resource",
			expected: "example.com",
		},
		{
			name:     "URL with port and path",
			origin:   "https://example.com:443/path",
			expected: "example.com",
		},
		{
			name:     "subdomain",
			origin:   "https://sub.example.com",
			expected: "sub.example.com",
		},
		{
			name:     "deep subdomain",
			origin:   "https://deep.sub.example.com",
			expected: "deep.sub.example.com",
		},
		{
			name:     "localhost",
			origin:   "http://localhost:3000",
			expected: "localhost",
		},
		{
			name:     "IP address",
			origin:   "http://192.168.1.1:8080",
			expected: "192.168.1.1",
		},
		{
			name:     "no protocol",
			origin:   "example.com",
			expected: "example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractHost(tt.origin)
			if result != tt.expected {
				t.Errorf("extractHost(%q) = %q; want %q", tt.origin, result, tt.expected)
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

		// Wildcard matches
		{
			name:     "wildcard matches subdomain",
			origin:   "https://sub.example.com",
			pattern:  "*.example.com",
			expected: true,
		},
		{
			name:     "wildcard matches deep subdomain",
			origin:   "https://deep.sub.example.com",
			pattern:  "*.example.com",
			expected: true,
		},
		{
			name:     "wildcard does not match root domain",
			origin:   "https://example.com",
			pattern:  "*.example.com",
			expected: false,
		},
		{
			name:     "wildcard does not match different domain",
			origin:   "https://sub.other.com",
			pattern:  "*.example.com",
			expected: false,
		},
		{
			name:     "wildcard with subdomain pattern",
			origin:   "https://app.sub.domain.tld",
			pattern:  "*.sub.domain.tld",
			expected: true,
		},
		{
			name:     "wildcard does not match partial",
			origin:   "https://notexample.com",
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
			pattern:  "*.localhost",
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
			allowedOrigins: []string{"*.example.com"},
			origin:         "https://app.example.com",
			expected:       true,
		},
		{
			name:           "allowed - mixed patterns",
			allowedOrigins: []string{"https://exact.com", "*.wildcard.com"},
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

// corsTestCase defines a test case for CORS middleware.
type corsTestCase struct {
	name                string
	allowedOrigins      []string
	requestOrigin       string
	requestMethod       string
	expectCORSHeaders   bool
	expectStatusCode    int
	expectAllowedOrigin string
}

// runCORSTest executes a single CORS test case.
func runCORSTest(t *testing.T, tt corsTestCase) {
	t.Helper()

	// Create a simple handler that returns 200 OK
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Create server with CORS config
	s := &Server{
		config: config.ServerConfig{
			CORS: config.CORSConfig{
				AllowedOrigins: tt.allowedOrigins,
			},
		},
	}

	// Wrap with CORS middleware
	handler := s.corsMiddleware(nextHandler)

	// Create request
	req := httptest.NewRequest(tt.requestMethod, "/api/v1/query", nil)
	if tt.requestOrigin != "" {
		req.Header.Set("Origin", tt.requestOrigin)
	}

	// Record response
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// Check status code
	if rr.Code != tt.expectStatusCode {
		t.Errorf("status code = %d; want %d", rr.Code, tt.expectStatusCode)
	}

	// Check CORS headers
	verifyCORSHeaders(t, rr, tt)
}

// verifyCORSHeaders checks the CORS headers in the response.
func verifyCORSHeaders(t *testing.T, rr *httptest.ResponseRecorder, tt corsTestCase) {
	t.Helper()

	allowOrigin := rr.Header().Get("Access-Control-Allow-Origin")

	if tt.expectCORSHeaders {
		verifyExpectedCORSHeaders(t, rr, tt.expectAllowedOrigin)
	} else if allowOrigin != "" {
		t.Errorf("expected no CORS headers, but got Access-Control-Allow-Origin = %q", allowOrigin)
	}
}

// verifyExpectedCORSHeaders checks that all expected CORS headers are present.
func verifyExpectedCORSHeaders(t *testing.T, rr *httptest.ResponseRecorder, expectedOrigin string) {
	t.Helper()

	allowOrigin := rr.Header().Get("Access-Control-Allow-Origin")
	allowMethods := rr.Header().Get("Access-Control-Allow-Methods")
	allowHeaders := rr.Header().Get("Access-Control-Allow-Headers")
	maxAge := rr.Header().Get("Access-Control-Max-Age")
	vary := rr.Header().Get("Vary")

	if allowOrigin != expectedOrigin {
		t.Errorf("Access-Control-Allow-Origin = %q; want %q", allowOrigin, expectedOrigin)
	}
	if allowMethods != "GET, OPTIONS" {
		t.Errorf("Access-Control-Allow-Methods = %q; want %q", allowMethods, "GET, OPTIONS")
	}
	if allowHeaders != "Accept, Content-Type, Authorization" {
		t.Errorf("Access-Control-Allow-Headers = %q; want %q", allowHeaders, "Accept, Content-Type, Authorization")
	}
	if maxAge != "86400" {
		t.Errorf("Access-Control-Max-Age = %q; want %q", maxAge, "86400")
	}
	if vary != "Origin" {
		t.Errorf("Vary = %q; want %q", vary, "Origin")
	}
}

func TestCORSMiddleware(t *testing.T) {
	tests := []corsTestCase{
		{
			name:                "allowed origin - GET request",
			allowedOrigins:      []string{"https://example.com"},
			requestOrigin:       "https://example.com",
			requestMethod:       http.MethodGet,
			expectCORSHeaders:   true,
			expectStatusCode:    http.StatusOK,
			expectAllowedOrigin: "https://example.com",
		},
		{
			name:                "allowed origin - OPTIONS preflight",
			allowedOrigins:      []string{"https://example.com"},
			requestOrigin:       "https://example.com",
			requestMethod:       http.MethodOptions,
			expectCORSHeaders:   true,
			expectStatusCode:    http.StatusNoContent,
			expectAllowedOrigin: "https://example.com",
		},
		{
			name:                "allowed wildcard origin",
			allowedOrigins:      []string{"*.example.com"},
			requestOrigin:       "https://app.example.com",
			requestMethod:       http.MethodGet,
			expectCORSHeaders:   true,
			expectStatusCode:    http.StatusOK,
			expectAllowedOrigin: "https://app.example.com",
		},
		{
			name:              "not allowed origin - no CORS headers",
			allowedOrigins:    []string{"https://example.com"},
			requestOrigin:     "https://evil.com",
			requestMethod:     http.MethodGet,
			expectCORSHeaders: false,
			expectStatusCode:  http.StatusOK,
		},
		{
			name:              "no origin header - no CORS headers",
			allowedOrigins:    []string{"https://example.com"},
			requestOrigin:     "",
			requestMethod:     http.MethodGet,
			expectCORSHeaders: false,
			expectStatusCode:  http.StatusOK,
		},
		{
			name:              "empty allowed origins - no CORS headers",
			allowedOrigins:    []string{},
			requestOrigin:     "https://example.com",
			requestMethod:     http.MethodGet,
			expectCORSHeaders: false,
			expectStatusCode:  http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runCORSTest(t, tt)
		})
	}
}

func TestCORSMiddleware_PreflightDoesNotCallNext(t *testing.T) {
	nextCalled := false
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	s := &Server{
		config: config.ServerConfig{
			CORS: config.CORSConfig{
				AllowedOrigins: []string{"https://example.com"},
			},
		},
	}

	handler := s.corsMiddleware(nextHandler)

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/query", nil)
	req.Header.Set("Origin", "https://example.com")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if nextCalled {
		t.Error("OPTIONS preflight request should not call next handler")
	}

	if rr.Code != http.StatusNoContent {
		t.Errorf("status code = %d; want %d", rr.Code, http.StatusNoContent)
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
			allowedOrigins: []string{"https://example.com", "*.other.com"},
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
