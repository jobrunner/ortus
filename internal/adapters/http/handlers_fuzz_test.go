package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// FuzzParseQueryParams feeds arbitrary raw query strings (the rawest external
// client input) through the query-param parser. It must never panic, and on
// success the returned params must satisfy the "coordinates present" invariant
// the parser claims to enforce.
func FuzzParseQueryParams(f *testing.F) {
	for _, q := range []string{
		"", "lon=1&lat=2", "x=1&y=2&srid=25832", "lon=0&lat=0",
		"lon=abc", "lat=", "srid=notint", "lon=1e999&lat=2",
		"properties=a,b,c", "lon=1&lon=2&lat=3", "%zz", "&&&", "lon=NaN&lat=Inf",
	} {
		f.Add(q)
	}
	s := &Server{}
	f.Fuzz(func(t *testing.T, raw string) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.URL.RawQuery = raw // set directly: url.Query() tolerates malformed input

		params, err := s.parseQueryParams(r) // must not panic
		if err != nil {
			return
		}
		if params.Lon == 0 && params.Lat == 0 && params.X == 0 && params.Y == 0 {
			t.Errorf("parseQueryParams(%q) returned no error but no coordinates", raw)
		}
	})
}
