package application

import (
	"context"
	"errors"
	"fmt"
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

// knownSRIDs is a transformer that supports only the listed SRIDs, like the real
// one does with spatial_ref_sys.
type knownSRIDs map[int]bool

func (k knownSRIDs) Transform(_ context.Context, coord domain.Coordinate, targetSRID int) (domain.Coordinate, error) {
	if !k[coord.SRID] || !k[targetSRID] {
		return domain.Coordinate{}, fmt.Errorf("unknown SRID in EPSG:%d -> EPSG:%d", coord.SRID, targetSRID)
	}
	coord.SRID = targetSRID
	return coord, nil
}
func (k knownSRIDs) IsSupported(from, to int) bool { return k[from] && k[to] }

// TestQueryPointUnknownSRIDIsInvalidInput: an SRID the transformer doesn't know
// is rejected as invalid input before any source is queried — never reported as
// ErrTransformFailed, which marks a server fault. This is the path MCP takes.
func TestQueryPointUnknownSRIDIsInvalidInput(t *testing.T) {
	svc := newQueryServiceWithTransformer(mixedSRIDRegistry(), knownSRIDs{domain.SRIDWGS84: true, 25832: true})

	_, err := svc.QueryPoint(context.Background(), domain.QueryRequest{
		Coordinate: domain.Coordinate{X: 477114, Y: 5549798, SRID: 999999},
	})
	var invalid *domain.ValidationError
	if !errors.As(err, &invalid) || invalid.Field != "srid" {
		t.Fatalf("err = %v, want a srid ValidationError", err)
	}
	if errors.Is(err, domain.ErrTransformFailed) {
		t.Fatalf("err = %v, an unknown client SRID must not count as a transform failure", err)
	}

	resp, err := svc.QueryPoint(context.Background(), domain.QueryRequest{
		Coordinate: domain.Coordinate{X: 477114, Y: 5549798, SRID: 25832},
	})
	if err != nil || len(resp.Results) != 2 {
		t.Fatalf("known SRID: err = %v, resp %+v; want both sources", err, resp)
	}
}

// TestQueryPointSRIDZeroIsWGS84: srid=0 is accepted as "WGS84 implied", so it
// must be queried as WGS84 — not handed to the transformer as EPSG:0, which
// would fail and turn an accepted input into a 500.
func TestQueryPointSRIDZeroIsWGS84(t *testing.T) {
	svc := newQueryServiceWithTransformer(mixedSRIDRegistry(), knownSRIDs{domain.SRIDWGS84: true, 25832: true})

	resp, err := svc.QueryPoint(context.Background(), domain.QueryRequest{
		Coordinate: domain.Coordinate{X: 8.68, Y: 50.1, SRID: 0},
	})
	if err != nil || len(resp.Results) != 2 {
		t.Fatalf("srid 0: err = %v, resp %+v; want both sources", err, resp)
	}
}
