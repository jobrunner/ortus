package http

import (
	"context"
	"errors"
	"net/http"

	"github.com/jobrunner/ortus/internal/domain"
)

// handleGazetteer serves the dedicated reverse-geocoding + bearing endpoint
// (GET /api/v1/gazetteer). It is registered only when the gazetteer feature is
// wired; otherwise the route does not exist.
func (s *Server) handleGazetteer(w http.ResponseWriter, r *http.Request) {
	params, err := s.parseQueryParams(r)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	coord := s.paramsToCoordinate(params)

	sections, err := s.gazetteerSections(r.Context(), coord)
	if err != nil {
		s.handleQueryError(w, err)
		return
	}

	out := map[string]interface{}{
		"coordinate": map[string]interface{}{"x": coord.X, "y": coord.Y, "srid": coord.SRID},
	}
	for k, v := range sections {
		out[k] = v
	}
	s.writeJSON(w, http.StatusOK, out)
}

// gazetteerSections resolves the admin hierarchy (Locate) and the bearing fix
// for a coordinate into a JSON-ready {admin, bearing} object. A part that has no
// result (ErrNotFound — no admin coverage, or no anchor in reach) is null rather
// than an error; any other failure is returned so the caller can map it.
//
// The {admin, bearing} object is the reusable unit for the planned batch
// endpoint: each batch entry is {id, coordinate, admin, bearing} with a
// caller-chosen echo id.
func (s *Server) gazetteerSections(ctx context.Context, coord domain.Coordinate) (map[string]interface{}, error) {
	out := map[string]interface{}{"admin": nil, "bearing": nil}

	loc, err := s.gazetteer.Locate(ctx, coord)
	switch {
	case err == nil:
		out["admin"] = formatLocality(loc)
	case errors.Is(err, domain.ErrNotFound):
		// no admin coverage at this point — leave admin null
	default:
		return nil, err
	}

	fix, err := s.gazetteer.Bearing(ctx, coord, s.effectiveBearingPolicy())
	switch {
	case err == nil:
		out["bearing"] = formatFix(fix)
	case errors.Is(err, domain.ErrNotFound):
		// no salient anchor within reach — leave bearing null
	default:
		return nil, err
	}
	return out, nil
}

// effectiveBearingPolicy returns the configured bearing policy, or the built-in
// defaults when none was wired (zero value has a nil Reach map).
func (s *Server) effectiveBearingPolicy() domain.BearingPolicy {
	if s.bearingPolicy.Reach != nil {
		return s.bearingPolicy
	}
	return domain.DefaultBearingPolicy()
}

// formatLocality renders a resolved admin hierarchy for JSON output.
func formatLocality(loc *domain.Locality) map[string]interface{} {
	hierarchy := make([]map[string]interface{}, len(loc.Chain))
	for i, u := range loc.Chain {
		hierarchy[i] = map[string]interface{}{
			"level":      u.Level,
			"name":       u.Name,
			"equivalent": u.Equivalent,
		}
	}
	return map[string]interface{}{
		"country_iso": loc.CountryISO,
		"hierarchy":   hierarchy,
	}
}

// formatFix renders a bearing fix for JSON output.
func formatFix(fix *domain.Fix) map[string]interface{} {
	return map[string]interface{}{
		"reference":   fix.Reference.Name,
		"class":       fix.Reference.Class.String(),
		"distance_km": fix.DistanceKM,
		"azimuth":     fix.Azimuth,
		"compass":     fix.Compass,
		"label":       fix.Label,
	}
}

// isTruthy reports whether a query-parameter value means "on". Used for the
// opt-in with-gazetteer flag on /query (default off).
func isTruthy(v string) bool {
	switch v {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
