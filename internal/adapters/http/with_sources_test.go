package http

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"go.opentelemetry.io/otel/metric/noop"

	"github.com/jobrunner/ortus/internal/application"
	"github.com/jobrunner/ortus/internal/config"
	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/input"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// spyQueryService records QueryPoint/QueryBatch calls and returns a canned
// one-source response, so tests can tell "the PiP query ran" apart from "it was
// skipped" — the registry in these tests is empty, so real responses carry no
// results either way.
type spyQueryService struct {
	pointCalls int
	batchCalls int
}

func (s *spyQueryService) QueryPoint(_ context.Context, req domain.QueryRequest) (*domain.QueryResponse, error) {
	s.pointCalls++
	return &domain.QueryResponse{
		Coordinate: req.Coordinate,
		Results:    []domain.QueryResult{{SourceID: "src-1", SourceName: "Source One"}},
	}, nil
}

func (s *spyQueryService) QueryPointInSource(_ context.Context, _ string, _ domain.QueryRequest) (*domain.QueryResult, error) {
	return nil, nil
}

func (s *spyQueryService) QueryBatch(_ context.Context, coords []domain.Coordinate, _, _ []string) ([]*domain.QueryResponse, error) {
	s.batchCalls++
	out := make([]*domain.QueryResponse, len(coords))
	for i, c := range coords {
		out[i] = &domain.QueryResponse{
			Coordinate: c,
			Results:    []domain.QueryResult{{SourceID: "src-1", SourceName: "Source One"}},
		}
	}
	return out, nil
}

// newSpyServer builds a Server whose query service is the spy, with an optional
// gazetteer so the with-sources / with-gazetteer independence is observable.
func newSpyServer(t *testing.T, spy *spyQueryService, gaz input.Gazetteer) *Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	reg := application.NewSourceRegistry(
		[]output.SpatialSource{&mockRepository{}}, &mockStorage{},
		noop.NewMeterProvider().Meter("test"), output.NoOpTracer{}, logger, "/tmp",
	)
	_ = reg.LoadAll(context.Background())
	health := application.NewHealthService(reg, true, output.NoOpTracer{})
	return NewServer(
		config.ServerConfig{Host: "localhost", Port: 8080, ReadTimeout: time.Second, WriteTimeout: time.Second},
		spy, reg, health, nil, logger, false,
		ServerOptions{Gazetteer: gaz, GazetteerLicense: sampleGazetteerLicense()},
	)
}

// TestQueryWithSourcesDefaultOn: without the flag (and with any non-falsy value)
// the PiP query runs and its results are returned.
func TestQueryWithSourcesDefaultOn(t *testing.T) {
	for _, q := range []string{"", "&with-sources=1", "&with-sources=true", "&with-sources=maybe"} {
		spy := &spyQueryService{}
		srv := newSpyServer(t, spy, nil)
		rec, body := doGET(t, srv, "/api/v1/query?lon=9.93&lat=49.79"+q)
		if rec.Code != http.StatusOK {
			t.Fatalf("%q: status = %d, want 200 (body: %s)", q, rec.Code, rec.Body.String())
		}
		if spy.pointCalls != 1 {
			t.Errorf("%q: QueryPoint calls = %d, want 1", q, spy.pointCalls)
		}
		if results, ok := body["results"].([]any); !ok || len(results) != 1 {
			t.Errorf("%q: results = %v, want 1 entry", q, body["results"])
		}
	}
}

// TestQueryWithSourcesOptOut: an explicit falsy with-sources skips the PiP query;
// the response keeps its shape (results: [], total_features: 0) and the gazetteer
// block still arrives (independent switches, gazetteer default on).
func TestQueryWithSourcesOptOut(t *testing.T) {
	for _, off := range []string{"0", "false", "no", "off", "FALSE"} {
		spy := &spyQueryService{}
		srv := newSpyServer(t, spy, fakeGazetteer{loc: sampleLocality(), fix: sampleFix()})
		rec, body := doGET(t, srv, "/api/v1/query?lon=9.93&lat=49.79&with-sources="+off)
		if rec.Code != http.StatusOK {
			t.Fatalf("with-sources=%s: status = %d, want 200 (body: %s)", off, rec.Code, rec.Body.String())
		}
		if spy.pointCalls != 0 {
			t.Errorf("with-sources=%s: QueryPoint calls = %d, want 0", off, spy.pointCalls)
		}
		results, ok := body["results"].([]any)
		if !ok || len(results) != 0 {
			t.Errorf("with-sources=%s: results = %v, want empty array", off, body["results"])
		}
		if tf, ok := body["total_features"].(float64); !ok || tf != 0 {
			t.Errorf("with-sources=%s: total_features = %v, want 0", off, body["total_features"])
		}
		if _, ok := body["processing_time_ms"]; !ok {
			t.Errorf("with-sources=%s: processing_time_ms missing", off)
		}
		if _, ok := body["gazetteer"].(map[string]any); !ok {
			t.Errorf("with-sources=%s: gazetteer block missing (switches must be independent)", off)
		}
		if _, ok := body["wgs84"].(map[string]any); !ok {
			t.Errorf("with-sources=%s: wgs84 block missing", off)
		}
	}
}

// TestQueryBothSwitchesOff: turning off sources AND gazetteer is allowed — the
// response degrades to coordinate + wgs84 (+ empty results), no error.
func TestQueryBothSwitchesOff(t *testing.T) {
	spy := &spyQueryService{}
	srv := newSpyServer(t, spy, fakeGazetteer{loc: sampleLocality(), fix: sampleFix()})
	rec, body := doGET(t, srv, "/api/v1/query?lon=9.93&lat=49.79&with-sources=0&with-gazetteer=0")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if spy.pointCalls != 0 {
		t.Errorf("QueryPoint calls = %d, want 0", spy.pointCalls)
	}
	if _, present := body["gazetteer"]; present {
		t.Errorf("gazetteer block present, want absent")
	}
	if results, ok := body["results"].([]any); !ok || len(results) != 0 {
		t.Errorf("results = %v, want empty array", body["results"])
	}
	if _, ok := body["wgs84"].(map[string]any); !ok {
		t.Errorf("wgs84 block missing")
	}
}

// TestQueryWithSourcesOffInvalidCoordinate: skipping the PiP query must not skip
// coordinate validation — an out-of-range latitude is still a 400.
func TestQueryWithSourcesOffInvalidCoordinate(t *testing.T) {
	spy := &spyQueryService{}
	srv := newSpyServer(t, spy, nil)
	rec, _ := doGET(t, srv, "/api/v1/query?lon=9.93&lat=99&with-sources=0")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	if spy.pointCalls != 0 {
		t.Errorf("QueryPoint calls = %d, want 0", spy.pointCalls)
	}
}

// TestSourcesRequested mirrors TestGazetteerEnrichmentRequested: default on,
// only explicit falsy values (case-insensitive) turn the PiP query off.
func TestSourcesRequested(t *testing.T) {
	for _, v := range []string{"", "1", "true", "yes", "on", "2", "maybe"} {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/query?with-sources="+v, nil)
		if !sourcesRequested(r) {
			t.Errorf("with-sources=%q = false, want on (default)", v)
		}
	}
	for _, v := range []string{"0", "false", "no", "off", "FALSE", "Off"} {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/query?with-sources="+v, nil)
		if sourcesRequested(r) {
			t.Errorf("with-sources=%q = true, want off", v)
		}
	}
}

// doSpyBatch posts a batch body against a spy-backed server and decodes the
// sync response.
func doSpyBatch(t *testing.T, spy *spyQueryService, gaz input.Gazetteer, body string) (rec *httptest.ResponseRecorder, items []map[string]any) {
	t.Helper()
	srv := newSpyServer(t, spy, gaz)
	rec = doBatch(t, srv, body, "")
	var resp struct {
		Results []map[string]any `json:"results"`
	}
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v (body: %s)", err, rec.Body.String())
		}
	}
	return rec, resp.Results
}

// TestQueryBatchWithSourcesDefaultOn: omitting with-sources keeps the PiP batch
// query on.
func TestQueryBatchWithSourcesDefaultOn(t *testing.T) {
	spy := &spyQueryService{}
	rec, items := doSpyBatch(t, spy, nil, `{"points":[{"id":"a","lon":9.93,"lat":49.79}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if spy.batchCalls != 1 {
		t.Errorf("QueryBatch calls = %d, want 1", spy.batchCalls)
	}
	if results, ok := items[0]["results"].([]any); !ok || len(results) != 1 {
		t.Errorf("item results = %v, want 1 entry", items[0]["results"])
	}
}

// TestQueryBatchWithSourcesOptOut: "with-sources": false skips the PiP batch
// query; each item keeps its shape (coordinate, results: []) and gazetteer
// enrichment still runs (default on, independent switch).
func TestQueryBatchWithSourcesOptOut(t *testing.T) {
	spy := &spyQueryService{}
	gaz := fakeGazetteer{loc: sampleLocality(), fix: sampleFix()}
	rec, items := doSpyBatch(t, spy, gaz,
		`{"with-sources":false,"points":[{"id":"a","lon":9.93,"lat":49.79},{"id":"b"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if spy.batchCalls != 0 {
		t.Errorf("QueryBatch calls = %d, want 0", spy.batchCalls)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if results, ok := items[0]["results"].([]any); !ok || len(results) != 0 {
		t.Errorf("item 0 results = %v, want empty array", items[0]["results"])
	}
	if _, ok := items[0]["coordinate"].(map[string]any); !ok {
		t.Errorf("item 0 coordinate missing")
	}
	if _, ok := items[0]["gazetteer"].(map[string]any); !ok {
		t.Errorf("item 0 gazetteer missing (switches must be independent)")
	}
	// Point "b" has no coordinates — its per-item error must survive the skip.
	if _, ok := items[1]["error"].(map[string]any); !ok {
		t.Errorf("item 1 error missing, got %v", items[1])
	}
}

// TestQueryBatchBothSwitchesOff: sources and gazetteer both off — items degrade
// to id + coordinate + wgs84 (+ empty results), no enrichment.
func TestQueryBatchBothSwitchesOff(t *testing.T) {
	spy := &spyQueryService{}
	gaz := fakeGazetteer{loc: sampleLocality(), fix: sampleFix()}
	rec, items := doSpyBatch(t, spy, gaz,
		`{"with-sources":false,"with-gazetteer":false,"points":[{"id":"a","lon":9.93,"lat":49.79}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if _, present := items[0]["gazetteer"]; present {
		t.Errorf("item 0 gazetteer present, want absent")
	}
	if results, ok := items[0]["results"].([]any); !ok || len(results) != 0 {
		t.Errorf("item 0 results = %v, want empty array", items[0]["results"])
	}
	if _, ok := items[0]["wgs84"].(map[string]any); !ok {
		t.Errorf("item 0 wgs84 missing")
	}
}

// TestQueryBatchWithSourcesConflict: restricting to specific sources while
// turning sources off contradicts itself → 400.
func TestQueryBatchWithSourcesConflict(t *testing.T) {
	spy := &spyQueryService{}
	rec, _ := doSpyBatch(t, spy, nil,
		`{"with-sources":false,"sources":["src-1"],"points":[{"id":"a","lon":9.93,"lat":49.79}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	if spy.batchCalls != 0 {
		t.Errorf("QueryBatch calls = %d, want 0", spy.batchCalls)
	}
}
