package main

import (
	"errors"
	"fmt"

	"github.com/jobrunner/ortus/internal/ports/input"
)

// The read-only side of perfgate: pointing a summary at another endpoint and
// printing it. Split from main.go, which holds the gate.

// applyOverrides points a summary at another endpoint or grouping than the
// baseline's. An override only makes sense while analyzing: a gate run must
// measure exactly what its budgets were written against, or they would not match.
func applyOverrides(bl *Baseline, summarize bool, groupBy, nameContains string) error {
	if !summarize && (groupBy != "" || nameContains != "") {
		return errors.New("-group-by and -name-contains only apply with -summarize; a gate run must use the baseline's endpoint and grouping")
	}
	if groupBy != "" {
		bl.GroupBy = groupBy
	}
	if nameContains != "" {
		bl.Endpoint = nameContains
	}
	return nil
}

// reportSummary prints a span summary that was only read, not gated. It names
// the filter and claims nothing about budgets: none were checked, and the
// baseline's would belong to another endpoint once -name-contains is used.
func reportSummary(bl *Baseline, s input.SpanSummary) {
	fmt.Printf("trace-summary: %d Traces mit %q im Namen, Request-p50 %.0f ms, p95 %.0f ms\n",
		s.Traces, bl.Endpoint, s.RootP50MS, s.RootP95MS)
	if s.Traces == 0 {
		fmt.Println("Keine Traces gefunden. Passt der Filter (-name-contains), liegen die Requests im Zeitfenster" +
			" (-since-min), und antwortet die richtige Instanz (-mcp)?")
		return
	}
	printSpans(s)
}

// printSpans lists the aggregated spans. "Aufr./Trace" and the trace count are
// shown together on purpose: per_trace divides by the traces that reach the
// span, so without the count next to it the number reads as an average over all
// requests and misleads.
func printSpans(s input.SpanSummary) {
	fmt.Printf("%-42s %11s %7s %9s %9s\n", "Span", "Aufr./Trace", "Traces", "p95 ms", "Summe ms")
	for _, st := range s.Spans {
		label := st.Name
		if st.Group != "" {
			label += " [" + st.Group + "]"
		}
		if len(label) > 42 {
			label = label[:39] + "..."
		}
		fmt.Printf("%-42s %11.1f %7d %9.1f %9.1f\n", label, st.PerTrace, st.Traces, st.P95MS, st.TotalMS)
	}
}
