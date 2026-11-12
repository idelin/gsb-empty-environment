package framepolicy

import (
	"net/url"
	"strconv"
	"strings"
)

// sourceKind classifies a parsed frame-ancestors source token.
type sourceKind int

const (
	sourceNone sourceKind = iota
	sourceSelf
	sourceScheme
	sourceHost
	// sourceInvalid means the token cannot be parsed. It makes the
	// enclosing directive fail closed.
	sourceInvalid
)

// networkSchemes are the schemes that a scheme-less host source may match,
// per the CSP host-source production.
var networkSchemes = map[string]bool{
	"http":  true,
	"https": true,
	"ws":    true,
	"wss":   true,
	"ftp":   true,
}

// defaultPorts maps a scheme to its default port for origin comparison.
var defaultPorts = map[string]int{
	"http":  80,
	"ws":    80,
	"https": 443,
	"wss":   443,
	"ftp":   21,
}

// source is one parsed frame-ancestors source expression.
type source struct {
	kind sourceKind

	// scheme is the matched scheme, lower-cased. For host sources it is
	// empty when the token omits an explicit scheme.
	scheme string

	// host is the matched host, lower-cased. An empty host with
	// wildcardHost true represents the bare "*" host-source.
	host         string
	wildcardHost bool

	// hasPort is false when the token omits a port (every port matches).
	hasPort bool
	// wildcardPort is true for the ":*" port production.
	wildcardPort bool
	port         int
}

// parseSource parses one frame-ancestors source token.
func parseSource(token string) (source, sourceKind) {
	switch {
	case strings.EqualFold(token, "'none'"):
		return source{}, sourceNone
	case strings.EqualFold(token, "'self'"):
		return source{kind: sourceSelf}, sourceSelf
	}

	if scheme, rest, ok := splitScheme(token); ok {
		if rest == "" {
			// "https:" style scheme-source.
			return source{kind: sourceScheme, scheme: scheme}, sourceScheme
		}
		if rest[0] != '/' || len(rest) < 2 || rest[1] != '/' {
			// A scheme prefix must be followed by "//" for a host-source.
			return source{}, sourceInvalid
		}
		hostPart := rest[2:]
		return parseHostSource(hostPart, scheme)
	}

	// A bare token without a scheme must be a host-source.
	return parseHostSource(token, "")
}

// splitScheme splits "scheme:rest" when token carries a valid scheme prefix.
func splitScheme(token string) (scheme, rest string, ok bool) {
	idx := strings.IndexByte(token, ':')
	if idx <= 0 {
		return "", "", false
	}
	scheme = token[:idx]
	if !isScheme(scheme) {
		return "", "", false
	}
	return strings.ToLower(scheme), token[idx+1:], true
}

// isScheme reports whether s is a syntactically valid RFC 3986 scheme.
func isScheme(s string) bool {
	if s == "" {
		return false
	}
	first := s[0]
	if !isAlpha(first) {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if !isAlpha(c) && !isDigit(c) && c != '+' && c != '-' && c != '.' {
			return false
		}
	}
	return true
}

// parseHostSource parses the host part of a host-source (the text following
// "scheme://"). An explicit scheme has already been extracted.
func parseHostSource(hostPart, scheme string) (source, sourceKind) {
	// Drop any path/query/fragment; frame-ancestors ignores them.
	if idx := strings.IndexAny(hostPart, "/?#"); idx >= 0 {
		hostPart = hostPart[:idx]
	}
	// Strip userinfo, which frame-ancestors does not use.
	if idx := strings.IndexByte(hostPart, '@'); idx >= 0 {
		hostPart = hostPart[idx+1:]
	}
	if hostPart == "" {
		return source{}, sourceInvalid
	}

	src := source{kind: sourceHost, scheme: scheme}

	if hostPart[0] == '[' {
		// Bracketed IP literal, optionally followed by a port.
		close := strings.IndexByte(hostPart, ']')
		if close < 0 {
			return source{}, sourceInvalid
		}
		src.host = strings.ToLower(hostPart[:close+1])
		remainder := hostPart[close+1:]
		if remainder != "" {
			if remainder[0] != ':' || !parsePort(remainder[1:], &src) {
				return source{}, sourceInvalid
			}
		}
		return src, sourceHost
	}

	// A host expression never contains a slash at this point, and may
	// contain at most one colon separating the port.
	if strings.Count(hostPart, ":") > 1 {
		return source{}, sourceInvalid
	}

	host := hostPart
	if idx := strings.IndexByte(hostPart, ':'); idx >= 0 {
		host = hostPart[:idx]
		if !parsePort(hostPart[idx+1:], &src) {
			return source{}, sourceInvalid
		}
	}

	if host == "*" {
		src.wildcardHost = true
		src.host = ""
		return src, sourceHost
	}
	if strings.HasPrefix(host, "*.") {
		src.wildcardHost = true
		host = host[2:]
	}
	if host == "" || !isHostName(host) {
		return source{}, sourceInvalid
	}
	src.host = strings.ToLower(host)
	return src, sourceHost
}

// parsePort fills the port fields of src and validates the port production.
func parsePort(raw string, src *source) bool {
	if raw == "" {
		return false
	}
	src.hasPort = true
	if raw == "*" {
		src.wildcardPort = true
		return true
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port <= 0 || port > 65535 {
		return false
	}
	src.port = port
	return true
}

// isHostName validates a non-wildcard host or IPv4 literal.
func isHostName(host string) bool {
	for i := 0; i < len(host); i++ {
		c := host[i]
		if isAlpha(c) || isDigit(c) || c == '.' || c == '-' {
			continue
		}
		return false
	}
	return true
}

// matches reports whether the ancestor URL satisfies this source. self is the
// URL of the protected document and is used only by 'self'.
func (s source) matches(ancestor, self *url.URL) bool {
	if ancestor == nil || ancestor.Scheme == "" || ancestor.Host == "" {
		return false
	}
	switch s.kind {
	case sourceSelf:
		return self != nil && sameOrigin(ancestor, self)
	case sourceScheme:
		return strings.EqualFold(ancestor.Scheme, s.scheme)
	case sourceHost:
		return s.matchHostSource(ancestor)
	default:
		return false
	}
}

// matchHostSource implements scheme, host and port matching for host-sources.
func (s source) matchHostSource(ancestor *url.URL) bool {
	scheme := strings.ToLower(ancestor.Scheme)

	if s.scheme != "" {
		if scheme != s.scheme {
			return false
		}
	} else if !networkSchemes[scheme] {
		// Scheme-less host-sources only match network schemes.
		return false
	}

	if !s.matchHost(strings.ToLower(ancestor.Hostname())) {
		return false
	}

	if s.hasPort && !s.wildcardPort && effectivePort(ancestor) != s.port {
		return false
	}
	return true
}

func (s source) matchHost(ancestorHost string) bool {
	if s.wildcardHost && s.host == "" {
		// Bare "*": any host.
		return true
	}
	if s.wildcardHost {
		// "*.example.com" matches "example.com" and any sub-domain of it.
		return ancestorHost == s.host || strings.HasSuffix(ancestorHost, "."+s.host)
	}
	return ancestorHost == s.host
}

// sameOrigin compares the tuple (scheme, host, port), treating default ports
// as equivalent to an omitted port.
func sameOrigin(a, b *url.URL) bool {
	if !strings.EqualFold(a.Scheme, b.Scheme) {
		return false
	}
	if !strings.EqualFold(a.Hostname(), b.Hostname()) {
		return false
	}
	return effectivePort(a) == effectivePort(b)
}

// effectivePort returns the explicit port or the scheme default. An unknown
// scheme with no explicit port yields 0, which only equals another origin in
// the same situation.
func effectivePort(u *url.URL) int {
	if port := u.Port(); port != "" {
		if parsed, err := strconv.Atoi(port); err == nil {
			return parsed
		}
	}
	return defaultPorts[strings.ToLower(u.Scheme)]
}

func isAlpha(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}
