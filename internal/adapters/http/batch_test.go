package http

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/metric/noop"

	"github.com/jobrunner/ortus/internal/application"
	"github.com/jobrunner/ortus/internal/config"
	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/input"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// newBatchServer builds a Server for batch tests with a mock (empty) source pool,
// small caps (so cap behavior is testable), and an optional gazetteer.
func newBatchServer(t *testing.T, gaz input.Gazetteer, maxSync, maxPoints int) *Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	reg := application.NewSourceRegistry(
		[]output.SpatialSource{&mockRepository{}}, &mockStorage{},
		noop.NewMeterProvider().Meter("test"), output.NoOpTracer{}, logger, "/tmp")
	_ = reg.LoadAll(context.Background())
	health := application.NewHealthService(reg, true, output.NoOpTracer{})
	query := application.NewQueryService(reg, nil, noop.NewMeterProvider().Meter("test"),
		output.NoOpTracer{}, logger, application.QueryServiceConfig{})
	return NewServer(
		config.ServerConfig{Host: "localhost", Port: 8080, ReadTimeout: time.Second, WriteTimeout: time.Second},
		query, reg, health, nil, logger, false,
		ServerOptions{Gazetteer: gaz, GazetteerLicense: sampleGazetteerLicense(),
			BatchMaxSyncPoints: maxSync, BatchMaxPoints: maxPoints},
	)
}

func doBatch(t *testing.T, srv *Server, body, accept string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/query/batch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	return rec
}

// TestQueryBatchSync: a sync POST returns {results,total,processing_time_ms}, one
// item per point in order, each with the echo id, coordinate and wgs84 block.
func TestQueryBatchSync(t *testing.T) {
	srv := newBatchServer(t, nil, 1000, 10000)
	rec := doBatch(t, srv, `{"points":[{"id":"a","lon":9.93,"lat":49.79},{"id":"b","lon":13.4,"lat":52.5}]}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Results []map[string]any `json:"results"`
		Total   int              `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Total != 2 || len(resp.Results) != 2 {
		t.Fatalf("total=%d results=%d, want 2", resp.Total, len(resp.Results))
	}
	if resp.Results[0]["id"] != "a" || resp.Results[1]["id"] != "b" {
		t.Errorf("echo ids = %v/%v, want a/b", resp.Results[0]["id"], resp.Results[1]["id"])
	}
	if w, ok := resp.Results[0]["wgs84"].(map[string]any); !ok || w["lon"] != 9.93 {
		t.Errorf("item 0 wgs84 = %v, want {lon:9.93,...}", resp.Results[0]["wgs84"])
	}
}

// TestQueryBatchEchoIndex: points without ids echo their 0-based index.
func TestQueryBatchEchoIndex(t *testing.T) {
	srv := newBatchServer(t, nil, 1000, 10000)
	rec := doBatch(t, srv, `{"points":[{"lon":1,"lat":1},{"lon":2,"lat":2}]}`, "")
	var resp struct {
		Results []map[string]any `json:"results"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Results[0]["id"] != "0" || resp.Results[1]["id"] != "1" {
		t.Errorf("ids = %v/%v, want 0/1", resp.Results[0]["id"], resp.Results[1]["id"])
	}
}

// TestQueryBatchNDJSON: Accept: application/x-ndjson streams one JSON object/line.
func TestQueryBatchNDJSON(t *testing.T) {
	srv := newBatchServer(t, nil, 1000, 10000)
	rec := doBatch(t, srv, `{"points":[{"id":"a","lon":1,"lat":1},{"id":"b","lon":2,"lat":2}]}`, "application/x-ndjson")
	if ct := rec.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Errorf("Content-Type = %q, want application/x-ndjson", ct)
	}
	var ids []string
	sc := bufio.NewScanner(bytes.NewReader(rec.Body.Bytes()))
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var item map[string]any
		if err := json.Unmarshal(line, &item); err != nil {
			t.Fatalf("line not valid JSON: %q (%v)", line, err)
		}
		ids = append(ids, item["id"].(string))
	}
	if len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Errorf("streamed ids = %v, want [a b]", ids)
	}
}

// TestQueryBatchSyncCapExceeded: over the sync cap without streaming → 413.
func TestQueryBatchSyncCapExceeded(t *testing.T) {
	srv := newBatchServer(t, nil, 2, 10)
	rec := doBatch(t, srv, `{"points":[{"lon":1,"lat":1},{"lon":2,"lat":2},{"lon":3,"lat":3}]}`, "")
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "x-ndjson") {
		t.Errorf("413 body should hint at streaming, got %s", rec.Body.String())
	}
	// Same batch WITH streaming accepted → 200.
	rec2 := doBatch(t, srv, `{"points":[{"lon":1,"lat":1},{"lon":2,"lat":2},{"lon":3,"lat":3}]}`, "application/x-ndjson")
	if rec2.Code != http.StatusOK {
		t.Errorf("streamed over-sync-cap status = %d, want 200", rec2.Code)
	}
}

// TestQueryBatchHardCapExceeded: over the hard cap → 400 regardless of streaming.
func TestQueryBatchHardCapExceeded(t *testing.T) {
	srv := newBatchServer(t, nil, 2, 2)
	rec := doBatch(t, srv, `{"points":[{"lon":1,"lat":1},{"lon":2,"lat":2},{"lon":3,"lat":3}]}`, "application/x-ndjson")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestQueryBatchItemError: a bad point yields a per-item error object; the batch
// still returns 200 and the other points succeed.
func TestQueryBatchItemError(t *testing.T) {
	srv := newBatchServer(t, nil, 1000, 10000)
	rec := doBatch(t, srv, `{"points":[{"id":"bad"},{"id":"ok","lon":1,"lat":1}]}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		Results []map[string]any `json:"results"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if _, hasErr := resp.Results[0]["error"]; !hasErr {
		t.Errorf("item 0 should carry an error object, got %v", resp.Results[0])
	}
	if _, hasErr := resp.Results[1]["error"]; hasErr {
		t.Errorf("item 1 should NOT have an error, got %v", resp.Results[1])
	}
}

// TestQueryBatchMGRS: a point given as mgrs resolves via its own UTM SRID,
// independent of the batch-level default SRID, and mixes cleanly with
// lon/lat points in the same request.
func TestQueryBatchMGRS(t *testing.T) {
	srv := newBatchServer(t, nil, 1000, 10000)
	rec := doBatch(t, srv,
		`{"points":[{"id":"a","mgrs":"31U CT 03760 87415"},{"id":"b","lon":1,"lat":1}]}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, hasErr := resp.Results[0]["error"]; hasErr {
		t.Fatalf("item 0 (mgrs) should not error, got %v", resp.Results[0])
	}
	// 31U CT 03760 87415 decodes to UTM zone 31N (SRID 32631), easting 303760,
	// northing 5787415 — matches the domain.ParseMGRS test vectors. This test
	// server has no CoordinateTransformer configured (mock repository), so no
	// wgs84 block is expected here — that reprojection is covered by the
	// domain-level ParseMGRS tests and the single-point handler test.
	c, ok := resp.Results[0]["coordinate"].(map[string]any)
	if !ok {
		t.Fatalf("item 0 missing coordinate block: %v", resp.Results[0])
	}
	if srid, _ := c["srid"].(float64); srid != 32631 {
		t.Errorf("item 0 coordinate.srid = %v, want 32631", c["srid"])
	}
	if x, _ := c["x"].(float64); x != 303760 {
		t.Errorf("item 0 coordinate.x = %v, want 303760", c["x"])
	}
	if y, _ := c["y"].(float64); y != 5787415 {
		t.Errorf("item 0 coordinate.y = %v, want 5787415", c["y"])
	}
	if _, hasErr := resp.Results[1]["error"]; hasErr {
		t.Errorf("item 1 (lon/lat) should not error, got %v", resp.Results[1])
	}
}

// TestQueryBatchMGRSInvalid: an invalid mgrs string surfaces as a per-item
// error, not a batch-wide failure.
func TestQueryBatchMGRSInvalid(t *testing.T) {
	srv := newBatchServer(t, nil, 1000, 10000)
	rec := doBatch(t, srv, `{"points":[{"id":"bad","mgrs":"not-mgrs"}]}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		Results []map[string]any `json:"results"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if _, hasErr := resp.Results[0]["error"]; !hasErr {
		t.Errorf("item 0 should carry an error object, got %v", resp.Results[0])
	}
}

// TestQueryBatchEmptyPoints: no points → 400.
func TestQueryBatchEmptyPoints(t *testing.T) {
	srv := newBatchServer(t, nil, 1000, 10000)
	if rec := doBatch(t, srv, `{"points":[]}`, ""); rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// TestQueryBatchBodyTooLarge: a request body over the MaxBytesReader limit is a
// 413 (not a generic 400 "invalid JSON"), so oversized payloads are distinguishable.
func TestQueryBatchBodyTooLarge(t *testing.T) {
	srv := newBatchServer(t, nil, 2, 2) // limit = 2*512 + 64KiB ≈ 65 KiB
	huge := strings.Repeat("x", 200_000)
	rec := doBatch(t, srv, `{"points":[{"id":"`+huge+`","lon":1,"lat":1}]}`, "")
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 for an oversized body", rec.Code)
	}
}

// TestQueryBatchGazetteer: with-gazetteer=true attaches a gazetteer block per item.
func TestQueryBatchGazetteer(t *testing.T) {
	srv := newBatchServer(t, fakeGazetteer{loc: sampleLocality(), fix: sampleFix()}, 1000, 10000)
	rec := doBatch(t, srv, `{"with-gazetteer":true,"points":[{"id":"a","lon":9.93,"lat":49.79}]}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Results []map[string]any `json:"results"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if _, ok := resp.Results[0]["gazetteer"].(map[string]any); !ok {
		t.Errorf("item 0 should carry a gazetteer block, got %v", resp.Results[0])
	}
}

// TestQueryBatchGazetteerDefaultOn: omitting with-gazetteer enriches by default
// (consistent with /query's opt-out behavior).
func TestQueryBatchGazetteerDefaultOn(t *testing.T) {
	srv := newBatchServer(t, fakeGazetteer{loc: sampleLocality(), fix: sampleFix()}, 1000, 10000)
	rec := doBatch(t, srv, `{"points":[{"id":"a","lon":9.93,"lat":49.79}]}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Results []map[string]any `json:"results"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if _, ok := resp.Results[0]["gazetteer"].(map[string]any); !ok {
		t.Errorf("omitted with-gazetteer should default ON; item 0 lacks gazetteer: %v", resp.Results[0])
	}
}

// TestQueryBatchGazetteerOptOut: an explicit with-gazetteer=false skips enrichment.
func TestQueryBatchGazetteerOptOut(t *testing.T) {
	srv := newBatchServer(t, fakeGazetteer{loc: sampleLocality(), fix: sampleFix()}, 1000, 10000)
	rec := doBatch(t, srv, `{"with-gazetteer":false,"points":[{"id":"a","lon":9.93,"lat":49.79}]}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Results []map[string]any `json:"results"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if _, ok := resp.Results[0]["gazetteer"]; ok {
		t.Errorf("with-gazetteer=false must skip enrichment; item 0 has a gazetteer block: %v", resp.Results[0])
	}
}

// TestLessTileLocalityGroupsByTile verifies the enrichment sort key groups points
// that share a 1° DEM tile contiguously (so per-point lookups reuse warm tile
// handles), and that it is a strict, deterministic ordering.
func TestLessTileLocalityGroupsByTile(t *testing.T) {
	c := domain.NewWGS84Coordinate
	// Two tiles interleaved on input; a negative-lon point checks floor (not trunc).
	pts := []domain.Coordinate{
		c(9.9, 49.1), c(-3.2, 40.8), c(9.1, 49.9), c(-3.9, 40.1), c(9.5, 49.5),
	}
	order := make([]int, len(pts))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return lessTileLocality(pts[order[a]], pts[order[b]]) })

	// Group each sorted point by its 1° tile; the sequence of distinct tiles must
	// have no tile reappearing after a different one (i.e. same-tile points are contiguous).
	seen := map[[2]int]bool{}
	var last [2]int
	haveLast := false
	for _, idx := range order {
		tile := [2]int{int(math.Floor(pts[idx].Y)), int(math.Floor(pts[idx].X))}
		if !haveLast || tile != last {
			if seen[tile] {
				t.Fatalf("tile %v reappeared non-contiguously in order %v", tile, order)
			}
			seen[tile] = true
			last, haveLast = tile, true
		}
	}
	// Strictness: a coordinate is never less than itself.
	if lessTileLocality(pts[0], pts[0]) {
		t.Error("lessTileLocality must be irreflexive")
	}
}
