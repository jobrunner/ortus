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

// gazetteerSections resolves a coordinate into a JSON-ready gazetteer object with
// the admin hierarchy (Locate), the containing island(s) (Islands), the bearing
// fix (Bearing) and the elevation (Elevation), plus a response-wide sources
// excerpt and the dataset license. A part that has no result (ErrNotFound — no
// admin coverage, not on an island, no anchor in reach, no DEM) is null rather
// than an error; any other failure is returned so the caller can map it.
//
// This object is the reusable unit for the planned batch endpoint: each batch
// entry is {id, coordinate, <these sections>} with a caller-chosen echo id.
func (s *Server) gazetteerSections(ctx context.Context, coord domain.Coordinate) (map[string]interface{}, error) {
	out := map[string]interface{}{"admin": nil, "islands": nil, "bearing": nil, "exposure": nil, "elevation": nil, "sources": []interface{}{}}
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

	// Islands: the named island(s) containing the point (a separate layer,
	// resolved independently of admin coverage). Empty ⇒ leave the block null.
	islands, err := s.gazetteer.Islands(ctx, coord)
	switch {
	case err != nil:
		return nil, err
	case len(islands) > 0:
		out["islands"] = formatIslands(islands, prov)
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

	// Exposure (terrain slope + aspect), next to the bearing. Derived from the DEM;
	// (nil, nil) when elevation is unwired or the point has no full-window coverage,
	// so the block stays null.
	exp, err := s.gazetteer.Exposure(ctx, coord)
	switch {
	case err != nil:
		return nil, err
	case exp != nil:
		out["exposure"] = formatExposure(exp)
	}

	// Elevation is optional: (nil, nil) means the feature is not wired, so leave
	// the block null. A non-nil result is rendered even at sea level (meters 0).
	elev, err := s.gazetteer.Elevation(ctx, coord)
	switch {
	case err != nil:
		return nil, err
	case elev != nil:
		out["elevation"] = formatElevation(elev)
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

// formatIslands renders the island(s) containing the point for JSON output,
// recording each island's name provenance in prov. Returned as an array (a point
// may lie on several nested islands); the block stays null upstream when empty.
func formatIslands(islands []domain.Island, prov *provenanceSet) []map[string]interface{} {
	out := make([]map[string]interface{}, len(islands))
	for i, is := range islands {
		out[i] = map[string]interface{}{
			"name":        is.Name,
			"name_native": is.NameNative,
			"name_source": prov.add(is.NameSource),
		}
	}
	return out
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

// formatExposure renders a terrain exposure (slope + aspect) for JSON output.
// aspect_deg/aspect_compass are null/empty when flat (aspect undefined). The DEM
// source's license/attribution is nested under "source", matching elevation.
func formatExposure(e *domain.Exposure) map[string]interface{} {
	out := map[string]interface{}{
		"slope_deg":        e.SlopeDeg,
		"slope_percent":    e.SlopePercent,
		"aspect_deg":       nil,
		"aspect_compass":   "",
		"flat":             e.Flat,
		"sample_spacing_m": e.SampleSpacingM,
	}
	if !e.Flat {
		out["aspect_deg"] = e.AspectDeg
		out["aspect_compass"] = e.AspectCompass
	}
	if !e.License.IsEmpty() {
		out["source"] = map[string]interface{}{
			"name":        e.License.Name,
			"url":         e.License.URL,
			"attribution": e.License.Attribution,
		}
	}
	return out
}

// formatElevation renders an elevation result for JSON output. The DEM source's
// license/attribution is nested under "source", distinct from the response-wide
// gazetteer "license" (a different dataset and license).
func formatElevation(e *domain.Elevation) map[string]interface{} {
	out := map[string]interface{}{
		"meters":                e.Meters,
		"accuracy_m":            e.AccuracyM,
		"accuracy_basis":        e.AccuracyBasis,
		"horizontal_accuracy_m": e.HorizontalM,
		"vertical_datum":        e.VerticalDatum,
		"sea_level":             e.SeaLevel,
		"surface_model":         e.SurfaceModel,
	}
	if !e.License.IsEmpty() {
		out["source"] = map[string]interface{}{
			"name":        e.License.Name,
			"url":         e.License.URL,
			"attribution": e.License.Attribution,
		}
	}
	return out
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
