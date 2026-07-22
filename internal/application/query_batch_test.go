package application

import (
	"context"
	"errors"
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
)

// batchTestRegistry builds a registry with two ready sources (pkg1, pkg2), each a
// single layer returning one feature. The owning mockRepository is NOT a
// BatchQuerier, so QueryBatch exercises the registry's per-point fallback loop.
func batchTestRegistry() *SourceRegistry {
	registry := newTestRegistry()
	repo := &mockRepository{
		packages: map[string]*domain.Source{
			"pkg1": {ID: "pkg1", Layers: []domain.Layer{{Name: "layer1", SRID: 4326}}},
			"pkg2": {ID: "pkg2", Layers: []domain.Layer{{Name: "layer1", SRID: 4326}}},
		},
		features: map[string][]domain.Feature{
			"pkg1:layer1": {{ID: 1, LayerName: "layer1", Properties: map[string]interface{}{"name": "a", "code": "A"}}},
			"pkg2:layer1": {{ID: 2, LayerName: "layer1", Properties: map[string]interface{}{"name": "b", "code": "B"}}},
		},
	}
	registry.mu.Lock()
	for _, id := range []string{"pkg1", "pkg2"} {
		registry.sources[id] = &sourceEntry{
			Source: &domain.Source{ID: id, Indexed: true, Layers: []domain.Layer{{Name: "layer1", SRID: 4326, HasIndex: true}}},
			Repo:   repo,
			Status: domain.StatusReady,
		}
	}
	registry.mu.Unlock()
	return registry
}

func batchCoords() []domain.Coordinate {
	return []domain.Coordinate{
		domain.NewWGS84Coordinate(10, 50),
		domain.NewWGS84Coordinate(11, 51),
		domain.NewWGS84Coordinate(12, 52),
	}
}

// TestQueryBatchAllSourcesOrder: no filter → each point gets both sources, and the
// response order + coordinate echo matches the input order.
func TestQueryBatchAllSourcesOrder(t *testing.T) {
	svc := newTestQueryService(batchTestRegistry())
	coords := batchCoords()

	out, err := svc.QueryBatch(context.Background(), coords, nil, nil)
	if err != nil {
		t.Fatalf("QueryBatch: %v", err)
	}
	if len(out) != len(coords) {
		t.Fatalf("len(out) = %d, want %d", len(out), len(coords))
	}
	for i := range coords {
		if out[i].Coordinate != coords[i] {
			t.Errorf("point %d: coordinate echo = %+v, want %+v", i, out[i].Coordinate, coords[i])
		}
		if len(out[i].Results) != 2 {
			t.Errorf("point %d: %d source results, want 2 (pkg1+pkg2)", i, len(out[i].Results))
		}
		if out[i].TotalFeatures != 2 {
			t.Errorf("point %d: TotalFeatures = %d, want 2", i, out[i].TotalFeatures)
		}
	}
}

// TestQueryBatchSourceFilter: a sources filter narrows to just that source.
func TestQueryBatchSourceFilter(t *testing.T) {
	svc := newTestQueryService(batchTestRegistry())
	out, err := svc.QueryBatch(context.Background(), batchCoords(), []string{"pkg1"}, nil)
	if err != nil {
		t.Fatalf("QueryBatch: %v", err)
	}
	for i := range out {
		if len(out[i].Results) != 1 || out[i].Results[0].SourceID != "pkg1" {
			t.Errorf("point %d: want a single pkg1 result, got %+v", i, out[i].Results)
		}
	}
}

// TestQueryBatchUnknownSource: an unknown requested source is a client error.
func TestQueryBatchUnknownSource(t *testing.T) {
	svc := newTestQueryService(batchTestRegistry())
	_, err := svc.QueryBatch(context.Background(), batchCoords(), []string{"nope"}, nil)
	if err != domain.ErrSourceNotFound {
		t.Errorf("err = %v, want %v", err, domain.ErrSourceNotFound)
	}
}

// TestQueryBatchProperties: the properties filter is applied per point.
func TestQueryBatchProperties(t *testing.T) {
	svc := newTestQueryService(batchTestRegistry())
	out, err := svc.QueryBatch(context.Background(), batchCoords()[:1], []string{"pkg1"}, []string{"name"})
	if err != nil {
		t.Fatalf("QueryBatch: %v", err)
	}
	feats := out[0].Results[0].Features
	if len(feats) != 1 {
		t.Fatalf("want 1 feature, got %d", len(feats))
	}
	if _, ok := feats[0].Properties["name"]; !ok {
		t.Error("filtered feature should keep 'name'")
	}
	if _, ok := feats[0].Properties["code"]; ok {
		t.Error("filtered feature should NOT keep 'code'")
	}
}

// errRegistry builds a one-source registry whose repo returns queryErr from every
// QueryPoint, so QueryBatch's error handling (fallback loop → batchLayer) is exercised.
func errRegistry(queryErr error) *SourceRegistry {
	registry := newTestRegistry()
	repo := &mockRepository{
		packages: map[string]*domain.Source{
			"pkg1": {ID: "pkg1", Layers: []domain.Layer{{Name: "layer1", SRID: 4326}}},
		},
		queryErr: queryErr,
	}
	registry.mu.Lock()
	registry.sources["pkg1"] = &sourceEntry{
		Source: &domain.Source{ID: "pkg1", Indexed: true, Layers: []domain.Layer{{Name: "layer1", SRID: 4326, HasIndex: true}}},
		Repo:   repo,
		Status: domain.StatusReady,
	}
	registry.mu.Unlock()
	return registry
}

// TestQueryBatchDeadlinePropagates: a context deadline from the query path must
// surface as a real error, not a misleading empty "no hits" success.
func TestQueryBatchDeadlinePropagates(t *testing.T) {
	svc := newTestQueryService(errRegistry(context.DeadlineExceeded))
	_, err := svc.QueryBatch(context.Background(), batchCoords(), nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}

// TestQueryBatchCancelPropagates: client cancellation is likewise propagated.
func TestQueryBatchCancelPropagates(t *testing.T) {
	svc := newTestQueryService(errRegistry(context.Canceled))
	_, err := svc.QueryBatch(context.Background(), batchCoords(), nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// TestQueryBatchAdapterErrorIsolated: a non-context adapter error is isolated (the
// batch still succeeds with empty results for that source), never aborting the batch.
func TestQueryBatchAdapterErrorIsolated(t *testing.T) {
	svc := newTestQueryService(errRegistry(errors.New("boom")))
	out, err := svc.QueryBatch(context.Background(), batchCoords(), nil, nil)
	if err != nil {
		t.Fatalf("adapter error should be isolated, got %v", err)
	}
	for i := range out {
		if len(out[i].Results) != 0 {
			t.Errorf("point %d: want empty results after isolated error, got %+v", i, out[i].Results)
		}
	}
}

// TestQueryBatchEmptyPool: with no sources the batch still returns one (empty)
// response per point in order, not an error.
func TestQueryBatchEmptyPool(t *testing.T) {
	svc := newTestQueryService(newTestRegistry())
	coords := batchCoords()
	out, err := svc.QueryBatch(context.Background(), coords, nil, nil)
	if err != nil {
		t.Fatalf("QueryBatch: %v", err)
	}
	if len(out) != len(coords) {
		t.Fatalf("len(out) = %d, want %d", len(out), len(coords))
	}
	for i := range out {
		if len(out[i].Results) != 0 || out[i].Coordinate != coords[i] {
			t.Errorf("point %d: want empty results + coord echo, got %+v", i, out[i])
		}
	}
}
