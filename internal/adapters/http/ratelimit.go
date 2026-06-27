package http

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// ipRateLimiter is a per-client-IP token-bucket limiter. It is opt-in
// (server.rate_limit.enabled) — intended for ortus exposed directly on a public
// IP without a rate-limiting gateway in front.
//
// Memory is bounded by an inline sweep: idle IP buckets are evicted on access
// once per ttl, so there is no background goroutine to manage.
type ipRateLimiter struct {
	mu        sync.Mutex
	buckets   map[string]*ipBucket
	rate      rate.Limit
	burst     int
	ttl       time.Duration
	lastSweep time.Time
	now       func() time.Time // injectable for tests
}

type ipBucket struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func newIPRateLimiter(r float64, burst int) *ipRateLimiter {
	if burst < 1 {
		burst = 1
	}
	return &ipRateLimiter{
		buckets: make(map[string]*ipBucket),
		rate:    rate.Limit(r),
		burst:   burst,
		ttl:     10 * time.Minute,
		now:     time.Now,
	}
}

// allow reports whether a request from ip may proceed now.
func (l *ipRateLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	if now.Sub(l.lastSweep) > l.ttl {
		for k, b := range l.buckets {
			if now.Sub(b.lastSeen) > l.ttl {
				delete(l.buckets, k)
			}
		}
		l.lastSweep = now
	}

	b, ok := l.buckets[ip]
	if !ok {
		b = &ipBucket{limiter: rate.NewLimiter(l.rate, l.burst)}
		l.buckets[ip] = b
	}
	b.lastSeen = now
	return b.limiter.Allow()
}

// parseCIDRs parses CIDR strings for the trusted-proxy list, returning the
// parsed networks and any entries that failed to parse (so the caller can warn —
// a silently-dropped CIDR would quietly disable X-Forwarded-For handling).
func parseCIDRs(cidrs []string) (nets []*net.IPNet, invalid []string) {
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, n, err := net.ParseCIDR(c); err == nil {
			nets = append(nets, n)
		} else {
			invalid = append(invalid, c)
		}
	}
	return nets, invalid
}

func ipInAny(ip net.IP, nets []*net.IPNet) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIP resolves the request's client IP for rate-limiting. By default it is
// the direct peer (RemoteAddr). X-Forwarded-For is consulted ONLY when the direct
// peer is itself a trusted proxy; even then the client is the RIGHT-most entry
// that is not a trusted proxy — never the left-most, which a client can spoof
// (proxies append to XFF rather than overwrite it).
func clientIP(r *http.Request, trusted []*net.IPNet) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr // RemoteAddr without a port (unusual) — use as-is
	}
	peer := net.ParseIP(host)
	if peer == nil || len(trusted) == 0 || !ipInAny(peer, trusted) {
		return host
	}
	// Direct peer is a trusted proxy: walk XFF right-to-left, skipping further
	// trusted hops; the first non-trusted address is the real client.
	parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		ip := strings.TrimSpace(parts[i])
		parsed := net.ParseIP(ip)
		if parsed == nil || ipInAny(parsed, trusted) {
			continue
		}
		return ip
	}
	// All XFF entries are trusted proxies (or none present) — fall back to peer.
	return host
}

// rateLimitMiddleware enforces the per-IP limit. Only mounted on the /api/v1
// subrouter (health/probe endpoints are never rate-limited).
func (s *Server) rateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r, s.trustedProxies)
		if !s.rateLimiter.allow(ip) {
			w.Header().Set("Retry-After", "1")
			s.writeError(w, http.StatusTooManyRequests, "Rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}
