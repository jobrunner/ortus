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

func (f fakeIndex) QueryKNN(_ context.Context, _ string, p domain.Coordinate, _ int, _ float64, filter *output.Filter) ([]output.NearFeature, error) {
	if f.knnErr != nil {
		return nil, f.knnErr
	}
	if filter == nil || len(filter.Values) == 0 {
		return nil, nil
	}
	key, _ := filter.Values[0].(string)
	feats := f.knn[key]
	out := make([]output.NearFeature, 0, len(feats))
	for i := range feats {
		coord, _ := parsePointWKT(feats[i].Geometry.WKT)
		out = append(out, output.NearFeature{Feature: feats[i], DistanceKM: equirectKM(p, coord)})
	}
	return out, nil
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
	return equirectKM(a, b), nil
}

// equirectKM is the equirectangular approximation shared by the fake DistanceKM
// and QueryKNN so both track the fed coordinates identically.
func equirectKM(a, b domain.Coordinate) float64 {
	dx := (b.X - a.X) * 111.32 * math.Cos(a.Y*math.Pi/180)
	dy := (b.Y - a.Y) * 111.32
	return math.Hypot(dx, dy)
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

func (m mapResolver) Resolve(iso string, level int) (LevelMeaning, bool) {
	e, ok := m[[2]any{iso, level}]
	return LevelMeaning{Equivalent: e}, ok
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

// islandFeature builds an islands-layer polygon feature with a name (+ optional
// native/source), mirroring adminFeature for the islands layer.
func islandFeature(name, native, source string) domain.Feature {
	return domain.Feature{
		LayerName:  "islands",
		Properties: map[string]any{"name": name, "name_native": native, "name_source": source},
	}
}

func islandsManifest() Manifest {
	m := testManifest()
	m.IslandsLayer = "islands"
	m.IslandsNameColumn = "name"
	m.NameNativeColumn = "name_native"
	m.NameSourceColumn = "name_source"
	return m
}

func TestIslandsResolvesContainingIslands(t *testing.T) {
	// PiP returns the island polygon(s) containing the point (arbitrary order);
	// Islands names them, orders by name, and carries native/source provenance.
	idx := fakeIndex{pip: []domain.Feature{
		islandFeature("Sylt", "", "latin-osm"),
		islandFeature("Amrum", "", "latin-osm"),
		islandFeature("", "", ""), // unnamed fill → skipped
	}}
	svc := NewService(idx, islandsManifest(), nil, nil, true)

	got, err := svc.Islands(context.Background(), domain.NewWGS84Coordinate(8.31, 54.9))
	if err != nil {
		t.Fatalf("Islands: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("islands len = %d, want 2 (unnamed fill skipped)", len(got))
	}
	if got[0].Name != "Amrum" || got[1].Name != "Sylt" {
		t.Errorf("islands order = [%q %q], want [Amrum Sylt] (name-sorted)", got[0].Name, got[1].Name)
	}
	if got[1].NameSource.Code != "latin-osm" {
		t.Errorf("name source = %q, want latin-osm", got[1].NameSource.Code)
	}
}

func TestIslandsUnconfiguredReturnsNil(t *testing.T) {
	// testManifest has no islands layer → the block is omitted (nil, no error),
	// and PointInPolygon is never called.
	idx := fakeIndex{pipErr: errors.New("PiP must not be called when islands unconfigured")}
	svc := NewService(idx, testManifest(), nil, nil, true)

	got, err := svc.Islands(context.Background(), domain.NewWGS84Coordinate(9.93, 49.79))
	if err != nil {
		t.Fatalf("Islands (unconfigured): unexpected error %v", err)
	}
	if got != nil {
		t.Errorf("islands = %v, want nil when no islands layer configured", got)
	}
}

func TestIslandsMissingLayerDegradesToNil(t *testing.T) {
	// An islands mapping that outruns the deployed dataset (layer absent) must
	// degrade to no-result, not fail the whole gazetteer response.
	idx := fakeIndex{pipErr: domain.ErrNotFound}
	svc := NewService(idx, islandsManifest(), nil, nil, true)

	got, err := svc.Islands(context.Background(), domain.NewWGS84Coordinate(8.31, 54.9))
	if err != nil {
		t.Fatalf("Islands (missing layer): want nil error, got %v", err)
	}
	if got != nil {
		t.Errorf("islands = %v, want nil on ErrNotFound", got)
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

func adminFeatureISO(level, name, iso string) domain.Feature {
	f := adminFeature(level, name)
	f.Properties["country_iso"] = iso
	return f
}

func TestLocateCountryFromMostLocalUnit(t *testing.T) {
	// A coarse polygon carries a different code than the local units (the IL/PS L2
	// quirk), and is returned first by PiP. The locality country must come from the
	// most-local unit, deterministically — not from PiP row order.
	idx := fakeIndex{pip: []domain.Feature{
		adminFeatureISO("2", "Israel", "PS"), // coarse, different code, listed first
		adminFeatureISO("8", "Tel Aviv-Yafo", "IL"),
		adminFeatureISO("4", "Tel Aviv District", "IL"),
	}}
	svc := NewService(idx, testManifest(), nil, nil, true)
	loc, err := svc.Locate(context.Background(), domain.NewWGS84Coordinate(34.78, 32.08))
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if loc.CountryISO != "IL" {
		t.Errorf("CountryISO = %q, want IL (most-local unit), not the coarse PS polygon", loc.CountryISO)
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
