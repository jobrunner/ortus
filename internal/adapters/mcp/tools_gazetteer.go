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
	Level      int    `json:"level"`
	Name       string `json:"name"`
	Equivalent string `json:"equivalent"`
}

// adminOut is the administrative hierarchy containing the coordinate.
type adminOut struct {
	CountryISO string         `json:"country_iso"`
	Hierarchy  []adminUnitOut `json:"hierarchy"`
}

// bearingOut is the bearing fix relative to the most salient nearby place.
type bearingOut struct {
	Reference  string  `json:"reference"`
	Class      string  `json:"class"`
	DistanceKM float64 `json:"distance_km"`
	Azimuth    float64 `json:"azimuth"`
	Compass    string  `json:"compass"`
	Label      string  `json:"label"`
}

// gazetteerOut is the tool result: admin and/or bearing, either of which is null
// when it has no result for the coordinate.
type gazetteerOut struct {
	Admin   *adminOut   `json:"admin"`
	Bearing *bearingOut `json:"bearing"`
}

func addGazetteer(srv *mcp.Server, deps Deps, _ *slog.Logger) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "gazetteer",
		Description: "Reverse-geocode a coordinate to its administrative hierarchy " +
			"(admin) and compute a bearing to the most salient nearby place " +
			"(bearing, e.g. '4 km E Würzburg'). Either part is null when it has no " +
			"result — no admin coverage, or no anchor within reach. Equivalent to " +
			"GET /api/v1/gazetteer.",
	}, func(ctx toolCtx, _ *callRequest, in gazetteerIn) (*callResult, gazetteerOut, error) {
		coord, err := selectCoordinate(in.Lon, in.Lat, in.X, in.Y, in.SRID)
		if err != nil {
			return nil, gazetteerOut{}, err
		}

		var out gazetteerOut

		loc, err := deps.Gazetteer.Locate(ctx, coord)
		switch {
		case err == nil:
			hierarchy := make([]adminUnitOut, len(loc.Chain))
			for i, u := range loc.Chain {
				hierarchy[i] = adminUnitOut{Level: u.Level, Name: u.Name, Equivalent: u.Equivalent}
			}
			out.Admin = &adminOut{CountryISO: loc.CountryISO, Hierarchy: hierarchy}
		case errors.Is(err, domain.ErrNotFound):
			// no admin coverage — leave nil
		default:
			return nil, gazetteerOut{}, err
		}

		fix, err := deps.Gazetteer.Bearing(ctx, coord, deps.BearingPolicy.OrDefault())
		switch {
		case err == nil:
			out.Bearing = &bearingOut{
				Reference:  fix.Reference.Name,
				Class:      fix.Reference.Class.String(),
				DistanceKM: fix.DistanceKM,
				Azimuth:    fix.Azimuth,
				Compass:    fix.Compass,
				Label:      fix.Label,
			}
		case errors.Is(err, domain.ErrNotFound):
			// no anchor within reach — leave nil
		default:
			return nil, gazetteerOut{}, err
		}

		return nil, out, nil
	})
}
