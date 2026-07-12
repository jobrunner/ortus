package gazetteer

import (
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
)

func cand(class domain.PlaceClass, name string, distKM float64) Candidate {
	return Candidate{Place: domain.Place{Name: name, Class: class}, DistanceKM: distKM}
}

// pcand builds a candidate with prominence fields for CompositeSalience tests.
func pcand(class domain.PlaceClass, name string, distKM float64, pop int64, capital, wikidata string) Candidate {
	return Candidate{
		Place: domain.Place{
			Name: name, Class: class,
			Population: pop, Capital: capital, Wikidata: wikidata,
		},
		DistanceKM: distKM,
	}
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

func TestCompositeSalienceSelect(t *testing.T) {
	cs := DefaultCompositeSalience() // PopWeight 1, WikiWeight .3, DecayPerKM .04, CapitalScale .8
	pol := domain.BearingPolicy{CandidateRadiusKM: 120}

	cases := []struct {
		name     string
		cands    []Candidate
		wantName string
		wantOK   bool
	}{
		{name: "empty", cands: nil, wantOK: false},
		{
			// The point of the whole feature: a big city a moderate distance away
			// beats an obscure town next door (today's bearing does the opposite).
			name: "prominent city at distance beats near small town",
			cands: []Candidate{
				pcand(domain.ClassTown, "Suburb", 1.9, 8000, "", "Q1"),
				pcand(domain.ClassCity, "Metropole", 15, 1_484_226, "4", "Q2"),
			},
			wantName: "Metropole", wantOK: true,
		},
		{
			// Balanced decay: a nearby famous city beats a bigger city much farther.
			name: "near famous city beats far bigger city",
			cands: []Candidate{
				pcand(domain.ClassCity, "Siena", 14, 53_903, "6", "Q1"),
				pcand(domain.ClassCity, "Firenze", 64, 382_808, "4", "Q2"),
			},
			wantName: "Siena", wantOK: true,
		},
		{
			// Capital bonus: equal population + distance, the higher-rank seat wins.
			name: "capital rank breaks otherwise-equal",
			cands: []Candidate{
				pcand(domain.ClassTown, "Plain", 10, 20000, "8", ""),
				pcand(domain.ClassTown, "Seat", 10, 20000, "4", ""),
			},
			wantName: "Seat", wantOK: true,
		},
		{
			// Population unknown → class prior; a city with no population still
			// outranks a village with no population at similar distance.
			name: "class prior when population unknown",
			cands: []Candidate{
				pcand(domain.ClassVillage, "Vlg", 3, 0, "", ""),
				pcand(domain.ClassCity, "Cty", 3, 0, "", ""),
			},
			wantName: "Cty", wantOK: true,
		},
		{
			// No proximity override (unlike RankedSalience): the near small town does
			// NOT automatically win — score decides, so the metropolis wins.
			name: "no proximity override",
			cands: []Candidate{
				pcand(domain.ClassTown, "Near", 2, 5000, "", ""),
				pcand(domain.ClassCity, "Big", 18, 800_000, "4", "Q9"),
			},
			wantName: "Big", wantOK: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			best, ok := cs.Select(tc.cands, pol)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && best.Place.Name != tc.wantName {
				t.Errorf("best = %q, want %q", best.Place.Name, tc.wantName)
			}
		})
	}
}

func TestCapitalBonusOrdering(t *testing.T) {
	// The default CapitalBonus table is monotonic: national ≥ regional ≥ lower
	// seats ≥ none; "yes" == national ("2"); a missing key (8, "") scores 0.
	cb := DefaultCompositeSalience().CapitalBonus
	if cb["yes"] != cb["2"] {
		t.Errorf("capital yes (%.2f) should equal national 2 (%.2f)", cb["yes"], cb["2"])
	}
	order := []string{"2", "3", "4", "5", "6", "7", "8", ""}
	for i := 1; i < len(order); i++ {
		if cb[order[i-1]] < cb[order[i]] {
			t.Errorf("CapitalBonus[%q]=%.2f should be >= CapitalBonus[%q]=%.2f",
				order[i-1], cb[order[i-1]], order[i], cb[order[i]])
		}
	}
	if cb["8"] != 0 || cb[""] != 0 {
		t.Errorf("municipal/none capital should add nothing (map miss → 0)")
	}
}

func TestCompositeSalienceTunableCapitalBonus(t *testing.T) {
	// A tuned CapitalBonus flows through score(): with a large national-capital
	// bonus, a population-less capital village outscores a plain city at equal
	// distance (default would pick the city on its higher class prior).
	cs := DefaultCompositeSalience()
	cs.CapitalBonus = map[string]float64{"2": 10}
	city := Candidate{Place: domain.Place{Name: "City", Class: domain.ClassCity}, DistanceKM: 5}
	capVillage := Candidate{Place: domain.Place{Name: "Capital", Class: domain.ClassVillage, Capital: "2"}, DistanceKM: 5}
	best, ok := cs.Select([]Candidate{city, capVillage}, domain.BearingPolicy{})
	if !ok || best.Place.Name != "Capital" {
		t.Errorf("tuned capital bonus should make the capital village win, got %+v (ok=%v)", best.Place, ok)
	}
}
