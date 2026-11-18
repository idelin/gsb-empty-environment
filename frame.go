package netsec

import (
	"net/http"
	"net/url"
	"strings"
)

// AllowFraming reports whether a page served at selfURL with response
// headers h may be framed by a document whose ancestor chain is
// ancestorURLs. ancestorURLs lists every embedding ancestor origin from
// the top-level window down to the direct parent; an empty slice means
// the page is not framed at all. Every layer of the chain must satisfy
// the embedding policy, not just the direct parent.
//
// Rules enforced:
//   - An enforcing Content-Security-Policy frame-ancestors directive
//     takes precedence and X-Frame-Options is ignored entirely.
//   - Multiple Content-Security-Policy headers intersect: an ancestor
//     must be allowed by every policy that carries frame-ancestors.
//   - Content-Security-Policy-Report-Only never participates.
//   - 'none' combined with other sources is inert; the other sources
//     still apply.
//   - X-Frame-Options accepts only DENY and SAMEORIGIN; repeated
//     headers, comma-joined or otherwise multiple values, and unknown
//     tokens are invalid and treated as a refusal to be embedded.
func AllowFraming(selfURL string, h http.Header, ancestorURLs []string) bool {
	self, selfOK := parseOrigin(selfURL)

	ancestors := make([]origin, 0, len(ancestorURLs))
	for _, raw := range ancestorURLs {
		a, ok := parseOrigin(raw)
		if !ok {
			return false
		}
		ancestors = append(ancestors, a)
	}
	if len(ancestors) == 0 {
		// Top-level navigation: the page is not framed at all.
		return true
	}

	if lists := frameAncestorsPolicies(h); len(lists) > 0 {
		for _, list := range lists {
			sources := parseSourceList(list)
			for _, ancestor := range ancestors {
				if !matchesAnySource(sources, self, selfOK, ancestor) {
					return false
				}
			}
		}
		return true
	}

	return xFrameOptionsAllows(h, self, selfOK, ancestors)
}

type origin struct {
	scheme string
	host   string
	port   string // explicit port, "" when absent
}

func parseOrigin(raw string) (origin, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return origin{}, false
	}
	host := u.Hostname()
	if host == "" {
		return origin{}, false
	}
	return origin{
		scheme: strings.ToLower(u.Scheme),
		host:   strings.ToLower(host),
		port:   u.Port(),
	}, true
}

func (o origin) effectivePort() string {
	if o.port != "" {
		return o.port
	}
	switch o.scheme {
	case "http":
		return "80"
	case "https":
		return "443"
	}
	return ""
}

func sameOrigin(a, b origin) bool {
	return a.scheme == b.scheme &&
		a.host == b.host &&
		a.effectivePort() == b.effectivePort()
}

// frameAncestorsPolicies collects the source lists of every
// frame-ancestors directive found in enforcing CSP headers. Report-Only
// headers are ignored. A single header value may carry several
// comma-separated policies; each policy contributes at most one
// frame-ancestors directive (the first, per CSP duplicate rules).
func frameAncestorsPolicies(h http.Header) [][]string {
	var lists [][]string
	for _, headerValue := range h.Values("Content-Security-Policy") {
		for _, policy := range strings.Split(headerValue, ",") {
			for _, directive := range strings.Split(policy, ";") {
				fields := strings.Fields(directive)
				if len(fields) > 0 && strings.EqualFold(fields[0], "frame-ancestors") {
					lists = append(lists, fields[1:])
					break
				}
			}
		}
	}
	return lists
}

type sourceExpr struct {
	none     bool
	self     bool
	scheme   string // non-empty for scheme-sources such as "https:"
	host     string // non-empty for host-sources
	wildcard bool   // host carries a "*." prefix
	port     string // explicit port, "" when unspecified
}

// parseSourceList parses one frame-ancestors source list. A 'none' that
// coexists with other sources is dropped and has no effect; a lone
// 'none' (or an empty list) matches nothing.
func parseSourceList(tokens []string) []sourceExpr {
	var sources []sourceExpr
	for _, token := range tokens {
		if strings.EqualFold(token, "'none'") {
			continue
		}
		sources = append(sources, parseSourceExpr(token))
	}
	if len(sources) > 0 {
		return sources
	}
	// Only 'none' was present (or the list was empty): deny everything.
	return []sourceExpr{{none: true}}
}

func parseSourceExpr(token string) sourceExpr {
	if strings.EqualFold(token, "'self'") {
		return sourceExpr{self: true}
	}
	rest := token
	var expr sourceExpr
	if i := strings.Index(rest, "://"); i >= 0 {
		expr.scheme = strings.ToLower(rest[:i])
		rest = rest[i+3:]
	} else if strings.HasSuffix(rest, ":") && !strings.Contains(rest, "/") {
		// Scheme-source such as "https:".
		scheme := strings.TrimSuffix(rest, ":")
		if isScheme(scheme) {
			return sourceExpr{scheme: strings.ToLower(scheme)}
		}
		return sourceExpr{none: true}
	}
	if slash := strings.IndexByte(rest, '/'); slash >= 0 {
		rest = rest[:slash]
	}
	if i := strings.LastIndex(rest, ":"); i >= 0 {
		expr.port = rest[i+1:]
		rest = rest[:i]
	}
	if strings.HasPrefix(rest, "*.") {
		expr.wildcard = true
		rest = rest[2:]
	}
	expr.host = strings.ToLower(rest)
	return expr
}

func isScheme(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case i > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'):
		default:
			return false
		}
	}
	return true
}

func matchesAnySource(sources []sourceExpr, self origin, selfOK bool, ancestor origin) bool {
	for _, src := range sources {
		if matchSource(src, self, selfOK, ancestor) {
			return true
		}
	}
	return false
}

func matchSource(src sourceExpr, self origin, selfOK bool, ancestor origin) bool {
	switch {
	case src.none:
		return false
	case src.self:
		return selfOK && sameOrigin(self, ancestor)
	case src.host == "":
		// Scheme-source.
		return src.scheme != "" && ancestor.scheme == src.scheme
	}
	if src.scheme != "" {
		if ancestor.scheme != src.scheme {
			return false
		}
	} else if selfOK && self.scheme != "" && ancestor.scheme != self.scheme {
		// A host-source without a scheme inherits the scheme of the
		// protected resource.
		return false
	}
	if src.wildcard {
		if !strings.HasSuffix(ancestor.host, "."+src.host) {
			return false
		}
	} else if ancestor.host != src.host {
		return false
	}
	if src.port != "" && ancestor.effectivePort() != src.port {
		return false
	}
	return true
}

func xFrameOptionsAllows(h http.Header, self origin, selfOK bool, ancestors []origin) bool {
	values := h.Values("X-Frame-Options")
	if len(values) == 0 {
		return true
	}
	if len(values) != 1 {
		// Repeated headers are invalid; refuse embedding.
		return false
	}
	value := strings.TrimSpace(values[0])
	if strings.Contains(value, ",") {
		// Comma-joined multiple values are invalid; refuse embedding.
		return false
	}
	switch strings.ToUpper(value) {
	case "DENY":
		return false
	case "SAMEORIGIN":
		if !selfOK {
			return false
		}
		for _, ancestor := range ancestors {
			if !sameOrigin(self, ancestor) {
				return false
			}
		}
		return true
	default:
		// Unknown or unsupported token (e.g. ALLOW-FROM): invalid,
		// refuse embedding.
		return false
	}
}
