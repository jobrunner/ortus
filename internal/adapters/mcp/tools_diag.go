package mcp

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jobrunner/ortus/internal/adapters/telemetry"
)

// registerDiagnosticTools mounts the five read-only tools that let an
// AI agent inspect ortus' tracing ring buffer and health.
func registerDiagnosticTools(srv *mcp.Server, deps Deps, logger *slog.Logger) {
	addListTraces(srv, deps, logger)
	addGetTrace(srv, deps, logger)
	addListActiveSpans(srv, deps, logger)
	addTracingStats(srv, deps, logger)
	addHealth(srv, deps, logger)
}

// ---- list_traces -----------------------------------------------------------

type listTracesIn struct {
	MinDurationMS float64 `json:"min_duration_ms,omitempty" jsonschema:"only return traces whose duration is at least this many milliseconds"`
	Status        string  `json:"status,omitempty" jsonschema:"filter by OTel status code: 'Ok', 'Error', or 'Unset'"`
	NameContains  string  `json:"name_contains,omitempty" jsonschema:"substring match against the root span name (case-insensitive)"`
	SinceISO      string  `json:"since_iso,omitempty" jsonschema:"only return traces that ended at or after this RFC3339 timestamp"`
	Limit         int     `json:"limit,omitempty" jsonschema:"maximum number of traces to return (default 20)"`
}

type listTracesOut struct {
	Traces []*telemetry.CapturedTrace `json:"traces"`
	Count  int                        `json:"count"`
}

func addListTraces(srv *mcp.Server, deps Deps, _ *slog.Logger) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "list_traces",
		Description: "List recent completed traces from ortus' in-memory ring buffer. " +
			"Both successful and error traces are searched. " +
			"Newest first. Use filters to narrow the result set.",
	}, func(_ toolCtx, _ *callRequest, in listTracesIn) (*callResult, listTracesOut, error) {
		if deps.Buffer == nil {
			return nil, listTracesOut{}, fmt.Errorf("tracing is disabled — set tracing.enabled in ortus config")
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 20
		}
		filter := telemetry.TraceFilter{
			MinDuration:  time.Duration(in.MinDurationMS * float64(time.Millisecond)),
			Status:       in.Status,
			NameContains: in.NameContains,
			Limit:        limit,
		}
		if in.SinceISO != "" {
			t, err := time.Parse(time.RFC3339, in.SinceISO)
			if err != nil {
				return nil, listTracesOut{}, fmt.Errorf("invalid since_iso %q: %w", in.SinceISO, err)
			}
			filter.Since = t
		}
		traces := deps.Buffer.ListTraces(filter)
		return nil, listTracesOut{Traces: traces, Count: len(traces)}, nil
	})
}

// ---- get_trace -------------------------------------------------------------

type getTraceIn struct {
	TraceID string `json:"trace_id" jsonschema:"hex-encoded 32-character trace id"`
}

type getTraceOut struct {
	Trace *telemetry.CapturedTrace `json:"trace,omitempty"`
	Found bool                     `json:"found"`
}

func addGetTrace(srv *mcp.Server, deps Deps, _ *slog.Logger) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_trace",
		Description: "Fetch a single completed trace by its hex trace_id, including " +
			"every span with its attributes, events, and any recorded errors. " +
			"Returns found=false if the trace was evicted from the buffer or never existed.",
	}, func(_ toolCtx, _ *callRequest, in getTraceIn) (*callResult, getTraceOut, error) {
		if deps.Buffer == nil {
			return nil, getTraceOut{}, fmt.Errorf("tracing is disabled — set tracing.enabled in ortus config")
		}
		t := deps.Buffer.GetTrace(in.TraceID)
		return nil, getTraceOut{Trace: t, Found: t != nil}, nil
	})
}

// ---- list_active_spans ----------------------------------------------------

type listActiveSpansIn struct {
	Limit int `json:"limit,omitempty" jsonschema:"maximum number of spans to return (default 50). Sorted newest-start first."`
}

type listActiveSpansOut struct {
	Spans []*telemetry.ActiveSpan `json:"spans"`
	Count int                     `json:"count"`
}

func addListActiveSpans(srv *mcp.Server, deps Deps, _ *slog.Logger) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "list_active_spans",
		Description: "Snapshot of every currently in-flight span — useful for hang detection. " +
			"Spans here have started but not yet ended. The age_ms field tells you how long " +
			"each has been running. Sorted newest-start first.",
	}, func(_ toolCtx, _ *callRequest, in listActiveSpansIn) (*callResult, listActiveSpansOut, error) {
		if deps.Buffer == nil {
			return nil, listActiveSpansOut{}, fmt.Errorf("tracing is disabled — set tracing.enabled in ortus config")
		}
		spans := deps.Buffer.ListActive()
		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}
		if len(spans) > limit {
			spans = spans[:limit]
		}
		return nil, listActiveSpansOut{Spans: spans, Count: len(spans)}, nil
	})
}

// ---- tracing_stats --------------------------------------------------------

type tracingStatsOut struct {
	Enabled        bool             `json:"enabled"`
	Stats          *telemetry.Stats `json:"stats,omitempty"`
	OTelErrorCount uint64           `json:"otel_error_count" jsonschema:"count of OTLP-exporter / SDK errors observed since process start"`
}

func addTracingStats(srv *mcp.Server, deps Deps, _ *slog.Logger) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "tracing_stats",
		Description: "Health of the tracing pipeline: ring-buffer occupancy, eviction counter, " +
			"and the number of internal OTel errors observed (mostly OTLP-exporter failures). " +
			"Use this to know whether ortus' tracing is functioning before relying on the other diagnostic tools.",
	}, func(_ toolCtx, _ *callRequest, _ any) (*callResult, tracingStatsOut, error) {
		out := tracingStatsOut{
			Enabled:        deps.Buffer != nil,
			OTelErrorCount: telemetry.OTelErrorCount(),
		}
		if deps.Buffer != nil {
			s := deps.Buffer.Stats()
			out.Stats = &s
		}
		return nil, out, nil
	})
}

// ---- health ---------------------------------------------------------------

type healthOut struct {
	Healthy        bool              `json:"healthy"`
	Ready          bool              `json:"ready"`
	PackagesLoaded int               `json:"packages_loaded"`
	PackagesReady  int               `json:"packages_ready"`
	Components     map[string]string `json:"components"`
}

func addHealth(srv *mcp.Server, deps Deps, _ *slog.Logger) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "health",
		Description: "Current health snapshot: liveness, readiness, package counts, " +
			"component status. Equivalent to the GET /health REST endpoint but " +
			"directly callable as an MCP tool.",
	}, func(ctx toolCtx, _ *callRequest, _ any) (*callResult, healthOut, error) {
		d := deps.HealthService.GetHealthDetails(ctx)
		return nil, healthOut{
			Healthy:        d.Healthy,
			Ready:          d.Ready,
			PackagesLoaded: d.PackagesLoaded,
			PackagesReady:  d.PackagesReady,
			Components:     d.Components,
		}, nil
	})
}

// stringifyJSON is a small helper used by the tools when they need to
// fall back to a TextContent block (e.g. for very large payloads). Not
// currently used — kept here as it's a frequently-wanted helper.
//
//nolint:unused
func stringifyJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("<marshal error: %v>", err)
	}
	return string(b)
}
