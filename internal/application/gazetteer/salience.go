package gazetteer

import (
	"math"

	"github.com/jobrunner/ortus/internal/domain"
)

// Candidate is a place under consideration as a bearing anchor, with its
// ellipsoidal distance from the query point in kilometers.
type Candidate struct {
	Place      domain.Place
	DistanceKM float64
}

// SalienceStrategy selects the best bearing anchor among candidates per a
// BearingPolicy. Implementations are pure (no I/O), so they are trivially
// testable and swappable: RankedSalience is the default for the osm-admin-places
// dataset (rank only, no population); a population-weighted variant can be added
// behind this interface when GeoNames population is merged in.
type SalienceStrategy interface {
	// Select returns the best anchor; ok is false when none is eligible.
	Select(cands []Candidate, pol domain.BearingPolicy) (best Candidate, ok bool)
}

// RankedSalience is the default strategy: a candidate is eligible when its
// distance is within its class reach (BearingPolicy.Reach); among eligible
// candidates the most salient class wins, nearer breaks ties, then the
// lexicographically smaller name for determinism. Branch-free over the reach
// table — adding a class is a policy entry, not a code path.
type RankedSalience struct{}

// Select implements SalienceStrategy. It first applies the proximity override —
// if a town-or-larger anchor is within PreferNearestKM, the nearest such wins
// outright (you are essentially at it, so name it; villages are excluded so an
// obscure hamlet never becomes the anchor). Otherwise the most salient eligible
// class wins.
func (RankedSalience) Select(cands []Candidate, pol domain.BearingPolicy) (Candidate, bool) {
	if c, ok := nearestProminent(cands, pol); ok {
		return c, true
	}
	return mostSalient(cands, pol)
}

// nearestProminent returns the nearest town-or-larger, eligible candidate within
// PreferNearestKM. ok is false when the override is off or nothing qualifies.
func nearestProminent(cands []Candidate, pol domain.BearingPolicy) (best Candidate, ok bool) {
	if pol.PreferNearestKM <= 0 {
		return Candidate{}, false
	}
	for _, c := range cands {
		if c.Place.Class < domain.ClassTown || !eligible(c, pol) || c.DistanceKM > pol.PreferNearestKM {
			continue
		}
		if !ok || nearer(c, best) {
			best, ok = c, true
		}
	}
	return best, ok
}

// mostSalient returns the most salient eligible candidate (class first, then
// nearer, then name).
func mostSalient(cands []Candidate, pol domain.BearingPolicy) (best Candidate, ok bool) {
	for _, c := range cands {
		if !eligible(c, pol) {
			continue
		}
		if !ok || moreSalient(c, best) {
			best, ok = c, true
		}
	}
	return best, ok
}

// eligible reports whether a candidate is within its class reach.
func eligible(c Candidate, pol domain.BearingPolicy) bool {
	reach := pol.ReachKM(c.Place.Class)
	return reach > 0 && c.DistanceKM <= reach
}

// nearer breaks ties by distance, then name (deterministic).
func nearer(a, b Candidate) bool {
	if a.DistanceKM != b.DistanceKM {
		return a.DistanceKM < b.DistanceKM
	}
	return a.Place.Name < b.Place.Name
}

// moreSalient reports whether a should beat b: more salient class first, then
// nearer, then the smaller name (deterministic final tie-break).
func moreSalient(a, b Candidate) bool {
	if a.Place.Class != b.Place.Class {
		return a.Place.Class > b.Place.Class
	}
	return nearer(a, b)
}

// Compile-time assertion that RankedSalience satisfies the strategy interface.
var _ SalienceStrategy = RankedSalience{}

// CompositeSalience picks the anchor by a prominence-vs-proximity score rather than
// by class-then-distance. It is the default for the enriched osm-admin-places dataset:
// a genuinely prominent city a moderate distance away beats an obscure village next
// door, which is what makes a bearing meaningful ("28 km NW München", not "2 km N
// Kleinkleckersdorf"). Pure function of the candidate's own fields.
//
//	score = PopWeight·log10(1+population)          (or ClassPrior[class] if population unknown)
//	      + CapitalScale·CapitalBonus[capital]     (seat of a broader admin unit → more notable)
//	      + WikiWeight   if the place has a wikidata QID
//	      − DecayPerKM·distance_km                  (nearer is better; slope tunes prominence-vs-proximity)
//
// The score is in log10 units, so DecayPerKM is "log-population-decades traded per km":
// at the calibrated 0.04, ~25 km of extra distance (1.0 / 0.04) offsets a 10× smaller
// population.
// Unlike RankedSalience it applies NO proximity override — that override is exactly
// what makes today's bearing pick the nearest small town; here the score decides.
type CompositeSalience struct {
	PopWeight    float64                       // multiplier on log10(1+population)
	WikiWeight   float64                       // additive bonus when a wikidata QID is present
	DecayPerKM   float64                       // score subtracted per km of distance
	CapitalScale float64                       // scales the CapitalBonus table
	ClassPrior   map[domain.PlaceClass]float64 // base score when population is unknown
	// CapitalBonus maps the OSM `capital` value (rank of the seat's unit) to a
	// log10-unit bonus before CapitalScale. Keys are the OSM capital= convention
	// (2=country … 8=municipality) plus "yes" (national). A missing key scores 0.
	CapitalBonus map[string]float64
}

// DefaultCandidateRadiusKM is the flat gather radius CompositeSalience uses (all
// classes): wide enough that a prominent city a moderate distance away is in the
// candidate pool, with the distance decay — not a hard per-class cap — doing the
// shaping. Shared by the app wiring, the fixture generator and the fixture test so
// they agree on what "composite" gathers.
const DefaultCandidateRadiusKM = 120.0

// DefaultCompositeSalience returns the calibrated "balanced" parameters (see
// PLAN-bearing-salience.md): a prominent city stays selectable to ~80 km, a mid-size
// city to ~30 km, a village only within a few km.
func DefaultCompositeSalience() CompositeSalience {
	return CompositeSalience{
		PopWeight:    1.0,
		WikiWeight:   0.3,
		DecayPerKM:   0.04,
		CapitalScale: 0.8,
		ClassPrior: map[domain.PlaceClass]float64{
			domain.ClassCity:    4.3,
			domain.ClassTown:    3.3,
			domain.ClassVillage: 2.3,
		},
		// A national/regional capital is a far better anchor than a municipal seat.
		// "yes" is treated as a national capital; low-rank (8+) values add nothing.
		CapitalBonus: map[string]float64{
			"yes": 2.0, "2": 2.0, "3": 1.5, "4": 1.2, "5": 0.6, "6": 0.4, "7": 0.2,
		},
	}
}

// Select implements SalienceStrategy: the highest-scoring candidate wins, ties broken
// by distance then name (deterministic). Candidates are already within the gather
// radius, so there is no further eligibility filter — the decay term does the shaping.
func (c CompositeSalience) Select(cands []Candidate, _ domain.BearingPolicy) (best Candidate, ok bool) {
	var bestScore float64
	for _, cand := range cands {
		sc := c.score(cand)
		if !ok || sc > bestScore || (sc == bestScore && nearer(cand, best)) {
			best, bestScore, ok = cand, sc, true
		}
	}
	return best, ok
}

// score computes the composite salience of a candidate (higher = better anchor).
func (c CompositeSalience) score(cand Candidate) float64 {
	p := cand.Place
	var base float64
	if p.Population > 0 {
		base = c.PopWeight * math.Log10(1+float64(p.Population))
	} else {
		base = c.ClassPrior[p.Class]
	}
	base += c.CapitalScale * c.CapitalBonus[p.Capital] // nil/missing key → 0
	if p.Wikidata != "" {
		base += c.WikiWeight
	}
	return base - c.DecayPerKM*cand.DistanceKM
}

// Compile-time assertion that CompositeSalience satisfies the strategy interface.
var _ SalienceStrategy = CompositeSalience{}
