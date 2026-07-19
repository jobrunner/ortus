package gazetteer

import (
	"context"
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
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
			if ok && (c.X != tc.wantX || c.Y != tc.wantY) {
				t.Errorf("coord = (%v,%v), want (%v,%v)", c.X, c.Y, tc.wantX, tc.wantY)
			}
		})
	}
}

// TestBuildFixLabelThreshold pins the in / prope / directional decision and the
// InsideLabelKM boundary (bearing.go buildFix).
func TestBuildFixLabelThreshold(t *testing.T) {
	svc := NewService(fakeIndex{}, testManifest(), nil, nil, true)
	pol := domain.DefaultBearingPolicy() // InsideLabelKM = 1.0
	ref := domain.Place{Name: "Würzburg", At: domain.NewWGS84Coordinate(9.93, 49.79)}
	p := domain.NewWGS84Coordinate(10.10, 49.79) // due east of ref, so Azimuth is well-defined

	// inside -> "in X", no direction.
	if fx := svc.buildFix(context.Background(), p, Candidate{Place: ref, DistanceKM: 20}, pol, true); !fx.Inside || fx.Label != "in Würzburg" {
		t.Errorf("inside: label=%q inside=%v, want 'in Würzburg'/true", fx.Label, fx.Inside)
	}
	// outside AND nearer than InsideLabelKM -> "prope X", no azimuth/direction.
	if fx := svc.buildFix(context.Background(), p, Candidate{Place: ref, DistanceKM: 0.5}, pol, false); fx.Label != "prope Würzburg" || fx.Compass != "" {
		t.Errorf("near: label=%q compass=%q, want 'prope Würzburg'/no compass", fx.Label, fx.Compass)
	}
	// exactly at InsideLabelKM is NOT "near" (boundary is <, not <=) -> directional.
	if fx := svc.buildFix(context.Background(), p, Candidate{Place: ref, DistanceKM: pol.InsideLabelKM}, pol, false); fx.Compass == "" || fx.Label == "prope Würzburg" {
		t.Errorf("at threshold should be directional, got label=%q compass=%q", fx.Label, fx.Compass)
	}
	// clearly outside -> directional label with a compass point.
	if fx := svc.buildFix(context.Background(), p, Candidate{Place: ref, DistanceKM: 12}, pol, false); fx.Compass == "" || fx.Label == "prope Würzburg" {
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
	cands, err := svc.gatherCandidates(context.Background(), p, pol, 0, false, "")
	if err != nil {
		t.Fatalf("gatherCandidates: %v", err)
	}
	if len(cands) != 1 || cands[0].Place.Class != domain.ClassCity {
		t.Fatalf("want only the city candidate (town/village skipped at radius 0), got %d: %+v", len(cands), cands)
	}
}
