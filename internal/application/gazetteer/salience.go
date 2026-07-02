package gazetteer

import "github.com/jobrunner/ortus/internal/domain"

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
