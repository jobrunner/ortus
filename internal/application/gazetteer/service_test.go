package gazetteer

import (
	"context"
	"errors"
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// stubIndex is a no-op SpatialIndex used to exercise the enabled path. M0 wires
// the seam only, so the methods are never actually invoked.
type stubIndex struct{}

func (stubIndex) QueryKNN(context.Context, string, domain.Coordinate, int, float64, *output.Filter) ([]domain.Feature, error) {
	return nil, nil
}
func (stubIndex) PointInPolygon(context.Context, string, domain.Coordinate) ([]domain.Feature, error) {
	return nil, nil
}
func (stubIndex) ResolveChain(context.Context, string, int64) ([]output.AdminRow, error) {
	return nil, nil
}
func (stubIndex) DistanceKM(domain.Coordinate, domain.Coordinate) (float64, error) { return 0, nil }
func (stubIndex) Azimuth(domain.Coordinate, domain.Coordinate) (float64, error)    { return 0, nil }

func TestServiceInert(t *testing.T) {
	ctx := context.Background()
	p := domain.NewWGS84Coordinate(9.93, 49.79)

	cases := []struct {
		name    string
		svc     *Service
		wantErr error
	}{
		{"disabled", NewService(stubIndex{}, false), ErrDisabled},
		{"enabled without index", NewService(nil, true), ErrDisabled},
		{"enabled with index", NewService(stubIndex{}, true), domain.ErrUnsupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.svc.Locate(ctx, p); !errors.Is(err, tc.wantErr) {
				t.Errorf("Locate err = %v, want %v", err, tc.wantErr)
			}
			if _, err := tc.svc.Bearing(ctx, p, domain.DefaultBearingPolicy()); !errors.Is(err, tc.wantErr) {
				t.Errorf("Bearing err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}
