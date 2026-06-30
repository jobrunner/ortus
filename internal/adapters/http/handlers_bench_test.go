package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

var (
	sinkParams *QueryParams
	sinkErr    error
	sinkAllow  bool
)

func BenchmarkParseQueryParams(b *testing.B) {
	s := &Server{}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.URL.RawQuery = "lon=11.58&lat=48.13&srid=4326&properties=name,pop"
	var (
		p   *QueryParams
		err error
	)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p, err = s.parseQueryParams(r)
	}
	sinkParams, sinkErr = p, err
}

func BenchmarkRateLimiterAllow(b *testing.B) {
	l := newIPRateLimiter(1e9, 1e9) // effectively unlimited: measure the hot path
	_ = l.allow("1.2.3.4")          // warm up: bucket creation + initial sweep happen outside timing
	var ok bool
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ok = l.allow("1.2.3.4")
	}
	sinkAllow = ok
}
