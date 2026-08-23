package input

import (
	"testing"
	"time"
)

// trace builds a CapturedTrace from (name, ms, attrs) triples, so the tests read
// as the span shapes they describe rather than as struct literals.
func trace(id string, rootMS float64, spans ...CapturedSpan) *CapturedTrace {
	return &CapturedTrace{
		TraceID:    id,
		RootName:   "GET /x",
		DurationMS: rootMS,
		SpanCount:  len(spans),
		Spans:      spans,
		Start:      time.Unix(0, 0),
	}
}

func span(name string, ms float64, attrs map[string]any) CapturedSpan {
	return CapturedSpan{Name: name, DurationMS: ms, Attributes: attrs}
}

func find(t *testing.T, s SpanSummary, name, group string) SpanStat {
	t.Helper()
	for _, st := range s.Spans {
		if st.Name == name && st.Group == group {
			return st
		}
	}
	t.Fatalf("no stat for %q/%q in %+v", name, group, s.Spans)
	return SpanStat{}
}

func TestSummarizeSortsByTotalTime(t *testing.T) {
	s := SummarizeSpans([]*CapturedTrace{
		trace("a", 100,
			span("cheap", 1, nil),
			span("expensive", 50, nil),
			span("medium", 10, nil),
		),
	}, "")
	if len(s.Spans) != 3 {
		t.Fatalf("expected 3 stats, got %d", len(s.Spans))
	}
	// Total time descending: the dominant cost must be first, since that is the
	// question the summary exists to answer.
	want := []string{"expensive", "medium", "cheap"}
	for i, w := range want {
		if s.Spans[i].Name != w {
			t.Errorf("position %d: want %q, got %q", i, w, s.Spans[i].Name)
		}
	}
}

// PerTrace is the N+1 detector — the reason this aggregation exists rather than
// just reading percentiles.
func TestPerTraceRevealsRepeatedCalls(t *testing.T) {
	var spans []CapturedSpan
	for i := 0; i < 512; i++ {
		spans = append(spans, span("SpatialIndex.ResolveChain", 1.4, nil))
	}
	s := SummarizeSpans([]*CapturedTrace{trace("a", 1114, spans...)}, "")
	st := find(t, s, "SpatialIndex.ResolveChain", "")
	if st.Spans != 512 || st.Traces != 1 {
		t.Fatalf("want 512 spans in 1 trace, got %d in %d", st.Spans, st.Traces)
	}
	if st.PerTrace != 512 {
		t.Errorf("per_trace: want 512, got %v", st.PerTrace)
	}
}

func TestPerTraceAveragesAcrossTraces(t *testing.T) {
	s := SummarizeSpans([]*CapturedTrace{
		trace("a", 10, span("q", 1, nil), span("q", 1, nil)),
		trace("b", 10, span("q", 1, nil), span("q", 1, nil), span("q", 1, nil), span("q", 1, nil)),
	}, "")
	st := find(t, s, "q", "")
	if st.Spans != 6 || st.Traces != 2 || st.PerTrace != 3 {
		t.Fatalf("want 6 spans / 2 traces / 3 per trace, got %d / %d / %v", st.Spans, st.Traces, st.PerTrace)
	}
}

func TestGroupBySplitsOneSpanNameByAttribute(t *testing.T) {
	s := SummarizeSpans([]*CapturedTrace{
		trace("a", 100,
			span("PointInPolygon", 80, map[string]any{"spatial.layer": "admin_levels"}),
			span("PointInPolygon", 2, map[string]any{"spatial.layer": "islands"}),
		),
	}, "spatial.layer")
	if len(s.Spans) != 2 {
		t.Fatalf("expected the name to split into 2 groups, got %d: %+v", len(s.Spans), s.Spans)
	}
	if got := find(t, s, "PointInPolygon", "admin_levels").TotalMS; got != 80 {
		t.Errorf("admin_levels total: want 80, got %v", got)
	}
	if got := find(t, s, "PointInPolygon", "islands").TotalMS; got != 2 {
		t.Errorf("islands total: want 2, got %v", got)
	}
}

// A span missing the group-by attribute must still be counted, otherwise the
// totals silently stop adding up and a slow path can hide in the gap.
func TestSpansMissingGroupAttributeStillCounted(t *testing.T) {
	s := SummarizeSpans([]*CapturedTrace{
		trace("a", 100,
			span("q", 5, map[string]any{"spatial.layer": "places"}),
			span("q", 7, nil),
		),
	}, "spatial.layer")
	var total float64
	for _, st := range s.Spans {
		total += st.TotalMS
	}
	if total != 12 {
		t.Fatalf("expected all 12ms accounted for, got %v across %+v", total, s.Spans)
	}
	if got := find(t, s, "q", "").TotalMS; got != 7 {
		t.Errorf("ungrouped span: want 7ms, got %v", got)
	}
}

func TestErrorsAreCountedSeparately(t *testing.T) {
	bad := span("q", 1, nil)
	bad.StatusCode = "Error"
	ok := span("q", 1, nil)
	ok.StatusCode = "Unset"
	s := SummarizeSpans([]*CapturedTrace{trace("a", 5, bad, ok)}, "")
	if got := find(t, s, "q", "").Errors; got != 1 {
		t.Errorf("errors: want 1, got %d", got)
	}
}

func TestRootPercentilesComeFromTraceDurations(t *testing.T) {
	var traces []*CapturedTrace
	for i := 1; i <= 100; i++ {
		traces = append(traces, trace(string(rune('a'+i%26))+string(rune(i)), float64(i), span("q", 1, nil)))
	}
	s := SummarizeSpans(traces, "")
	if s.Traces != 100 {
		t.Fatalf("want 100 traces, got %d", s.Traces)
	}
	if s.RootP50MS != 51 || s.RootP95MS != 96 || s.RootMaxMS != 100 {
		t.Errorf("percentiles off: p50=%v p95=%v max=%v", s.RootP50MS, s.RootP95MS, s.RootMaxMS)
	}
}

func TestEmptyAndNilInputsAreSafe(t *testing.T) {
	if s := SummarizeSpans(nil, ""); s.Traces != 0 || len(s.Spans) != 0 {
		t.Errorf("nil input should summarize to nothing, got %+v", s)
	}
	// A nil element must not panic: ListTraces returns pointers, and a racing
	// eviction is exactly the kind of thing that produces one.
	if s := SummarizeSpans([]*CapturedTrace{nil}, ""); s.Traces != 0 {
		t.Errorf("nil trace should be skipped, got %+v", s)
	}
}
