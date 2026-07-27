package gazetteer

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// placeFeature builds a places-layer feature at (lon, 50°N); latitude is fixed
// since these tests vary only longitude to control the east-west bearing.
func placeFeature(class, name string, adminID int, lon float64) domain.Feature {
	f := domain.Feature{
		LayerName: "places",
		// country_iso matches the mock admin polygons ("DE") so the same-country guard
		// admits these candidates; tests exercising cross-country are explicit.
		Properties: map[string]any{"place": class, "name": name, "admin_id": adminID, "country_iso": "DE"},
	}
	f.Geometry.WKT = fmt.Sprintf("POINT(%g 50)", lon)
	return f
}

// adminFeatureID is adminFeature with an explicit fid (needed to resolve the
// boundary-constraint ancestor).
func adminFeatureID(fid int64, level, name string) domain.Feature {
	f := adminFeature(level, name)
	f.ID = fid
	return f
}

// noConstraint is the default policy with the boundary constraint disabled, to
// isolate the salience selection.
func noConstraint() domain.BearingPolicy {
	pol := domain.DefaultBearingPolicy()
	pol.ConstraintTier = ""
	return pol
}

func TestBearingInsideByDistance(t *testing.T) {
	// A village whose node is well within its inside-radius (~0.36 km < 0.8 km) → the
	// point counts as "in <village>": Latin "in", no direction, Inside=true.
	idx := fakeIndex{knn: map[string][]domain.Feature{"village": {placeFeature("village", "Rödelsee", 0, 10.005)}}}
	svc := NewService(idx, testManifest(), nil, nil, true)

	fix, err := svc.Bearing(context.Background(), domain.NewWGS84Coordinate(10.0, 50.0), noConstraint())
	if err != nil {
		t.Fatalf("Bearing: %v", err)
	}
	if !fix.Inside || fix.Label != "in Rödelsee" || fix.Compass != "" {
		t.Errorf("got label=%q inside=%v compass=%q, want 'in Rödelsee'/true/no-compass", fix.Label, fix.Inside, fix.Compass)
	}
}

func TestBearingNotInsideBeyondRadius(t *testing.T) {
	// The Schwanberg case: the nearest village is ~1.8 km away — beyond the 0.8 km
	// village radius — so it is NOT "in <village>"; the fix falls through to a
	// directional bearing to the salient city. (Old admin-containment logic wrongly
	// said "in <village>" here.)
	idx := fakeIndex{
		knn: map[string][]domain.Feature{
			"village": {placeFeature("village", "Rödelsee", 0, 10.025)}, // ~1.79 km E, beyond 0.8 km
			"city":    {placeFeature("city", "Bigtown", 0, 9.9)},        // salient anchor for the fallback
		},
	}
	svc := NewService(idx, testManifest(), nil, nil, true)

	fix, err := svc.Bearing(context.Background(), domain.NewWGS84Coordinate(10.0, 50.0), noConstraint())
	if err != nil {
		t.Fatalf("Bearing: %v", err)
	}
	if fix.Inside || fix.Reference.Name != "Bigtown" || fix.Compass != "E" {
		t.Errorf("got label=%q inside=%v ref=%q, want directional to Bigtown (not 'in Rödelsee')", fix.Label, fix.Inside, fix.Reference.Name)
	}
}

func TestBearingClassPrecedence(t *testing.T) {
	// All within reach but beyond the 5 km proximity override → the most salient
	// (city) wins outright over town and village.
	idx := fakeIndex{knn: map[string][]domain.Feature{
		"city":    {placeFeature("city", "Bigtown", 0, 9.9)},   // ~7.2 km W of query → point is E of it
		"town":    {placeFeature("town", "Midtown", 0, 10.09)}, // ~6.4 km (beyond override)
		"village": {placeFeature("village", "Smallville", 0, 10.06)},
	}}
	svc := NewService(idx, testManifest(), nil, nil, true)

	fix, err := svc.Bearing(context.Background(), domain.NewWGS84Coordinate(10.0, 50.0), noConstraint())
	if err != nil {
		t.Fatalf("Bearing: %v", err)
	}
	if fix.Reference.Name != "Bigtown" {
		t.Errorf("reference = %q, want Bigtown (city outranks nearer town/village)", fix.Reference.Name)
	}
	if fix.Compass != "E" {
		t.Errorf("compass = %q, want E", fix.Compass)
	}
	if fix.Label != "7 km E Bigtown" {
		t.Errorf("label = %q, want '7 km E Bigtown'", fix.Label)
	}
}

func TestBearingReachExclusion(t *testing.T) {
	// The only city is beyond its 60 km reach; the town within reach wins.
	idx := fakeIndex{knn: map[string][]domain.Feature{
		"city": {placeFeature("city", "Faraway", 0, 11.0)}, // ~72 km, out of reach
		"town": {placeFeature("town", "Midtown", 0, 10.02)},
	}}
	svc := NewService(idx, testManifest(), nil, nil, true)

	fix, err := svc.Bearing(context.Background(), domain.NewWGS84Coordinate(10.0, 50.0), noConstraint())
	if err != nil {
		t.Fatalf("Bearing: %v", err)
	}
	if fix.Reference.Name != "Midtown" {
		t.Errorf("reference = %q, want Midtown (city out of reach)", fix.Reference.Name)
	}
}

func TestBearingInsideLabel(t *testing.T) {
	// The directionless "prope" path: the anchor is beyond its inside-radius (so NOT
	// "in X") yet within InsideLabelKM (1 km), where a compass bearing would be noise.
	// Smallville sits ~0.93 km away — outside the 0.8 km village inside-radius but
	// inside the 1 km prope threshold.
	idx := fakeIndex{knn: map[string][]domain.Feature{
		"village": {placeFeature("village", "Smallville", 0, 10.013)}, // ~0.93 km
	}}
	svc := NewService(idx, testManifest(), nil, nil, true)

	fix, err := svc.Bearing(context.Background(), domain.NewWGS84Coordinate(10.0, 50.0), noConstraint())
	if err != nil {
		t.Fatalf("Bearing: %v", err)
	}
	if fix.Label != "prope Smallville" {
		t.Errorf("label = %q, want 'prope Smallville'", fix.Label)
	}
	if fix.Compass != "" {
		t.Errorf("compass = %q, want empty (inside prope threshold)", fix.Compass)
	}
	if fix.Inside {
		t.Error("Inside = true, want false (beyond the village inside-radius)")
	}
}

func TestBearingAdminContainmentNoLongerForcesInside(t *testing.T) {
	// Regression guard for the fix: being inside the anchor's admin unit no longer
	// yields "in X". The city node is ~3.6 km away — beyond its 3 km inside-radius —
	// so although the point lies in Ochsenfurt's admin polygon (fid 42), the label is
	// directional, not "in Ochsenfurt". (Old admin-containment logic wrongly said
	// "in Ochsenfurt" here — the same class of bug as a point 15 km inside a large
	// municipality being reported as "in <town>".)
	idx := fakeIndex{
		// ~3.6 km E — beyond the 3 km city inside-radius, so placeInsideOf rejects it and
		// the fix falls through to a directional bearing (same fixture serves both paths).
		knn: map[string][]domain.Feature{"city": {placeFeature("city", "Ochsenfurt", 42, 10.05)}},
		pip: []domain.Feature{adminFeatureID(42, "8", "Ochsenfurt")}, // query point ∈ fid 42
	}
	svc := NewService(idx, testManifest(), nil, nil, true)

	fix, err := svc.Bearing(context.Background(), domain.NewWGS84Coordinate(10.0, 50.0), noConstraint())
	if err != nil {
		t.Fatalf("Bearing: %v", err)
	}
	if fix.Inside {
		t.Error("Inside = true, want false (3.6 km exceeds the city inside-radius; admin containment must not force 'in')")
	}
	if fix.Compass == "" || fix.Label == "in Ochsenfurt" {
		t.Errorf("label = %q compass = %q, want a directional bearing to Ochsenfurt", fix.Label, fix.Compass)
	}
}

func TestBearingNoCandidate(t *testing.T) {
	svc := NewService(fakeIndex{}, testManifest(), nil, nil, true)
	if _, err := svc.Bearing(context.Background(), domain.NewWGS84Coordinate(10, 50), noConstraint()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Bearing with no candidate = %v, want ErrNotFound", err)
	}
}

func TestBearingBoundaryConstraint(t *testing.T) {
	// A nearer city in a different state must be skipped in favor of the in-state
	// one when the constraint tier is "state".
	idx := fakeIndex{
		pip: []domain.Feature{adminFeatureID(20, "4", "Bayern")}, // query point's state = fid 20
		knn: map[string][]domain.Feature{
			"city": {
				placeFeature("city", "OtherState", 9, 10.05), // nearer (~3.6 km), state fid 99
				placeFeature("city", "SameState", 8, 10.1),   // farther (~7.2 km), state fid 20
			},
		},
		chains: map[int64][]output.AdminRow{
			9: {{FID: 9, Level: 8, CountryISO: "DE"}, {FID: 99, Level: 4, CountryISO: "DE"}},
			8: {{FID: 8, Level: 8, CountryISO: "DE"}, {FID: 20, Level: 4, CountryISO: "DE"}},
		},
	}
	resolver := mapResolver{[2]any{"DE", 4}: "state", [2]any{"DE", 8}: "municipality"}
	svc := NewService(idx, testManifest(), resolver, nil, true) // ConstraintTier "state" (default)

	fix, err := svc.Bearing(context.Background(), domain.NewWGS84Coordinate(10.0, 50.0), domain.DefaultBearingPolicy())
	if err != nil {
		t.Fatalf("Bearing: %v", err)
	}
	if fix.Reference.Name != "SameState" {
		t.Errorf("reference = %q, want SameState (nearer OtherState is across the boundary)", fix.Reference.Name)
	}
}

func TestBearingSameCountryConstraint(t *testing.T) {
	// Requirement #1: the anchor must be in the query point's country. A nearer city
	// across the border (different country_iso) is skipped for the in-country one,
	// even with the state constraint off (so only the country guard is in play).
	foreign := placeFeature("city", "Foreign", 0, 10.03) // ~2.1 km, but abroad
	foreign.Properties["country_iso"] = "FR"
	idx := fakeIndex{
		pip: []domain.Feature{adminFeatureID(2, "2", "Germany")}, // query point ∈ DE
		knn: map[string][]domain.Feature{
			"city": {foreign, placeFeature("city", "Domestic", 0, 10.1)}, // farther (~7.2 km), DE
		},
	}
	svc := NewService(idx, testManifest(), nil, nil, true)

	fix, err := svc.Bearing(context.Background(), domain.NewWGS84Coordinate(10.0, 50.0), noConstraint())
	if err != nil {
		t.Fatalf("Bearing: %v", err)
	}
	if fix.Reference.Name != "Domestic" {
		t.Errorf("reference = %q, want Domestic (nearer Foreign is across the country border)", fix.Reference.Name)
	}
}

func TestBearingConstraintAncestorErrorPropagates(t *testing.T) {
	// A PointInPolygon failure while resolving the constraint tier must surface,
	// not silently disable the boundary constraint.
	sentinel := errors.New("pip failed")
	resolver := mapResolver{[2]any{"DE", 4}: "state"}
	svc := NewService(fakeIndex{pipErr: sentinel}, testManifest(), resolver, nil, true)
	if _, err := svc.Bearing(context.Background(), domain.NewWGS84Coordinate(10, 50), domain.DefaultBearingPolicy()); !errors.Is(err, sentinel) {
		t.Errorf("Bearing err = %v, want wrapped sentinel", err)
	}
}

func TestBearingSameTierErrorPropagates(t *testing.T) {
	// A ResolveChain failure while checking a candidate's tier must surface, not
	// silently exclude the candidate (which would mask the failure as ErrNotFound).
	sentinel := errors.New("resolvechain failed")
	idx := fakeIndex{
		pip:      []domain.Feature{adminFeatureID(20, "4", "Bayern")}, // query state resolves
		knn:      map[string][]domain.Feature{"city": {placeFeature("city", "X", 9, 10.1)}},
		chainErr: sentinel,
	}
	resolver := mapResolver{[2]any{"DE", 4}: "state"}
	svc := NewService(idx, testManifest(), resolver, nil, true)
	if _, err := svc.Bearing(context.Background(), domain.NewWGS84Coordinate(10, 50), domain.DefaultBearingPolicy()); !errors.Is(err, sentinel) {
		t.Errorf("Bearing err = %v, want wrapped sentinel", err)
	}
}

func TestBearingIndexErrorPropagates(t *testing.T) {
	sentinel := errors.New("knn failed")
	svc := NewService(fakeIndex{knnErr: sentinel}, testManifest(), nil, nil, true)
	if _, err := svc.Bearing(context.Background(), domain.NewWGS84Coordinate(10, 50), noConstraint()); !errors.Is(err, sentinel) {
		t.Errorf("Bearing err = %v, want wrapped sentinel", err)
	}
}
