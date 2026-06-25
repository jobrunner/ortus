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

	// Compare "METHOD path" pairs (not just paths) so a GET→POST drift on the
	// same path is caught too.

	// 1. Registered /api/v1 operations (path relative to the /api/v1 server).
	routes := map[string]bool{}
	err := srv.Router().Walk(func(route *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		// GetPathTemplate errors for matcher-only routes (no path) — those just
		// aren't path routes, so act only on the ones that have a template.
		if tmpl, tErr := route.GetPathTemplate(); tErr == nil {
			if rel, ok := strings.CutPrefix(tmpl, "/api/v1"); ok && rel != "" {
				methods, _ := route.GetMethods()
				for _, m := range methods {
					routes[strings.ToUpper(m)+" "+rel] = true
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk router: %v", err)
	}

	// 2. Documented operations from the embedded spec (the bytes actually served
	// at /openapi.json), minus the root health endpoints.
	specJSON, err := getOpenAPIJSON()
	if err != nil {
		t.Fatalf("getOpenAPIJSON: %v", err)
	}
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(specJSON, &spec); err != nil {
		t.Fatalf("unmarshal spec: %v", err)
	}
	httpMethods := map[string]bool{
		"GET": true, "POST": true, "PUT": true, "DELETE": true,
		"PATCH": true, "HEAD": true, "OPTIONS": true,
	}
	documented := map[string]bool{}
	for p, ops := range spec.Paths {
		if strings.HasPrefix(p, "/health") {
			continue
		}
		for op := range ops {
			if m := strings.ToUpper(op); httpMethods[m] {
				documented[m+" "+p] = true
			}
		}
	}

	// 3. Both directions.
	for r := range routes {
		if !documented[r] {
			t.Errorf("route %q (under /api/v1) is registered but NOT documented in openapi.yaml", r)
		}
	}
	for op := range documented {
		if !routes[op] {
			t.Errorf("openapi.yaml documents %q but no matching /api/v1 route is registered", op)
		}
	}
}
