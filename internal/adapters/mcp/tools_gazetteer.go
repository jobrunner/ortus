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

// gazetteerOut is the tool result: admin, islands, bearing and elevation, any of
// which is null when it has no result for the coordinate (no admin coverage, not
// on an island, no anchor in reach, or no DEM wired). Sources is the response-wide
// provenance excerpt describing each name_source code that appears above; License
// is the dataset attribution (null when unset).
type gazetteerOut struct {
	Admin     *adminOut     `json:"admin"`
	Islands   []islandOut   `json:"islands"`
	Bearing   *bearingOut   `json:"bearing"`
	Elevation *elevationOut `json:"elevation"`
	Sources   []sourceOut   `json:"sources"`
	License   *licenseOut   `json:"license"`
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

func addGazetteer(srv *mcp.Server, deps Deps, _ *slog.Logger) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "gazetteer",
		Description: "Reverse-geocode a coordinate to its administrative hierarchy " +
			"(admin), name the island(s) containing it (islands, when an islands " +
			"layer is configured), compute a bearing to the most salient nearby " +
			"place (bearing, e.g. '4 km E Würzburg'), and report the height above " +
			"sea level (elevation, meters; when a DEM is configured). Any part is " +
			"null when it has no result — no admin coverage, not on an island, no " +
			"anchor within reach, or no elevation configured. Equivalent to " +
			"GET /api/v1/gazetteer.",
	}, func(ctx toolCtx, _ *callRequest, in gazetteerIn) (*callResult, gazetteerOut, error) {
		coord, err := selectCoordinate(in.Lon, in.Lat, in.X, in.Y, in.SRID)
		if err != nil {
			return nil, gazetteerOut{}, err
		}

		out := gazetteerOut{Sources: []sourceOut{}}
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
