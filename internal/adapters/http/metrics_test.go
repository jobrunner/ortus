package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// TestRoutePath_CollapsesDynamicSegments is the contract for issue #14:
// 100 different package IDs must collapse to ONE path-label value, not 100.
func TestRoutePath_CollapsesDynamicSegments(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	hm := newHTTPMetrics(provider.Meter("test"))

	r := mux.NewRouter()
	r.Use(hm.middleware)
	r.HandleFunc("/api/v1/packages/{packageId}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}).Methods(http.MethodGet)

	// Three different IDs hitting the same template.
	for _, id := range []string{"alpha", "beta", "gamma"} {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/packages/"+id, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("id %q: got %d, want 200", id, w.Code)
		}
	}

	got := collectCounter(t, reader, "ortus.http.requests")
	if len(got) != 1 {
		t.Fatalf("expected 1 series, got %d: %v", len(got), got)
	}
	for attrs, count := range got {
		if count != 3 {
			t.Errorf("counter value = %d, want 3", count)
		}
		if path, ok := attrs.Value("path"); !ok || path.AsString() != "/api/v1/packages/{packageId}" {
			t.Errorf("path label = %v, want %q", path, "/api/v1/packages/{packageId}")
		}
	}
}

// TestRoutePath_StaticRoutePassthrough ensures static routes use their
// literal path as the label (no surprise rewrites).
func TestRoutePath_StaticRoutePassthrough(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	hm := newHTTPMetrics(provider.Meter("test"))

	r := mux.NewRouter()
	r.Use(hm.middleware)
	r.HandleFunc("/api/v1/query", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}).Methods(http.MethodGet)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/query", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)

	got := collectCounter(t, reader, "ortus.http.requests")
	var foundPath string
	for attrs := range got {
		if v, ok := attrs.Value("path"); ok {
			foundPath = v.AsString()
		}
	}
	if foundPath != "/api/v1/query" {
		t.Errorf("path label = %q, want %q", foundPath, "/api/v1/query")
	}
}

// TestRoutePath_UnmatchedReturnsUnknown asserts the fallback for the
// NotFoundHandler path (when wrapped manually) or for handlers invoked
// without a matched route in the request.
func TestRoutePath_UnmatchedReturnsUnknown(t *testing.T) {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/whatever", nil)
	if got := routePath(req); got != "unknown" {
		t.Errorf("routePath(no route in ctx) = %q, want %q", got, "unknown")
	}
}

// collectCounter pulls the named counter out of the reader and returns
// counts keyed by attribute set.
func collectCounter(t *testing.T, reader sdkmetric.Reader, name string) map[attribute.Set]int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	out := map[attribute.Set]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("metric %q has unexpected data type %T", name, m.Data)
			}
			for _, dp := range sum.DataPoints {
				out[dp.Attributes] = dp.Value
			}
		}
	}
	return out
}
