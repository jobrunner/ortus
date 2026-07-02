package domain

import "testing"

func TestPlaceClassString(t *testing.T) {
	cases := map[PlaceClass]string{
		ClassVillage:   "village",
		ClassTown:      "town",
		ClassCity:      "city",
		ClassUnknown:   "unknown",
		PlaceClass(99): "unknown",
	}
	for c, want := range cases {
		if got := c.String(); got != want {
			t.Errorf("PlaceClass(%d).String() = %q, want %q", c, got, want)
		}
	}
}

func TestParsePlaceClass(t *testing.T) {
	cases := map[string]struct {
		want PlaceClass
		ok   bool
	}{
		"village": {ClassVillage, true},
		"town":    {ClassTown, true},
		"city":    {ClassCity, true},
		"hamlet":  {ClassUnknown, false},
		"":        {ClassUnknown, false},
	}
	for in, exp := range cases {
		got, ok := ParsePlaceClass(in)
		if got != exp.want || ok != exp.ok {
			t.Errorf("ParsePlaceClass(%q) = %v, %v; want %v, %v", in, got, ok, exp.want, exp.ok)
		}
	}
}

func TestPlaceClassOrdering(t *testing.T) {
	// Salience ordering is load-bearing for the bearing selection (§6).
	if ClassCity <= ClassTown || ClassTown <= ClassVillage || ClassVillage <= ClassUnknown {
		t.Fatal("PlaceClass constants are not ordered by ascending salience")
	}
}

func TestDefaultBearingPolicy(t *testing.T) {
	p := DefaultBearingPolicy()
	if p.ReachKM(ClassVillage) != 5 || p.ReachKM(ClassTown) != 18 || p.ReachKM(ClassCity) != 60 {
		t.Errorf("unexpected reach radii: %+v", p.Reach)
	}
	if p.ConstraintTier != "state" {
		t.Errorf("ConstraintTier = %q, want state", p.ConstraintTier)
	}
	if p.InsideLabelKM != 1.0 {
		t.Errorf("InsideLabelKM = %v, want 1.0", p.InsideLabelKM)
	}
	if p.CompassPoints != 8 {
		t.Errorf("CompassPoints = %d, want 8", p.CompassPoints)
	}
}

func TestBearingPolicyReachKMUnknownClass(t *testing.T) {
	p := DefaultBearingPolicy()
	if got := p.ReachKM(ClassUnknown); got != 0 {
		t.Errorf("ReachKM(unknown) = %v, want 0 (never an anchor)", got)
	}
}

func TestBearingPolicyOrDefault(t *testing.T) {
	// A zero policy (nil Reach) falls back to the defaults.
	if got := (BearingPolicy{}).OrDefault(); got.ReachKM(ClassCity) != DefaultBearingPolicy().ReachKM(ClassCity) {
		t.Errorf("zero policy OrDefault did not fall back to defaults: %+v", got)
	}
	// A configured policy is returned unchanged.
	custom := BearingPolicy{Reach: map[PlaceClass]float64{ClassCity: 42}}
	if got := custom.OrDefault(); got.ReachKM(ClassCity) != 42 {
		t.Errorf("configured policy OrDefault = %+v, want the policy itself", got)
	}
}
