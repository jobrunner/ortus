package http

import (
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/jobrunner/ortus/internal/domain"
)

// parseDecimalCoordinates reads the plain lon/lat/x/y query parameters into
// params, in place. Split out of parseQueryParams (handlers.go) to keep that
// function's complexity within budget.
func parseDecimalCoordinates(q url.Values, params *QueryParams) error {
	if lon := q.Get("lon"); lon != "" {
		v, err := strconv.ParseFloat(lon, 64)
		if err != nil {
			return errors.New("invalid lon parameter")
		}
		params.Lon = v
	}

	if lat := q.Get("lat"); lat != "" {
		v, err := strconv.ParseFloat(lat, 64)
		if err != nil {
			return errors.New("invalid lat parameter")
		}
		params.Lat = v
	}

	if x := q.Get("x"); x != "" {
		v, err := strconv.ParseFloat(x, 64)
		if err != nil {
			return errors.New("invalid x parameter")
		}
		params.X = v
	}

	if y := q.Get("y"); y != "" {
		v, err := strconv.ParseFloat(y, 64)
		if err != nil {
			return errors.New("invalid y parameter")
		}
		params.Y = v
	}

	return nil
}

// mgrsQueryParams resolves an mgrs query parameter into QueryParams carrying
// the decoded UTM easting/northing and its zone's SRID. Split out of
// parseQueryParams (handlers.go) to keep that function's branching within the
// complexity budget.
func mgrsQueryParams(mgrs, properties string) (*QueryParams, error) {
	coord, err := domain.ParseMGRS(mgrs)
	if err != nil {
		return nil, err
	}
	srid, err := domain.UTMSRIDForZone(coord.Zone, coord.Hemisphere)
	if err != nil {
		return nil, err
	}
	params := &QueryParams{X: coord.Easting, Y: coord.Northing, SRID: srid}
	if properties != "" {
		params.Properties = strings.Split(properties, ",")
	}
	return params, nil
}
