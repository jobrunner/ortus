package geopackage_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jobrunner/ortus/internal/adapters/geopackage"
	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// recordingTracer captures the spans a decorator opens, so a test can assert on
// names and attributes without an OTel pipeline.
type recordingTracer struct{ spans []*recordedSpan }

type recordedSpan struct {
	name    string
	attrs   map[string]any
	status  output.StatusCode
	err     error
	ended   bool
	statusM string
}

func (r *recordingTracer) Start(ctx context.Context, name string, opts ...output.StartSpanOption) (context.Context, output.Span) {
	var o output.StartSpanOptions
	for _, opt := range opts {
		opt(&o)
	}
	sp := &recordedSpan{name: name, attrs: map[string]any{}}
	for _, a := range o.Attributes {
		sp.attrs[a.Key] = a.Value
	}
	r.spans = append(r.spans, sp)
	return ctx, sp
}

func (s *recordedSpan) SetAttributes(attrs ...output.Attribute) {
	for _, a := range attrs {
		s.attrs[a.Key] = a.Value
	}
}
func (s *recordedSpan) AddEvent(string, ...output.Attribute) {}
func (s *recordedSpan) RecordError(err error)                { s.err = err }
func (s *recordedSpan) SetStatus(c output.StatusCode, m string) {
	s.status = c
	s.statusM = m
}
func (s *recordedSpan) End() { s.ended = true }

// stubIndex is a SpatialIndex that returns canned results or a canned error.
type stubIndex struct{ err error }

func (s stubIndex) QueryKNN(context.Context, string, domain.Coordinate, int, float64, *output.Filter) ([]output.NearFeature, error) {
	if s.err != nil {
		return nil, s.err
	}
	return []output.NearFeature{{}, {}}, nil
}

func (s stubIndex) PointInPolygon(context.Context, string, domain.Coordinate) ([]domain.Feature, error) {
	if s.err != nil {
		return nil, s.err
	}
	return []domain.Feature{{}}, nil
}

func (s stubIndex) ResolveChains(_ context.Context, _ string, fromFIDs []int64, _ output.AdminColumns) (map[int64][]output.AdminRow, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := map[int64][]output.AdminRow{}
	for _, fid := range fromFIDs {
		out[fid] = []output.AdminRow{{}, {}, {}}
	}
	return out, nil
}

func (s stubIndex) DistanceKM(context.Context, domain.Coordinate, domain.Coordinate) (float64, error) {
	return 1, s.err
}

func (s stubIndex) Azimuth(context.Context, domain.Coordinate, domain.Coordinate) (float64, error) {
	return 90, s.err
}

func TestTracedSpatialIndexRecordsLayerAndCounts(t *testing.T) {
	tr := &recordingTracer{}
	idx := geopackage.NewTracedSpatialIndex(stubIndex{}, tr)
	ctx := context.Background()
	p := domain.NewWGS84Coordinate(10, 50)

	if _, err := idx.PointInPolygon(ctx, "mountains", p); err != nil {
		t.Fatal(err)
	}
	if _, err := idx.QueryKNN(ctx, "places", p, 5, 30, &output.Filter{Column: "place", Values: []any{"town", "city"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := idx.ResolveChains(ctx, "admin_levels", []int64{42, 43}, output.AdminColumns{}); err != nil {
		t.Fatal(err)
	}

	if len(tr.spans) != 3 {
		t.Fatalf("expected 3 spans, got %d", len(tr.spans))
	}
	for _, sp := range tr.spans {
		if !sp.ended {
			t.Errorf("%s was not ended", sp.name)
		}
		if sp.status != output.StatusOK {
			t.Errorf("%s status = %v, want OK", sp.name, sp.status)
		}
	}

	pip := tr.spans[0]
	if pip.name != "SpatialIndex.PointInPolygon" {
		t.Errorf("name = %q", pip.name)
	}
	// The layer attribute is what lets the perf gate budget per layer instead of
	// lumping every PointInPolygon together, so it must always be present.
	if pip.attrs["spatial.layer"] != "mountains" {
		t.Errorf("spatial.layer = %v, want mountains", pip.attrs["spatial.layer"])
	}
	if pip.attrs["spatial.result.count"] != 1 {
		t.Errorf("result.count = %v, want 1", pip.attrs["spatial.result.count"])
	}

	knn := tr.spans[1]
	if knn.attrs["spatial.knn.filtered"] != true {
		t.Errorf("knn.filtered = %v, want true", knn.attrs["spatial.knn.filtered"])
	}
	if knn.attrs["spatial.knn.filter_column"] != "place" {
		t.Errorf("knn.filter_column = %v", knn.attrs["spatial.knn.filter_column"])
	}
	// Values are counted, not recorded: the count explains the query plan without
	// putting query contents on a span.
	if knn.attrs["spatial.knn.filter_values"] != 2 {
		t.Errorf("knn.filter_values = %v, want 2", knn.attrs["spatial.knn.filter_values"])
	}
	if _, present := knn.attrs["spatial.knn.filter_value_list"]; present {
		t.Error("filter values must not be recorded verbatim")
	}

	chain := tr.spans[2]
	if chain.name != "SpatialIndex.ResolveChains" {
		t.Errorf("name = %q", chain.name)
	}
	// Seeds in, chains out: the pair is what shows the batch is actually batching
	// rather than degenerating into one call per id.
	if chain.attrs["spatial.chains.seeds"] != 2 {
		t.Errorf("chains.seeds = %v, want 2", chain.attrs["spatial.chains.seeds"])
	}
	if chain.attrs["spatial.chains.resolved"] != 2 {
		t.Errorf("chains.resolved = %v, want 2", chain.attrs["spatial.chains.resolved"])
	}
}

func TestTracedSpatialIndexRecordsErrors(t *testing.T) {
	want := errors.New("spatialite exploded")
	tr := &recordingTracer{}
	idx := geopackage.NewTracedSpatialIndex(stubIndex{err: want}, tr)

	if _, err := idx.PointInPolygon(context.Background(), "islands", domain.NewWGS84Coordinate(0, 0)); !errors.Is(err, want) {
		t.Fatalf("error not propagated: %v", err)
	}
	if len(tr.spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(tr.spans))
	}
	sp := tr.spans[0]
	if !errors.Is(sp.err, want) {
		t.Errorf("span error = %v, want %v", sp.err, want)
	}
	if sp.status != output.StatusError {
		t.Errorf("span status = %v, want Error", sp.status)
	}
	if !sp.ended {
		t.Error("span was not ended on the error path")
	}
}

// A nil tracer must be safe: the composition root wires the decorator
// unconditionally, and tracing is off by default.
func TestTracedSpatialIndexNilTracerIsSafe(t *testing.T) {
	idx := geopackage.NewTracedSpatialIndex(stubIndex{}, nil)
	if _, err := idx.Azimuth(context.Background(),
		domain.NewWGS84Coordinate(0, 0), domain.NewWGS84Coordinate(1, 1)); err != nil {
		t.Fatal(err)
	}
}
