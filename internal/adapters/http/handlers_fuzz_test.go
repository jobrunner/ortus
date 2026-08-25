package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// FuzzParseQueryParams feeds arbitrary raw query strings (the rawest external
// client input) through the query-param parser. The invariant is simply that
// it never panics regardless of input — we intentionally do NOT assert on the
// parsed values, to avoid coupling the fuzz test to parser quirks like the
// current all-zero-coordinate rejection.
func FuzzParseQueryParams(f *testing.F) {
	for _, q := range []string{
		"", "lon=1&lat=2", "x=1&y=2&srid=25832", "lon=0&lat=0",
		"lon=abc", "lat=", "srid=notint", "lon=1e999&lat=2",
		"properties=a,b,c", "lon=1&lon=2&lat=3", "%zz", "&&&", "lon=NaN&lat=Inf",
		"mgrs=31U+CT+03760+87415", "mgrs=31UCT0376087415", "mgrs=",
		"mgrs=not-mgrs", "mgrs=99Z+ZZ+0+0", "mgrs=31U+CT+03760+87415&lon=1&lat=1",
		"mgrs=%zz", "mgrs=" + string(rune(0)),
	} {
		f.Add(q)
	}
	s := &Server{}
	f.Fuzz(func(t *testing.T, raw string) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.URL.RawQuery = raw // set directly: url.Query() tolerates malformed input

		_, _ = s.parseQueryParams(r) // invariant: must not panic on any input
	})
}
