package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/jobrunner/ortus/internal/config"
)

// TestFeatureGeometryEndToEnd walks the path a map client takes through the real
// App: find a feature with a point query, then redeem the returned
// source_id/layer/id for its outline. The source is stored in EPSG:25832, so the
// default answer (EPSG:4326) proves the whole geometry is reprojected by the real
// SpatiaLite transformer, not only the query point.
func TestFeatureGeometryEndToEnd(t *testing.T) {
	dir := t.TempDir()
	writeSquareGPKG(t, filepath.Join(dir, "utm.gpkg"), 25832, 535000, 5510000, 540000, 5515000)

	cfg := &config.Config{}
	cfg.Storage.Type = config.StorageTypeLocal
	cfg.Storage.LocalPath = dir
	cfg.Metrics.Enabled = false

	ctx := context.Background()
	a, err := New(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Shutdown(context.Background()) })
	if err := a.Registry.LoadAll(ctx); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	h := a.HTTPServer.Router()

	get := func(t *testing.T, path string, into any) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
				t.Fatalf("decode %s: %v\n%s", path, err, rec.Body)
			}
		}
		return rec
	}

	var query struct {
		Results []struct {
			SourceID string `json:"source_id"`
			Kind     string `json:"kind"`
			Features []struct {
				ID    int64  `json:"id"`
				Layer string `json:"layer"`
			} `json:"features"`
		} `json:"results"`
	}
	if rec := get(t, "/api/v1/query?lon=9.52316&lat=49.76028", &query); rec.Code != http.StatusOK {
		t.Fatalf("query status = %d: %s", rec.Code, rec.Body)
	}
	if len(query.Results) != 1 || len(query.Results[0].Features) != 1 {
		t.Fatalf("query results = %+v, want one feature", query.Results)
	}
	res := query.Results[0]
	if res.Kind != "vector" {
		t.Errorf("query result kind = %q, want vector", res.Kind)
	}

	var source struct {
		Kind string `json:"kind"`
	}
	if rec := get(t, "/api/v1/sources/"+res.SourceID, &source); rec.Code != http.StatusOK || source.Kind != "vector" {
		t.Errorf("source status = %d, kind = %q, want 200 and vector", rec.Code, source.Kind)
	}

	type feature struct {
		Type     string     `json:"type"`
		ID       int64      `json:"id"`
		BBox     [4]float64 `json:"bbox"`
		SRID     int        `json:"srid"`
		Geometry struct {
			Type        string          `json:"type"`
			Coordinates [][][][]float64 `json:"coordinates"`
		} `json:"geometry"`
		Properties map[string]any `json:"properties"`
	}
	path := fmt.Sprintf("/api/v1/sources/%s/layers/%s/features/%d", res.SourceID, res.Features[0].Layer, res.Features[0].ID)

	t.Run("default is WGS84", func(t *testing.T) {
		var f feature
		rec := get(t, path, &f)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/geo+json" {
			t.Errorf("Content-Type = %q", ct)
		}
		if f.Type != "Feature" || f.SRID != 4326 || f.Geometry.Type != "MultiPolygon" || f.Properties["name"] != "square" {
			t.Errorf("feature = %+v", f)
		}
		// The 5 km UTM square around Wertheim, in degrees.
		want := [4]float64{9.486, 49.741, 9.556, 49.786}
		for i := range want {
			if d := f.BBox[i] - want[i]; d < -0.002 || d > 0.002 {
				t.Errorf("bbox = %v, want about %v", f.BBox, want)
				break
			}
		}
		if len(f.Geometry.Coordinates) != 1 || len(f.Geometry.Coordinates[0]) != 1 || len(f.Geometry.Coordinates[0][0]) != 5 {
			t.Fatalf("coordinates = %v, want one closed ring of 5 positions", f.Geometry.Coordinates)
		}
		// Every vertex lies inside the reported box — up to the 7 decimals the
		// coordinates are rounded to, which the box is not.
		const eps = 1e-7
		for _, p := range f.Geometry.Coordinates[0][0] {
			if p[0] < f.BBox[0]-eps || p[0] > f.BBox[2]+eps || p[1] < f.BBox[1]-eps || p[1] > f.BBox[3]+eps {
				t.Errorf("vertex %v outside bbox %v", p, f.BBox)
			}
		}
	})

	t.Run("layer srid on request", func(t *testing.T) {
		var f feature
		if rec := get(t, path+"?srid=25832", &f); rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body)
		}
		if f.SRID != 25832 || f.BBox != [4]float64{535000, 5510000, 540000, 5515000} {
			t.Errorf("srid = %d, bbox = %v, want the stored square", f.SRID, f.BBox)
		}
	})

	for name, tc := range map[string]struct {
		path string
		want int
	}{
		"unknown srid":    {path + "?srid=999999", http.StatusBadRequest},
		"unknown feature": {fmt.Sprintf("/api/v1/sources/%s/layers/area/features/999", res.SourceID), http.StatusNotFound},
		"unknown layer":   {fmt.Sprintf("/api/v1/sources/%s/layers/nope/features/1", res.SourceID), http.StatusNotFound},
		"unknown source":  {"/api/v1/sources/nope/layers/area/features/1", http.StatusNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			var ignored map[string]any
			if rec := get(t, tc.path, &ignored); rec.Code != tc.want {
				t.Errorf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body)
			}
		})
	}
}
