package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jobrunner/ortus/internal/ports/input"
)

// summary builds a SpanSummary from (name, group, perTrace, p95) tuples, so the
// tests read as the span shapes they describe.
func summary(rootP95 float64, stats ...input.SpanStat) input.SpanSummary {
	return input.SpanSummary{Traces: 25, RootP95MS: rootP95, Spans: stats}
}

func stat(name, group string, perTrace, p95 float64) input.SpanStat {
	return input.SpanStat{Name: name, Group: group, PerTrace: perTrace, P95MS: p95, Traces: 25}
}

func TestCheckPassesWithinBudget(t *testing.T) {
	bl := &Baseline{
		MaxRootP95MS: 100,
		Budgets:      []Budget{{Name: "q", Group: "admin", MaxPerTrace: 2, MaxP95MS: 20}},
	}
	got := check(bl, summary(90, stat("q", "admin", 2, 19)), 1.0, 3.0)
	if len(got) != 0 {
		t.Fatalf("expected no violations, got %+v", got)
	}
}

// A call-count violation must be reported as hard: it is a property of the code,
// not of the machine, which is the whole reason the gate can be strict about it.
func TestCallCountViolationIsHard(t *testing.T) {
	bl := &Baseline{Budgets: []Budget{{Name: "q", MaxPerTrace: 2, MaxP95MS: 20}}}
	got := check(bl, summary(0, stat("q", "", 235, 1)), 1.0, 3.0)
	if len(got) != 1 {
		t.Fatalf("expected exactly one violation, got %+v", got)
	}
	if !got[0].hard {
		t.Errorf("call-count violation must be marked hard: %+v", got[0])
	}
	if !strings.Contains(got[0].what, "235") {
		t.Errorf("message should name the measured count: %q", got[0].what)
	}
}

// Durations are checked loosely, so a run inside the tolerance must not fire even
// when it exceeds the committed number — otherwise CI noise would make the gate
// useless and it would get switched off.
func TestDurationsAreCheckedWithTolerance(t *testing.T) {
	bl := &Baseline{Budgets: []Budget{{Name: "q", MaxPerTrace: 2, MaxP95MS: 20}}}
	if got := check(bl, summary(0, stat("q", "", 2, 55)), 1.0, 3.0); len(got) != 0 {
		t.Fatalf("55ms is within 20ms x3, expected no violation, got %+v", got)
	}
	got := check(bl, summary(0, stat("q", "", 2, 61)), 1.0, 3.0)
	if len(got) != 1 || got[0].hard {
		t.Fatalf("61ms exceeds 20ms x3 and must be a soft violation, got %+v", got)
	}
}

func TestRootPercentileIsBudgeted(t *testing.T) {
	bl := &Baseline{MaxRootP95MS: 100}
	if got := check(bl, summary(290), 1.0, 3.0); len(got) != 0 {
		t.Fatalf("290ms is within 100ms x3, got %+v", got)
	}
	if got := check(bl, summary(310), 1.0, 3.0); len(got) != 1 {
		t.Fatalf("310ms exceeds 100ms x3, got %+v", got)
	}
}

// A budgeted span that stops appearing is reported rather than passing silently.
// It means either a real optimization (update the baseline) or a code path that
// stopped being traced — and the second is exactly what the trace-coverage gate
// and this one exist to catch together.
func TestMissingSpanIsReported(t *testing.T) {
	bl := &Baseline{Budgets: []Budget{{Name: "gone", MaxPerTrace: 1, MaxP95MS: 10}}}
	got := check(bl, summary(0, stat("other", "", 1, 1)), 1.0, 3.0)
	if len(got) != 1 || !strings.Contains(got[0].what, "fehlt") {
		t.Fatalf("expected a missing-span finding, got %+v", got)
	}
}

// Groups are part of a budget's identity: a per-layer budget must not be
// satisfied by the same span name on a different layer.
func TestBudgetsAreMatchedPerGroup(t *testing.T) {
	bl := &Baseline{Budgets: []Budget{{Name: "q", Group: "admin", MaxPerTrace: 1, MaxP95MS: 10}}}
	got := check(bl, summary(0, stat("q", "islands", 1, 1)), 1.0, 3.0)
	if len(got) != 1 || !strings.Contains(got[0].what, "admin") {
		t.Fatalf("expected the admin budget to be unmatched, got %+v", got)
	}
}

func TestRoundUpKeepsCeilingsReadable(t *testing.T) {
	cases := map[float64]float64{0: 1, 0.4: 1, 3.2: 4, 9.9: 10, 12: 20, 141: 150, 204: 210}
	for in, want := range cases {
		if got := roundUp(in); got != want {
			t.Errorf("roundUp(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestLoadBaselineRejectsAnEmptyCoordinateSet(t *testing.T) {
	dir := t.TempDir()
	rel := filepath.Join(filepath.Base(dir), "b.json")
	// loadBaseline reads through an os.Root at the working directory, so the
	// fixture has to live under it.
	if err := os.MkdirAll(filepath.Base(dir), 0o755); err != nil {
		t.Skipf("cannot create a repo-relative fixture dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Base(dir)) })
	if err := os.WriteFile(rel, []byte(`{"endpoint":"/x","coordinates":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadBaseline(rel); err == nil {
		t.Fatal("a baseline with no coordinates must be rejected — it would measure nothing and pass")
	}
}

func TestWriteBaselineRoundTripsAndKeepsKnownIssues(t *testing.T) {
	dir := filepath.Base(t.TempDir())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Skipf("cannot create a repo-relative fixture dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	rel := filepath.Join(dir, "b.json")

	bl := &Baseline{
		Endpoint:    "/api/v1/gazetteer",
		Coordinates: [][2]float64{{10, 50}},
		GroupBy:     "spatial.layer",
		KnownIssues: []string{"a defect the numbers still contain"},
	}
	s := summary(90,
		stat("GET /api/v1/gazetteer", "", 1, 90),
		stat("SpatialIndex.QueryKNN", "places", 5.6, 17),
	)
	if err := writeBaseline(rel, bl, s); err != nil {
		t.Fatalf("writeBaseline: %v", err)
	}

	raw, err := os.ReadFile(rel)
	if err != nil {
		t.Fatal(err)
	}
	var out Baseline
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	// A baseline is a record, not an endorsement — the known-issue notes must
	// survive a rewrite, or every -update would quietly launder them away.
	if len(out.KnownIssues) != 1 {
		t.Errorf("known issues lost on rewrite: %+v", out.KnownIssues)
	}
	// The root span is covered by MaxRootP95MS; budgeting it again would report
	// the same regression twice.
	for _, b := range out.Budgets {
		if strings.HasPrefix(b.Name, "GET ") {
			t.Errorf("root span must not get its own budget: %+v", b)
		}
	}
	if len(out.Budgets) != 1 || out.Budgets[0].Group != "places" {
		t.Fatalf("expected one per-layer budget, got %+v", out.Budgets)
	}
	if out.Budgets[0].MaxPerTrace != 5.6 {
		t.Errorf("call-count limit is taken as measured: got %v", out.Budgets[0].MaxPerTrace)
	}
	if out.MaxRootP95MS < 90 {
		t.Errorf("root ceiling must not be written below the measured p95: %v", out.MaxRootP95MS)
	}
}

func TestDriveCountsOnlySuccessfulRequests(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.RequestURI())
		// Fail the second coordinate, so the count has to distinguish.
		if len(got) == 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	bl := &Baseline{Endpoint: "/api/v1/gazetteer", Coordinates: [][2]float64{{10, 50}, {11, 51}, {12, 52}}}
	ok, err := drive(context.Background(), srv.URL, bl)
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	if ok != 2 {
		t.Errorf("successful requests = %d, want 2 (one of three failed)", ok)
	}
	if len(got) != 3 {
		t.Fatalf("expected all three coordinates to be driven, got %v", got)
	}
	// The committed coordinates must reach the service unrounded, or two runs
	// would not be measuring the same points.
	if !strings.Contains(got[0], "lon=10.000000") || !strings.Contains(got[0], "lat=50.000000") {
		t.Errorf("first request URI = %q, want the committed coordinate", got[0])
	}
}

// A trailing slash on the base URL must not produce a double slash in the path;
// that would 404 and be reported as an error rather than a config slip.
func TestDriveNormalizesTheBaseURL(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	bl := &Baseline{Endpoint: "/api/v1/gazetteer", Coordinates: [][2]float64{{1, 2}}}
	if _, err := drive(context.Background(), srv.URL+"/", bl); err != nil {
		t.Fatalf("drive: %v", err)
	}
	if path != "/api/v1/gazetteer" {
		t.Errorf("path = %q, want /api/v1/gazetteer", path)
	}
}

func TestReportShowsTraceCountBesidePerTrace(t *testing.T) {
	out := captureStdout(t, func() {
		report(&Baseline{MaxRootP95MS: 100}, summary(90, stat("q", "admin", 2, 19)), nil)
	})
	// per_trace divides by the traces that reach the span, so the count has to be
	// visible next to it — without it the number reads as an average over all
	// requests and misleads (it misled me twice).
	if !strings.Contains(out, "Traces") {
		t.Errorf("report must label the trace count:\n%s", out)
	}
	if !strings.Contains(out, "q [admin]") {
		t.Errorf("report must show the grouped span label:\n%s", out)
	}
	if !strings.Contains(out, "perfgate ok") {
		t.Errorf("a clean run must say so:\n%s", out)
	}
}

// captureStdout runs fn with os.Stdout redirected and returns what it wrote.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var sb strings.Builder
		_, _ = io.Copy(&sb, r)
		done <- sb.String()
	}()
	fn()
	_ = w.Close()
	os.Stdout = orig
	return <-done
}
