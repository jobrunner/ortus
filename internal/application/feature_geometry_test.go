package application

import (
	"context"
	"errors"
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
)

// featureRepository is a mockRepository that also offers the optional
// output.FeatureReader capability, like the GeoPackage adapter.
type featureRepository struct {
	mockRepository
	feature domain.Feature
	err     error
	gotFID  int64
}

func (m *featureRepository) ReadFeature(_ context.Context, _, layer string, fid int64) (domain.Feature, error) {
	m.gotFID = fid
	if m.err != nil {
		return domain.Feature{}, m.err
	}
	f := m.feature
	f.ID, f.LayerName = fid, layer
	return f, nil
}

// projectingTransformer is a mockTransformer that also implements
// output.GeometryProjector and records what it was asked to project.
type projectingTransformer struct {
	mockTransformer
	err              error
	gotFrom, gotTo   int
	gotWKB           []byte
	unsupportedSRIDs map[int]bool
}

func (m *projectingTransformer) IsSupported(from, to int) bool {
	return !m.unsupportedSRIDs[from] && !m.unsupportedSRIDs[to]
}

func (m *projectingTransformer) ProjectGeometry(_ context.Context, wkb []byte, from, to int) ([]byte, domain.Extent, error) {
	m.gotWKB, m.gotFrom, m.gotTo = wkb, from, to
	if m.err != nil {
		return nil, domain.Extent{}, m.err
	}
	return []byte(`{"type":"MultiPolygon","coordinates":[]}`),
		domain.Extent{MinX: 1, MinY: 2, MaxX: 3, MaxY: 4, SRID: to}, nil
}

var testLicense = domain.License{Name: "CC BY 4.0", URL: "https://example.org/by", Attribution: "© Example"}

// featureRegistry holds a vector source "parks" (polygon layer "areas" in
// EPSG:25832 plus a point layer "huts") served by repo, and a raster source
// "dem" served by an adapter without the FeatureReader capability.
func featureRegistry(repo *featureRepository) *SourceRegistry {
	registry := newTestRegistry()
	registry.mu.Lock()
	registry.sources["parks"] = &sourceEntry{
		Source: &domain.Source{
			ID: "parks", Kind: domain.SourceKindVector, Indexed: true, License: testLicense,
			Layers: []domain.Layer{
				{Name: "areas", SRID: 25832, GeometryType: string(domain.GeomMultiPolygon), HasIndex: true},
				{Name: "huts", SRID: 25832, GeometryType: string(domain.GeomPoint), HasIndex: true},
			},
		},
		Repo:   repo,
		Status: domain.StatusReady,
	}
	registry.sources["dem"] = &sourceEntry{
		Source: &domain.Source{
			ID: "dem", Kind: domain.SourceKindRaster, Indexed: true,
			Layers: []domain.Layer{{Name: "elevation", SRID: 4326, GeometryType: string(domain.GeomRaster)}},
		},
		Repo:   &mockRepository{},
		Status: domain.StatusReady,
	}
	// A vector-kind source whose adapter lacks the capability.
	registry.sources["plain"] = &sourceEntry{
		Source: &domain.Source{
			ID: "plain", Kind: domain.SourceKindVector, Indexed: true,
			Layers: []domain.Layer{{Name: "areas", SRID: 4326, GeometryType: string(domain.GeomPolygon)}},
		},
		Repo:   &mockRepository{},
		Status: domain.StatusReady,
	}
	registry.sources["loading"] = &sourceEntry{
		Source: &domain.Source{ID: "loading", Kind: domain.SourceKindVector},
		Repo:   repo,
		Status: domain.StatusLoading,
	}
	registry.mu.Unlock()
	return registry
}

func TestFeatureGeometry(t *testing.T) {
	repo := &featureRepository{feature: domain.Feature{
		Properties: map[string]interface{}{"name": "Moor"},
		Geometry:   domain.Geometry{WKB: []byte{1, 2, 3}, SRID: 25832},
	}}
	tr := &projectingTransformer{}
	svc := newQueryServiceWithTransformer(featureRegistry(repo), tr)

	got, err := svc.FeatureGeometry(context.Background(), domain.FeatureRef{SourceID: "parks", Layer: "areas", ID: 42})
	if err != nil {
		t.Fatalf("FeatureGeometry: %v", err)
	}

	if got.SourceID != "parks" || got.Layer != "areas" || got.ID != 42 {
		t.Errorf("identity = (%q, %q, %d), want (parks, areas, 42)", got.SourceID, got.Layer, got.ID)
	}
	if got.Properties["name"] != "Moor" {
		t.Errorf("properties = %v", got.Properties)
	}
	if got.License != testLicense {
		t.Errorf("license = %+v, want the source's license", got.License)
	}
	// No SRID asked for: WGS84, projected from the layer's SRID.
	if tr.gotFrom != 25832 || tr.gotTo != domain.SRIDWGS84 || string(tr.gotWKB) != "\x01\x02\x03" {
		t.Errorf("projected %v from %d to %d", tr.gotWKB, tr.gotFrom, tr.gotTo)
	}
	want := domain.Extent{MinX: 1, MinY: 2, MaxX: 3, MaxY: 4, SRID: domain.SRIDWGS84}
	if got.Extent != want {
		t.Errorf("extent = %+v, want %+v", got.Extent, want)
	}
	if string(got.GeoJSON) != `{"type":"MultiPolygon","coordinates":[]}` {
		t.Errorf("geojson = %s", got.GeoJSON)
	}
}

func TestFeatureGeometryRequestedSRID(t *testing.T) {
	tr := &projectingTransformer{}
	svc := newQueryServiceWithTransformer(featureRegistry(&featureRepository{
		feature: domain.Feature{Geometry: domain.Geometry{WKB: []byte{1}, SRID: 25832}},
	}), tr)

	got, err := svc.FeatureGeometry(context.Background(),
		domain.FeatureRef{SourceID: "parks", Layer: "areas", ID: 1, SRID: 3857})
	if err != nil {
		t.Fatalf("FeatureGeometry: %v", err)
	}
	if tr.gotTo != 3857 || got.Extent.SRID != 3857 {
		t.Errorf("projected to %d, extent SRID %d, want 3857", tr.gotTo, got.Extent.SRID)
	}
}

func TestFeatureGeometryErrors(t *testing.T) {
	var validation *domain.ValidationError
	tests := []struct {
		name    string
		ref     domain.FeatureRef
		readErr error
		projErr error
		check   func(error) bool
	}{
		{"unknown source", domain.FeatureRef{SourceID: "nope", Layer: "areas", ID: 1}, nil, nil,
			func(err error) bool { return errors.Is(err, domain.ErrSourceNotFound) }},
		{"source not ready", domain.FeatureRef{SourceID: "loading", Layer: "areas", ID: 1}, nil, nil,
			func(err error) bool { return errors.Is(err, domain.ErrSourceNotFound) }},
		{"raster source", domain.FeatureRef{SourceID: "dem", Layer: "elevation", ID: 1}, nil, nil,
			func(err error) bool { return errors.Is(err, domain.ErrUnsupported) }},
		{"adapter without capability", domain.FeatureRef{SourceID: "plain", Layer: "areas", ID: 1}, nil, nil,
			func(err error) bool { return errors.Is(err, domain.ErrUnsupported) }},
		{"unknown layer", domain.FeatureRef{SourceID: "parks", Layer: "nope", ID: 1}, nil, nil,
			func(err error) bool { return errors.Is(err, domain.ErrLayerNotFound) }},
		{"point layer", domain.FeatureRef{SourceID: "parks", Layer: "huts", ID: 1}, nil, nil,
			func(err error) bool { return errors.Is(err, domain.ErrUnsupported) }},
		{"unknown srid", domain.FeatureRef{SourceID: "parks", Layer: "areas", ID: 1, SRID: 999999}, nil, nil,
			func(err error) bool { return errors.As(err, &validation) }},
		{"unknown feature", domain.FeatureRef{SourceID: "parks", Layer: "areas", ID: 1}, domain.ErrFeatureNotFound, nil,
			func(err error) bool { return errors.Is(err, domain.ErrFeatureNotFound) }},
		{"projection fails", domain.FeatureRef{SourceID: "parks", Layer: "areas", ID: 1}, nil, errors.New("boom"),
			func(err error) bool { return errors.Is(err, domain.ErrTransformFailed) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &featureRepository{
				feature: domain.Feature{Geometry: domain.Geometry{WKB: []byte{1}, SRID: 25832}},
				err:     tt.readErr,
			}
			tr := &projectingTransformer{err: tt.projErr, unsupportedSRIDs: map[int]bool{999999: true}}
			svc := newQueryServiceWithTransformer(featureRegistry(repo), tr)

			got, err := svc.FeatureGeometry(context.Background(), tt.ref)
			if err == nil || !tt.check(err) {
				t.Fatalf("err = %v (result %+v)", err, got)
			}
		})
	}
}

// A transformer that cannot project whole geometries leaves the capability
// unavailable rather than panicking.
func TestFeatureGeometryWithoutProjector(t *testing.T) {
	repo := &featureRepository{feature: domain.Feature{Geometry: domain.Geometry{WKB: []byte{1}}}}
	svc := newQueryServiceWithTransformer(featureRegistry(repo), &mockTransformer{})

	_, err := svc.FeatureGeometry(context.Background(), domain.FeatureRef{SourceID: "parks", Layer: "areas", ID: 1})
	if !errors.Is(err, domain.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}
