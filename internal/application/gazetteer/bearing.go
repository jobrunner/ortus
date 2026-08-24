package gazetteer

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// salienceClasses is the fixed iteration order over settlement classes. Order is
// irrelevant to the outcome (the salience strategy decides), but fixing it keeps
// candidate gathering deterministic.
var salienceClasses = []domain.PlaceClass{domain.ClassCity, domain.ClassTown, domain.ClassVillage}

// Bearing returns the most salient nearby place as a bearing fix ("4 km E
// Würzburg"), selected per the BearingPolicy. It gathers the nearest eligible
// place of each class within that class's reach, optionally constrains anchors to
// the query point's boundary tier, and lets the salience strategy pick the best.
func (s *Service) Bearing(ctx context.Context, p domain.Coordinate, pol domain.BearingPolicy) (*domain.Fix, error) {
	ctx, span, err := s.beginSection(ctx, "Bearing", p)
	if err != nil {
		return nil, err
	}
	defer span.End()
	// The admin point-in-polygon gives the query's country (same-country anchor guard)
	// and the boundary-constraint tier ancestor.
	containing, err := s.adminContaining(ctx, p)
	if err != nil {
		return nil, err
	}
	queryCountry := s.countryOf(containing)
	ancestor, constrained := s.constraintAncestorIn(containing, pol.ConstraintTier)
	ic := insideConstraint{country: queryCountry, ancestor: ancestor, constrained: constrained, tier: pol.ConstraintTier}
	// "in {X}": the nearest place whose class inside-radius covers the point — i.e. we
	// are standing IN that settlement. Decided by distance to the place point (proxy for
	// the settlement's extent), NOT by administrative containment: a municipality polygon
	// is large and rural, so containment wrongly reported fields/forest kilometers from a
	// village as "in <village>" (e.g. the Schwanberg plateau, 1.8 km from Rödelsee). The
	// same country + boundary-tier guards as anchor selection still apply (see admits).
	// When a built-up sampler is wired, the point must also sit on built-up fabric —
	// this suppresses "in <village>" for points within the radius but in fields/parks.
	if s.builtUpAllows(ctx, p) {
		if in, ok, inErr := s.placeInsideOf(ctx, p, pol, ic); inErr != nil {
			return nil, inErr
		} else if ok {
			return &domain.Fix{Reference: in.Place, DistanceKM: in.DistanceKM, Inside: true, Label: "in " + in.Place.Name}, nil
		}
	}
	// Otherwise, a directional bearing to the most salient nearby anchor.
	cands, err := s.gatherCandidates(ctx, p, pol, ic)
	if err != nil {
		return nil, err
	}
	best, ok := s.salience.Select(cands, pol)
	if !ok {
		return nil, fmt.Errorf("bearing (%v): %w", p, domain.ErrNotFound)
	}
	return s.buildFix(ctx, p, best, pol), nil
}

// insideCandidateK is how many nearest places PER CLASS placeInsideOf inspects — a
// few, so it can skip a nearest that fails the country/tier guards and still find a
// qualifying one within the radius.
const insideCandidateK = 5

// insideConstraint bundles the guards the "in {X}" scan applies to a candidate place:
// same country (skipped when the query country is unknown, e.g. a point in no admin
// polygon) and — when the boundary constraint is active — the same tier ancestor as the
// query point. Without the tier guard, a place just across a state line could be reported
// as the settlement you are "in"; anchor selection applies the same guards, keeping the
// two consistent.
type insideConstraint struct {
	country     string
	ancestor    int64
	constrained bool
	tier        string
}

// placeInsideOf returns the nearest place that covers p within ITS OWN class
// inside-radius — the settlement the point counts as being "in". Each class is queried
// independently within its own radius (city widest, village tightest) so a qualifying
// town/city is never hidden behind many nearer out-of-radius villages; the overall
// nearest qualifier wins (standing in a village names the village, not a larger town
// whose wider radius also reaches). ok is false when no class qualifies (open country /
// between settlements).
//
// Every class's candidates are collected before any are filtered, so the
// boundary-tier check runs once for the whole set instead of once per candidate.
// QueryKNN orders nearest-first within a class, but the winner is the overall
// nearest admitted candidate, so the flat scan below is equivalent to the previous
// per-class scan. The explicit distance check does not rely on the index honoring
// the KNN radius bound.
func (s *Service) placeInsideOf(ctx context.Context, p domain.Coordinate, pol domain.BearingPolicy, ic insideConstraint) (Candidate, bool, error) {
	cands, err := s.insideCandidates(ctx, p, pol)
	if err != nil {
		return Candidate{}, false, err
	}

	guard, err := s.newTierGuard(ctx, ic, cands)
	if err != nil {
		return Candidate{}, false, err
	}
	var best Candidate
	found := false
	for _, c := range cands {
		if !guard.admits(c.Place) {
			continue
		}
		if !found || c.DistanceKM < best.DistanceKM {
			best, found = c, true
		}
	}
	return best, found, nil
}

// insideCandidates collects, across all classes, the places whose own class
// inside-radius covers p. The explicit distance check does not rely on the index
// honoring the KNN radius bound.
func (s *Service) insideCandidates(ctx context.Context, p domain.Coordinate, pol domain.BearingPolicy) ([]Candidate, error) {
	var cands []Candidate
	for _, c := range salienceClasses {
		r := pol.InsideRadiusKM(c)
		if r <= 0 {
			continue
		}
		near, err := s.index.QueryKNN(ctx, s.manifest.PlacesLayer, p, insideCandidateK, r,
			&output.Filter{Column: s.manifest.RankColumn, Values: []any{c.String()}})
		if err != nil {
			return nil, err
		}
		for i := range near {
			if near[i].DistanceKM > r {
				continue
			}
			place, ok := s.placeFromFeature(&near[i].Feature)
			if !ok {
				continue
			}
			cands = append(cands, Candidate{Place: place, DistanceKM: near[i].DistanceKM})
		}
	}
	return cands, nil
}

// tierGuard applies the anchor guards to a candidate set: same country, and — when
// the boundary constraint is active — the same boundary-tier ancestor as the query
// point.
//
// It exists to turn an N+1 into a single query: the tier check needs each
// candidate's admin lineage, and resolving that one candidate at a time meant 234
// SpatiaLite round-trips per bearing request.
//
// Measured honestly, batching removed the round-trips but NOT the time — the same
// ~690 ms now sits in one span instead of 234. So the cost is the chain walking
// itself, not the per-query overhead, and this change is a prerequisite for fixing
// that rather than the fix. What it does buy: 234 fewer connection acquisitions
// per request, which is what made throughput collapse under concurrency, and a
// single span to attribute the remaining cost to.
type tierGuard struct {
	ic     insideConstraint
	levels LevelResolver
	chains map[int64][]output.AdminRow
}

// newTierGuard resolves the lineage of every candidate's admin unit in one call.
// When the constraint is inactive no lineage is needed, so no query is issued.
//
// A failed batch aborts the request, exactly as a failed single walk used to: a
// transient index failure must not quietly admit a cross-tier anchor or turn into
// a spurious "not found".
func (s *Service) newTierGuard(ctx context.Context, ic insideConstraint, cands []Candidate) (tierGuard, error) {
	g := tierGuard{ic: ic, levels: s.levels}
	if !ic.constrained || len(cands) == 0 {
		return g, nil
	}
	fids := make([]int64, 0, len(cands))
	for _, c := range cands {
		if c.Place.AdminID != 0 {
			fids = append(fids, c.Place.AdminID)
		}
	}
	if len(fids) == 0 {
		return g, nil
	}
	chains, err := s.index.ResolveChains(ctx, s.manifest.AdminLayer, fids, output.AdminColumns{
		ParentFK: s.manifest.ParentFKColumn,
		Level:    s.manifest.LevelColumn,
		Country:  s.manifest.CountryColumn,
	})
	if err != nil {
		return tierGuard{}, err
	}
	g.chains = chains
	return g, nil
}

// admits reports whether a place may be used as an anchor (or named as the
// settlement the point is "in"). A place with unknown admin (AdminID 0) is
// excluded under an active constraint because its lineage cannot be verified.
func (g tierGuard) admits(place domain.Place) bool {
	if g.ic.country != "" && place.CountryISO != g.ic.country {
		return false
	}
	if !g.ic.constrained {
		return true
	}
	if place.AdminID == 0 {
		return false
	}
	// First unit in the chain whose level maps to the tier decides — the same
	// most-local-wins rule the per-candidate walk used.
	for _, r := range g.chains[place.AdminID] {
		if eq, ok := g.levels.Resolve(r.CountryISO, r.Level); ok && eq.Equivalent == g.ic.tier {
			return r.FID == g.ic.ancestor
		}
	}
	return false
}

// gatherCandidates collects the constraint-satisfying candidates of each class, up to
// CandidateLimit per class. RankedSalience uses each class's per-class reach as the
// gather radius (and later selects the nearest eligible); CompositeSalience uses a wider
// flat CandidateRadiusKM and lets its score decide. Either way the salience strategy
// picks the winner.
func (s *Service) gatherCandidates(ctx context.Context, p domain.Coordinate, pol domain.BearingPolicy, ic insideConstraint) ([]Candidate, error) {
	var raw []Candidate
	for _, class := range salienceClasses {
		if pol.GatherRadiusKM(class) <= 0 {
			continue
		}
		cs, err := s.candidatesInClass(ctx, p, class, pol)
		if err != nil {
			return nil, err
		}
		raw = append(raw, cs...)
	}
	// One batched lineage resolve for every class's candidates together, then the
	// guards run in memory. Filtering per class would reintroduce one query per
	// class-set; filtering per candidate is what cost 234 queries per request.
	guard, err := s.newTierGuard(ctx, ic, raw)
	if err != nil {
		return nil, err
	}
	cands := make([]Candidate, 0, len(raw))
	for _, c := range raw {
		if guard.admits(c.Place) {
			cands = append(cands, c)
		}
	}
	return cands, nil
}

// candidatesInClass returns the places of a class within its gather radius that also
// satisfy the boundary constraint (when in force), each paired with its distance,
// nearest first. Empty when none qualify.
// The guards (same country, boundary tier) are NOT applied here — the caller runs
// them over all classes at once, so the lineage lookup they need is a single
// batched query rather than one per candidate.
func (s *Service) candidatesInClass(ctx context.Context, p domain.Coordinate, class domain.PlaceClass, pol domain.BearingPolicy) ([]Candidate, error) {
	near, err := s.index.QueryKNN(ctx, s.manifest.PlacesLayer, p, pol.CandidateLimit(), pol.GatherRadiusKM(class),
		&output.Filter{Column: s.manifest.RankColumn, Values: []any{class.String()}})
	if err != nil {
		return nil, err
	}
	out := make([]Candidate, 0, len(near))
	for i := range near {
		place, ok := s.placeFromFeature(&near[i].Feature)
		if !ok {
			continue
		}
		// Distance is already computed by the KNN query (same ellipsoidal metric as
		// DistanceKM), so no per-candidate distance round-trip is needed.
		out = append(out, Candidate{Place: place, DistanceKM: near[i].DistanceKM})
	}
	return out, nil
}

// constraintAncestorIn resolves the fid of the admin unit at the configured tier
// (e.g. "state") among the polygons that already contain the query point (fetched
// once by the caller). ok is false when there is no tier or none resolves — the
// caller then runs unconstrained.
func (s *Service) constraintAncestorIn(containing []domain.Feature, tier string) (fid int64, ok bool) {
	if tier == "" {
		return 0, false
	}
	for i := range containing {
		f := &containing[i]
		level, atoiErr := strconv.Atoi(f.GetStringProperty(s.manifest.LevelColumn))
		if atoiErr != nil {
			continue
		}
		if m, resolved := s.levels.Resolve(f.GetStringProperty(s.manifest.CountryColumn), level); resolved && m.Equivalent == tier {
			return f.ID, true
		}
	}
	return 0, false
}

// countryOf returns the ISO country code of the query point from its containing admin
// polygons. The MOST-LOCAL polygon (highest admin_level) wins: it is both deterministic
// (independent of PointInPolygon's return order) and more reliable than the country
// outline, whose NE-join code can be wrong in disputed areas (e.g. the Golan point sits
// in an admin_level-2 polygon mis-coded PS while its L4/L5/L8 units are correctly IL).
// Empty when the point is in no polygon (e.g. open sea) — the caller then skips the
// same-country guard rather than dropping every candidate.
func (s *Service) countryOf(containing []domain.Feature) string {
	best, bestLevel := "", -1
	for i := range containing {
		iso := containing[i].GetStringProperty(s.manifest.CountryColumn)
		if iso == "" {
			continue
		}
		// Coverage fills / non-numeric levels sort below any real level (mapped to
		// -1 here; numeric levels are >= 2), so a real local unit always outranks
		// them. On a tie (e.g. a boundary-inclusive point in two same-level polygons
		// of different countries — a disputed border) the lexicographically smaller
		// ISO wins, so the result is independent of PointInPolygon's return order.
		level, atoiErr := strconv.Atoi(containing[i].GetStringProperty(s.manifest.LevelColumn))
		if atoiErr != nil {
			level = -1
		}
		if best == "" || level > bestLevel || (level == bestLevel && iso < best) {
			best, bestLevel = iso, level
		}
	}
	return best
}

// placeFromFeature maps a places-layer feature to a domain.Place, parsing the
// point geometry. ok is false when the geometry is not a usable point.
func (s *Service) placeFromFeature(f *domain.Feature) (domain.Place, bool) {
	coord, ok := parsePointWKT(f.Geometry.WKT)
	if !ok {
		return domain.Place{}, false
	}
	class, _ := domain.ParsePlaceClass(f.GetStringProperty(s.manifest.RankColumn))
	return domain.Place{
		Name:       f.GetStringProperty(s.manifest.NameColumn),
		NameNative: f.GetStringProperty(s.manifest.NameNativeColumn),
		NameSource: s.resolveNameSource(f.GetStringProperty(s.manifest.NameSourceColumn)),
		Class:      class,
		AdminID:    int64(f.GetIntProperty(s.manifest.AdminFKColumn)),
		CountryISO: f.GetStringProperty(s.manifest.CountryColumn),
		At:         coord,
		// Prominence signals (CompositeSalience). Columns are optional: an unset manifest
		// column name reads back as zero/empty and the strategy treats it as unknown.
		Population: int64(f.GetIntProperty(s.manifest.PopulationColumn)),
		Capital:    f.GetStringProperty(s.manifest.CapitalColumn),
		Wikidata:   f.GetStringProperty(s.manifest.NotabilityColumn),
	}, true
}

// buildFix renders a directional bearing to a NON-inside anchor (the "in {X}" case
// is decided earlier, in Bearing, by placeInsideOf). Below InsideLabelKM it keeps a
// directionless "prope {X}" (a bearing from a few hundred meters is noise); otherwise
// the directional label. If Azimuth fails (degenerate geometry) it keeps the "prope"
// fallback rather than dropping an otherwise valid anchor. The Latin prefixes follow
// specimen-label convention: "prope" is the established Latin locality term for "near".
func (s *Service) buildFix(ctx context.Context, p domain.Coordinate, best Candidate, pol domain.BearingPolicy) *domain.Fix {
	ref := best.Place
	fix := &domain.Fix{Reference: ref, DistanceKM: best.DistanceKM}

	if best.DistanceKM < pol.InsideLabelKM {
		fix.Label = "prope " + ref.Name
		return fix
	}
	fix.Label = "prope " + ref.Name
	if az, azErr := s.index.Azimuth(ctx, ref.At, p); azErr == nil {
		fix.Azimuth = az
		fix.Compass = domain.Compass(az, pol.CompassPoints)
		fix.Label = domain.FormatBearingLabel(domain.RoundDistanceKM(best.DistanceKM), fix.Compass, ref.Name)
	}
	return fix
}

// parsePointWKT extracts a WGS84 coordinate from a POINT WKT string such as
// "POINT(10.02 50)" or "POINT Z(10 50 3)".
func parsePointWKT(wkt string) (domain.Coordinate, bool) {
	open := strings.IndexByte(wkt, '(')
	closeIdx := strings.IndexByte(wkt, ')')
	if open < 0 || closeIdx < open {
		return domain.Coordinate{}, false
	}
	fields := strings.Fields(wkt[open+1 : closeIdx])
	if len(fields) < 2 {
		return domain.Coordinate{}, false
	}
	x, err1 := strconv.ParseFloat(fields[0], 64)
	y, err2 := strconv.ParseFloat(fields[1], 64)
	if err1 != nil || err2 != nil {
		return domain.Coordinate{}, false
	}
	return domain.NewWGS84Coordinate(x, y), true
}
