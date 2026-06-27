package http

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIPRateLimiterAllow(t *testing.T) {
	l := newIPRateLimiter(1, 2) // 1 req/s, burst 2

	// Burst of 2 allowed for the same IP, then denied. (Separate statements:
	// each allow() consumes a token, so they're not redundant.)
	if !l.allow("1.1.1.1") {
		t.Fatal("first request should be allowed")
	}
	if !l.allow("1.1.1.1") {
		t.Fatal("second request (within burst 2) should be allowed")
	}
	if l.allow("1.1.1.1") {
		t.Error("third request should be denied (burst exhausted)")
	}
	// A different IP has its own bucket.
	if !l.allow("2.2.2.2") {
		t.Error("different IP should be allowed")
	}
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name    string
		remote  string
		xff     string
		trusted []string
		want    string
	}{
		{"remoteaddr default", "1.2.3.4:5678", "", nil, "1.2.3.4"},
		{"xff ignored without trusted proxy", "1.2.3.4:5678", "9.9.9.9", nil, "1.2.3.4"},
		{"xff used when peer is trusted", "1.2.3.4:5678", "9.9.9.9", []string{"1.2.3.0/24"}, "9.9.9.9"},
		{"xff ignored when peer not trusted", "1.2.3.4:5678", "9.9.9.9", []string{"10.0.0.0/8"}, "1.2.3.4"},
		{"xff list takes left-most", "1.2.3.4:5678", "9.9.9.9, 1.2.3.4", []string{"1.2.3.0/24"}, "9.9.9.9"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tt.remote
			if tt.xff != "" {
				r.Header.Set("X-Forwarded-For", tt.xff)
			}
			if got := clientIP(r, parseCIDRs(tt.trusted)); got != tt.want {
				t.Errorf("clientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	s := &Server{
		rateLimiter: newIPRateLimiter(1, 1), // 1/s, burst 1
		logger:      slog.New(slog.NewTextHandler(httptest.NewRecorder().Body, nil)),
	}
	h := s.rateLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	call := func() int {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/query", nil)
		r.RemoteAddr = "5.5.5.5:1111"
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, r)
		return rr.Code
	}

	if got := call(); got != http.StatusOK {
		t.Errorf("first request = %d, want 200", got)
	}
	if got := call(); got != http.StatusTooManyRequests {
		t.Errorf("second request = %d, want 429", got)
	}
}
