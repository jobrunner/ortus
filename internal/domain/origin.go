package domain

import (
	"fmt"
	"strings"
)

// Origin is a web origin: the scheme/host/port triple a browser sends in the
// Origin header. All three parts identify it — two URLs that differ in scheme
// or port are different origins, even on the same host.
type Origin struct {
	Scheme string // "https"
	Host   string // "example.com", or a bracketed IPv6 literal "[::1]"
	Port   string // "" when the scheme's default port is used
}

// OriginPattern is one entry of an allow-list: either an exact origin, or a
// wildcard covering the subdomains of a host ("https://*.example.com").
//
// The wildcard applies to the leading host label only. Scheme and port still
// have to match exactly, because widening them would hand responses to servers
// the operator never listed — a plaintext "http://" sibling, or a different
// service on another port of the same host.
type OriginPattern struct {
	origin   Origin
	wildcard bool   // host was written as "*.<suffix>"
	suffix   string // ".example.com" — the part after the "*", wildcard only
	raw      string // as configured, for error messages
}

// ParseOrigin parses a concrete origin such as "https://example.com:8443".
//
// It rejects anything that is not a bare origin — a missing scheme, a missing
// host, or a path. A path is not part of an origin; silently dropping one would
// turn "https://*.example.com/private" into a rule covering every subdomain.
func ParseOrigin(s string) (Origin, error) {
	scheme, rest, ok := strings.Cut(s, "://")
	if !ok || scheme == "" {
		// Show the fix for the shape actually given: a wildcard entry is the
		// form people have to migrate, so echo it back with a scheme.
		return Origin{}, fmt.Errorf("origin %q needs a scheme — write it as https://%s",
			s, strings.TrimPrefix(s, "://"))
	}
	if strings.Contains(rest, "/") {
		return Origin{}, fmt.Errorf("origin %q must not contain a path", s)
	}

	host, port := splitHostPort(rest)
	if host == "" {
		return Origin{}, fmt.Errorf("origin %q needs a host", s)
	}

	return Origin{Scheme: scheme, Host: host, Port: port}, nil
}

// splitHostPort separates an optional ":port" from a host, leaving a bracketed
// IPv6 literal intact — its colons belong to the address, not to a port.
func splitHostPort(hostPort string) (host, port string) {
	if after, found := strings.CutPrefix(hostPort, "["); found {
		literal, rest, closed := strings.Cut(after, "]")
		if !closed {
			return hostPort, "" // unbalanced: treat the whole thing as the host
		}
		return "[" + literal + "]", strings.TrimPrefix(rest, ":")
	}

	if h, p, found := strings.Cut(hostPort, ":"); found {
		return h, p
	}
	return hostPort, ""
}

// ParseOriginPattern parses one allow-list entry. A wildcard entry must carry a
// scheme like any other origin, and the "*" must be a whole leading label:
// "https://*.example.com" is valid, "https://sub*.example.com" is not.
func ParseOriginPattern(s string) (OriginPattern, error) {
	origin, err := ParseOrigin(s)
	if err != nil {
		return OriginPattern{}, err
	}

	if !strings.Contains(origin.Host, "*") {
		return OriginPattern{origin: origin, raw: s}, nil
	}
	if !strings.HasPrefix(origin.Host, "*.") || strings.Contains(origin.Host[2:], "*") {
		return OriginPattern{}, fmt.Errorf(
			"origin pattern %q: \"*\" must be the whole leading host label (e.g. https://*.example.com)", s)
	}

	return OriginPattern{
		origin:   origin,
		wildcard: true,
		suffix:   origin.Host[1:], // "*.example.com" -> ".example.com"
		raw:      s,
	}, nil
}

// String returns the pattern as it was configured.
func (p OriginPattern) String() string { return p.raw }

// MatchesString reports whether a raw Origin header value matches the pattern.
// A malformed origin never matches.
func (p OriginPattern) MatchesString(origin string) bool {
	parsed, err := ParseOrigin(origin)
	if err != nil {
		return false
	}
	return p.Matches(parsed)
}

// Matches reports whether the origin is covered by the pattern.
func (p OriginPattern) Matches(o Origin) bool {
	if o.Scheme != p.origin.Scheme || o.Port != p.origin.Port {
		return false
	}
	if !p.wildcard {
		return o.Host == p.origin.Host
	}
	// Requiring more than the suffix keeps the bare domain out; keeping the
	// leading dot keeps "notexample.com" out.
	return strings.HasSuffix(o.Host, p.suffix) && len(o.Host) > len(p.suffix)
}
