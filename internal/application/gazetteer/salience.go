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

// Select implements SalienceStrategy.
func (RankedSalience) Select(cands []Candidate, pol domain.BearingPolicy) (best Candidate, ok bool) {
	for _, c := range cands {
		reach := pol.ReachKM(c.Place.Class)
		if reach <= 0 || c.DistanceKM > reach {
			continue // class has no reach, or candidate is out of range
		}
		if !ok || moreSalient(c, best) {
			best, ok = c, true
		}
	}
	return best, ok
}

// moreSalient reports whether a should beat b: more salient class first, then
// nearer, then the smaller name (deterministic final tie-break).
func moreSalient(a, b Candidate) bool {
	switch {
	case a.Place.Class != b.Place.Class:
		return a.Place.Class > b.Place.Class
	case a.DistanceKM != b.DistanceKM:
		return a.DistanceKM < b.DistanceKM
	default:
		return a.Place.Name < b.Place.Name
	}
}

// Compile-time assertion that RankedSalience satisfies the strategy interface.
var _ SalienceStrategy = RankedSalience{}
