package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/input"
)

// handleGazetteer serves the dedicated reverse-geocoding + bearing endpoint
// (GET /api/v1/gazetteer). It is registered only when the gazetteer feature is
// wired; otherwise the route does not exist.
func (s *Server) handleGazetteer(w http.ResponseWriter, r *http.Request) {
	// Open a request-scoped point-in-polygon cache. Locate and Bearing both ask
	// which admin polygons contain the point; without a scope that identical query
	// runs twice per request, which measured as the response's largest single cost.
	// The scope lives exactly as long as this request.
	r = r.WithContext(input.WithPointInPolygonCache(r.Context()))

	params, err := s.parseQueryParams(r)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	coord := s.paramsToCoordinate(params)

	// Reproject to WGS84 (the gazetteer dataset's SRID). A non-4326 input is
	// transformed rather than rejected; a non-transformable SRID is a client error
	// (422), while an internal transform failure maps to 5xx via handleQueryError.
	wgs, terr := s.toWGS84(r.Context(), coord)
	switch {
	case terr == nil:
		// proceed
	case errors.Is(terr, errNotTransformable):
		s.writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf(
			"coordinate SRID %d cannot be transformed to WGS84 (EPSG:4326); "+
				"query with srid=4326 (lon/lat), or run ortus with a coordinate transformer",
			coord.SRID))
		return
	default:
		s.handleQueryError(w, terr)
		return
	}

	sections, err := s.gazetteerSections(r.Context(), wgs)
	if err != nil {
		s.handleQueryError(w, err)
		return
	}

	out := map[string]interface{}{
		"coordinate": map[string]interface{}{"x": coord.X, "y": coord.Y, "srid": coord.SRID},
		"wgs84":      wgs84Block(wgs),
	}
	for k, v := range sections {
		out[k] = v
	}
	s.writeJSON(w, http.StatusOK, out)
}

// gazetteerSections resolves a coordinate into a JSON-ready gazetteer object with
// the admin hierarchy (Locate), the containing island(s) (Islands), the bearing
// fix (Bearing), the terrain exposure (Exposure) and the elevation (Elevation),
// plus a response-wide sources excerpt and the dataset license. Every part is
// null when it has no result: admin/bearing signal absence with ErrNotFound (no
// coverage / no anchor in reach), while the optional DEM-derived exposure/elevation
// (and islands) return a nil result when unwired or uncovered. Any other failure
// is returned so the caller can map it.
//
// This object is the reusable unit for the planned batch endpoint: each batch
// entry is {id, coordinate, <these sections>} with a caller-chosen echo id.
func (s *Server) gazetteerSections(ctx context.Context, coord domain.Coordinate) (map[string]interface{}, error) {
	out := map[string]interface{}{"admin": nil, "islands": nil, "mountains": nil, "bearing": nil, "exposure": nil, "elevation": nil, "sources": []interface{}{}}
	prov := newProvenanceSet()

	// Per-section wall-clock timing. The generic /query response's
	// processing_time_ms covers only the source point-queries and excludes this
	// enrichment, so without these numbers the (often dominant) DEM-derived
	// exposure/elevation cost is invisible. mark() records the time since the
	// previous mark; the sections run sequentially, so the diffs partition the
	// total. Exposed as the "timings_ms" block (per section + total).
	timings := map[string]interface{}{}
	tStart := time.Now()
	prev := tStart
	mark := func(name string) {
		now := time.Now()
		timings[name] = now.Sub(prev).Milliseconds()
		prev = now
	}

	loc, err := s.gazetteer.Locate(ctx, coord)
	switch {
	case err == nil:
		out["admin"] = formatLocality(loc, prov)
	case errors.Is(err, domain.ErrNotFound):
		// no admin coverage at this point — leave admin null
	default:
		return nil, err
	}

	mark("locate")

	// Islands: the named island(s) containing the point (a separate layer,
	// resolved independently of admin coverage). Empty ⇒ leave the block null.
	islands, err := s.gazetteer.Islands(ctx, coord)
	switch {
	case err != nil:
		return nil, err
	case len(islands) > 0:
		out["islands"] = formatIslands(islands, prov)
	}

	mark("islands")

	// Mountains: the smallest containing range and single-mountain (per landform),
	// a separate layer resolved independently of admin coverage. nil ⇒ block null.
	if err := s.addMountains(ctx, coord, out, prov); err != nil {
		return nil, err
	}

	mark("mountains")

	fix, err := s.gazetteer.Bearing(ctx, coord, s.bearingPolicy.OrDefault())
	switch {
	case err == nil:
		out["bearing"] = formatFix(fix, prov)
	case errors.Is(err, domain.ErrNotFound):
		// no salient anchor within reach — leave bearing null
	default:
		return nil, err
	}

	mark("bearing")

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

	mark("exposure")

	// Elevation is optional: (nil, nil) means the feature is not wired, so leave
	// the block null. A non-nil result is rendered even at sea level (meters 0).
	elev, err := s.gazetteer.Elevation(ctx, coord)
	switch {
	case err != nil:
		return nil, err
	case elev != nil:
		out["elevation"] = formatElevation(elev)
	}
	mark("elevation")

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

	// Which of the optional blocks above this deployment can answer at all. Every
	// one of them renders null both when it is absent from the dataset and when
	// the point simply has no result — and the second is a normal answer, since a
	// point on flat ground belongs to no mountain. Without this, a package that
	// silently lost a layer is indistinguishable from correct behavior.
	caps := s.gazetteer.Capabilities()
	out["available"] = map[string]interface{}{
		"islands":   caps.Islands,
		"mountains": caps.Mountains,
		"exposure":  caps.Exposure,
		"elevation": caps.Elevation,
	}

	timings["total"] = time.Since(tStart).Milliseconds()
	out["timings_ms"] = timings
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

// addMountains resolves the mountains layer and attaches the block to out; a nil
// result (point on no mountain / layer unconfigured) leaves the block null.
func (s *Server) addMountains(ctx context.Context, coord domain.Coordinate, out map[string]interface{}, prov *provenanceSet) error {
	mountains, err := s.gazetteer.Mountains(ctx, coord)
	if err != nil {
		return err
	}
	if mountains != nil {
		out["mountains"] = formatMountains(mountains, prov)
	}
	return nil
}

// formatMountains renders the two-level mountains result (smallest containing
// range + single-mountain) as an object with `mountain` and `range` keys, each
// null or a mountain object. The block itself stays null upstream when neither
// landform matches.
func formatMountains(m *domain.MountainResult, prov *provenanceSet) map[string]interface{} {
	out := map[string]interface{}{"mountain": nil, "range": nil}
	if m.Range != nil {
		out["range"] = formatMountain(m.Range, prov)
	}
	if m.Mountain != nil {
		out["mountain"] = formatMountain(m.Mountain, prov)
	}
	return out
}

// formatMountain renders one mountain range / single-mountain for JSON output,
// recording its name provenance in prov. `elevation` (summit height, m) is present
// only for a single-mountain; a range omits it.
func formatMountain(m *domain.Mountain, prov *provenanceSet) map[string]interface{} {
	out := map[string]interface{}{
		"name":        m.Name,
		"name_native": m.NameNative,
		"name_source": prov.add(m.NameSource),
	}
	if m.HasElevation {
		out["elevation"] = m.ElevationM
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
// unset/WGS84 (the coordinate constructors default to it).
func isWGS84(c domain.Coordinate) bool {
	return c.SRID == 0 || c.SRID == domain.SRIDWGS84
}

// errNotTransformable marks a coordinate whose SRID cannot be reprojected to
// WGS84 (no transformer wired, or the SRID pair is unsupported) — a client
// concern. It is distinct from an *internal* transform failure, which toWGS84
// returns as a different (wrapped) error so callers can map it to 5xx / log it.
var errNotTransformable = errors.New("coordinate SRID not transformable to WGS84")

// toWGS84 returns the coordinate reprojected to WGS84 (X=lon, Y=lat). It returns
// errNotTransformable when the input is not WGS84 and can't be reprojected (a
// client concern → 422 / omit block), or a wrapped error when the transform
// itself failed (an internal concern → 5xx / log). nil error ⇒ ready to use.
func (s *Server) toWGS84(ctx context.Context, c domain.Coordinate) (domain.Coordinate, error) {
	if isWGS84(c) {
		// Already WGS84 — normalize SRID 0 → 4326 so the returned coord carries an
		// explicit WGS84 SRID for downstream consistency (requireWGS84 accepts 0 too).
		return domain.NewWGS84Coordinate(c.X, c.Y), nil
	}
	if s.transformer == nil || !s.transformer.IsSupported(c.SRID, domain.SRIDWGS84) {
		return domain.Coordinate{}, errNotTransformable
	}
	w, err := s.transformer.Transform(ctx, c, domain.SRIDWGS84)
	if err != nil {
		return domain.Coordinate{}, fmt.Errorf("reproject SRID %d to WGS84: %w", c.SRID, err)
	}
	return w, nil
}

// wgs84Block renders the always-present WGS84 coordinate block. It is lon/lat
// (not x/y/srid) because it is an explicitly-geographic coordinate other services
// can compute with and store, regardless of the query's input SRID.
func wgs84Block(c domain.Coordinate) map[string]interface{} {
	return map[string]interface{}{"lon": c.X, "lat": c.Y}
}
