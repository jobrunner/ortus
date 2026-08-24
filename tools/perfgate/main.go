// Command perfgate is the hard performance gate. It drives a fixed request set
// against a running ortus, reads the resulting spans back over MCP, and compares
// them against a committed baseline.
//
// The gate budgets two different things, and the distinction is the whole point:
//
//	call counts  — hard limits. How many SpatiaLite round-trips one request makes
//	               is a property of the code, not of the machine: it does not
//	               change because CI is busy. A regression from 6 queries to 512
//	               is caught exactly, on the first run, with no flake budget.
//	durations    — loose ceilings. Wall-clock on shared CI runners is noisy, so
//	               timings are checked with a wide tolerance. They catch an order
//	               of magnitude, not a 20% drift.
//
// A gate built only on milliseconds would have to set its tolerance so wide that
// it never fires; a gate on call counts fires the moment an N+1 is introduced.
//
// Usage:
//
//	go run ./tools/perfgate -base http://127.0.0.1:8080 -mcp http://127.0.0.1:9091/mcp
//	go run ./tools/perfgate ... -update    # rewrite the baseline deliberately
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jobrunner/ortus/internal/ports/input"
)

// Budget is one span's committed allowance.
type Budget struct {
	Name string `json:"name"`
	// Group is the group-by attribute value (e.g. the spatial layer), so budgets
	// can be per-layer rather than lumping every PointInPolygon together.
	Group string `json:"group,omitempty"`
	// MaxPerTrace is the hard limit: calls of this span per request. Checked
	// exactly, because it does not depend on machine speed.
	MaxPerTrace float64 `json:"max_per_trace"`
	// MaxP95MS is the loose ceiling for this span's own p95 duration.
	MaxP95MS float64 `json:"max_p95_ms"`
}

// Baseline is the committed performance contract.
type Baseline struct {
	Comment string `json:"_comment"`
	// Endpoint is the path the gate drives, relative to -base.
	Endpoint string `json:"endpoint"`
	// Coordinates are the fixed request set. Committed so runs are comparable:
	// random points would move the numbers independently of the code.
	Coordinates [][2]float64 `json:"coordinates"`
	GroupBy     string       `json:"group_by"`
	// KnownIssues records defects the current numbers contain. A baseline is a
	// record of what the code does today, not an endorsement of it: without this
	// field, a committed 235-calls-per-request budget reads as "intended".
	// Preserved verbatim across -update runs.
	KnownIssues []string `json:"_known_issues,omitempty"`
	// MaxRootP95MS bounds end-to-end request latency (loose; see the note above).
	MaxRootP95MS float64  `json:"max_root_p95_ms"`
	Budgets      []Budget `json:"budgets"`
}

func main() {
	var (
		base       = flag.String("base", "http://127.0.0.1:8080", "ortus HTTP base URL")
		mcpURL     = flag.String("mcp", "http://127.0.0.1:9091/mcp", "ortus MCP endpoint (needs mcp.enabled and tracing.enabled)")
		path       = flag.String("baseline", "perf/baseline.json", "baseline file (repo-relative path)")
		update     = flag.Bool("update", false, "rewrite the baseline from this run instead of checking it")
		countSlack = flag.Float64("count-slack", 1.0, "multiplier applied to committed call-count limits")
		timeSlack  = flag.Float64("time-slack", 3.0, "multiplier applied to committed duration ceilings")
		summarize  = flag.Bool("summarize", false, "only read and print the current span summary; drive no requests and check no budgets")
		sinceMin   = flag.Float64("since-min", 15, "with -summarize: how many minutes back to aggregate")
		groupBy    = flag.String("group-by", "", "override the baseline's group_by attribute (analysis aid, e.g. spatial.chain.from_fid)")
	)
	flag.Parse()

	bl, err := loadBaseline(*path)
	if err != nil {
		fatal("baseline: %v", err)
	}
	// An override only makes sense while analyzing: a gate run must use the
	// grouping its budgets were written against, or the budgets would not match.
	if *groupBy != "" {
		if !*summarize {
			fatal("-group-by only applies with -summarize; a gate run must use the baseline's grouping")
		}
		bl.GroupBy = *groupBy
	}

	// Read-only mode: the same span_summary call the gate makes, for use during an
	// analysis. It exists so nobody reimplements the aggregation in a throwaway
	// script when the native MCP tools are unavailable — the server code is the
	// same, so the numbers are comparable with a gate run.
	if *summarize {
		since := time.Now().UTC().Add(-time.Duration(*sinceMin) * time.Minute)
		summary, err := fetchSummary(*mcpURL, bl, since)
		if err != nil {
			fatal("reading spans over MCP: %v", err)
		}
		report(bl, summary, nil)
		return
	}

	// Drive the fixed request set. Sequential on purpose: the gate measures the
	// shape of one request, and concurrency would only add scheduling noise to
	// the per-span numbers it compares.
	since := time.Now().UTC()
	okCount, err := drive(context.Background(), *base, bl)
	if err != nil {
		fatal("driving requests: %v", err)
	}
	if okCount != len(bl.Coordinates) {
		fatal("only %d of %d requests succeeded — fix the errors before trusting timings",
			okCount, len(bl.Coordinates))
	}

	summary, err := fetchSummary(*mcpURL, bl, since)
	if err != nil {
		fatal("reading spans over MCP: %v", err)
	}
	if summary.Traces == 0 {
		fatal("no traces captured — is tracing.enabled set on the target instance?")
	}

	if *update {
		if err := writeBaseline(*path, bl, summary); err != nil {
			fatal("writing baseline: %v", err)
		}
		fmt.Printf("✅ Baseline aktualisiert: %s (%d Traces, %d Span-Budgets)\n",
			*path, summary.Traces, len(summary.Spans))
		return
	}

	violations := check(bl, summary, *countSlack, *timeSlack)
	report(bl, summary, violations)
	if len(violations) > 0 {
		os.Exit(1)
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "perfgate: "+format+"\n", args...)
	os.Exit(2)
}

func loadBaseline(path string) (*Baseline, error) {
	// Read through an os.Root anchored at the working directory: the path comes
	// from a flag, and this confines the read to the repository instead of
	// trusting whatever traversal was passed in. The flag is therefore a
	// repo-relative path, which is how the Makefile targets use it.
	root, err := os.OpenRoot(".")
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()

	file, err := root.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	raw, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	var bl Baseline
	if err := json.Unmarshal(raw, &bl); err != nil {
		return nil, err
	}
	if len(bl.Coordinates) == 0 {
		return nil, fmt.Errorf("%s declares no coordinates", path)
	}
	return &bl, nil
}

// drive fires one request per committed coordinate and returns how many
// succeeded.
func drive(ctx context.Context, base string, bl *Baseline) (int, error) {
	client := &http.Client{Timeout: 120 * time.Second}
	ok := 0
	for _, c := range bl.Coordinates {
		url := fmt.Sprintf("%s%s?lon=%.6f&lat=%.6f", strings.TrimRight(base, "/"), bl.Endpoint, c[0], c[1])
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return ok, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return ok, err
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			ok++
		}
	}
	return ok, nil
}

// fetchSummary calls the span_summary MCP tool, which is the same aggregation the
// agent-facing tooling uses — one implementation, so the gate and a human
// debugging the same regression see identical numbers.
func fetchSummary(endpoint string, bl *Baseline, since time.Time) (input.SpanSummary, error) {
	var zero input.SpanSummary
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client := mcp.NewClient(&mcp.Implementation{Name: "perfgate", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint}, nil)
	if err != nil {
		return zero, fmt.Errorf("connect %s: %w", endpoint, err)
	}
	defer func() { _ = session.Close() }()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "span_summary",
		Arguments: map[string]any{
			// Nanosecond precision matters: at second granularity the window also
			// catches traces from just before the run, and extra traces *dilute*
			// per_trace — so the gate would under-report call counts and could miss
			// the very regression it exists to catch. Measured: a diluted window
			// reported 218 calls/request where the exact one reported 234.9.
			"since_iso":     since.Format(time.RFC3339Nano),
			"name_contains": bl.Endpoint,
			"group_by":      bl.GroupBy,
			"limit":         len(bl.Coordinates) + 10,
		},
	})
	if err != nil {
		return zero, err
	}
	if res.IsError {
		return zero, fmt.Errorf("span_summary returned an error: %v", res.Content)
	}
	var text strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	var summary input.SpanSummary
	if err := json.Unmarshal([]byte(text.String()), &summary); err != nil {
		return zero, fmt.Errorf("decoding span_summary output: %w", err)
	}
	return summary, nil
}

type violation struct {
	what string
	hard bool
}

// check compares the run against the baseline. Missing budgets are reported too:
// a span that vanished is either a real optimization (update the baseline) or a
// code path that stopped being traced, and the second is exactly the regression
// the trace-coverage gate and this gate exist to catch together.
func check(bl *Baseline, s input.SpanSummary, countSlack, timeSlack float64) []violation {
	var out []violation
	seen := map[string]input.SpanStat{}
	for _, st := range s.Spans {
		seen[st.Name+"\x00"+st.Group] = st
	}

	if limit := bl.MaxRootP95MS * timeSlack; bl.MaxRootP95MS > 0 && s.RootP95MS > limit {
		out = append(out, violation{
			what: fmt.Sprintf("Request-p95 %.0f ms überschreitet %.0f ms (Budget %.0f × Toleranz %.1f)",
				s.RootP95MS, limit, bl.MaxRootP95MS, timeSlack),
		})
	}

	for _, b := range bl.Budgets {
		label := b.Name
		if b.Group != "" {
			label += " [" + b.Group + "]"
		}
		st, ok := seen[b.Name+"\x00"+b.Group]
		if !ok {
			out = append(out, violation{
				what: fmt.Sprintf("%s fehlt im Trace — entweder wegoptimiert (Baseline aktualisieren) "+
					"oder nicht mehr instrumentiert", label),
			})
			continue
		}
		if limit := b.MaxPerTrace * countSlack; st.PerTrace > limit {
			out = append(out, violation{
				hard: true,
				what: fmt.Sprintf("%s: %.1f Aufrufe/Request überschreitet %.1f — "+
					"das ist eine strukturelle Regression, keine Messschwankung", label, st.PerTrace, limit),
			})
		}
		if limit := b.MaxP95MS * timeSlack; b.MaxP95MS > 0 && st.P95MS > limit {
			out = append(out, violation{
				what: fmt.Sprintf("%s: p95 %.1f ms überschreitet %.1f ms (Budget %.1f × Toleranz %.1f)",
					label, st.P95MS, limit, b.MaxP95MS, timeSlack),
			})
		}
	}
	return out
}

func report(bl *Baseline, s input.SpanSummary, violations []violation) {
	fmt.Printf("perfgate: %d Traces, Request-p50 %.0f ms, p95 %.0f ms (Budget-p95 %.0f ms)\n",
		s.Traces, s.RootP50MS, s.RootP95MS, bl.MaxRootP95MS)
	// "Aufr./Trace" and the trace count are shown together on purpose: per_trace
	// divides by the traces that reach the span, so without the count next to it
	// the number reads as an average over all requests and misleads.
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
	if len(violations) == 0 {
		fmt.Println("\n✅ perfgate ok — alle Span-Budgets eingehalten.")
		return
	}
	fmt.Fprintf(os.Stderr, "\n❌ perfgate: %d Budget-Verstoß/Verstöße\n\n", len(violations))
	for _, v := range violations {
		marker := "·"
		if v.hard {
			marker = "‼"
		}
		fmt.Fprintf(os.Stderr, "  %s %s\n", marker, v.what)
	}
	fmt.Fprint(os.Stderr, "\n‼ = harte Grenze (Aufrufzahl, maschinenunabhängig).\n"+
		"Absichtliche Änderung? `-update` schreibt die Baseline neu.\n")
}

// writeBaseline records the current run as the new contract. Call-count limits are
// taken as-is (they are exact), duration ceilings from the measured p95 so the
// committed number reflects reality rather than an invented round figure.
func writeBaseline(path string, bl *Baseline, s input.SpanSummary) error {
	out := *bl
	out.MaxRootP95MS = roundUp(s.RootP95MS)
	out.Budgets = nil
	for _, st := range s.Spans {
		// Root spans are covered by MaxRootP95MS; budgeting them again would
		// double-report the same regression.
		if st.Name == bl.Endpoint || strings.HasPrefix(st.Name, "GET ") {
			continue
		}
		out.Budgets = append(out.Budgets, Budget{
			Name:        st.Name,
			Group:       st.Group,
			MaxPerTrace: st.PerTrace,
			MaxP95MS:    roundUp(st.P95MS),
		})
	}
	sort.Slice(out.Budgets, func(i, j int) bool {
		if out.Budgets[i].Name != out.Budgets[j].Name {
			return out.Budgets[i].Name < out.Budgets[j].Name
		}
		return out.Budgets[i].Group < out.Budgets[j].Group
	})
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	// Write through the same os.Root the read side uses. The path comes from a
	// flag, and having one side confined to the repository while the other writes
	// wherever it is pointed would make the confinement decorative.
	root, err := os.OpenRoot(".")
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()

	file, err := root.Create(filepath.Clean(path))
	if err != nil {
		return err
	}
	if _, err := file.Write(append(raw, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

// roundUp gives the committed ceilings a readable granularity so a baseline diff
// shows meaningful movement rather than measurement jitter.
func roundUp(ms float64) float64 {
	switch {
	case ms < 1:
		return 1
	case ms < 10:
		return float64(int(ms) + 1)
	default:
		return float64((int(ms)/10 + 1) * 10)
	}
}
