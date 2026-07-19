package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"testing"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// testMeter returns a no-op OTel meter for tests that don't care about
// metric output. Centralized so the import + helper stays in one place.
func testMeter() metric.Meter { return noop.NewMeterProvider().Meter("test") }

func newTestQueryService(registry *SourceRegistry) *QueryService {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	return NewQueryService(
		registry,
		nil, // No transformer needed for basic tests
		testMeter(),
		output.NoOpTracer{},
		logger,
		QueryServiceConfig{
			MaxFeatures: 100,
		},
	)
}

func TestQueryServiceDefaultConfig(t *testing.T) {
	registry := newTestRegistry()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	svc := NewQueryService(
		registry,
		nil,
		testMeter(),
		output.NoOpTracer{},
		logger,
		QueryServiceConfig{}, // Empty config
	)

	if svc.maxFeatures != 1000 {
		t.Errorf("maxFeatures = %d, want 1000", svc.maxFeatures)
	}
}

func TestQueryServiceQueryPointInvalidCoordinate(t *testing.T) {
	registry := newTestRegistry()
	svc := newTestQueryService(registry)

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
	svc := newTestQueryService(registry)

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

	repo := &mockRepository{
		packages: map[string]*domain.Source{
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

	// Add a ready package owned by the features repo.
	registry.mu.Lock()
	registry.sources["test-pkg"] = &sourceEntry{
		Source: &domain.Source{
			ID:      "test-pkg",
			Name:    "Test Package",
			Indexed: true,
			Layers: []domain.Layer{
				{Name: "layer1", GeometryType: "POLYGON", SRID: 4326, HasIndex: true},
			},
		},
		Repo:   repo,
		Status: domain.StatusReady,
	}
	registry.mu.Unlock()

	svc := newTestQueryService(registry)

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

	repo := &mockRepository{
		packages: map[string]*domain.Source{
			"pkg1": {ID: "pkg1", Layers: []domain.Layer{{Name: "layer1", SRID: 4326}}},
			"pkg2": {ID: "pkg2", Layers: []domain.Layer{{Name: "layer1", SRID: 4326}}},
		},
		features: map[string][]domain.Feature{
			"pkg1:layer1": {{ID: 1, LayerName: "layer1"}},
			"pkg2:layer1": {{ID: 2, LayerName: "layer1"}},
		},
	}

	// Add two ready packages owned by the features repo.
	registry.mu.Lock()
	registry.sources["pkg1"] = &sourceEntry{
		Source: &domain.Source{
			ID:      "pkg1",
			Indexed: true,
			Layers:  []domain.Layer{{Name: "layer1", SRID: 4326, HasIndex: true}},
		},
		Repo:   repo,
		Status: domain.StatusReady,
	}
	registry.sources["pkg2"] = &sourceEntry{
		Source: &domain.Source{
			ID:      "pkg2",
			Indexed: true,
			Layers:  []domain.Layer{{Name: "layer1", SRID: 4326, HasIndex: true}},
		},
		Repo:   repo,
		Status: domain.StatusReady,
	}
	registry.mu.Unlock()

	svc := newTestQueryService(registry)

	req := domain.QueryRequest{
		Coordinate: domain.NewWGS84Coordinate(10, 50),
		SourceID:   "pkg1",
	}

	resp, err := svc.QueryPoint(context.Background(), req)
	if err != nil {
		t.Fatalf("QueryPoint failed: %v", err)
	}

	if len(resp.Results) != 1 {
		t.Errorf("len(Results) = %d, want 1", len(resp.Results))
	}
	if resp.Results[0].SourceID != "pkg1" {
		t.Errorf("SourceID = %s, want pkg1", resp.Results[0].SourceID)
	}
}

func TestQueryServiceQueryPointPackageNotFound(t *testing.T) {
	registry := newTestRegistry()
	svc := newTestQueryService(registry)

	req := domain.QueryRequest{
		Coordinate: domain.NewWGS84Coordinate(10, 50),
		SourceID:   "nonexistent",
	}

	_, err := svc.QueryPoint(context.Background(), req)
	if err != domain.ErrSourceNotFound {
		t.Errorf("err = %v, want %v", err, domain.ErrSourceNotFound)
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
			svc := NewQueryService(registry, tt.transformer, testMeter(), output.NoOpTracer{}, logger, QueryServiceConfig{})

			coord := domain.NewCoordinate(10, 50, tt.coordSRID)
			layer := &domain.Layer{SRID: tt.layerSRID}

			_, ok := svc.transformCoordinate(context.Background(), coord, layer)
			if ok != tt.wantOK {
				t.Errorf("transformCoordinate() ok = %v, want %v", ok, tt.wantOK)
			}
		})
	}
}

func TestIsCanceled(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"client canceled", context.Canceled, true},
		{"wrapped canceled", fmt.Errorf("layer query: %w", context.Canceled), true},
		{"server deadline (not a client abort)", context.DeadlineExceeded, false},
		{"other error", errors.New("boom"), false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isCanceled(tt.err); got != tt.want {
				t.Errorf("isCanceled(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
