package application

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

func newTestQueryService(registry *PackageRegistry, repo *mockRepository) *QueryService {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	return NewQueryService(
		registry,
		repo,
		nil, // No transformer needed for basic tests
		&output.NoOpMetrics{},
		logger,
		QueryServiceConfig{
			DefaultSRID: domain.SRIDWGS84,
			MaxFeatures: 100,
		},
	)
}

func TestQueryServiceDefaultConfig(t *testing.T) {
	registry := newTestRegistry()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	svc := NewQueryService(
		registry,
		&mockRepository{},
		nil,
		&output.NoOpMetrics{},
		logger,
		QueryServiceConfig{}, // Empty config
	)

	if svc.defaultSRID != domain.SRIDWGS84 {
		t.Errorf("defaultSRID = %d, want %d", svc.defaultSRID, domain.SRIDWGS84)
	}
	if svc.maxFeatures != 1000 {
		t.Errorf("maxFeatures = %d, want 1000", svc.maxFeatures)
	}
}

func TestQueryServiceQueryPointInvalidCoordinate(t *testing.T) {
	registry := newTestRegistry()
	svc := newTestQueryService(registry, &mockRepository{})

	req := domain.QueryRequest{
		Coordinate: domain.NewWGS84Coordinate(200, 0), // Invalid longitude
	}

	_, err := svc.QueryPoint(context.Background(), req)
	if err == nil {
		t.Error("QueryPoint should fail with invalid coordinate")
	}
}

func TestQueryServiceQueryPointNoPackages(t *testing.T) {
	registry := newTestRegistry()
	svc := newTestQueryService(registry, &mockRepository{})

	req := domain.QueryRequest{
		Coordinate: domain.NewWGS84Coordinate(10, 50),
	}

	resp, err := svc.QueryPoint(context.Background(), req)
	if err != nil {
		t.Fatalf("QueryPoint failed: %v", err)
	}

	if len(resp.Results) != 0 {
		t.Errorf("len(Results) = %d, want 0", len(resp.Results))
	}
	if resp.TotalFeatures != 0 {
		t.Errorf("TotalFeatures = %d, want 0", resp.TotalFeatures)
	}
}

func TestQueryServiceQueryPointWithFeatures(t *testing.T) {
	registry := newTestRegistry()

	// Add a ready package
	registry.mu.Lock()
	registry.packages["test-pkg"] = &packageEntry{
		Package: &domain.GeoPackage{
			ID:      "test-pkg",
			Name:    "Test Package",
			Indexed: true,
			Layers: []domain.Layer{
				{Name: "layer1", GeometryType: "POLYGON", SRID: 4326, HasIndex: true},
			},
		},
		Status: domain.StatusReady,
	}
	registry.mu.Unlock()

	repo := &mockRepository{
		packages: map[string]*domain.GeoPackage{
			"test-pkg": {
				ID:     "test-pkg",
				Name:   "Test Package",
				Layers: []domain.Layer{{Name: "layer1", GeometryType: "POLYGON", SRID: 4326}},
			},
		},
		features: map[string][]domain.Feature{
			"test-pkg:layer1": {
				{ID: 1, LayerName: "layer1", Properties: map[string]interface{}{"name": "Feature 1"}},
				{ID: 2, LayerName: "layer1", Properties: map[string]interface{}{"name": "Feature 2"}},
			},
		},
	}

	svc := newTestQueryService(registry, repo)

	req := domain.QueryRequest{
		Coordinate: domain.NewWGS84Coordinate(10, 50),
	}

	resp, err := svc.QueryPoint(context.Background(), req)
	if err != nil {
		t.Fatalf("QueryPoint failed: %v", err)
	}

	if len(resp.Results) != 1 {
		t.Errorf("len(Results) = %d, want 1", len(resp.Results))
	}
	if resp.TotalFeatures != 2 {
		t.Errorf("TotalFeatures = %d, want 2", resp.TotalFeatures)
	}
}

func TestQueryServiceQueryPointSpecificPackage(t *testing.T) {
	registry := newTestRegistry()

	// Add two ready packages
	registry.mu.Lock()
	registry.packages["pkg1"] = &packageEntry{
		Package: &domain.GeoPackage{
			ID:      "pkg1",
			Indexed: true,
			Layers:  []domain.Layer{{Name: "layer1", SRID: 4326, HasIndex: true}},
		},
		Status: domain.StatusReady,
	}
	registry.packages["pkg2"] = &packageEntry{
		Package: &domain.GeoPackage{
			ID:      "pkg2",
			Indexed: true,
			Layers:  []domain.Layer{{Name: "layer1", SRID: 4326, HasIndex: true}},
		},
		Status: domain.StatusReady,
	}
	registry.mu.Unlock()

	repo := &mockRepository{
		packages: map[string]*domain.GeoPackage{
			"pkg1": {ID: "pkg1", Layers: []domain.Layer{{Name: "layer1", SRID: 4326}}},
			"pkg2": {ID: "pkg2", Layers: []domain.Layer{{Name: "layer1", SRID: 4326}}},
		},
		features: map[string][]domain.Feature{
			"pkg1:layer1": {{ID: 1, LayerName: "layer1"}},
			"pkg2:layer1": {{ID: 2, LayerName: "layer1"}},
		},
	}

	svc := newTestQueryService(registry, repo)

	req := domain.QueryRequest{
		Coordinate: domain.NewWGS84Coordinate(10, 50),
		PackageID:  "pkg1",
	}

	resp, err := svc.QueryPoint(context.Background(), req)
	if err != nil {
		t.Fatalf("QueryPoint failed: %v", err)
	}

	if len(resp.Results) != 1 {
		t.Errorf("len(Results) = %d, want 1", len(resp.Results))
	}
	if resp.Results[0].PackageID != "pkg1" {
		t.Errorf("PackageID = %s, want pkg1", resp.Results[0].PackageID)
	}
}

func TestQueryServiceQueryPointPackageNotFound(t *testing.T) {
	registry := newTestRegistry()
	svc := newTestQueryService(registry, &mockRepository{})

	req := domain.QueryRequest{
		Coordinate: domain.NewWGS84Coordinate(10, 50),
		PackageID:  "nonexistent",
	}

	_, err := svc.QueryPoint(context.Background(), req)
	if err != domain.ErrPackageNotFound {
		t.Errorf("err = %v, want %v", err, domain.ErrPackageNotFound)
	}
}

func TestQueryServiceFilterProperties(t *testing.T) {
	svc := &QueryService{}

	features := []domain.Feature{
		{
			ID: 1,
			Properties: map[string]interface{}{
				"name":    "Feature 1",
				"type":    "building",
				"area":    100.5,
				"private": "secret",
			},
		},
	}

	filtered := svc.filterProperties(features, []string{"name", "area"})

	if len(filtered[0].Properties) != 2 {
		t.Errorf("len(Properties) = %d, want 2", len(filtered[0].Properties))
	}
	if filtered[0].Properties["name"] != "Feature 1" {
		t.Error("name should be preserved")
	}
	if filtered[0].Properties["area"] != 100.5 {
		t.Error("area should be preserved")
	}
	if _, ok := filtered[0].Properties["private"]; ok {
		t.Error("private should be filtered out")
	}
}

func TestQueryServiceApplyMaxFeaturesLimit(t *testing.T) {
	svc := &QueryService{maxFeatures: 5}

	tests := []struct {
		name           string
		existing       int
		newFeatures    int
		wantCount      int
		wantMaxReached bool
	}{
		{"under limit", 0, 3, 3, false},
		{"exactly at limit", 0, 5, 5, false},
		{"over limit", 0, 10, 5, true},
		{"partially filled", 3, 5, 2, true},
		{"already at limit", 5, 5, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &domain.QueryResult{
				Features: make([]domain.Feature, tt.existing),
			}
			newFeatures := make([]domain.Feature, tt.newFeatures)
			for i := range newFeatures {
				newFeatures[i] = domain.Feature{ID: int64(i)}
			}

			got, maxReached := svc.applyMaxFeaturesLimit(newFeatures, result)

			if len(got) != tt.wantCount {
				t.Errorf("len(got) = %d, want %d", len(got), tt.wantCount)
			}
			if maxReached != tt.wantMaxReached {
				t.Errorf("maxReached = %v, want %v", maxReached, tt.wantMaxReached)
			}
		})
	}
}

func TestQueryServiceTransformCoordinate(t *testing.T) {
	registry := newTestRegistry()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	tests := []struct {
		name        string
		transformer output.CoordinateTransformer
		coordSRID   int
		layerSRID   int
		wantOK      bool
	}{
		{
			name:        "same SRID",
			transformer: nil,
			coordSRID:   4326,
			layerSRID:   4326,
			wantOK:      true,
		},
		{
			name:        "different SRID without transformer",
			transformer: nil,
			coordSRID:   4326,
			layerSRID:   25832,
			wantOK:      false,
		},
		{
			name:        "different SRID with transformer",
			transformer: &mockTransformer{shouldFail: false},
			coordSRID:   4326,
			layerSRID:   25832,
			wantOK:      true,
		},
		{
			name:        "transformation fails",
			transformer: &mockTransformer{shouldFail: true},
			coordSRID:   4326,
			layerSRID:   25832,
			wantOK:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewQueryService(registry, &mockRepository{}, tt.transformer, &output.NoOpMetrics{}, logger, QueryServiceConfig{})

			coord := domain.NewCoordinate(10, 50, tt.coordSRID)
			layer := &domain.Layer{SRID: tt.layerSRID}

			_, ok := svc.transformCoordinate(context.Background(), coord, layer)
			if ok != tt.wantOK {
				t.Errorf("transformCoordinate() ok = %v, want %v", ok, tt.wantOK)
			}
		})
	}
}
