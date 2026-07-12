package gazetteer

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// knnPerClass is how many nearest places per class the bearing query fetches. A
// small k > 1 leaves room to skip candidates that fail the boundary constraint
// (the nearest of a class may be across the tier boundary).
const knnPerClass = 10

// salienceClasses is the fixed iteration order over settlement classes. Order is
// irrelevant to the outcome (the salience strategy decides), but fixing it keeps
// candidate gathering deterministic.
var salienceClasses = []domain.PlaceClass{domain.ClassCity, domain.ClassTown, domain.ClassVillage}

// Bearing returns the most salient nearby place as a bearing fix ("4 km E
// Würzburg"), selected per the BearingPolicy. It gathers the nearest eligible
// place of each class within that class's reach, optionally constrains anchors to
// the query point's boundary tier, and lets the salience strategy pick the best.
func (s *Service) Bearing(ctx context.Context, p domain.Coordinate, pol domain.BearingPolicy) (*domain.Fix, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := requireWGS84(p); err != nil {
		return nil, err
	}
	// One point-in-polygon over the admin layer serves BOTH the boundary constraint
	// (which tier ancestor contains the point) and the in/prope decision (is the point
	// inside the anchor's own admin unit) — so we query it once, not twice.
	containing, err := s.index.PointInPolygon(ctx, s.manifest.AdminLayer, p)
	if err != nil {
		return nil, err
	}
	ancestor, constrained := s.constraintAncestorIn(containing, pol.ConstraintTier)
	// Requirement: a bearing anchor must lie in the same country as the query point.
	// The state-tier constraint implies this where it applies, but not where the point
	// has no state ancestor — and the composite candidate radius is wide enough to reach
	// across a border — so enforce it explicitly from the point's own containing country.
	queryCountry := s.countryOf(containing)
	cands, err := s.gatherCandidates(ctx, p, pol, ancestor, constrained, queryCountry)
	if err != nil {
		return nil, err
	}
	best, ok := s.salience.Select(cands, pol)
	if !ok {
		return nil, fmt.Errorf("bearing (%v): %w", p, domain.ErrNotFound)
	}
	inside := containsAdminUnit(containing, best.Place.AdminID)
	return s.buildFix(ctx, p, best, pol, inside), nil
}

// gatherCandidates collects the constraint-satisfying candidates of each class within
// that class's gather radius. RankedSalience gets the nearest per class (its per-class
// reach as the radius); CompositeSalience gets a wider pool (a flat CandidateRadiusKM)
// and lets its score decide. Either way the salience strategy picks the winner.
func (s *Service) gatherCandidates(ctx context.Context, p domain.Coordinate, pol domain.BearingPolicy, ancestor int64, constrained bool, country string) ([]Candidate, error) {
	var cands []Candidate
	for _, class := range salienceClasses {
		if pol.GatherRadiusKM(class) <= 0 {
			continue
		}
		cs, err := s.candidatesInClass(ctx, p, class, pol, ancestor, constrained, country)
		if err != nil {
			return nil, err
		}
		cands = append(cands, cs...)
	}
	return cands, nil
}

// candidatesInClass returns the places of a class within its gather radius that also
// satisfy the boundary constraint (when in force), each paired with its distance,
// nearest first. Empty when none qualify.
func (s *Service) candidatesInClass(ctx context.Context, p domain.Coordinate, class domain.PlaceClass, pol domain.BearingPolicy, ancestor int64, constrained bool, country string) ([]Candidate, error) {
	feats, err := s.index.QueryKNN(ctx, s.manifest.PlacesLayer, p, knnPerClass, pol.GatherRadiusKM(class),
		&output.Filter{Column: s.manifest.RankColumn, Values: []any{class.String()}})
	if err != nil {
		return nil, err
	}
	var out []Candidate
	for i := range feats {
		place, ok := s.placeFromFeature(&feats[i])
		if !ok {
			continue
		}
		// Same-country guard (see Bearing): drop anchors outside the query's country.
		// Skipped only when the query country is unknown (no containing polygon).
		if country != "" && place.CountryISO != country {
			continue
		}
		if constrained {
			same, err := s.sameTier(ctx, place.AdminID, ancestor, pol.ConstraintTier)
			if err != nil {
				return nil, err
			}
			if !same {
				continue
			}
		}
		dist, err := s.index.DistanceKM(ctx, p, place.At)
		if err != nil {
			return nil, err
		}
		out = append(out, Candidate{Place: place, DistanceKM: dist})
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
		// Coverage fills / non-numeric levels sort below any real level (-1 here, but
		// numeric levels are >= 2), so a real local unit always outranks them; among
		// fills the first non-empty still wins.
		level, _ := strconv.Atoi(containing[i].GetStringProperty(s.manifest.LevelColumn))
		if best == "" || level > bestLevel {
			best, bestLevel = iso, level
		}
	}
	return best
}

// containsAdminUnit reports whether the query point's containing admin polygons
// include the unit adminFID (the anchor place's own unit) — the containment test
// behind "in X" vs "prope X". A zero fid (unknown admin) yields false, so the caller
// falls back to the distance heuristic.
func containsAdminUnit(containing []domain.Feature, adminFID int64) bool {
	if adminFID == 0 {
		return false
	}
	for i := range containing {
		if containing[i].ID == adminFID {
			return true
		}
	}
	return false
}

// sameTier reports whether a place's admin chain reaches the same tier ancestor
// as the query point. A place with unknown admin (AdminID 0) is excluded (can't
// verify), but a real ResolveChain error is returned rather than silently
// dropping the candidate — else a transient index failure could quietly admit a
// cross-tier anchor or turn into a spurious ErrNotFound.
func (s *Service) sameTier(ctx context.Context, placeAdminID, ancestorFID int64, tier string) (bool, error) {
	if placeAdminID == 0 {
		return false, nil
	}
	chain, err := s.index.ResolveChain(ctx, s.manifest.AdminLayer, placeAdminID, output.AdminColumns{
		ParentFK: s.manifest.ParentFKColumn,
		Level:    s.manifest.LevelColumn,
		Name:     s.manifest.AdminNameColumn,
		Country:  s.manifest.CountryColumn,
	})
	if err != nil {
		return false, err
	}
	for _, r := range chain {
		if eq, ok := s.levels.Resolve(r.CountryISO, r.Level); ok && eq.Equivalent == tier {
			return r.FID == ancestorFID, nil
		}
	}
	return false, nil
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

// buildFix renders the bearing fix. The "in X" vs "prope X" vs "N km <dir> X"
// choice comes first from containment (inside, decided by the caller via the
// point's admin polygons — true even far from a big place's center node), then
// the near-but-outside distance threshold, then the directional label. If Azimuth
// fails (degenerate geometry) it keeps the directionless "prope" fallback rather
// than dropping an otherwise valid anchor.
func (s *Service) buildFix(ctx context.Context, p domain.Coordinate, best Candidate, pol domain.BearingPolicy, inside bool) *domain.Fix {
	ref := best.Place
	fix := &domain.Fix{Reference: ref, DistanceKM: best.DistanceKM}

	// Label prefixes follow specimen-label convention: Latin "in" (inside the
	// place's admin unit) and "prope" (near it) — "prope" is the established Latin
	// locality term for "near" (abbr. pr.); we spell it out to stay unambiguous.
	if inside {
		fix.Inside = true
		fix.Label = "in " + ref.Name
		return fix
	}
	if best.DistanceKM < pol.InsideLabelKM {
		// Near, but outside the place's admin unit.
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
