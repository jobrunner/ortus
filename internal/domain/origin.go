package domain

import (
	"fmt"
	"strconv"
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
}

// browserSchemes are the schemes a browser can actually put in an Origin
// header: http and https for ordinary pages, plus the extension schemes.
// Anything else in an allow-list is far more often a typo than an intention.
var browserSchemes = map[string]bool{
	"http":                 true,
	"https":                true,
	"chrome-extension":     true,
	"moz-extension":        true,
	"safari-web-extension": true,
}

// HasBrowserScheme reports whether the scheme is one a browser can send.
//
// A misspelled scheme is the one typo that survives every other check:
// "htps://example.com" is a structurally valid origin, so it parses, passes
// validation, and then silently matches nothing. Callers use this to say so
// — a warning rather than a rejection, because the list above cannot be
// proven exhaustive for every browser.
func (o Origin) HasBrowserScheme() bool { return browserSchemes[o.Scheme] }

// ParseOrigin parses a concrete origin such as "https://example.com:8443".
//
// It rejects anything that is not a bare origin — a missing scheme, a missing
// host, or a path. A path is not part of an origin; silently dropping one would
// turn "https://*.example.com/private" into a rule covering every subdomain.
func ParseOrigin(s string) (Origin, error) {
	// Browsers send "null" for opaque origins: sandboxed iframes, file://
	// documents, some cross-site redirects. Allow-listing it would open the API
	// to any sandboxed document anywhere, and it cannot be narrowed — so it is
	// refused rather than matched. Reject it here with its own reason; the
	// generic "needs a scheme" advice would suggest "https://null".
	if s == "null" {
		return Origin{}, fmt.Errorf(
			"the opaque origin \"null\" cannot be allow-listed — it would admit any " +
				"sandboxed document; grant the real origin instead")
	}

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

	host, port, hasPort, err := splitHostPort(rest)
	if err != nil {
		return Origin{}, fmt.Errorf("origin %q: %w", s, err)
	}
	if host == "" {
		return Origin{}, fmt.Errorf("origin %q needs a host", s)
	}
	// A port outside 1-65535 is one no browser can ever send, so the entry
	// would be a rule that silently never matches.
	if hasPort {
		if n, convErr := strconv.Atoi(port); convErr != nil || n < 1 || n > 65535 {
			return Origin{}, fmt.Errorf("origin %q has an invalid port %q (expected 1-65535)", s, port)
		}
	}

	return Origin{Scheme: scheme, Host: host, Port: port}, nil
}

// splitHostPort separates an optional ":port" from a host, leaving a bracketed
// IPv6 literal intact — its colons belong to the address, not to a port.
// hasPort distinguishes "no port given" from a port that is present but empty
// ("example.com:"), which is malformed rather than a default.
func splitHostPort(hostPort string) (host, port string, hasPort bool, err error) {
	if after, found := strings.CutPrefix(hostPort, "["); found {
		literal, rest, closed := strings.Cut(after, "]")
		if !closed {
			return hostPort, "", false, nil // unbalanced: treat the whole thing as the host
		}
		// Only ":port" may follow the literal. Anything else is neither host
		// nor port; letting it through would leave an entry no browser origin
		// can match.
		if rest != "" && !strings.HasPrefix(rest, ":") {
			return "", "", false, fmt.Errorf("unexpected %q after the IPv6 literal (expected \":port\" or nothing)", rest)
		}
		p, hasP := strings.CutPrefix(rest, ":")
		return "[" + literal + "]", p, hasP, nil
	}

	if h, p, found := strings.Cut(hostPort, ":"); found {
		return h, p, true, nil
	}
	return hostPort, "", false, nil
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
		return OriginPattern{origin: origin}, nil
	}
	// The base host must be present: "https://*." would leave the suffix ".",
	// which matches any host ending in a dot.
	base, ok := strings.CutPrefix(origin.Host, "*.")
	if !ok || base == "" || strings.Contains(base, "*") {
		return OriginPattern{}, fmt.Errorf(
			"origin pattern %q: \"*\" must be the whole leading host label of a host "+
				"(e.g. https://*.example.com)", s)
	}

	return OriginPattern{
		origin:   origin,
		wildcard: true,
		suffix:   origin.Host[1:], // "*.example.com" -> ".example.com"
	}, nil
}

// MatchesString reports whether a raw Origin header value matches the pattern.
// A malformed origin never matches.
func (p OriginPattern) MatchesString(origin string) bool {
	parsed, err := ParseOrigin(origin)
	if err != nil {
		return false
	}
	return p.Matches(parsed)
}

// HasBrowserScheme reports whether the pattern's scheme is one a browser can
// send, so a caller holding a parsed entry need not reach inside it.
func (p OriginPattern) HasBrowserScheme() bool { return p.origin.HasBrowserScheme() }

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
