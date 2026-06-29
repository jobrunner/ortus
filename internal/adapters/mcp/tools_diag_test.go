package mcp_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/metric/noop"

	mcpAdapter "github.com/jobrunner/ortus/internal/adapters/mcp"
	"github.com/jobrunner/ortus/internal/adapters/storage"
	"github.com/jobrunner/ortus/internal/adapters/telemetry"
	"github.com/jobrunner/ortus/internal/application"
	"github.com/jobrunner/ortus/internal/ports/input"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// diagDeps builds MCP deps backed by a real, controllable telemetry provider,
// returning the tracer so a test can seed traces / active spans into the buffer.
func diagDeps(t *testing.T) (mcpAdapter.Deps, output.Tracer) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	meter := noop.NewMeterProvider().Meter("test")

	tp, err := telemetry.NewProvider(context.Background(), telemetry.ProviderOptions{
		ServiceName: "ortus-test", SampleRatio: 1.0, BufferSize: 16,
	}, logger)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	tr := telemetry.NewTracer(tp.TracerProvider())
	store := storage.NewTracedStorage(stubStorage{}, tr, "local")
	reg := application.NewSourceRegistry([]output.SpatialSource{fakeRepo{}}, store, meter, tr, logger, "/tmp")
	qs := application.NewQueryService(reg, nil, meter, tr, logger, application.QueryServiceConfig{})
	hs := application.NewHealthService(reg, true, tr)

	return mcpAdapter.Deps{
		Telemetry: tp.Buffer(), QueryService: qs, Registry: reg, HealthService: hs, Version: "test",
	}, tr
}

func serveDeps(t *testing.T, deps mcpAdapter.Deps) *mcp.ClientSession {
	t.Helper()
	srv := mcpAdapter.New(mcpAdapter.Options{Host: "127.0.0.1", Port: 0, Path: "/mcp"}, deps,
		slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return connectClient(t, ts)
}

func callTool(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	return res
}

// toolJSON concatenates the result's text content (which the SDK fills with the
// JSON-encoded structured output) and decodes it into a map.
func toolJSON(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	// Only success results carry structured output; surface a tool error here
	// rather than letting it fail later as an opaque JSON-decode error.
	if res.IsError {
		t.Fatalf("tool returned IsError; content=%v", res.Content)
	}
	var text strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text.String()), &out); err != nil {
		t.Fatalf("decode tool output %q: %v", text.String(), err)
	}
	return out
}

func TestDiagTools_WithTraces(t *testing.T) {
	deps, tr := diagDeps(t)

	// Seed one completed trace into the ring buffer (OnEnd is synchronous).
	_, span := tr.Start(context.Background(), "QueryService.QueryPoint")
	span.End()

	session := serveDeps(t, deps)

	// list_traces: default limit + a name filter must return the seeded trace.
	res := callTool(t, session, "list_traces", map[string]any{"name_contains": "QueryService"})
	if res.IsError {
		t.Fatalf("list_traces errored: %v", res.Content)
	}
	if c, _ := toolJSON(t, res)["count"].(float64); c < 1 {
		t.Errorf("list_traces count = %v, want >= 1", c)
	}

	// Invalid since_iso is a handler-level error.
	if res := callTool(t, session, "list_traces", map[string]any{"since_iso": "not-a-timestamp"}); !res.IsError {
		t.Error("list_traces with invalid since_iso should be an error")
	}

	// get_trace: real id -> found; bogus id -> not found (both non-error).
	seeded := deps.Telemetry.ListTraces(input.TraceFilter{Limit: 10})
	if len(seeded) == 0 {
		t.Fatal("expected a captured trace in the buffer")
	}
	res = callTool(t, session, "get_trace", map[string]any{"trace_id": seeded[0].TraceID})
	if found, _ := toolJSON(t, res)["found"].(bool); !found {
		t.Errorf("get_trace(real id) found = false, want true")
	}
	res = callTool(t, session, "get_trace", map[string]any{"trace_id": "00000000000000000000000000000000"})
	if found, _ := toolJSON(t, res)["found"].(bool); found {
		t.Errorf("get_trace(bogus id) found = true, want false")
	}

	// tracing_stats: enabled with a live buffer.
	res = callTool(t, session, "tracing_stats", nil)
	if enabled, _ := toolJSON(t, res)["enabled"].(bool); !enabled {
		t.Errorf("tracing_stats enabled = false, want true")
	}
}

func TestDiagTools_ActiveSpansTruncated(t *testing.T) {
	deps, tr := diagDeps(t)
	session := serveDeps(t, deps)

	_, s1 := tr.Start(context.Background(), "InFlight1")
	_, s2 := tr.Start(context.Background(), "InFlight2")
	defer func() { s1.End(); s2.End() }()

	// Two spans in flight, limit 1 -> truncated to 1 (exercises spans[:limit]).
	res := callTool(t, session, "list_active_spans", map[string]any{"limit": 1})
	if res.IsError {
		t.Fatalf("list_active_spans errored: %v", res.Content)
	}
	if c, _ := toolJSON(t, res)["count"].(float64); c != 1 {
		t.Errorf("active spans count = %v, want 1 (truncated by limit)", c)
	}
}

func TestDiagTools_TracingDisabled(t *testing.T) {
	deps, _ := diagDeps(t)
	deps.Telemetry = nil // tracing off
	session := serveDeps(t, deps)

	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"list_traces", nil},
		{"get_trace", map[string]any{"trace_id": "x"}},
		{"list_active_spans", nil},
	} {
		if res := callTool(t, session, tc.name, tc.args); !res.IsError {
			t.Errorf("%s with tracing disabled should be an error", tc.name)
		}
	}

	// tracing_stats degrades gracefully: enabled=false, not an error.
	res := callTool(t, session, "tracing_stats", nil)
	if res.IsError {
		t.Fatalf("tracing_stats should not error when disabled: %v", res.Content)
	}
	if enabled, _ := toolJSON(t, res)["enabled"].(bool); enabled {
		t.Errorf("tracing_stats enabled = true, want false")
	}
}
