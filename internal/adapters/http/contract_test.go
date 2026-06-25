package http

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

// TestRoutesMatchOpenAPISpec is the HTTP-contract fitness function: every
// registered /api/v1 route must be documented in the embedded OpenAPI spec and
// vice-versa. This catches exactly the drift that bit us in the Package→Source
// rename — a route renamed in code but not the spec (or the reverse).
//
// Scope: the /api/v1 business surface. The root health endpoints (/health*) are
// compared separately by their own handlers; /sync is operator-only and not
// part of the documented query contract.
func TestRoutesMatchOpenAPISpec(t *testing.T) {
	srv := newTestServer(nil, nil, nil)

	// 1. Registered /api/v1 route templates (path relative to the /api/v1 server).
	routes := map[string]bool{}
	err := srv.Router().Walk(func(route *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		// GetPathTemplate errors for matcher-only routes (no path) — those just
		// aren't path routes, so act only on the ones that have a template.
		if tmpl, tErr := route.GetPathTemplate(); tErr == nil {
			if rel, ok := strings.CutPrefix(tmpl, "/api/v1"); ok && rel != "" {
				routes[rel] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk router: %v", err)
	}

	// 2. Documented paths from the embedded spec (the bytes actually served at
	// /openapi.json), minus the root health endpoints.
	specJSON, err := getOpenAPIJSON()
	if err != nil {
		t.Fatalf("getOpenAPIJSON: %v", err)
	}
	var spec struct {
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(specJSON, &spec); err != nil {
		t.Fatalf("unmarshal spec: %v", err)
	}
	documented := map[string]bool{}
	for p := range spec.Paths {
		if strings.HasPrefix(p, "/health") {
			continue
		}
		documented[p] = true
	}

	// 3. Both directions.
	for r := range routes {
		if !documented[r] {
			t.Errorf("route /api/v1%s is registered but NOT documented in openapi.yaml", r)
		}
	}
	for p := range documented {
		if !routes[p] {
			t.Errorf("openapi.yaml documents %q but no /api/v1 route is registered for it", p)
		}
	}
}
