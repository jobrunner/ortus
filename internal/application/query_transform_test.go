package application

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// mixedSRIDRegistry builds a registry with a WGS84 source ("wgs") and an
// ETRS89/UTM source ("utm"), so a query hits one layer that needs no transform
// and one that does — the shape where a failing transformer used to yield a
// plausible-looking but incomplete answer.
func mixedSRIDRegistry() *SourceRegistry {
	registry := newTestRegistry()
	layers := map[string][]domain.Layer{
		"wgs": {{Name: "layer1", SRID: domain.SRIDWGS84, HasIndex: true}},
		"utm": {{Name: "layer1", SRID: 25832, HasIndex: true}},
	}
	repo := &mockRepository{
		packages: map[string]*domain.Source{
			"wgs": {ID: "wgs", Layers: layers["wgs"]},
			"utm": {ID: "utm", Layers: layers["utm"]},
		},
		features: map[string][]domain.Feature{
			"wgs:layer1": {{ID: 1, LayerName: "layer1"}},
			"utm:layer1": {{ID: 2, LayerName: "layer1"}},
		},
	}
	registry.mu.Lock()
	for id, ls := range layers {
		registry.sources[id] = &sourceEntry{
			Source: &domain.Source{ID: id, Indexed: true, Layers: ls},
			Repo:   repo,
			Status: domain.StatusReady,
		}
	}
	registry.mu.Unlock()
	return registry
}

func newQueryServiceWithTransformer(registry *SourceRegistry, tr output.CoordinateTransformer) *QueryService {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	return NewQueryService(registry, tr, testMeter(), output.NoOpTracer{}, logger, QueryServiceConfig{MaxFeatures: 100})
}

// TestQueryPointTransformFailurePropagates: a broken transformer is a server
// fault. It must surface as ErrTransformFailed instead of silently dropping
// every layer whose SRID differs and answering with the rest.
func TestQueryPointTransformFailurePropagates(t *testing.T) {
	svc := newQueryServiceWithTransformer(mixedSRIDRegistry(), &mockTransformer{shouldFail: true})

	resp, err := svc.QueryPoint(context.Background(), domain.QueryRequest{Coordinate: domain.NewWGS84Coordinate(8.68, 50.1)})
	if !errors.Is(err, domain.ErrTransformFailed) {
		t.Fatalf("err = %v (resp %+v), want ErrTransformFailed", err, resp)
	}
}

func TestQueryPointTransformsAcrossSRIDs(t *testing.T) {
	svc := newQueryServiceWithTransformer(mixedSRIDRegistry(), &mockTransformer{})

	resp, err := svc.QueryPoint(context.Background(), domain.QueryRequest{Coordinate: domain.NewWGS84Coordinate(8.68, 50.1)})
	if err != nil {
		t.Fatalf("QueryPoint: %v", err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("want results from both sources, got %+v", resp.Results)
	}
}

func TestQueryBatchTransformFailurePropagates(t *testing.T) {
	svc := newQueryServiceWithTransformer(mixedSRIDRegistry(), &mockTransformer{shouldFail: true})

	out, err := svc.QueryBatch(context.Background(), batchCoords(), nil, nil)
	if !errors.Is(err, domain.ErrTransformFailed) {
		t.Fatalf("err = %v (out %v), want ErrTransformFailed", err, out)
	}
}
