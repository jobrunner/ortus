package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
)

// featureQueryService answers FeatureGeometry with a canned result or error and
// records the reference it was asked for.
type featureQueryService struct {
	spyQueryService
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

func sampleFeatureGeometry() *domain.FeatureGeometry {
	return &domain.FeatureGeometry{
		SourceID:   "ffh",
		Layer:      "areas",
		ID:         42,
		Properties: map[string]interface{}{"name": "Moor"},
		GeoJSON:    []byte(`{"type":"MultiPolygon","coordinates":[[[[12,0],[14,0],[14,4],[12,4],[12,0]]]]}`),
		Extent:     domain.Extent{MinX: 12, MinY: 0, MaxX: 14, MaxY: 4, SRID: domain.SRIDWGS84},
		License:    domain.License{Name: "CC BY 4.0", URL: "https://example.org/by", Attribution: "© Example"},
	}
}

func TestGetFeature(t *testing.T) {
	svc := &featureQueryService{result: sampleFeatureGeometry()}
	srv := newSpyServer(t, &svc.spyQueryService, nil)
	srv.queryService = svc

	rec, body := doGET(t, srv, "/api/v1/sources/ffh/layers/areas/features/42")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/geo+json" {
		t.Errorf("Content-Type = %q, want application/geo+json", ct)
	}
	want := domain.FeatureRef{SourceID: "ffh", Layer: "areas", ID: 42, SRID: domain.SRIDWGS84}
	if svc.got != want {
		t.Errorf("asked for %+v, want %+v", svc.got, want)
	}

	if body["type"] != "Feature" || body["id"] != float64(42) {
		t.Errorf("type/id = %v/%v", body["type"], body["id"])
	}
	if body["source_id"] != "ffh" || body["layer"] != "areas" || body["srid"] != float64(4326) {
		t.Errorf("origin = %v/%v/%v", body["source_id"], body["layer"], body["srid"])
	}
	if got := fmt.Sprint(body["bbox"]); got != "[12 0 14 4]" {
		t.Errorf("bbox = %s, want [12 0 14 4]", got)
	}
	geom, _ := body["geometry"].(map[string]any)
	if geom["type"] != "MultiPolygon" {
		t.Errorf("geometry = %v", body["geometry"])
	}
	props, _ := body["properties"].(map[string]any)
	if props["name"] != "Moor" {
		t.Errorf("properties = %v", body["properties"])
	}
	lic, _ := body["license"].(map[string]any)
	if lic["name"] != "CC BY 4.0" || lic["url"] != "https://example.org/by" || lic["attribution"] != "© Example" {
		t.Errorf("license = %v", body["license"])
	}
}

func TestGetFeatureRequestedSRID(t *testing.T) {
	svc := &featureQueryService{result: sampleFeatureGeometry()}
	srv := newSpyServer(t, &svc.spyQueryService, nil)
	srv.queryService = svc

	if rec, _ := doGET(t, srv, "/api/v1/sources/ffh/layers/areas/features/42?srid=25832"); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if svc.got.SRID != 25832 {
		t.Errorf("SRID = %d, want 25832", svc.got.SRID)
	}
}

// A package without license and a feature without attributes still yield a
// valid GeoJSON Feature: no license member, an empty properties object.
func TestGetFeatureWithoutLicenseAndProperties(t *testing.T) {
	fg := sampleFeatureGeometry()
	fg.License, fg.Properties = domain.License{}, nil
	svc := &featureQueryService{result: fg}
	srv := newSpyServer(t, &svc.spyQueryService, nil)
	srv.queryService = svc

	rec, body := doGET(t, srv, "/api/v1/sources/ffh/layers/areas/features/42")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if _, has := body["license"]; has {
		t.Errorf("license present: %v", body["license"])
	}
	if props, ok := body["properties"].(map[string]any); !ok || len(props) != 0 {
		t.Errorf("properties = %v, want {}", body["properties"])
	}
}

func TestGetFeatureErrors(t *testing.T) {
	tests := []struct {
		name string
		path string
		err  error
		want int
	}{
		{"non-numeric id", "/api/v1/sources/ffh/layers/areas/features/abc", nil, http.StatusBadRequest},
		{"zero id", "/api/v1/sources/ffh/layers/areas/features/0", nil, http.StatusBadRequest},
		{"non-numeric srid", "/api/v1/sources/ffh/layers/areas/features/1?srid=wgs84", nil, http.StatusBadRequest},
		{"negative srid", "/api/v1/sources/ffh/layers/areas/features/1?srid=-1", nil, http.StatusBadRequest},
		{"unknown srid", "/api/v1/sources/ffh/layers/areas/features/1?srid=999999",
			domain.UnknownSRIDError(999999), http.StatusBadRequest},
		{"unknown source", "/api/v1/sources/nope/layers/areas/features/1", domain.ErrSourceNotFound, http.StatusNotFound},
		{"unknown layer", "/api/v1/sources/ffh/layers/nope/features/1", domain.ErrLayerNotFound, http.StatusNotFound},
		{"unknown feature", "/api/v1/sources/ffh/layers/areas/features/9", domain.ErrFeatureNotFound, http.StatusNotFound},
		{"raster source", "/api/v1/sources/dem/layers/elevation/features/1",
			fmt.Errorf("raster: %w", domain.ErrUnsupported), http.StatusUnprocessableEntity},
		{"projection failed", "/api/v1/sources/ffh/layers/areas/features/1",
			fmt.Errorf("%w: boom", domain.ErrTransformFailed), http.StatusInternalServerError},
		{"timeout", "/api/v1/sources/ffh/layers/areas/features/1", context.DeadlineExceeded, http.StatusGatewayTimeout},
		{"adapter failure", "/api/v1/sources/ffh/layers/areas/features/1",
			&domain.QueryError{Layer: "areas", Err: errors.New("disk")}, http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &featureQueryService{result: sampleFeatureGeometry(), err: tt.err}
			srv := newSpyServer(t, &svc.spyQueryService, nil)
			srv.queryService = svc

			rec, body := doGET(t, srv, tt.path)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body)
			}
			if body["message"] == "" || body["message"] == nil {
				t.Errorf("error body has no message: %s", rec.Body)
			}
		})
	}
}

func TestGetFeatureNotFoundMessage(t *testing.T) {
	svc := &featureQueryService{err: domain.ErrFeatureNotFound}
	srv := newSpyServer(t, &svc.spyQueryService, nil)
	srv.queryService = svc

	_, body := doGET(t, srv, "/api/v1/sources/ffh/layers/areas/features/9")
	if body["message"] != "Feature not found" {
		t.Errorf("message = %v, want %q", body["message"], "Feature not found")
	}
}
