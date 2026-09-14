package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jobrunner/ortus/internal/config"
)

// The allow-list is parsed once, when the server is built — not on every
// request. Config validation already guarantees the patterns parse, so
// re-parsing them per request buys nothing and puts string work on the hot path
// of every cross-origin call.
func TestCORSPatternsAreParsedOnce(t *testing.T) {
	srv := newCORSServer(t, corsTestOrigin, "https://*.example.org")

	if got := len(srv.corsPatterns); got != 2 {
		t.Fatalf("len(corsPatterns) = %d, want 2 — the allow-list is not pre-parsed", got)
	}
}

// A Server built by hand (tests, or a future caller that skips config
// validation) must not take an unusable entry down with it: the entry is
// dropped and the usable ones keep working.
func TestCORSUnusableEntryIsDroppedNotFatal(t *testing.T) {
	srv := newTestServerWith(config.ServerConfig{
		Host:         "localhost",
		Port:         8080,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		// "*.example.com" has no scheme and can never match; config.Validate
		// rejects it at startup, so reaching the server means validation was
		// bypassed.
		CORS: config.CORSConfig{AllowedOrigins: []string{"*.example.com", corsTestOrigin}},
	}, nil)

	if got := len(srv.corsPatterns); got != 1 {
		t.Errorf("len(corsPatterns) = %d, want 1 (the unusable entry dropped)", got)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sources", nil)
	req.Header.Set("Origin", corsTestOrigin)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != corsTestOrigin {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q — the usable entry stopped working", got, corsTestOrigin)
	}
}

// With every entry unusable there is nothing to match, and CORS must behave as
// if it were switched off rather than answering with empty headers.
func TestCORSAllEntriesUnusableBehavesAsDisabled(t *testing.T) {
	srv := newTestServerWith(config.ServerConfig{
		Host:         "localhost",
		Port:         8080,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		CORS:         config.CORSConfig{AllowedOrigins: []string{"*.example.com"}},
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sources", nil)
	req.Header.Set("Origin", corsTestOrigin)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if vary := rec.Header().Values("Vary"); len(vary) != 0 {
		t.Errorf("Vary = %v, want none when no usable origin is configured", vary)
	}
}
