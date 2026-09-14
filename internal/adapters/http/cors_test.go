package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jobrunner/ortus/internal/config"
)

// The matching rules themselves are covered by TestOriginPatternMatches in
// internal/domain, where they live. This pins the adapter's use of them: the
// configured allow-list, parsed by initCORS, decides.
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
			// Built through NewServer so the check runs against the allow-list
			// as initCORS parsed it, not against a hand-set field.
			s := newCORSServer(t, tt.allowedOrigins...)

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
