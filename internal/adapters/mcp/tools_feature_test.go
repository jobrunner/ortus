package mcp_test

import (
	"context"
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/input"
)

// featureQueryService overrides FeatureGeometry of a real query service with a
// canned answer, recording the reference the tool asked for.
type featureQueryService struct {
	input.QueryService
	result *domain.FeatureGeometry
	err    error
	got    domain.FeatureRef
}

func (f *featureQueryService) FeatureGeometry(_ context.Context, ref domain.FeatureRef) (*domain.FeatureGeometry, error) {
	f.got = ref
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

func TestGetFeatureGeometryTool(t *testing.T) {
	deps, _ := diagDeps(t)
	svc := &featureQueryService{
		QueryService: deps.QueryService,
		result: &domain.FeatureGeometry{
			SourceID:   "ffh",
			Layer:      "areas",
			ID:         42,
			Properties: map[string]interface{}{"name": "Moor"},
			GeoJSON:    []byte(`{"type":"MultiPolygon","coordinates":[[[[12,0],[14,0],[14,4],[12,0]]]]}`),
			Extent:     domain.Extent{MinX: 12, MinY: 0, MaxX: 14, MaxY: 4, SRID: 25832},
			License:    domain.License{Name: "CC BY 4.0", Attribution: "© Example"},
		},
	}
	deps.QueryService = svc
	session := serveDeps(t, deps)

	out := toolJSON(t, callTool(t, session, "get_feature_geometry", map[string]any{
		"source_id": "ffh", "layer": "areas", "feature_id": 42, "srid": 25832,
	}))

	want := domain.FeatureRef{SourceID: "ffh", Layer: "areas", ID: 42, SRID: 25832}
	if svc.got != want {
		t.Errorf("asked for %+v, want %+v", svc.got, want)
	}
	if out["type"] != "Feature" || out["id"] != float64(42) || out["srid"] != float64(25832) {
		t.Errorf("type/id/srid = %v/%v/%v", out["type"], out["id"], out["srid"])
	}
	if bbox, _ := out["bbox"].([]any); len(bbox) != 4 || bbox[0] != float64(12) || bbox[3] != float64(4) {
		t.Errorf("bbox = %v", out["bbox"])
	}
	if geom, _ := out["geometry"].(map[string]any); geom["type"] != "MultiPolygon" {
		t.Errorf("geometry = %v", out["geometry"])
	}
	if props, _ := out["properties"].(map[string]any); props["name"] != "Moor" {
		t.Errorf("properties = %v", out["properties"])
	}
	if lic, _ := out["license"].(map[string]any); lic["name"] != "CC BY 4.0" || lic["attribution"] != "© Example" {
		t.Errorf("license = %v", out["license"])
	}
}

func TestGetFeatureGeometryToolErrors(t *testing.T) {
	deps, _ := diagDeps(t)
	deps.QueryService = &featureQueryService{QueryService: deps.QueryService, err: domain.ErrFeatureNotFound}
	session := serveDeps(t, deps)

	for name, args := range map[string]map[string]any{
		"missing source":  {"source_id": " ", "layer": "areas", "feature_id": 1},
		"missing layer":   {"source_id": "ffh", "layer": "", "feature_id": 1},
		"unknown feature": {"source_id": "ffh", "layer": "areas", "feature_id": 1},
	} {
		if res := callTool(t, session, "get_feature_geometry", args); !res.IsError {
			t.Errorf("%s: want a tool error, got %+v", name, res.Content)
		}
	}
}
