package gazetteer

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/input"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// geoFeat builds an admin feature carrying only the columns countryOf reads.
func geoFeat(level, iso string) domain.Feature {
	return domain.Feature{Properties: map[string]any{"admin_level": level, "country_iso": iso}}
}

// TestCountryOf pins the "most-local polygon wins, tie -> smaller ISO, non-numeric
// level sorts below real levels, empty ISO skipped" logic (bearing.go countryOf).
func TestCountryOf(t *testing.T) {
	svc := NewService(fakeIndex{}, testManifest(), nil, nil, true)
	cases := []struct {
		name       string
		containing []domain.Feature
		want       string
	}{
		{"no polygons -> empty", nil, ""},
		{"single", []domain.Feature{geoFeat("6", "DE")}, "DE"},
		{"most-local (higher level) wins", []domain.Feature{geoFeat("2", "DE"), geoFeat("8", "FR")}, "FR"},
		{"lower level loses even if listed first", []domain.Feature{geoFeat("8", "FR"), geoFeat("2", "DE")}, "FR"},
		{"tie on level -> lexicographically smaller ISO", []domain.Feature{geoFeat("4", "PL"), geoFeat("4", "DE")}, "DE"},
		// Smaller ISO listed first: a later equal-level entry must not overwrite it,
		// so the level comparison has to stay strict rather than accept equality.
		{"tie on level, smaller ISO first stays", []domain.Feature{geoFeat("4", "DE"), geoFeat("4", "PL")}, "DE"},
		{"non-numeric level sorts below a real level", []domain.Feature{geoFeat("x", "FR"), geoFeat("2", "DE")}, "DE"},
		{"all non-numeric -> tie at -1 -> smaller ISO", []domain.Feature{geoFeat("x", "FR"), geoFeat("y", "DE")}, "DE"},
		{"empty ISO skipped despite higher level", []domain.Feature{geoFeat("8", ""), geoFeat("2", "DE")}, "DE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := svc.countryOf(tc.containing); got != tc.want {
				t.Errorf("countryOf = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestParsePointWKT pins the POINT-WKT parsing guards (bearing.go parsePointWKT).
func TestParsePointWKT(t *testing.T) {
	cases := []struct {
		name   string
		wkt    string
		wantX  float64
		wantY  float64
		wantOK bool
	}{
		{"valid 2D", "POINT(10.02 50)", 10.02, 50, true},
		{"valid Z (extra field ignored)", "POINT Z(10 50 3)", 10, 50, true},
		{"leading paren at index 0", "(10 50)", 10, 50, true}, // opening paren at the start must stay valid
		{"no parentheses", "POINT 10 50", 0, 0, false},
		{"reversed parentheses", "POINT)10 50(", 0, 0, false},
		{"too few fields", "POINT(10)", 0, 0, false},
		{"non-numeric", "POINT(a b)", 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, ok := parsePointWKT(tc.wkt)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && (math.Abs(c.X-tc.wantX) > 1e-9 || math.Abs(c.Y-tc.wantY) > 1e-9) {
				t.Errorf("coord = (%v,%v), want (%v,%v)", c.X, c.Y, tc.wantX, tc.wantY)
			}
		})
	}
}

// TestBuildFixLabelThreshold pins the in / prope / directional decision and the
// InsideLabelKM boundary (bearing.go buildFix).
func TestBuildFixLabelThreshold(t *testing.T) {
	svc := NewService(fakeIndex{}, testManifest(), nil, nil, true)
	pol := domain.DefaultBearingPolicy() // its InsideLabelKM threshold is one kilometer
	ref := domain.Place{Name: "Würzburg", At: domain.NewWGS84Coordinate(9.93, 49.79)}
	p := domain.NewWGS84Coordinate(10.10, 49.79) // due east of ref, so Azimuth is well-defined

	// nearer than InsideLabelKM -> "prope X", no azimuth/direction. (The "in X" case is
	// decided earlier by placeInsideOf, not buildFix — see TestBearingInsideByDistance.)
	if fx := svc.buildFix(context.Background(), p, Candidate{Place: ref, DistanceKM: 0.5}, pol); fx.Label != "prope Würzburg" || fx.Compass != "" {
		t.Errorf("near: label=%q compass=%q, want 'prope Würzburg'/no compass", fx.Label, fx.Compass)
	}
	// exactly at InsideLabelKM is NOT "near" (boundary is <, not <=) -> directional.
	if fx := svc.buildFix(context.Background(), p, Candidate{Place: ref, DistanceKM: pol.InsideLabelKM}, pol); fx.Compass == "" || fx.Label == "prope Würzburg" {
		t.Errorf("at threshold should be directional, got label=%q compass=%q", fx.Label, fx.Compass)
	}
	// clearly outside -> directional label with a compass point.
	if fx := svc.buildFix(context.Background(), p, Candidate{Place: ref, DistanceKM: 12}, pol); fx.Compass == "" || fx.Label == "prope Würzburg" {
		t.Errorf("far: expected directional label, got label=%q compass=%q", fx.Label, fx.Compass)
	}
}

// TestGatherCandidatesSkipsZeroRadius pins the `GatherRadiusKM(class) <= 0` skip:
// a class with no gather radius must not contribute candidates (bearing.go
// gatherCandidates).
func TestGatherCandidatesSkipsZeroRadius(t *testing.T) {
	idx := fakeIndex{knn: map[string][]domain.Feature{
		"city":    {placeFeature("city", "C", 1, 10.1)},
		"town":    {placeFeature("town", "T", 2, 10.1)},
		"village": {placeFeature("village", "V", 3, 10.1)},
	}}
	svc := NewService(idx, testManifest(), nil, nil, true)
	// Rank-mode policy: only ClassCity has a reach; town/village -> GatherRadiusKM 0 -> skipped.
	pol := domain.BearingPolicy{Reach: map[domain.PlaceClass]float64{domain.ClassCity: 60}}
	p := domain.NewWGS84Coordinate(10.0, 50.0)
	cands, err := svc.gatherCandidates(context.Background(), p, pol, insideConstraint{})
	if err != nil {
		t.Fatalf("gatherCandidates: %v", err)
	}
	if len(cands) != 1 || cands[0].Place.Class != domain.ClassCity {
		t.Fatalf("want only the city candidate (town/village skipped at radius 0), got %d: %+v", len(cands), cands)
	}
}

// countingIndex wraps a fakeIndex and counts the batched chain calls, so a test can
// assert on the NUMBER of round-trips rather than only on the result.
type countingIndex struct {
	fakeIndex
	chainCalls *int
	seedsSeen  *int
	pipCalls   *int
}

func (c countingIndex) PointInPolygon(ctx context.Context, layer string, p domain.Coordinate) ([]domain.Feature, error) {
	if c.pipCalls != nil {
		*c.pipCalls++
	}
	return c.fakeIndex.PointInPolygon(ctx, layer, p)
}

func (c countingIndex) ResolveChains(ctx context.Context, layer string, fromFIDs []int64, cols output.AdminColumns) (map[int64][]output.AdminRow, error) {
	*c.chainCalls++
	*c.seedsSeen += len(fromFIDs)
	return c.fakeIndex.ResolveChains(ctx, layer, fromFIDs, cols)
}

// TestGatherCandidatesResolvesChainsInOneBatch is the regression test for the N+1
// that cost 234 SpatiaLite round-trips and ~700 ms per bearing request: the tier
// constraint used to resolve each candidate's admin lineage with its own query.
//
// It asserts the round-trip COUNT, not the duration — the count is a property of
// the code and does not move with machine speed, so this fails deterministically
// the moment the per-candidate query returns.
func TestGatherCandidatesResolvesChainsInOneBatch(t *testing.T) {
	const perClass = 12
	knn := map[string][]domain.Feature{}
	chains := map[int64][]output.AdminRow{}
	for ci, class := range []string{"city", "town", "village"} {
		feats := make([]domain.Feature, 0, perClass)
		for i := range perClass {
			// A distinct admin id per place, so deduplication cannot mask a
			// per-candidate query as a single batched one.
			adminID := int64(ci*100 + i + 1)
			feats = append(feats, placeFeature(class, "P", int(adminID), 10.0+float64(i)*0.01))
			chains[adminID] = []output.AdminRow{
				{FID: adminID, Level: 8, CountryISO: "DE"},
				{FID: 7, Level: 4, CountryISO: "DE"}, // shared state-level ancestor
			}
		}
		knn[class] = feats
	}

	calls, seeds := 0, 0
	idx := countingIndex{
		fakeIndex:  fakeIndex{knn: knn, chains: chains},
		chainCalls: &calls,
		seedsSeen:  &seeds,
	}
	svc := NewService(idx, testManifest(), mapResolver{{"DE", 4}: "state", {"DE", 8}: "municipality"}, nil, true)
	pol := domain.BearingPolicy{Reach: map[domain.PlaceClass]float64{
		domain.ClassCity: 60, domain.ClassTown: 60, domain.ClassVillage: 60,
	}, ConstraintTier: "state"}

	cands, err := svc.gatherCandidates(context.Background(), domain.NewWGS84Coordinate(10.0, 50.0), pol,
		insideConstraint{constrained: true, ancestor: 7, tier: "state"})
	if err != nil {
		t.Fatalf("gatherCandidates: %v", err)
	}
	if calls != 1 {
		t.Errorf("ResolveChains called %d times, want exactly 1 — the lineage lookup must be batched across all classes", calls)
	}
	if seeds < perClass*3 {
		t.Errorf("batch carried %d seeds, want >= %d — every candidate's lineage must be in the one call", seeds, perClass*3)
	}
	// All candidates share ancestor 7, so the guard must admit every one of them:
	// batching must not change which anchors qualify.
	if len(cands) != perClass*3 {
		t.Errorf("admitted %d candidates, want %d", len(cands), perClass*3)
	}
}

// TestGatherCandidatesSkipsChainQueryWhenUnconstrained pins that an inactive
// constraint issues no lineage query at all, rather than a batch of zero.
func TestGatherCandidatesSkipsChainQueryWhenUnconstrained(t *testing.T) {
	calls, seeds := 0, 0
	idx := countingIndex{
		fakeIndex: fakeIndex{knn: map[string][]domain.Feature{
			"city": {placeFeature("city", "C", 1, 10.05)},
		}},
		chainCalls: &calls,
		seedsSeen:  &seeds,
	}
	svc := NewService(idx, testManifest(), mapResolver{{"DE", 4}: "state", {"DE", 8}: "municipality"}, nil, true)
	pol := domain.BearingPolicy{Reach: map[domain.PlaceClass]float64{domain.ClassCity: 60}}

	if _, err := svc.gatherCandidates(context.Background(), domain.NewWGS84Coordinate(10.0, 50.0), pol,
		insideConstraint{}); err != nil {
		t.Fatalf("gatherCandidates: %v", err)
	}
	if calls != 0 {
		t.Errorf("ResolveChains called %d times with no active constraint, want 0", calls)
	}
}

// TestAdminPointInPolygonIsSharedWithinARequest pins the request-scoped cache.
// Locate and Bearing both need the containing admin polygons; without the cache
// that identical query ran twice per request and was the response's largest
// single cost. Counted, not timed, so the assertion is deterministic.
func TestAdminPointInPolygonIsSharedWithinARequest(t *testing.T) {
	newSvc := func(pip *int) *Service {
		idx := countingIndex{
			fakeIndex: fakeIndex{
				pip: []domain.Feature{adminFeature("4", "Bayern"), adminFeature("8", "Würzburg")},
				knn: map[string][]domain.Feature{"city": {placeFeature("city", "Würzburg", 1, 9.9)}},
			},
			pipCalls: pip,
		}
		svc := NewService(idx, testManifest(), mapResolver{{"DE", 4}: "state", {"DE", 8}: "municipality"}, nil, true)
		return svc
	}
	p := domain.NewWGS84Coordinate(10.0, 50.0)
	pol := domain.BearingPolicy{Reach: map[domain.PlaceClass]float64{domain.ClassCity: 60}}

	t.Run("with a scope the query runs once", func(t *testing.T) {
		calls := 0
		svc := newSvc(&calls)
		ctx := input.WithPointInPolygonCache(context.Background())
		if _, err := svc.Locate(ctx, p); err != nil {
			t.Fatalf("Locate: %v", err)
		}
		if _, err := svc.Bearing(ctx, p, pol); err != nil && !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("Bearing: %v", err)
		}
		if calls != 1 {
			t.Errorf("admin PointInPolygon ran %d times, want 1", calls)
		}
	})

	// Without a scope the service must keep working, just without the saving —
	// a caller that does not open one (a test, a direct embedder) is not broken.
	t.Run("without a scope it is a pass-through", func(t *testing.T) {
		calls := 0
		svc := newSvc(&calls)
		ctx := context.Background()
		if _, err := svc.Locate(ctx, p); err != nil {
			t.Fatalf("Locate: %v", err)
		}
		if _, err := svc.Bearing(ctx, p, pol); err != nil && !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("Bearing: %v", err)
		}
		if calls != 2 {
			t.Errorf("admin PointInPolygon ran %d times without a scope, want 2", calls)
		}
	})

	// A different coordinate must not be answered from the cache.
	t.Run("a different point is not served from the cache", func(t *testing.T) {
		calls := 0
		svc := newSvc(&calls)
		ctx := input.WithPointInPolygonCache(context.Background())
		if _, err := svc.Locate(ctx, p); err != nil {
			t.Fatalf("Locate: %v", err)
		}
		if _, err := svc.Locate(ctx, domain.NewWGS84Coordinate(11.0, 51.0)); err != nil {
			t.Fatalf("Locate(other): %v", err)
		}
		if calls != 2 {
			t.Errorf("admin PointInPolygon ran %d times for two distinct points, want 2", calls)
		}
	})
}
