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
		LayerName:  "places",
		Properties: map[string]any{"place": class, "name": name, "admin_id": adminID},
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
	// A place inside the InsideLabelKM threshold gets a directionless label.
	idx := fakeIndex{knn: map[string][]domain.Feature{
		"village": {placeFeature("village", "Smallville", 0, 10.005)}, // ~0.36 km
	}}
	svc := NewService(idx, testManifest(), nil, nil, true)

	fix, err := svc.Bearing(context.Background(), domain.NewWGS84Coordinate(10.0, 50.0), noConstraint())
	if err != nil {
		t.Fatalf("Bearing: %v", err)
	}
	if fix.Label != "bei Smallville" {
		t.Errorf("label = %q, want 'bei Smallville'", fix.Label)
	}
	if fix.Compass != "" {
		t.Errorf("compass = %q, want empty (inside threshold)", fix.Compass)
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

func TestBearingIndexErrorPropagates(t *testing.T) {
	sentinel := errors.New("knn failed")
	svc := NewService(fakeIndex{knnErr: sentinel}, testManifest(), nil, nil, true)
	if _, err := svc.Bearing(context.Background(), domain.NewWGS84Coordinate(10, 50), noConstraint()); !errors.Is(err, sentinel) {
		t.Errorf("Bearing err = %v, want wrapped sentinel", err)
	}
}
