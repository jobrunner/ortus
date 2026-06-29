package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func BenchmarkParseQueryParams(b *testing.B) {
	s := &Server{}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.URL.RawQuery = "lon=11.58&lat=48.13&srid=4326&properties=name,pop"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = s.parseQueryParams(r)
	}
}

func BenchmarkRateLimiterAllow(b *testing.B) {
	l := newIPRateLimiter(1e9, 1e9) // effectively unlimited: measure the hot path, not blocking
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = l.allow("1.2.3.4")
	}
}
