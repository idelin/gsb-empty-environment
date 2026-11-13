package netsec

import (
	"net/http"
	"sort"
	"strings"
)

const (
	HeaderCFVisitor          = "Cf-Visitor"
	HeaderFastlySSL          = "Fastly-Ssl"
	HeaderForwarded          = "Forwarded"
	HeaderFrontEndHTTPS      = "Front-End-Https"
	HeaderXForwardedFor      = "X-Forwarded-For"
	HeaderXForwardedHost     = "X-Forwarded-Host"
	HeaderXForwardedPort     = "X-Forwarded-Port"
	HeaderXForwardedProto    = "X-Forwarded-Proto"
	HeaderXForwardedProtocol = "X-Forwarded-Protocol"
	HeaderXForwardedSsl      = "X-Forwarded-Ssl"
	HeaderXForwardedScheme   = "X-Forwarded-Scheme"
	HeaderXOriginalHost      = "X-Original-Host"
	HeaderXOriginalProto     = "X-Original-Proto"
	HeaderXOriginalURL       = "X-Original-Url"
	HeaderXRealIP            = "X-Real-Ip"
	HeaderXRewriteURL        = "X-Rewrite-Url"
	HeaderXScheme            = "X-Scheme"
	HeaderXURLScheme         = "X-Url-Scheme"
)

// IsForwardingHeader reports whether name is a header used to describe the
// client connection to an upstream server. Forwarding headers are stripped
// unless the caller has included them in the cache key.
func IsForwardingHeader(name string) bool {
	name = canonicalHeaderName(name)
	switch name {
	case HeaderCFVisitor,
		HeaderFastlySSL,
		HeaderForwarded,
		HeaderFrontEndHTTPS,
		HeaderXRealIP,
		HeaderXRewriteURL,
		HeaderXScheme,
		HeaderXURLScheme,
		"X-Client-Ip",
		"X-Cluster-Client-Ip",
		"X-Http-Method-Override",
		"X-Method-Override",
		"CF-Connecting-Ip",
		"Fastly-Client-Ip",
		"True-Client-Ip":
		return true
	}
	return strings.HasPrefix(name, "X-Forwarded-") ||
		strings.HasPrefix(name, "X-Original-")
}

// IsSchemeHeader reports whether name carries the original request scheme.
func IsSchemeHeader(name string) bool {
	switch canonicalHeaderName(name) {
	case HeaderCFVisitor,
		HeaderFastlySSL,
		HeaderForwarded,
		HeaderFrontEndHTTPS,
		HeaderXForwardedProto,
		HeaderXForwardedProtocol,
		HeaderXForwardedScheme,
		HeaderXForwardedSsl,
		HeaderXOriginalProto,
		HeaderXScheme,
		HeaderXURLScheme:
		return true
	default:
		return false
	}
}

// IsHopByHopHeader reports whether name is a hop-by-hop header. Such headers
// must not be forwarded by a proxy regardless of the cache-key policy.
func IsHopByHopHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "connection",
		"keep-alive",
		"proxy-authenticate",
		"proxy-authorization",
		"proxy-connection",
		"te",
		"trailer",
		"transfer-encoding",
		"upgrade":
		return true
	default:
		return false
	}
}

// ParseVary parses one or more Vary header values. The returned names are
// canonical, deduplicated, and sorted. "*" is preserved as the first element.
func ParseVary(values ...string) []string {
	seen := make(map[string]struct{})
	var names []string
	all := false
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if part == "*" {
				all = true
				continue
			}
			name := canonicalHeaderName(part)
			if name == "" {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if all {
		return append([]string{"*"}, names...)
	}
	return names
}

// VaryAllowsStorage reports whether a response with the supplied Vary values
// may be stored in a shared cache. Vary: * forbids shared storage.
func VaryAllowsStorage(values ...string) bool {
	for _, name := range ParseVary(values...) {
		if name == "*" {
			return false
		}
	}
	return true
}

// VaryIncludes reports whether name is explicitly listed by Vary. Name matching
// is case-insensitive.
func VaryIncludes(values []string, name string) bool {
	name = canonicalHeaderName(name)
	for _, vary := range ParseVary(values...) {
		if vary == name {
			return true
		}
	}
	return false
}

// VaryNames returns the canonical header names listed by Vary. A "*" entry is
// omitted because it is not a concrete header name; use VaryAllowsStorage to
// reject such responses.
func VaryNames(values ...string) []string {
	names := ParseVary(values...)
	out := names[:0]
	for _, name := range names {
		if name != "*" {
			out = append(out, name)
		}
	}
	return out
}

// VaryFromHeader extracts and parses the Vary header from h.
func VaryFromHeader(h http.Header) []string {
	return ParseVary(headerValues(h, "Vary")...)
}

func canonicalHeaderName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	return http.CanonicalHeaderKey(name)
}

func equalHeaderName(a, b string) bool {
	return strings.EqualFold(a, b)
}

func headerValues(h http.Header, name string) []string {
	if h == nil {
		return nil
	}
	name = canonicalHeaderName(name)
	keys := make([]string, 0, 1)
	for key := range h {
		if equalHeaderName(key, name) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var values []string
	for _, key := range keys {
		values = append(values, h[key]...)
	}
	return values
}

func deleteHeader(h http.Header, name string) {
	for key := range h {
		if equalHeaderName(key, name) {
			delete(h, key)
		}
	}
}

func connectionHeaders(h http.Header) map[string]struct{} {
	headers := make(map[string]struct{})
	for _, value := range headerValues(h, "Connection") {
		for _, part := range strings.Split(value, ",") {
			name := canonicalHeaderName(part)
			if name != "" {
				headers[name] = struct{}{}
			}
		}
	}
	return headers
}

func canonicalNameSet(names []string) map[string]struct{} {
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = canonicalHeaderName(name)
		if name == "" || name == "*" {
			continue
		}
		set[name] = struct{}{}
	}
	return set
}

func sortedNames(set map[string]struct{}) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
