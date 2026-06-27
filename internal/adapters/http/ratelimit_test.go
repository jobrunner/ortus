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
		{"xff ignored when peer not trusted", "1.2.3.4:5678", "9.9.9.9", []string{"10.0.0.0/8"}, "1.2.3.4"},
		{"rightmost-non-trusted is the client", "1.2.3.4:5678", "9.9.9.9, 1.2.3.4", []string{"1.2.3.0/24"}, "9.9.9.9"},
		// Spoof attempt: client injects a fake left-most entry; the real client
		// IP is what the trusted proxy appended (right-most non-trusted).
		{"spoofed left-most ignored", "1.2.3.4:5678", "1.1.1.1, 5.5.5.5", []string{"1.2.3.0/24"}, "5.5.5.5"},
		{"all-trusted chain falls back to peer", "1.2.3.4:5678", "1.2.3.5", []string{"1.2.3.0/24"}, "1.2.3.4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tt.remote
			if tt.xff != "" {
				r.Header.Set("X-Forwarded-For", tt.xff)
			}
			nets, _ := parseCIDRs(tt.trusted)
			if got := clientIP(r, nets); got != tt.want {
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

	call := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/query", nil)
		r.RemoteAddr = "5.5.5.5:1111"
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, r)
		return rr
	}

	if rr := call(); rr.Code != http.StatusOK {
		t.Errorf("first request = %d, want 200", rr.Code)
	}
	rr := call()
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("second request = %d, want 429", rr.Code)
	}
	if ra := rr.Header().Get("Retry-After"); ra == "" {
		t.Error("429 response should set a Retry-After header")
	}
}
