package http

import (
	"context"
	"errors"
	"net/http"
	"strings"

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
	out := map[string]interface{}{"admin": nil, "bearing": nil, "sources": []interface{}{}}
	prov := newProvenanceSet()

	loc, err := s.gazetteer.Locate(ctx, coord)
	switch {
	case err == nil:
		out["admin"] = formatLocality(loc, prov)
	case errors.Is(err, domain.ErrNotFound):
		// no admin coverage at this point — leave admin null
	default:
		return nil, err
	}

	fix, err := s.gazetteer.Bearing(ctx, coord, s.bearingPolicy.OrDefault())
	switch {
	case err == nil:
		out["bearing"] = formatFix(fix, prov)
	case errors.Is(err, domain.ErrNotFound):
		// no salient anchor within reach — leave bearing null
	default:
		return nil, err
	}
	// Response-wide provenance excerpt: each distinct name_source code that appears
	// above, described once (not repeated per record).
	out["sources"] = prov.list()
	// Dataset-wide license/attribution for the gazetteer data (OSM/ODbL, GeoNames,
	// Natural Earth, …), so a client has everything it must display in one place.
	if !s.gazetteerLicense.IsEmpty() {
		out["license"] = map[string]interface{}{
			"name":        s.gazetteerLicense.Name,
			"url":         s.gazetteerLicense.URL,
			"attribution": s.gazetteerLicense.Attribution,
		}
	}
	return out, nil
}

// provenanceSet collects the distinct name-source provenances seen in a response,
// so the response-wide "sources" block lists each code once.
type provenanceSet struct {
	seen  map[string]bool
	items []map[string]interface{}
}

func newProvenanceSet() *provenanceSet { return &provenanceSet{seen: map[string]bool{}} }

// add records a code (once) and returns it for inline use per record.
func (p *provenanceSet) add(ns domain.NameProvenance) string {
	if ns.Code == "" || p.seen[ns.Code] {
		return ns.Code
	}
	p.seen[ns.Code] = true
	p.items = append(p.items, map[string]interface{}{
		"code": ns.Code, "short": ns.Short, "long": ns.Long, "standard": ns.Standard,
	})
	return ns.Code
}

func (p *provenanceSet) list() []map[string]interface{} {
	if p.items == nil {
		return []map[string]interface{}{}
	}
	return p.items
}

// formatLocality renders a resolved admin hierarchy for JSON output, recording
// each unit's name provenance in prov.
func formatLocality(loc *domain.Locality, prov *provenanceSet) map[string]interface{} {
	hierarchy := make([]map[string]interface{}, len(loc.Chain))
	for i, u := range loc.Chain {
		hierarchy[i] = map[string]interface{}{
			"level":                  u.Level,
			"name":                   u.Name,
			"name_native":            u.NameNative,
			"name_source":            prov.add(u.NameSource),
			"equivalent":             u.Equivalent,
			"local_term":             u.LocalTerm,
			"equivalent_description": u.EquivalentDesc,
		}
	}
	return map[string]interface{}{
		"country_iso": loc.CountryISO,
		"hierarchy":   hierarchy,
	}
}

// formatFix renders a bearing fix for JSON output, recording the anchor's name
// provenance in prov.
func formatFix(fix *domain.Fix, prov *provenanceSet) map[string]interface{} {
	return map[string]interface{}{
		"reference":   fix.Reference.Name,
		"name_native": fix.Reference.NameNative,
		"name_source": prov.add(fix.Reference.NameSource),
		"class":       fix.Reference.Class.String(),
		"distance_km": fix.DistanceKM,
		"azimuth":     fix.Azimuth,
		"compass":     fix.Compass,
		"label":       fix.Label,
		"inside":      fix.Inside,
	}
}

// gazetteerEnrichmentRequested reports whether /query should attach the gazetteer
// block. Enrichment is ON by default when the feature is wired; a client opts out
// only with an explicit falsy with-gazetteer value (0/false/no/off) to skip the
// extra Locate+Bearing spatial work. Any other value — including an unrecognized
// one — leaves enrichment on.
func gazetteerEnrichmentRequested(r *http.Request) bool {
	switch strings.ToLower(r.URL.Query().Get("with-gazetteer")) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

// isWGS84 reports whether a coordinate is WGS84 (EPSG:4326), treating SRID 0 as
// unset/WGS84 (the coordinate constructors default to it). The gazetteer dataset
// is 4326-only, so enrichment is skipped for any other SRID rather than attempted
// and failed.
func isWGS84(c domain.Coordinate) bool {
	return c.SRID == 0 || c.SRID == domain.SRIDWGS84
}
