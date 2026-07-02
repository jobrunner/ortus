package gazetteer

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// fakeIndex is a configurable SpatialIndex for service tests: PointInPolygon and
// QueryKNN return canned features (KNN keyed by the place-class filter value),
// ResolveChain returns a canned chain per starting fid, and Distance/Azimuth are
// computed with an equirectangular approximation so they track the fed coords.
type fakeIndex struct {
	pip      []domain.Feature
	pipErr   error
	knn      map[string][]domain.Feature
	knnErr   error
	chains   map[int64][]output.AdminRow
	chainErr error
}

func (f fakeIndex) QueryKNN(_ context.Context, _ string, _ domain.Coordinate, _ int, _ float64, filter *output.Filter) ([]domain.Feature, error) {
	if f.knnErr != nil {
		return nil, f.knnErr
	}
	if filter == nil || len(filter.Values) == 0 {
		return nil, nil
	}
	key, _ := filter.Values[0].(string)
	return f.knn[key], nil
}
func (f fakeIndex) PointInPolygon(context.Context, string, domain.Coordinate) ([]domain.Feature, error) {
	return f.pip, f.pipErr
}
func (f fakeIndex) ResolveChain(_ context.Context, _ string, fromFID int64, _ output.AdminColumns) ([]output.AdminRow, error) {
	if f.chainErr != nil {
		return nil, f.chainErr
	}
	return f.chains[fromFID], nil
}
func (f fakeIndex) DistanceKM(_ context.Context, a, b domain.Coordinate) (float64, error) {
	dx := (b.X - a.X) * 111.32 * math.Cos(a.Y*math.Pi/180)
	dy := (b.Y - a.Y) * 111.32
	return math.Hypot(dx, dy), nil
}
func (f fakeIndex) Azimuth(_ context.Context, from, to domain.Coordinate) (float64, error) {
	dx := (to.X - from.X) * math.Cos(from.Y*math.Pi/180)
	dy := to.Y - from.Y
	deg := math.Atan2(dx, dy) * 180 / math.Pi
	if deg < 0 {
		deg += 360
	}
	return deg, nil
}

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

	// A service that is disabled, or enabled but not wired with an index, is inert:
	// both query methods return ErrDisabled without touching any dependency.
	cases := map[string]*Service{
		"disabled":              NewService(fakeIndex{}, testManifest(), nil, nil, false),
		"enabled without index": NewService(nil, testManifest(), nil, nil, true),
	}
	for name, svc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.Locate(ctx, p); !errors.Is(err, ErrDisabled) {
				t.Errorf("Locate err = %v, want ErrDisabled", err)
			}
			if _, err := svc.Bearing(ctx, p, domain.DefaultBearingPolicy()); !errors.Is(err, ErrDisabled) {
				t.Errorf("Bearing err = %v, want ErrDisabled", err)
			}
		})
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
	svc := NewService(idx, testManifest(), resolver, nil, true)

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

func TestLocateBearingRejectNonWGS84(t *testing.T) {
	svc := NewService(fakeIndex{}, testManifest(), nil, nil, true)
	p := domain.NewCoordinate(1_400_000, 6_600_000, domain.SRIDWebMercator) // 3857, not 4326
	if _, err := svc.Locate(context.Background(), p); !errors.Is(err, domain.ErrUnsupportedProjection) {
		t.Errorf("Locate non-4326 = %v, want ErrUnsupportedProjection", err)
	}
	if _, err := svc.Bearing(context.Background(), p, domain.DefaultBearingPolicy()); !errors.Is(err, domain.ErrUnsupportedProjection) {
		t.Errorf("Bearing non-4326 = %v, want ErrUnsupportedProjection", err)
	}
}

func TestLocateNoCoverage(t *testing.T) {
	svc := NewService(fakeIndex{pip: nil}, testManifest(), nil, nil, true)
	if _, err := svc.Locate(context.Background(), domain.NewWGS84Coordinate(0, 0)); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Locate with no coverage = %v, want ErrNotFound", err)
	}
}

func TestLocatePropagatesIndexError(t *testing.T) {
	sentinel := errors.New("db down")
	svc := NewService(fakeIndex{pipErr: sentinel}, testManifest(), nil, nil, true)
	if _, err := svc.Locate(context.Background(), domain.NewWGS84Coordinate(10, 50)); !errors.Is(err, sentinel) {
		t.Errorf("Locate err = %v, want wrapped sentinel", err)
	}
}

func TestLocateWithoutResolverLeavesEquivalentEmpty(t *testing.T) {
	idx := fakeIndex{pip: []domain.Feature{adminFeature("8", "Würzburg")}}
	svc := NewService(idx, testManifest(), nil, nil, true) // nil resolver → noop
	loc, err := svc.Locate(context.Background(), domain.NewWGS84Coordinate(9.93, 49.79))
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if len(loc.Chain) != 1 || loc.Chain[0].Equivalent != "" {
		t.Errorf("chain = %+v, want one unit with empty Equivalent", loc.Chain)
	}
}
