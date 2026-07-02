package gazetteer

import (
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
)

func cand(class domain.PlaceClass, name string, distKM float64) Candidate {
	return Candidate{Place: domain.Place{Name: name, Class: class}, DistanceKM: distKM}
}

func TestRankedSalienceSelect(t *testing.T) {
	pol := domain.DefaultBearingPolicy() // village 5, town 18, city 60

	cases := []struct {
		name     string
		cands    []Candidate
		wantName string
		wantOK   bool
	}{
		{name: "empty", cands: nil, wantOK: false},
		{
			// Beyond the proximity override (town at 8 km > 5 km), pure salience
			// applies: the city outranks town and village.
			name:     "city outranks town and village beyond override",
			cands:    []Candidate{cand(domain.ClassVillage, "V", 1), cand(domain.ClassCity, "C", 8), cand(domain.ClassTown, "T", 8)},
			wantName: "C", wantOK: true,
		},
		{
			name:     "city out of reach loses to eligible town",
			cands:    []Candidate{cand(domain.ClassCity, "C", 80), cand(domain.ClassTown, "T", 10)},
			wantName: "T", wantOK: true,
		},
		{
			name:   "all out of reach → none",
			cands:  []Candidate{cand(domain.ClassVillage, "V", 6), cand(domain.ClassTown, "T", 20)},
			wantOK: false,
		},
		{
			name:     "same class → nearer wins",
			cands:    []Candidate{cand(domain.ClassTown, "Far", 10), cand(domain.ClassTown, "Near", 4)},
			wantName: "Near", wantOK: true,
		},
		{
			name:     "same class, same distance → smaller name wins",
			cands:    []Candidate{cand(domain.ClassTown, "Bravo", 4), cand(domain.ClassTown, "Alpha", 4)},
			wantName: "Alpha", wantOK: true,
		},
		{
			name:   "unknown class (no reach) is never eligible",
			cands:  []Candidate{cand(domain.ClassUnknown, "U", 0.1)},
			wantOK: false,
		},
		{
			// Proximity override: a nearby town beats a far (more salient) city.
			name:     "near town beats far city (override)",
			cands:    []Candidate{cand(domain.ClassCity, "FarCity", 23), cand(domain.ClassTown, "NearTown", 2)},
			wantName: "NearTown", wantOK: true,
		},
		{
			// Override excludes villages — a nearby village does NOT beat a far city.
			name:     "near village does not trigger override",
			cands:    []Candidate{cand(domain.ClassCity, "City", 16), cand(domain.ClassVillage, "Hamlet", 1)},
			wantName: "City", wantOK: true,
		},
		{
			// Both town and city inside the override radius → nearest wins.
			name:     "override picks nearest prominent",
			cands:    []Candidate{cand(domain.ClassCity, "City", 4), cand(domain.ClassTown, "Town", 2)},
			wantName: "Town", wantOK: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			best, ok := RankedSalience{}.Select(tc.cands, pol)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && best.Place.Name != tc.wantName {
				t.Errorf("best = %q, want %q", best.Place.Name, tc.wantName)
			}
		})
	}
}
