package telemetry

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	otelcodes "go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// newTestProvider builds a TracerProvider with the given ring buffer as the
// only span processor. AlwaysSample so every span is captured.
func newTestProvider(buf *RingBuffer) *sdktrace.TracerProvider {
	return sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(buf),
	)
}

func TestRingBuffer_CaptureSingleTrace(t *testing.T) {
	buf := NewRingBuffer(8, "ortus-test")
	tp := newTestProvider(buf)
	tr := tp.Tracer("test")

	ctx, root := tr.Start(context.Background(), "root")
	_, child := tr.Start(ctx, "child")
	child.End()
	root.End()

	traces := buf.ListTraces(TraceFilter{})
	if len(traces) != 1 {
		t.Fatalf("want 1 trace, got %d", len(traces))
	}
	got := traces[0]
	if got.RootName != "root" {
		t.Errorf("RootName = %q, want %q", got.RootName, "root")
	}
	if got.SpanCount != 2 {
		t.Errorf("SpanCount = %d, want 2", got.SpanCount)
	}
}

func TestRingBuffer_GetTrace(t *testing.T) {
	buf := NewRingBuffer(4, "ortus-test")
	tp := newTestProvider(buf)
	tr := tp.Tracer("test")

	_, span := tr.Start(context.Background(), "lookup")
	id := span.SpanContext().TraceID().String()
	span.End()

	got := buf.GetTrace(id)
	if got == nil {
		t.Fatalf("GetTrace(%q) returned nil", id)
	}
	if got.TraceID != id {
		t.Errorf("TraceID = %q, want %q", got.TraceID, id)
	}

	// Unknown id and malformed id both yield nil.
	if buf.GetTrace("00000000000000000000000000000000") != nil {
		t.Error("expected nil for unknown id")
	}
	if buf.GetTrace("not-hex") != nil {
		t.Error("expected nil for malformed id")
	}
}

func TestRingBuffer_EvictsOldestWhenFull(t *testing.T) {
	buf := NewRingBuffer(2, "ortus-test")
	tp := newTestProvider(buf)
	tr := tp.Tracer("test")

	ids := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		_, span := tr.Start(context.Background(), "op")
		ids = append(ids, span.SpanContext().TraceID().String())
		span.End()
		// Force ordering by giving each trace a distinct end timestamp slot.
		time.Sleep(1 * time.Millisecond)
	}

	if got := buf.Stats().TracesStored; got != 2 {
		t.Errorf("TracesStored = %d, want 2", got)
	}
	if buf.GetTrace(ids[0]) != nil {
		t.Error("oldest trace should have been evicted")
	}
	if buf.GetTrace(ids[1]) == nil || buf.GetTrace(ids[2]) == nil {
		t.Error("newer traces should be retained")
	}
	if got := buf.Stats().Evicted; got != 1 {
		t.Errorf("Evicted = %d, want 1", got)
	}
}

func TestRingBuffer_ListNewestFirstAndLimit(t *testing.T) {
	buf := NewRingBuffer(8, "ortus-test")
	tp := newTestProvider(buf)
	tr := tp.Tracer("test")

	ids := make([]string, 0, 4)
	for i := 0; i < 4; i++ {
		_, span := tr.Start(context.Background(), "op")
		ids = append(ids, span.SpanContext().TraceID().String())
		span.End()
		time.Sleep(1 * time.Millisecond)
	}

	traces := buf.ListTraces(TraceFilter{Limit: 2})
	if len(traces) != 2 {
		t.Fatalf("len = %d, want 2", len(traces))
	}
	// Newest first means ids[3] then ids[2].
	if traces[0].TraceID != ids[3] {
		t.Errorf("traces[0].TraceID = %q, want %q", traces[0].TraceID, ids[3])
	}
	if traces[1].TraceID != ids[2] {
		t.Errorf("traces[1].TraceID = %q, want %q", traces[1].TraceID, ids[2])
	}
}

func TestRingBuffer_FilterByNameContains(t *testing.T) {
	buf := NewRingBuffer(8, "ortus-test")
	tp := newTestProvider(buf)
	tr := tp.Tracer("test")

	for _, name := range []string{"QueryService.QueryPoint", "Repository.Open", "OtherOp"} {
		_, span := tr.Start(context.Background(), name)
		span.End()
	}

	got := buf.ListTraces(TraceFilter{NameContains: "query"})
	if len(got) != 1 || got[0].RootName != "QueryService.QueryPoint" {
		t.Errorf("got %v, want only QueryService.QueryPoint", got)
	}
}

func TestRingBuffer_ZeroCapacityDisables(t *testing.T) {
	buf := NewRingBuffer(0, "ortus-test")
	tp := newTestProvider(buf)
	tr := tp.Tracer("test")

	_, span := tr.Start(context.Background(), "op")
	span.End()

	if got := len(buf.ListTraces(TraceFilter{})); got != 0 {
		t.Errorf("ListTraces len = %d, want 0", got)
	}
	if got := buf.Stats().TracesStored; got != 0 {
		t.Errorf("TracesStored = %d, want 0", got)
	}
}

func TestRingBuffer_ListActive_ShowsInFlight(t *testing.T) {
	buf := NewRingBuffer(8, "ortus-test")
	tp := newTestProvider(buf)
	tr := tp.Tracer("test")

	// Open two spans without ending them yet.
	ctx1, span1 := tr.Start(context.Background(), "outer")
	_, span2 := tr.Start(ctx1, "inner")

	active := buf.ListActive()
	if len(active) != 2 {
		t.Fatalf("ListActive len = %d, want 2", len(active))
	}
	names := []string{active[0].Name, active[1].Name}
	if !contains(names, "outer") || !contains(names, "inner") {
		t.Errorf("names = %v, want outer + inner", names)
	}

	// Age must be >= 0
	for _, a := range active {
		if a.AgeMS < 0 {
			t.Errorf("AgeMS = %f, want >= 0", a.AgeMS)
		}
	}

	// Closing the spans should drain ListActive.
	span2.End()
	span1.End()
	if got := len(buf.ListActive()); got != 0 {
		t.Errorf("after End, ListActive len = %d, want 0", got)
	}
}

func TestRingBuffer_ErrorPoolSeparateEviction(t *testing.T) {
	buf := NewRingBuffer(2, "ortus-test")
	tp := newTestProvider(buf)
	tr := tp.Tracer("test")

	// 3 successful traces (capacity 2 → 1 success evicted)
	for i := 0; i < 3; i++ {
		_, s := tr.Start(context.Background(), "ok")
		s.End()
		time.Sleep(1 * time.Millisecond)
	}

	// 3 error traces (capacity 2 → 1 error evicted)
	errIDs := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		_, s := tr.Start(context.Background(), "fail")
		s.SetStatus(otelcodes.Error, "boom")
		errIDs = append(errIDs, s.SpanContext().TraceID().String())
		s.End()
		time.Sleep(1 * time.Millisecond)
	}

	stats := buf.Stats()
	if stats.TracesStored != 2 {
		t.Errorf("TracesStored = %d, want 2", stats.TracesStored)
	}
	if stats.ErrorTracesStored != 2 {
		t.Errorf("ErrorTracesStored = %d, want 2", stats.ErrorTracesStored)
	}

	// The newest two errors must still be retrievable — successes must NOT
	// have evicted any of them.
	if buf.GetTrace(errIDs[1]) == nil {
		t.Error("error trace [1] should be retained")
	}
	if buf.GetTrace(errIDs[2]) == nil {
		t.Error("error trace [2] should be retained")
	}
	// Oldest error should be evicted.
	if buf.GetTrace(errIDs[0]) != nil {
		t.Error("oldest error trace should have been evicted")
	}
}

func contains(slice []string, target string) bool {
	for _, s := range slice {
		if s == target {
			return true
		}
	}
	return false
}

func TestTracer_RecordErrorIncludesStack(t *testing.T) {
	buf := NewRingBuffer(4, "ortus-test")
	tp := newTestProvider(buf)

	// Use the OTel adapter so this exercises the production code path
	// (RecordError → trace.WithStackTrace(true)).
	tr := NewTracer(tp)

	_, span := tr.Start(context.Background(), "errorOp")
	recordErrorInHelper(span) // distinct frame so we can grep for it
	span.End()

	traces := buf.ListTraces(TraceFilter{})
	if len(traces) != 1 {
		t.Fatalf("want 1 trace, got %d", len(traces))
	}
	if len(traces[0].Spans) != 1 || len(traces[0].Spans[0].Events) == 0 {
		t.Fatalf("expected at least one event; got spans=%v", traces[0].Spans)
	}

	// OTel exposes the error as an "exception" event with attributes
	// exception.type / exception.message / exception.stacktrace.
	ev := traces[0].Spans[0].Events[0]
	if ev.Name != "exception" {
		t.Fatalf("event name = %q, want %q", ev.Name, "exception")
	}
	stack, ok := ev.Attributes["exception.stacktrace"].(string)
	if !ok || stack == "" {
		t.Fatalf("missing exception.stacktrace attribute: %v", ev.Attributes)
	}
	if !strings.Contains(stack, "recordErrorInHelper") {
		t.Errorf("stack trace does not mention recordErrorInHelper: %s", stack)
	}
}

// recordErrorInHelper exists so the captured stack has a distinct frame
// name we can grep for in the test assertion.
func recordErrorInHelper(span interface{ RecordError(error) }) {
	span.RecordError(errBoom)
}

var errBoom = errBoomError("boom")

type errBoomError string

func (e errBoomError) Error() string { return string(e) }

func TestRingBuffer_ConcurrentWrites(t *testing.T) {
	buf := NewRingBuffer(256, "ortus-test")
	tp := newTestProvider(buf)
	tr := tp.Tracer("test")

	const writers = 16
	const perWriter = 64

	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				_, span := tr.Start(context.Background(), "op")
				span.End()
			}
		}()
	}
	wg.Wait()

	stats := buf.Stats()
	totalSeen := stats.TracesStored + int(stats.Evicted)
	if totalSeen != writers*perWriter {
		t.Errorf("totalSeen = %d, want %d", totalSeen, writers*perWriter)
	}
	if stats.TracesStored > 256 {
		t.Errorf("TracesStored = %d, exceeds capacity", stats.TracesStored)
	}
}
