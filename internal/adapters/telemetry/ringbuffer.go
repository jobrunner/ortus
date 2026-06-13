// Package telemetry provides the OpenTelemetry adapter for the Tracer port,
// including a trace-grouped in-memory ring buffer designed for future MCP
// queries.
package telemetry

import (
	"context"
	"sort"
	"sync"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

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
// RingBuffer.ListActive so the MCP server can answer "what's currently
// running?" — the question you need to ask when something hangs.
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

// RingBuffer is an in-memory trace-grouped span store. It implements
// sdktrace.SpanProcessor so it sits inside the regular OTel pipeline.
//
// Eviction policy: per-pool FIFO with separate retention for error traces.
// Each pool holds up to `capacity` traces. Successful traces never evict
// error traces, and vice versa — guaranteeing the last N errors stay
// queryable even under load. In-flight (active) spans are also tracked so
// hanging operations are visible to the MCP server.
type RingBuffer struct {
	capacity int

	mu          sync.RWMutex
	active      map[trace.TraceID]*traceBuffer   // in-progress traces (root span not yet ended)
	activeSpans map[trace.SpanID]*ActiveSpan     // every started, not-yet-ended span
	finished    map[trace.TraceID]*CapturedTrace // completed traces, indexed by id
	orderOK     []trace.TraceID                  // FIFO of successful trace IDs
	orderErr    []trace.TraceID                  // FIFO of error trace IDs (separate eviction)
	evicted     uint64
	service     string
}

type traceBuffer struct {
	spans      []CapturedSpan
	rootEnded  bool
	rootName   string
	start      time.Time
	end        time.Time
	statusCode string
	statusMsg  string
	openSpans  int // spans started but not yet ended that belong to this trace
}

// NewRingBuffer creates a ring buffer with the given per-pool capacity (in
// number of retained completed traces). Maximum memory use is 2*capacity
// completed traces. Capacity <= 0 disables retention entirely (active-span
// tracking also disabled).
func NewRingBuffer(capacity int, serviceName string) *RingBuffer {
	if capacity < 0 {
		capacity = 0
	}
	return &RingBuffer{
		capacity:    capacity,
		active:      make(map[trace.TraceID]*traceBuffer),
		activeSpans: make(map[trace.SpanID]*ActiveSpan),
		finished:    make(map[trace.TraceID]*CapturedTrace),
		service:     serviceName,
	}
}

// Capacity returns the per-pool capacity.
func (r *RingBuffer) Capacity() int { return r.capacity }

// OnStart implements sdktrace.SpanProcessor. We snapshot the span as
// "active" so ListActive can surface hangs to the MCP server, and we
// maintain a per-trace open-span counter so the trace is only finalized
// once every span has ended (the root may legally end before its
// children, so finalizing on rootEnded alone evicts traces prematurely).
func (r *RingBuffer) OnStart(_ context.Context, s sdktrace.ReadWriteSpan) {
	if r.capacity == 0 {
		return
	}
	sc := s.SpanContext()
	if !sc.IsValid() {
		return
	}

	parent := ""
	if p := s.Parent(); p.IsValid() {
		parent = p.SpanID().String()
	}

	attrs := make(map[string]any, len(s.Attributes()))
	for _, kv := range s.Attributes() {
		attrs[string(kv.Key)] = kv.Value.AsInterface()
	}

	a := &ActiveSpan{
		TraceID:      sc.TraceID().String(),
		SpanID:       sc.SpanID().String(),
		ParentSpanID: parent,
		Name:         s.Name(),
		Kind:         s.SpanKind().String(),
		Start:        s.StartTime(),
		Attributes:   attrs,
	}

	r.mu.Lock()
	r.activeSpans[sc.SpanID()] = a
	traceID := sc.TraceID()
	buf, ok := r.active[traceID]
	if !ok {
		buf = &traceBuffer{}
		r.active[traceID] = buf
	}
	buf.openSpans++
	r.mu.Unlock()
}

// OnEnd implements sdktrace.SpanProcessor. It records the span, decrements
// the per-trace open-span counter, and finalizes the trace only once the
// root has ended AND no spans remain open. This is the correct condition
// for OTel: parents and children can end in any order.
func (r *RingBuffer) OnEnd(s sdktrace.ReadOnlySpan) {
	if r.capacity == 0 {
		return
	}
	sc := s.SpanContext()
	if !sc.IsValid() {
		return
	}

	captured := captureSpan(s)
	traceID := sc.TraceID()
	// A span is the local root of its trace when it has no parent, OR when
	// its parent came from a remote service (distributed-trace continuation
	// via incoming traceparent). In both cases we own the trace ID locally
	// and should treat this span as the root for finalization purposes.
	parent := s.Parent()
	isRoot := !parent.IsValid() || parent.IsRemote() || parent.TraceID() != sc.TraceID()

	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.activeSpans, sc.SpanID())

	buf, ok := r.active[traceID]
	if !ok {
		buf = &traceBuffer{}
		r.active[traceID] = buf
	}
	buf.spans = append(buf.spans, captured)
	if buf.openSpans > 0 {
		buf.openSpans--
	}
	if buf.start.IsZero() || captured.Start.Before(buf.start) {
		buf.start = captured.Start
	}
	if captured.End.After(buf.end) {
		buf.end = captured.End
	}
	if isRoot {
		buf.rootEnded = true
		buf.rootName = captured.Name
		buf.statusCode = captured.StatusCode
		buf.statusMsg = captured.StatusMsg
	}

	// Finalize only when the root has ended AND every started span has
	// also ended. This handles the legal OTel case where a child outlives
	// its parent.
	if buf.rootEnded && buf.openSpans == 0 {
		r.finalizeLocked(traceID, buf)
	}
}

// Shutdown implements sdktrace.SpanProcessor. It is a no-op — the ring buffer
// is in-memory and not flushable. We do not clear data so the MCP server can
// still read traces during shutdown.
func (r *RingBuffer) Shutdown(_ context.Context) error { return nil }

// ForceFlush implements sdktrace.SpanProcessor.
func (r *RingBuffer) ForceFlush(_ context.Context) error { return nil }

// finalizeLocked promotes an active trace buffer into a finished CapturedTrace
// and applies the eviction policy. Caller must hold r.mu.
func (r *RingBuffer) finalizeLocked(traceID trace.TraceID, buf *traceBuffer) {
	delete(r.active, traceID)

	spans := append([]CapturedSpan(nil), buf.spans...)
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].Start.Before(spans[j].Start) })

	ct := &CapturedTrace{
		TraceID:    traceID.String(),
		RootName:   buf.rootName,
		Service:    r.service,
		Start:      buf.start,
		End:        buf.end,
		DurationMS: float64(buf.end.Sub(buf.start).Microseconds()) / 1000.0,
		StatusCode: buf.statusCode,
		SpanCount:  len(spans),
		Spans:      spans,
	}

	r.finished[traceID] = ct

	// Route to error or success pool. Errors get their own FIFO so they
	// don't get evicted by routine successful traces.
	if ct.StatusCode == "Error" {
		r.orderErr = append(r.orderErr, traceID)
		for len(r.orderErr) > r.capacity {
			victim := r.orderErr[0]
			r.orderErr = r.orderErr[1:]
			delete(r.finished, victim)
			r.evicted++
		}
	} else {
		r.orderOK = append(r.orderOK, traceID)
		for len(r.orderOK) > r.capacity {
			victim := r.orderOK[0]
			r.orderOK = r.orderOK[1:]
			delete(r.finished, victim)
			r.evicted++
		}
	}
}

// GetTrace returns a single trace by its hex trace ID. Returns nil if unknown
// or already evicted.
func (r *RingBuffer) GetTrace(traceID string) *CapturedTrace {
	id, err := trace.TraceIDFromHex(traceID)
	if err != nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.finished[id]
	if !ok {
		return nil
	}
	return cloneTrace(t)
}

// ListTraces returns traces matching the filter, newest first. Successful
// and error traces are merged into a single time-ordered stream so callers
// don't need to query each pool separately.
func (r *RingBuffer) ListTraces(f TraceFilter) []*CapturedTrace {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Merge both pools by trace End time, newest first.
	all := make([]*CapturedTrace, 0, len(r.orderOK)+len(r.orderErr))
	for _, id := range r.orderOK {
		if t, ok := r.finished[id]; ok {
			all = append(all, t)
		}
	}
	for _, id := range r.orderErr {
		if t, ok := r.finished[id]; ok {
			all = append(all, t)
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].End.After(all[j].End) })

	out := make([]*CapturedTrace, 0, len(all))
	for _, t := range all {
		if !filterMatch(t, f) {
			continue
		}
		out = append(out, cloneTrace(t))
		if f.Limit > 0 && len(out) >= f.Limit {
			break
		}
	}
	return out
}

// ListActive returns a snapshot of in-flight spans, newest start first.
// This is the answer to "what's running right now?" — essential for
// diagnosing hangs the MCP server cannot otherwise see, because hung spans
// never complete and never enter the finished pool.
func (r *RingBuffer) ListActive() []*ActiveSpan {
	now := time.Now()
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*ActiveSpan, 0, len(r.activeSpans))
	for _, a := range r.activeSpans {
		cp := *a
		cp.AgeMS = float64(now.Sub(a.Start).Microseconds()) / 1000.0
		// Shallow clone of attrs since they're maps.
		cp.Attributes = make(map[string]any, len(a.Attributes))
		for k, v := range a.Attributes {
			cp.Attributes[k] = v
		}
		out = append(out, &cp)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start.After(out[j].Start) })
	return out
}

// Stats returns a snapshot of buffer state.
func (r *RingBuffer) Stats() Stats {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s := Stats{
		Capacity:          r.capacity,
		TracesActive:      len(r.active),
		SpansActive:       len(r.activeSpans),
		TracesStored:      len(r.orderOK),
		ErrorTracesStored: len(r.orderErr),
		Evicted:           r.evicted,
	}
	// OldestEnd/NewestEnd across both pools.
	for _, id := range r.orderOK {
		if t, ok := r.finished[id]; ok {
			if s.OldestEnd.IsZero() || t.End.Before(s.OldestEnd) {
				s.OldestEnd = t.End
			}
			if t.End.After(s.NewestEnd) {
				s.NewestEnd = t.End
			}
		}
	}
	for _, id := range r.orderErr {
		if t, ok := r.finished[id]; ok {
			if s.OldestEnd.IsZero() || t.End.Before(s.OldestEnd) {
				s.OldestEnd = t.End
			}
			if t.End.After(s.NewestEnd) {
				s.NewestEnd = t.End
			}
		}
	}
	return s
}

func filterMatch(t *CapturedTrace, f TraceFilter) bool {
	if f.MinDuration > 0 && time.Duration(t.DurationMS*float64(time.Millisecond)) < f.MinDuration {
		return false
	}
	if f.Status != "" && t.StatusCode != f.Status {
		return false
	}
	if f.NameContains != "" && !containsFold(t.RootName, f.NameContains) {
		return false
	}
	if !f.Since.IsZero() && t.End.Before(f.Since) {
		return false
	}
	return true
}

func captureSpan(s sdktrace.ReadOnlySpan) CapturedSpan {
	sc := s.SpanContext()
	parent := ""
	if p := s.Parent(); p.IsValid() {
		parent = p.SpanID().String()
	}

	attrs := make(map[string]any, len(s.Attributes()))
	for _, kv := range s.Attributes() {
		attrs[string(kv.Key)] = kv.Value.AsInterface()
	}

	events := make([]CapturedEvent, 0, len(s.Events()))
	for _, ev := range s.Events() {
		eattrs := make(map[string]any, len(ev.Attributes))
		for _, kv := range ev.Attributes {
			eattrs[string(kv.Key)] = kv.Value.AsInterface()
		}
		events = append(events, CapturedEvent{
			Name:       ev.Name,
			Time:       ev.Time,
			Attributes: eattrs,
		})
	}

	end := s.EndTime()
	start := s.StartTime()
	return CapturedSpan{
		TraceID:      sc.TraceID().String(),
		SpanID:       sc.SpanID().String(),
		ParentSpanID: parent,
		Name:         s.Name(),
		Kind:         s.SpanKind().String(),
		Start:        start,
		End:          end,
		DurationMS:   float64(end.Sub(start).Microseconds()) / 1000.0,
		StatusCode:   s.Status().Code.String(),
		StatusMsg:    s.Status().Description,
		Attributes:   attrs,
		Events:       events,
	}
}

func cloneTrace(t *CapturedTrace) *CapturedTrace {
	cp := *t
	cp.Spans = append([]CapturedSpan(nil), t.Spans...)
	return &cp
}

// containsFold is a tiny ASCII case-insensitive substring check. Avoids
// importing strings just for this.
func containsFold(s, sub string) bool {
	if sub == "" {
		return true
	}
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			a := s[i+j]
			b := sub[j]
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
