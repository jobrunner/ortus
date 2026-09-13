package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"github.com/jobrunner/ortus/internal/config"
)

// These tests drive the handler the HTTP server actually serves, not the
// middleware in isolation. That distinction is the whole point: the pre-existing
// CORS tests wrapped a stub handler, so they never met the router — and a
// preflight never reaches an r.Use middleware, because OPTIONS matches no route.

const corsTestOrigin = "https://app.example.com"

func newCORSServer(t *testing.T, origins ...string) *Server {
	t.Helper()
	return newTestServerWithConfig(config.ServerConfig{
		Host:         "localhost",
		Port:         8080,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		CORS:         config.CORSConfig{AllowedOrigins: origins},
	})
}

// served returns what the server hands to net/http — the router plus whatever
// wraps it.
func served(t *testing.T, srv *Server) http.Handler {
	t.Helper()
	return srv.server.Handler
}

func preflight(t *testing.T, srv *Server, method, path, origin string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodOptions, path, nil)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", method)
	req.Header.Set("Access-Control-Request-Headers", "content-type")

	rec := httptest.NewRecorder()
	served(t, srv).ServeHTTP(rec, req)
	return rec
}

// The reported bug: a browser cannot POST to /api/v1/query/batch cross-origin
// because its preflight is answered without any CORS headers.
func TestCORSPreflightForBatchIsAnswered(t *testing.T) {
	srv := newCORSServer(t, corsTestOrigin)

	rec := preflight(t, srv, http.MethodPost, "/api/v1/query/batch", corsTestOrigin)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != corsTestOrigin {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, corsTestOrigin)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, http.MethodPost) {
		t.Errorf("Access-Control-Allow-Methods = %q, missing POST", got)
	}
}

// Every writing route must preflight correctly, derived from the route table so
// a new POST/PUT/DELETE endpoint is covered the day it is registered.
func TestCORSPreflightForEveryWritingRoute(t *testing.T) {
	srv := newCORSServer(t, corsTestOrigin)

	type op struct{ method, path string }
	var ops []op
	err := srv.Router().Walk(func(route *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		// GetPathTemplate/GetMethods error for routes that have no path or no
		// method matcher; act only on the ones that have both. Path-var routes
		// are skipped because they carry no concrete URL to send.
		if tmpl, tErr := route.GetPathTemplate(); tErr == nil && !strings.Contains(tmpl, "{") {
			if methods, mErr := route.GetMethods(); mErr == nil {
				for _, m := range methods {
					// GET/HEAD are simple requests — no preflight to pin.
					if m != http.MethodGet && m != http.MethodHead && m != http.MethodOptions {
						ops = append(ops, op{m, tmpl})
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk router: %v", err)
	}
	if len(ops) == 0 {
		t.Fatal("no writing routes found — the walk is broken, not the service")
	}

	for _, o := range ops {
		t.Run(o.method+" "+o.path, func(t *testing.T) {
			rec := preflight(t, srv, o.method, o.path, corsTestOrigin)

			if rec.Code != http.StatusNoContent {
				t.Errorf("status = %d, want 204", rec.Code)
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != corsTestOrigin {
				t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, corsTestOrigin)
			}
			if got := rec.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, o.method) {
				t.Errorf("Access-Control-Allow-Methods = %q, missing %q", got, o.method)
			}
		})
	}
}

// A method no route serves must never be advertised as allowed.
func TestCORSPreflightDoesNotAdvertiseUnroutedMethod(t *testing.T) {
	srv := newCORSServer(t, corsTestOrigin)

	rec := preflight(t, srv, http.MethodDelete, "/api/v1/query/batch", corsTestOrigin)

	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "" {
		t.Errorf("Access-Control-Allow-Methods = %q, want empty (DELETE is not routed)", got)
	}
}

// A disallowed origin gets no Allow-Origin, but the response still varies by
// Origin — otherwise a shared cache may hand one origin's response to another.
func TestCORSDisallowedOriginGetsVaryButNoAllowOrigin(t *testing.T) {
	srv := newCORSServer(t, corsTestOrigin)

	rec := preflight(t, srv, http.MethodPost, "/api/v1/query/batch", "https://evil.example.org")

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty", got)
	}
	if vary := rec.Header().Values("Vary"); len(vary) == 0 {
		t.Error("Vary must be set for a cross-origin request, allowed or not")
	}
}

// A bare OPTIONS (no Origin, no Access-Control-Request-Method) is not a
// preflight and must keep reaching the router, or enabling CORS would silently
// turn every OPTIONS into a 204.
func TestCORSBareOptionsFallsThroughToRouter(t *testing.T) {
	srv := newCORSServer(t, corsTestOrigin)

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/query/batch", nil)
	rec := httptest.NewRecorder()
	served(t, srv).ServeHTTP(rec, req)

	if rec.Code == http.StatusNoContent {
		t.Error("bare OPTIONS was answered 204 by the CORS layer; it must fall through")
	}
}

// Cross-origin GET is a simple request: it must keep working exactly as before.
func TestCORSSimpleGETStillCarriesAllowOrigin(t *testing.T) {
	srv := newCORSServer(t, corsTestOrigin)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sources", nil)
	req.Header.Set("Origin", corsTestOrigin)
	rec := httptest.NewRecorder()
	served(t, srv).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != corsTestOrigin {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, corsTestOrigin)
	}
}

// Handler() and Router() are not interchangeable: CORS lives outside the
// router, so anything that SERVES requests must take Handler(). This is the
// guard for the TLS server in app.go — serving Router() there would give
// HTTPS clients a service without CORS, and the difference is invisible
// until a browser sends a preflight.
func TestServedHandlerCarriesCORSButBareRouterDoesNot(t *testing.T) {
	srv := newCORSServer(t, corsTestOrigin)

	newPreflight := func() *http.Request {
		req := httptest.NewRequest(http.MethodOptions, "/api/v1/query/batch", nil)
		req.Header.Set("Origin", corsTestOrigin)
		req.Header.Set("Access-Control-Request-Method", http.MethodPost)
		return req
	}

	viaHandler := httptest.NewRecorder()
	srv.Handler().ServeHTTP(viaHandler, newPreflight())
	if viaHandler.Code != http.StatusNoContent {
		t.Errorf("Handler() preflight status = %d, want 204", viaHandler.Code)
	}

	viaRouter := httptest.NewRecorder()
	srv.Router().ServeHTTP(viaRouter, newPreflight())
	if got := viaRouter.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("bare Router() answered with Access-Control-Allow-Origin %q — "+
			"is CORS wired as an r.Use middleware again?", got)
	}
}

// With CORS off the responses must be byte-identical to a service without it.
func TestCORSDisabledSendsNoHeaders(t *testing.T) {
	srv := newCORSServer(t) // no origins configured

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sources", nil)
	req.Header.Set("Origin", corsTestOrigin)
	rec := httptest.NewRecorder()
	served(t, srv).ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty when CORS is disabled", got)
	}
	if vary := rec.Header().Values("Vary"); len(vary) != 0 {
		t.Errorf("Vary = %v, want none when CORS is disabled", vary)
	}
}
