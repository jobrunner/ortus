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
	ancestor, constrained := s.constraintAncestor(ctx, p, pol.ConstraintTier)
	cands, err := s.gatherCandidates(ctx, p, pol, ancestor, constrained)
	if err != nil {
		return nil, err
	}
	best, ok := s.salience.Select(cands, pol)
	if !ok {
		return nil, fmt.Errorf("bearing (%v): %w", p, domain.ErrNotFound)
	}
	return s.buildFix(p, best, pol), nil
}

// gatherCandidates collects the nearest eligible place of each class: one
// class-filtered KNN within the class reach, keeping the nearest candidate that
// also satisfies the boundary constraint (when one is in force).
func (s *Service) gatherCandidates(ctx context.Context, p domain.Coordinate, pol domain.BearingPolicy, ancestor int64, constrained bool) ([]Candidate, error) {
	var cands []Candidate
	for _, class := range salienceClasses {
		reach := pol.ReachKM(class)
		if reach <= 0 {
			continue
		}
		feats, err := s.index.QueryKNN(ctx, s.manifest.PlacesLayer, p, knnPerClass, reach,
			&output.Filter{Column: s.manifest.RankColumn, Values: []any{class.String()}})
		if err != nil {
			return nil, err
		}
		for i := range feats {
			place, ok := s.placeFromFeature(&feats[i])
			if !ok {
				continue
			}
			if constrained && !s.sameTier(ctx, place.AdminID, ancestor, pol.ConstraintTier) {
				continue
			}
			dist, err := s.index.DistanceKM(p, place.At)
			if err != nil {
				return nil, err
			}
			cands = append(cands, Candidate{Place: place, DistanceKM: dist})
			break // nearest surviving of this class is enough
		}
	}
	return cands, nil
}

// constraintAncestor resolves the fid of the admin unit at the configured tier
// (e.g. "state") that contains the query point, via the containing admin
// polygons. ok is false when there is no tier or none resolves — the caller then
// runs unconstrained rather than failing.
func (s *Service) constraintAncestor(ctx context.Context, p domain.Coordinate, tier string) (int64, bool) {
	if tier == "" {
		return 0, false
	}
	feats, err := s.index.PointInPolygon(ctx, s.manifest.AdminLayer, p)
	if err != nil {
		return 0, false
	}
	for i := range feats {
		f := &feats[i]
		level, err := strconv.Atoi(f.GetStringProperty(s.manifest.LevelColumn))
		if err != nil {
			continue
		}
		if eq, ok := s.levels.Resolve(f.GetStringProperty(s.manifest.CountryColumn), level); ok && eq == tier {
			return f.ID, true
		}
	}
	return 0, false
}

// sameTier reports whether a place's admin chain reaches the same tier ancestor
// as the query point. A place with unknown admin (AdminID 0) or an unresolvable
// chain is excluded, so the constraint never silently admits an unverifiable
// anchor.
func (s *Service) sameTier(ctx context.Context, placeAdminID, ancestorFID int64, tier string) bool {
	if placeAdminID == 0 {
		return false
	}
	chain, err := s.index.ResolveChain(ctx, s.manifest.AdminLayer, placeAdminID)
	if err != nil {
		return false
	}
	for _, r := range chain {
		if eq, ok := s.levels.Resolve(r.CountryISO, r.Level); ok && eq == tier {
			return r.FID == ancestorFID
		}
	}
	return false
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
		Name:    f.GetStringProperty(s.manifest.NameColumn),
		Class:   class,
		AdminID: int64(f.GetIntProperty(s.manifest.AdminFKColumn)),
		At:      coord,
	}, true
}

// buildFix renders the bearing fix. Below the inside threshold the point is
// essentially at the reference, so it labels "bei {name}" without a direction;
// otherwise it quantizes the reference→point azimuth to a compass point.
func (s *Service) buildFix(p domain.Coordinate, best Candidate, pol domain.BearingPolicy) *domain.Fix {
	ref := best.Place
	fix := &domain.Fix{Reference: ref, DistanceKM: best.DistanceKM}
	if best.DistanceKM < pol.InsideLabelKM {
		fix.Label = "bei " + ref.Name
		return fix
	}
	az, err := s.index.Azimuth(ref.At, p)
	if err != nil {
		// Azimuth failed (degenerate geometry); fall back to a directionless label
		// rather than dropping an otherwise valid anchor.
		fix.Label = "bei " + ref.Name
		return fix
	}
	fix.Azimuth = az
	fix.Compass = domain.Compass(az, pol.CompassPoints)
	fix.Label = domain.FormatBearingLabel(domain.RoundDistanceKM(best.DistanceKM), fix.Compass, ref.Name)
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
