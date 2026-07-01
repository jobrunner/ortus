package gazetteer

import (
	"context"
	"errors"
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// fakeIndex is a configurable SpatialIndex for service tests: PointInPolygon
// returns canned features; the other methods are inert (M3 exercises them).
type fakeIndex struct {
	pip    []domain.Feature
	pipErr error
}

func (f fakeIndex) QueryKNN(context.Context, string, domain.Coordinate, int, float64, *output.Filter) ([]domain.Feature, error) {
	return nil, nil
}
func (f fakeIndex) PointInPolygon(context.Context, string, domain.Coordinate) ([]domain.Feature, error) {
	return f.pip, f.pipErr
}
func (f fakeIndex) ResolveChain(context.Context, string, int64) ([]output.AdminRow, error) {
	return nil, nil
}
func (f fakeIndex) DistanceKM(domain.Coordinate, domain.Coordinate) (float64, error) { return 0, nil }
func (f fakeIndex) Azimuth(domain.Coordinate, domain.Coordinate) (float64, error)    { return 0, nil }

// testManifest matches the fixture/real column names.
func testManifest() Manifest {
	return Manifest{
		PlacesLayer: "places", RankColumn: "place", NameColumn: "name", AdminFKColumn: "admin_id",
		AdminLayer: "admin_levels", LevelColumn: "admin_level", AdminNameColumn: "name", ParentFKColumn: "parent_id",
		CountryColumn: "country_iso", ConstraintTier: "state",
	}
}

// mapResolver is a simple LevelResolver backed by an (iso,level)→equivalent map.
type mapResolver map[[2]any]string

func (m mapResolver) Resolve(iso string, level int) (string, bool) {
	e, ok := m[[2]any{iso, level}]
	return e, ok
}

func adminFeature(level, name string) domain.Feature {
	return domain.Feature{
		LayerName:  "admin_levels",
		Properties: map[string]any{"admin_level": level, "name": name, "country_iso": "DE"},
	}
}

func TestServiceInert(t *testing.T) {
	ctx := context.Background()
	p := domain.NewWGS84Coordinate(9.93, 49.79)

	cases := []struct {
		name    string
		svc     *Service
		wantErr error
	}{
		{"disabled", NewService(fakeIndex{}, testManifest(), nil, false), ErrDisabled},
		{"enabled without index", NewService(nil, testManifest(), nil, true), ErrDisabled},
		{"enabled with index", NewService(fakeIndex{}, testManifest(), nil, true), domain.ErrUnsupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Bearing is not implemented until M3.
			if _, err := tc.svc.Bearing(ctx, p, domain.DefaultBearingPolicy()); !errors.Is(err, tc.wantErr) {
				t.Errorf("Bearing err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestLocateDisabled(t *testing.T) {
	svc := NewService(fakeIndex{}, testManifest(), nil, false)
	if _, err := svc.Locate(context.Background(), domain.NewWGS84Coordinate(10, 50)); !errors.Is(err, ErrDisabled) {
		t.Errorf("Locate on disabled = %v, want ErrDisabled", err)
	}
}

func TestLocateBuildsEnrichedChain(t *testing.T) {
	// PiP returns nested polygons in arbitrary order; Locate must order them
	// most-local-first and enrich each level from the resolver.
	idx := fakeIndex{pip: []domain.Feature{
		adminFeature("2", "Deutschland"),
		adminFeature("8", "Würzburg"),
		adminFeature("4", "Bayern"),
		adminFeature("", "coverage fill"), // non-numeric level → skipped
	}}
	resolver := mapResolver{
		[2]any{"DE", 2}: "country",
		[2]any{"DE", 4}: "state",
		[2]any{"DE", 8}: "municipality",
	}
	svc := NewService(idx, testManifest(), resolver, true)

	loc, err := svc.Locate(context.Background(), domain.NewWGS84Coordinate(9.93, 49.79))
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if loc.CountryISO != "DE" {
		t.Errorf("CountryISO = %q, want DE", loc.CountryISO)
	}
	wantLevels := []int{8, 4, 2}
	wantEquiv := []string{"municipality", "state", "country"}
	if len(loc.Chain) != 3 {
		t.Fatalf("chain length = %d, want 3 (fill skipped)", len(loc.Chain))
	}
	for i, u := range loc.Chain {
		if u.Level != wantLevels[i] || u.Equivalent != wantEquiv[i] {
			t.Errorf("chain[%d] = {L%d %q}, want {L%d %q}", i, u.Level, u.Equivalent, wantLevels[i], wantEquiv[i])
		}
	}
	if loc.Chain[0].Name != "Würzburg" {
		t.Errorf("most-local name = %q, want Würzburg", loc.Chain[0].Name)
	}
}

func TestLocateNoCoverage(t *testing.T) {
	svc := NewService(fakeIndex{pip: nil}, testManifest(), nil, true)
	if _, err := svc.Locate(context.Background(), domain.NewWGS84Coordinate(0, 0)); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Locate with no coverage = %v, want ErrNotFound", err)
	}
}

func TestLocatePropagatesIndexError(t *testing.T) {
	sentinel := errors.New("db down")
	svc := NewService(fakeIndex{pipErr: sentinel}, testManifest(), nil, true)
	if _, err := svc.Locate(context.Background(), domain.NewWGS84Coordinate(10, 50)); !errors.Is(err, sentinel) {
		t.Errorf("Locate err = %v, want wrapped sentinel", err)
	}
}

func TestLocateWithoutResolverLeavesEquivalentEmpty(t *testing.T) {
	idx := fakeIndex{pip: []domain.Feature{adminFeature("8", "Würzburg")}}
	svc := NewService(idx, testManifest(), nil, true) // nil resolver → noop
	loc, err := svc.Locate(context.Background(), domain.NewWGS84Coordinate(9.93, 49.79))
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if len(loc.Chain) != 1 || loc.Chain[0].Equivalent != "" {
		t.Errorf("chain = %+v, want one unit with empty Equivalent", loc.Chain)
	}
}
