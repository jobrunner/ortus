package input

import "time"

// TelemetryQuery is the primary port a driving adapter (the MCP server) uses to
// read captured trace data — "what ran, what's running, what failed". It is the
// seam that keeps the MCP adapter decoupled from the concrete telemetry adapter:
// MCP depends on this interface, the telemetry ring buffer implements it, and
// the composition root wires them together.
//
// The DTOs below are the contract (serialized to MCP/JSON); they are defined
// here, not in the telemetry adapter, so neither side imports the other. The
// telemetry adapter aliases these types so its internal code is unchanged.
type TelemetryQuery interface {
	// GetTrace returns a completed trace by id, or nil if not retained.
	GetTrace(id string) *CapturedTrace
	// ListTraces returns completed traces matching the filter, newest first.
	ListTraces(TraceFilter) []*CapturedTrace
	// ListActive returns in-flight spans — the answer to "what's running now?".
	ListActive() []*ActiveSpan
	// Stats summarizes buffer contents.
	Stats() Stats
	// OTelErrorCount is the process-wide count of OTel internal errors.
	OTelErrorCount() uint64
}

// CapturedEvent is a serializable copy of a span event.
type CapturedEvent struct {
	Name       string         `json:"name"`
	Time       time.Time      `json:"time"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// CapturedSpan is a serializable copy of a span. Only data useful for the MCP
// server is retained — no SDK objects.
type CapturedSpan struct {
	TraceID      string          `json:"trace_id"`
	SpanID       string          `json:"span_id"`
	ParentSpanID string          `json:"parent_span_id,omitempty"`
	Name         string          `json:"name"`
	Kind         string          `json:"kind"`
	Start        time.Time       `json:"start"`
	End          time.Time       `json:"end"`
	DurationMS   float64         `json:"duration_ms"`
	StatusCode   string          `json:"status_code"`
	StatusMsg    string          `json:"status_message,omitempty"`
	Attributes   map[string]any  `json:"attributes,omitempty"`
	Events       []CapturedEvent `json:"events,omitempty"`
}

// CapturedTrace is a complete trace tree as captured by the ring buffer.
type CapturedTrace struct {
	TraceID    string         `json:"trace_id"`
	RootName   string         `json:"root_name"`
	Service    string         `json:"service"`
	Start      time.Time      `json:"start"`
	End        time.Time      `json:"end"`
	DurationMS float64        `json:"duration_ms"`
	StatusCode string         `json:"status_code"`
	SpanCount  int            `json:"span_count"`
	Spans      []CapturedSpan `json:"spans"`
}

// ActiveSpan is a lightweight snapshot of an in-flight span. Returned by
// ListActive so the MCP server can answer "what's currently running?" — the
// question you need to ask when something hangs.
type ActiveSpan struct {
	TraceID      string         `json:"trace_id"`
	SpanID       string         `json:"span_id"`
	ParentSpanID string         `json:"parent_span_id,omitempty"`
	Name         string         `json:"name"`
	Kind         string         `json:"kind"`
	Start        time.Time      `json:"start"`
	AgeMS        float64        `json:"age_ms"`
	Attributes   map[string]any `json:"attributes,omitempty"`
}

// TraceFilter narrows down ListTraces results.
type TraceFilter struct {
	// MinDuration retains only traces longer than this. Zero means no filter.
	MinDuration time.Duration
	// Status retains only traces with this status. Valid values follow the
	// OTel codes.Code stringification: "Ok", "Error", "Unset" (mixed case).
	// Empty means no filter.
	Status string
	// NameContains retains only traces whose root span name contains this
	// substring. Empty means no filter.
	NameContains string
	// Since retains only traces that ended at or after this time. Zero means
	// no filter.
	Since time.Time
	// Limit caps the number of results. Zero means no cap.
	Limit int
}

// Stats summarizes ring-buffer contents for /health, /stats, and MCP overview.
type Stats struct {
	Capacity          int       `json:"capacity"`      // per-pool capacity
	TracesActive      int       `json:"traces_active"` // traces with at least one open span
	SpansActive       int       `json:"spans_active"`  // open spans (across all traces)
	TracesStored      int       `json:"traces_stored"` // successful, retained
	ErrorTracesStored int       `json:"error_traces_stored"`
	Evicted           uint64    `json:"evicted_total"`
	OldestEnd         time.Time `json:"oldest_end,omitempty"`
	NewestEnd         time.Time `json:"newest_end,omitempty"`
}
