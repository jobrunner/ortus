package domain_test

import (
	"strings"
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
)

func TestParseOrigin(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		wantScheme string
		wantHost   string
		wantPort   string
		wantErr    string // substring; empty ⇒ expect success
	}{
		{name: "https without port", in: "https://example.com", wantScheme: "https", wantHost: "example.com"},
		{name: "https with port", in: "https://example.com:8443", wantScheme: "https", wantHost: "example.com", wantPort: "8443"},
		{name: "http localhost with port", in: "http://localhost:3000", wantScheme: "http", wantHost: "localhost", wantPort: "3000"},
		{name: "subdomain", in: "https://deep.sub.example.com", wantScheme: "https", wantHost: "deep.sub.example.com"},
		{name: "IPv4 with port", in: "http://192.168.1.1:8080", wantScheme: "http", wantHost: "192.168.1.1", wantPort: "8080"},

		// IPv6 literals are bracketed; the colons inside belong to the host.
		{name: "IPv6 without port", in: "http://[::1]", wantScheme: "http", wantHost: "[::1]"},
		{name: "IPv6 with port", in: "http://[::1]:8080", wantScheme: "http", wantHost: "[::1]", wantPort: "8080"},
		{name: "full IPv6 without port", in: "https://[2001:db8::1]", wantScheme: "https", wantHost: "[2001:db8::1]"},
		{
			// Malformed, but it must not be torn into a bogus host/port pair —
			// keep it whole so the comparison simply fails to match.
			name: "unbalanced bracket stays one host", in: "http://[::1", wantScheme: "http", wantHost: "[::1",
		},

		// A port a browser can never send makes the entry dead on arrival; the
		// allow-list must reject it rather than accept a rule that cannot match.
		{name: "non-numeric port", in: "https://example.com:not-a-port", wantErr: "port"},
		{name: "port above the range", in: "https://example.com:65536", wantErr: "port"},
		{name: "port zero", in: "https://example.com:0", wantErr: "port"},
		{name: "empty port", in: "https://example.com:", wantErr: "port"},
		{name: "highest valid port", in: "https://example.com:65535", wantScheme: "https", wantHost: "example.com", wantPort: "65535"},

		// Browsers send "null" for opaque origins (sandboxed iframes, file://,
		// some redirects). Allow-listing it would let any sandboxed document
		// read the API, so it is refused — but with a reason of its own, not
		// the misleading "needs a scheme".
		{name: "null origin", in: "null", wantErr: "opaque"},

		// Text after the closing bracket is neither host nor port. It used to
		// land in Port unvalidated, leaving an entry that no browser origin can
		// ever match.
		{name: "text after IPv6 literal", in: "https://[::1]typo", wantErr: "after the IPv6 literal"},
		{name: "unbalanced closing only", in: "https://[::1]:", wantErr: "port"},

		{name: "no scheme", in: "example.com", wantErr: "needs a scheme"},
		{name: "empty scheme", in: "://example.com", wantErr: "needs a scheme"},
		{name: "empty host", in: "https://", wantErr: "host"},
		{name: "path is not part of an origin", in: "https://example.com/app", wantErr: "path"},
		{name: "trailing slash is a path", in: "https://example.com/", wantErr: "path"},
		{name: "empty string", in: "", wantErr: "needs a scheme"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.ParseOrigin(tt.in)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("ParseOrigin(%q) = %+v, nil; want an error containing %q", tt.in, got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("ParseOrigin(%q) error = %q; want it to contain %q", tt.in, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseOrigin(%q) error = %v; want success", tt.in, err)
			}
			if got.Scheme != tt.wantScheme || got.Host != tt.wantHost || got.Port != tt.wantPort {
				t.Errorf("ParseOrigin(%q) = (%q, %q, %q); want (%q, %q, %q)",
					tt.in, got.Scheme, got.Host, got.Port, tt.wantScheme, tt.wantHost, tt.wantPort)
			}
		})
	}
}

func TestParseOriginPatternRejects(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr string
	}{
		{name: "exact origin", in: "https://example.com"},
		{name: "wildcard with scheme", in: "https://*.example.com"},
		{name: "wildcard with scheme and port", in: "http://*.localhost:3000"},

		{
			name:    "scheme-less wildcard",
			in:      "*.example.com",
			wantErr: "needs a scheme",
		},
		{
			// A path on a pattern used to be stripped, silently widening
			// "https://*.example.com/private" to every subdomain.
			name:    "wildcard with path",
			in:      "https://*.example.com/private",
			wantErr: "path",
		},
		{
			name:    "separator without a scheme",
			in:      "://*.example.com",
			wantErr: "needs a scheme",
		},
		{
			// Only a whole leading label may be wildcarded.
			name:    "wildcard inside a label",
			in:      "https://sub*.example.com",
			wantErr: "*",
		},
		{
			name:    "wildcard not at the start of the host",
			in:      "https://sub.*.example.com",
			wantErr: "*",
		},
		{
			name:    "bare wildcard host",
			in:      "https://*",
			wantErr: "*",
		},
		{
			// "*." passes a naive prefix check but leaves an empty base host,
			// and the resulting "." suffix would match hosts like "evil.".
			name:    "wildcard without a base host",
			in:      "https://*.",
			wantErr: "*",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := domain.ParseOriginPattern(tt.in)

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ParseOriginPattern(%q) error = %v; want success", tt.in, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ParseOriginPattern(%q) = nil error; want one containing %q", tt.in, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("ParseOriginPattern(%q) error = %q; want it to contain %q", tt.in, err, tt.wantErr)
			}
		})
	}
}

func TestOriginPatternMatches(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		origin  string
		want    bool
	}{
		// Exact
		{name: "exact match", pattern: "https://example.com", origin: "https://example.com", want: true},
		{name: "exact with port", pattern: "https://example.com:8080", origin: "https://example.com:8080", want: true},
		{name: "exact rejects other scheme", pattern: "https://example.com", origin: "http://example.com"},
		{name: "exact rejects other host", pattern: "https://example.com", origin: "https://other.com"},
		{name: "exact rejects other port", pattern: "https://example.com:8080", origin: "https://example.com:9090"},
		{name: "exact rejects a subdomain", pattern: "https://example.com", origin: "https://sub.example.com"},

		// Wildcard on the host label
		{name: "wildcard matches subdomain", pattern: "https://*.example.com", origin: "https://sub.example.com", want: true},
		{name: "wildcard matches deep subdomain", pattern: "https://*.example.com", origin: "https://deep.sub.example.com", want: true},
		{name: "wildcard rejects the bare domain", pattern: "https://*.example.com", origin: "https://example.com"},
		{name: "wildcard rejects a different domain", pattern: "https://*.example.com", origin: "https://sub.other.com"},
		{name: "wildcard rejects a partial label", pattern: "https://*.example.com", origin: "https://notexample.com"},

		// The point of this whole change: no widening across scheme or port.
		{name: "wildcard does not widen https to http", pattern: "https://*.example.com", origin: "http://sub.example.com"},
		{name: "wildcard does not widen http to https", pattern: "http://*.example.com", origin: "https://sub.example.com"},
		{name: "wildcard does not widen to another port", pattern: "https://*.example.com", origin: "https://sub.example.com:8443"},
		{name: "wildcard with port rejects the default port", pattern: "https://*.example.com:8443", origin: "https://sub.example.com"},
		{name: "wildcard with port matches that port", pattern: "http://*.localhost:3000", origin: "http://sub.localhost:3000", want: true},

		// A malformed origin never matches.
		{name: "malformed origin", pattern: "https://*.example.com", origin: "sub.example.com"},
		{name: "empty origin", pattern: "https://example.com", origin: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pattern, err := domain.ParseOriginPattern(tt.pattern)
			if err != nil {
				t.Fatalf("ParseOriginPattern(%q) error = %v", tt.pattern, err)
			}

			if got := pattern.MatchesString(tt.origin); got != tt.want {
				t.Errorf("%q.MatchesString(%q) = %v; want %v", tt.pattern, tt.origin, got, tt.want)
			}
		})
	}
}
