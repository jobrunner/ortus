package mcp

import (
	"errors"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jobrunner/ortus/internal/domain"
)

// gazetteerIn is the input for the gazetteer tool: a coordinate, as WGS84 lon/lat
// or an x/y/srid pair. The gazetteer dataset is EPSG:4326.
type gazetteerIn struct {
	Lon  *float64 `json:"lon,omitempty" jsonschema:"longitude in WGS84 (EPSG:4326); pair with 'lat'"`
	Lat  *float64 `json:"lat,omitempty" jsonschema:"latitude in WGS84 (EPSG:4326); pair with 'lon'"`
	X    *float64 `json:"x,omitempty" jsonschema:"easting in the given SRID; pair with 'y'"`
	Y    *float64 `json:"y,omitempty" jsonschema:"northing in the given SRID; pair with 'x'"`
	SRID int      `json:"srid,omitempty" jsonschema:"spatial reference id for x/y; defaults to 4326 (WGS84)"`
}

// adminUnitOut is one level of the resolved administrative hierarchy.
type adminUnitOut struct {
	Level                 int    `json:"level"`
	Name                  string `json:"name"`
	NameNative            string `json:"name_native"`
	NameSource            string `json:"name_source"`
	Equivalent            string `json:"equivalent"`
	LocalTerm             string `json:"local_term"`
	EquivalentDescription string `json:"equivalent_description"`
}

// adminOut is the administrative hierarchy containing the coordinate.
type adminOut struct {
	CountryISO string         `json:"country_iso"`
	Hierarchy  []adminUnitOut `json:"hierarchy"`
}

// islandOut is one island whose polygon contains the coordinate.
type islandOut struct {
	Name       string `json:"name"`
	NameNative string `json:"name_native"`
	NameSource string `json:"name_source"`
}

// mountainOut is one mountain range or single-mountain territory whose polygon
// contains the coordinate. Elevation (summit height, m) is present only for a
// single-mountain; the key is omitted entirely for a range.
type mountainOut struct {
	Name       string `json:"name"`
	NameNative string `json:"name_native"`
	NameSource string `json:"name_source"`
	// Elevation is omitted for a range (nil), present for a single-mountain — so the
	// MCP shape matches the HTTP one, which omits the key on ranges.
	Elevation *float64 `json:"elevation,omitempty"`
}

// mountainsOut is the two-level mountains result: the smallest containing
// single-mountain and range (either null). Null when the point is on neither or
// no mountains layer is configured.
type mountainsOut struct {
	Mountain *mountainOut `json:"mountain"`
	Range    *mountainOut `json:"range"`
}

// bearingOut is the bearing fix relative to the most salient nearby place.
type bearingOut struct {
	Reference  string  `json:"reference"`
	NameNative string  `json:"name_native"`
	NameSource string  `json:"name_source"`
	Class      string  `json:"class"`
	DistanceKM float64 `json:"distance_km"`
	Azimuth    float64 `json:"azimuth"`
	Compass    string  `json:"compass"`
	Label      string  `json:"label"`
	// Inside: the query point lies within the reference's admin unit ("in X", not
	// "prope X") — decided by containment, not distance.
	Inside bool `json:"inside"`
}

// sourceOut describes one distinct name_source code seen in a response, so the
// response-wide "sources" block lists each code once rather than per record.
type sourceOut struct {
	Code     string `json:"code"`
	Short    string `json:"short"`
	Long     string `json:"long"`
	Standard string `json:"standard"`
}

// licenseOut is the dataset-wide license/attribution for the gazetteer data.
type licenseOut struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	Attribution string `json:"attribution"`
}

// elevationOut is the height above sea level at the coordinate, with accuracy
// metadata. Source carries the DEM's own license/attribution, distinct from the
// gazetteer License. Null when the elevation feature is not wired.
type elevationOut struct {
	Meters              float64     `json:"meters"`
	AccuracyM           float64     `json:"accuracy_m"`
	AccuracyBasis       string      `json:"accuracy_basis"`
	HorizontalAccuracyM float64     `json:"horizontal_accuracy_m"`
	VerticalDatum       string      `json:"vertical_datum"`
	SeaLevel            bool        `json:"sea_level"`
	SurfaceModel        string      `json:"surface_model"`
	Source              *licenseOut `json:"source"`
}

// exposureOut is the terrain slope + aspect at the coordinate, derived from the
// DEM. AspectDeg is null and AspectCompass empty when Flat (aspect undefined).
// Source carries the DEM's license (same source as elevation). Null when the
// elevation feature is not wired or the point has no full-window DEM coverage.
type exposureOut struct {
	SlopeDeg       float64     `json:"slope_deg"`
	SlopePercent   float64     `json:"slope_percent"`
	AspectDeg      *float64    `json:"aspect_deg"`
	AspectCompass  string      `json:"aspect_compass"`
	Flat           bool        `json:"flat"`
	SampleSpacingM float64     `json:"sample_spacing_m"`
	Source         *licenseOut `json:"source"`
}

// gazetteerOut is the tool result: admin, islands, bearing, exposure and
// elevation, any of which is null when it has no result for the coordinate (no
// admin coverage, not on an island, no anchor in reach, no DEM wired, or — for
// exposure — the point/neighbor lacks coverage). Sources is the response-wide
// provenance excerpt describing each name_source code that appears above; License
// is the dataset attribution (null when unset).
type gazetteerOut struct {
	// Available says which optional parts this deployment can answer at all.
	// Without it a null part is ambiguous: absent from the dataset, or present and
	// simply no result here. That mattered less while elevation reported sea level
	// for uncovered ground — an MCP client could tell the two apart by the block
	// being non-null. Now that uncovered ground is null too, this is the only way
	// to distinguish them, so it is not optional polish.
	Available availableOut  `json:"available"`
	Admin     *adminOut     `json:"admin"`
	Islands   []islandOut   `json:"islands"`
	Mountains *mountainsOut `json:"mountains"`
	Bearing   *bearingOut   `json:"bearing"`
	Exposure  *exposureOut  `json:"exposure"`
	Elevation *elevationOut `json:"elevation"`
	Sources   []sourceOut   `json:"sources"`
	License   *licenseOut   `json:"license"`
}

// availableOut mirrors domain.GazetteerCapabilities: what this deployment can
// answer, independent of whether the queried point has a result.
type availableOut struct {
	Islands   bool `json:"islands"`
	Mountains bool `json:"mountains"`
	Exposure  bool `json:"exposure"`
	Elevation bool `json:"elevation"`
}

// provenanceSet collects the distinct name-source provenances seen in a
// response, so the response-wide "sources" block lists each code once.
type provenanceSet struct {
	seen  map[string]bool
	items []sourceOut
}

func newProvenanceSet() *provenanceSet { return &provenanceSet{seen: map[string]bool{}} }

// add records a code (once) and returns it for inline use per record.
func (p *provenanceSet) add(ns domain.NameProvenance) string {
	if ns.Code == "" || p.seen[ns.Code] {
		return ns.Code
	}
	p.seen[ns.Code] = true
	p.items = append(p.items, sourceOut{Code: ns.Code, Short: ns.Short, Long: ns.Long, Standard: ns.Standard})
	return ns.Code
}

func (p *provenanceSet) list() []sourceOut {
	if p.items == nil {
		return []sourceOut{}
	}
	return p.items
}

// islandOuts maps resolved islands to their output shape, recording each name
// provenance in prov. Returns nil for no islands so the block serializes as null.
func islandOuts(islands []domain.Island, prov *provenanceSet) []islandOut {
	if len(islands) == 0 {
		return nil
	}
	out := make([]islandOut, len(islands))
	for i, is := range islands {
		out[i] = islandOut{
			Name:       is.Name,
			NameNative: is.NameNative,
			NameSource: prov.add(is.NameSource),
		}
	}
	return out
}

// newMountainsOut maps the two-level mountains result to its output shape,
// recording name provenance in prov. Returns nil when the result is nil so the
// block serializes as null.
func newMountainsOut(m *domain.MountainResult, prov *provenanceSet) *mountainsOut {
	if m == nil {
		return nil
	}
	return &mountainsOut{
		Mountain: mountainOutFrom(m.Mountain, prov),
		Range:    mountainOutFrom(m.Range, prov),
	}
}

// mountainOutFrom maps one mountain (range or single-mountain) to its output
// shape, or nil. Elevation is set only for a single-mountain (HasElevation).
func mountainOutFrom(m *domain.Mountain, prov *provenanceSet) *mountainOut {
	if m == nil {
		return nil
	}
	out := &mountainOut{
		Name:       m.Name,
		NameNative: m.NameNative,
		NameSource: prov.add(m.NameSource),
	}
	if m.HasElevation {
		e := m.ElevationM
		out.Elevation = &e
	}
	return out
}

// newElevationOut maps an elevation result to its output shape, nesting the DEM
// source license under Source. Returns nil when elevation is unwired (nil), so
// the block serializes as null.
func newElevationOut(elev *domain.Elevation) *elevationOut {
	if elev == nil {
		return nil
	}
	eo := &elevationOut{
		Meters:              elev.Meters,
		AccuracyM:           elev.AccuracyM,
		AccuracyBasis:       elev.AccuracyBasis,
		HorizontalAccuracyM: elev.HorizontalM,
		VerticalDatum:       elev.VerticalDatum,
		SeaLevel:            elev.SeaLevel,
		SurfaceModel:        elev.SurfaceModel,
	}
	if !elev.License.IsEmpty() {
		eo.Source = &licenseOut{Name: elev.License.Name, URL: elev.License.URL, Attribution: elev.License.Attribution}
	}
	return eo
}

// newExposureOut maps a terrain exposure to its output shape. AspectDeg is nil
// and AspectCompass empty when flat. Returns nil when exposure is unavailable
// (nil), so the block serializes as null.
func newExposureOut(exp *domain.Exposure) *exposureOut {
	if exp == nil {
		return nil
	}
	eo := &exposureOut{
		SlopeDeg:       exp.SlopeDeg,
		SlopePercent:   exp.SlopePercent,
		Flat:           exp.Flat,
		SampleSpacingM: exp.SampleSpacingM,
	}
	if !exp.Flat {
		az := exp.AspectDeg
		eo.AspectDeg = &az
		eo.AspectCompass = exp.AspectCompass
	}
	if !exp.License.IsEmpty() {
		eo.Source = &licenseOut{Name: exp.License.Name, URL: exp.License.URL, Attribution: exp.License.Attribution}
	}
	return eo
}

func addGazetteer(srv *mcp.Server, deps Deps, _ *slog.Logger) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "gazetteer",
		Description: "Reverse-geocode a coordinate to its administrative hierarchy " +
			"(admin), name the island(s) containing it (islands, when an islands " +
			"layer is configured), name the mountain range and single mountain it " +
			"lies in (mountains.range / mountains.mountain, smallest containing per " +
			"landform, when a mountains layer is configured), compute a bearing to " +
			"the most salient nearby place (bearing, e.g. '4 km E Würzburg'), report " +
			"the terrain slope and the direction it faces (exposure/aspect), and " +
			"report the height above sea level (elevation, meters; exposure + " +
			"elevation need a DEM). A part is null when it has no result — no admin " +
			"coverage, not on an island/mountain, no anchor within reach, no DEM, or " +
			"(exposure) the point/neighbor lacks coverage. elevation is null " +
			"OUTSIDE DEM coverage too: a point beyond the DEM's edge has no known " +
			"height, and reporting 0 m there would assert sea level for ground the " +
			"DEM never saw. sea_level=true means the DEM covers the point and holds " +
			"no value — water it surveyed. The available block says which optional parts " +
			"this deployment can answer at all, which is what distinguishes a null " +
			"part meaning 'not in this dataset' from one meaning 'no result here'. " +
			"Equivalent to GET /api/v1/gazetteer.",
	}, func(ctx toolCtx, _ *callRequest, in gazetteerIn) (*callResult, gazetteerOut, error) {
		coord, err := selectCoordinate(in.Lon, in.Lat, in.X, in.Y, in.SRID)
		if err != nil {
			return nil, gazetteerOut{}, err
		}

		caps := deps.Gazetteer.Capabilities()
		out := gazetteerOut{
			Sources: []sourceOut{},
			Available: availableOut{
				Islands:   caps.Islands,
				Mountains: caps.Mountains,
				Exposure:  caps.Exposure,
				Elevation: caps.Elevation,
			},
		}
		prov := newProvenanceSet()

		loc, err := deps.Gazetteer.Locate(ctx, coord)
		switch {
		case err == nil:
			hierarchy := make([]adminUnitOut, len(loc.Chain))
			for i, u := range loc.Chain {
				hierarchy[i] = adminUnitOut{
					Level:                 u.Level,
					Name:                  u.Name,
					NameNative:            u.NameNative,
					NameSource:            prov.add(u.NameSource),
					Equivalent:            u.Equivalent,
					LocalTerm:             u.LocalTerm,
					EquivalentDescription: u.EquivalentDesc,
				}
			}
			out.Admin = &adminOut{CountryISO: loc.CountryISO, Hierarchy: hierarchy}
		case errors.Is(err, domain.ErrNotFound):
			// no admin coverage — leave nil
		default:
			return nil, gazetteerOut{}, err
		}

		islands, err := deps.Gazetteer.Islands(ctx, coord)
		if err != nil {
			return nil, gazetteerOut{}, err
		}
		out.Islands = islandOuts(islands, prov)

		mountains, err := deps.Gazetteer.Mountains(ctx, coord)
		if err != nil {
			return nil, gazetteerOut{}, err
		}
		out.Mountains = newMountainsOut(mountains, prov)

		fix, err := deps.Gazetteer.Bearing(ctx, coord, deps.BearingPolicy.OrDefault())
		switch {
		case err == nil:
			out.Bearing = &bearingOut{
				Reference:  fix.Reference.Name,
				NameNative: fix.Reference.NameNative,
				NameSource: prov.add(fix.Reference.NameSource),
				Class:      fix.Reference.Class.String(),
				DistanceKM: fix.DistanceKM,
				Azimuth:    fix.Azimuth,
				Compass:    fix.Compass,
				Label:      fix.Label,
				Inside:     fix.Inside,
			}
		case errors.Is(err, domain.ErrNotFound):
			// no anchor within reach — leave nil
		default:
			return nil, gazetteerOut{}, err
		}

		exp, err := deps.Gazetteer.Exposure(ctx, coord)
		if err != nil {
			return nil, gazetteerOut{}, err
		}
		out.Exposure = newExposureOut(exp)

		elev, err := deps.Gazetteer.Elevation(ctx, coord)
		if err != nil {
			return nil, gazetteerOut{}, err
		}
		out.Elevation = newElevationOut(elev)

		out.Sources = prov.list()
		if !deps.GazetteerLicense.IsEmpty() {
			out.License = &licenseOut{
				Name:        deps.GazetteerLicense.Name,
				URL:         deps.GazetteerLicense.URL,
				Attribution: deps.GazetteerLicense.Attribution,
			}
		}
		return nil, out, nil
	})
}
