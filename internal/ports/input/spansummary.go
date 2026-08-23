package input

import (
	"math"
	"sort"
	"strconv"
)

// SpanStat aggregates every occurrence of one span name (optionally split by an
// attribute) across a set of traces.
//
// PerTrace is the field that earns this type its keep: a span that runs 512 times
// in a single request is an N+1 query pattern, and no percentile reveals that —
// only the ratio does. Totals alone would show "703 ms in ResolveChain" and leave
// the reader guessing whether that is one slow query or hundreds of fast ones.
type SpanStat struct {
	Name string `json:"name"`
	// Group is the value of the group-by attribute (e.g. the "spatial.layer" a
	// query hit). Empty when not grouping, or when a span lacks the attribute.
	Group    string  `json:"group,omitempty"`
	Spans    int     `json:"spans"`
	Traces   int     `json:"traces"`
	PerTrace float64 `json:"per_trace"`
	TotalMS  float64 `json:"total_ms"`
	MeanMS   float64 `json:"mean_ms"`
	P50MS    float64 `json:"p50_ms"`
	P95MS    float64 `json:"p95_ms"`
	MaxMS    float64 `json:"max_ms"`
	Errors   int     `json:"errors,omitempty"`
}

// SpanSummary is the aggregate view of a set of traces: what ran, how often, and
// where the time went. It answers the perf question ("which span dominates?") and
// the debugging question ("what is called more often than it should be?").
type SpanSummary struct {
	Traces    int        `json:"traces"`
	RootP50MS float64    `json:"root_p50_ms"`
	RootP95MS float64    `json:"root_p95_ms"`
	RootMaxMS float64    `json:"root_max_ms"`
	GroupBy   string     `json:"group_by,omitempty"`
	Spans     []SpanStat `json:"spans"`
}

// SummarizeSpans aggregates spans across traces, newest-first order irrelevant.
//
// groupBy names a span attribute to split by (e.g. "spatial.layer"); pass "" to
// aggregate by span name alone. Spans missing the attribute fall into an
// unlabeled group rather than being dropped, so the totals always add up.
func SummarizeSpans(traces []*CapturedTrace, groupBy string) SpanSummary {
	type key struct{ name, group string }
	durations := map[key][]float64{}
	traceIDs := map[key]map[string]struct{}{}
	errors := map[key]int{}
	var roots []float64

	for _, tr := range traces {
		if tr == nil {
			continue
		}
		roots = append(roots, tr.DurationMS)
		for _, sp := range tr.Spans {
			k := key{name: sp.Name, group: groupValue(sp, groupBy)}
			durations[k] = append(durations[k], sp.DurationMS)
			if traceIDs[k] == nil {
				traceIDs[k] = map[string]struct{}{}
			}
			traceIDs[k][tr.TraceID] = struct{}{}
			// StatusCode mirrors the OTel status; only Error is a real failure,
			// Unset is the normal state for a span nobody set a status on.
			if sp.StatusCode == "Error" {
				errors[k]++
			}
		}
	}

	out := SpanSummary{
		Traces:    len(roots),
		RootP50MS: percentile(roots, 0.50),
		RootP95MS: percentile(roots, 0.95),
		RootMaxMS: percentile(roots, 1.0),
		GroupBy:   groupBy,
	}
	for k, ds := range durations {
		out.Spans = append(out.Spans, statOf(k.name, k.group, ds, len(traceIDs[k]), errors[k]))
	}
	// Sort by total time so the dominant span is first — the order a reader wants
	// when asking where the time went. Name/group break ties for stable output.
	sort.Slice(out.Spans, func(i, j int) bool {
		a, b := out.Spans[i], out.Spans[j]
		if a.TotalMS != b.TotalMS {
			return a.TotalMS > b.TotalMS
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Group < b.Group
	})
	return out
}

// groupValue reads the group-by attribute off a span. A span that lacks the
// attribute yields "", which keeps it in an unlabeled group rather than dropping
// it — otherwise the totals would silently stop adding up.
func groupValue(sp CapturedSpan, groupBy string) string {
	if groupBy == "" {
		return ""
	}
	if v, ok := sp.Attributes[groupBy]; ok {
		return attrString(v)
	}
	return ""
}

// statOf turns one span group's raw durations into its aggregate.
func statOf(name, group string, durations []float64, nTraces, nErrors int) SpanStat {
	var total float64
	for _, d := range durations {
		total += d
	}
	stat := SpanStat{
		Name:    name,
		Group:   group,
		Spans:   len(durations),
		Traces:  nTraces,
		TotalMS: round2(total),
		MeanMS:  round2(total / float64(len(durations))),
		P50MS:   round2(percentile(durations, 0.50)),
		P95MS:   round2(percentile(durations, 0.95)),
		MaxMS:   round2(percentile(durations, 1.0)),
		Errors:  nErrors,
	}
	if nTraces > 0 {
		stat.PerTrace = round2(float64(len(durations)) / float64(nTraces))
	}
	return stat
}

// attrString renders an attribute value for grouping. Attributes arrive as any
// (they cross a JSON boundary), so numbers may be float64 even when they were
// written as ints.
func attrString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		// -1 gives the shortest representation that round-trips, so an integer
		// attribute that arrived as float64 groups as "3" rather than "3.000000".
		return strconv.FormatFloat(t, 'g', -1, 64)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	default:
		return ""
	}
}

// percentile returns the p-quantile (0..1) using nearest-rank on a copy, so the
// caller's slice keeps its order. Returns 0 for an empty input.
func percentile(vs []float64, p float64) float64 {
	if len(vs) == 0 {
		return 0
	}
	s := make([]float64, len(vs))
	copy(s, vs)
	sort.Float64s(s)
	i := int(p * float64(len(s)))
	if i >= len(s) {
		i = len(s) - 1
	}
	return s[i]
}

// round2 keeps the JSON readable; sub-0.01ms precision is noise at this scale.
func round2(f float64) float64 { return math.Round(f*100) / 100 }
