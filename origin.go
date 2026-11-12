package netsec

import (
	"net/url"
	"strconv"
	"strings"
)

// CanonicalOrigin parses an Origin header value and returns a canonical
// scheme://host[:port] representation. It returns false for missing, opaque
// ("null"), or malformed origins.
//
// Scheme and host are compared case-insensitively. Default ports 80 and 443
// are removed. Userinfo, paths, queries, fragments, whitespace, and other
// forms that cannot be an RFC 6454 origin are rejected.
func CanonicalOrigin(raw string) (string, bool) {
	origin := strings.TrimSpace(raw)
	if origin == "" || origin == "null" {
		return "", false
	}
	if strings.ContainsAny(origin, " \t\r\n\x00?#") || strings.Contains(origin, `\`) {
		return "", false
	}

	u, err := url.Parse(origin)
	if err != nil {
		return "", false
	}

	scheme := strings.ToLower(u.Scheme)
	if !validOriginScheme(scheme) || u.Opaque != "" || u.Host == "" {
		return "", false
	}
	if u.User != nil {
		return "", false
	}
	if u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery {
		return "", false
	}
	if u.Fragment != "" || u.RawFragment != "" {
		return "", false
	}
	if strings.Contains(u.Host, "%") {
		return "", false
	}
	if strings.HasSuffix(u.Host, ":") {
		return "", false
	}

	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", false
	}

	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", false
		}
		if (scheme == "http" && n == 80) || (scheme == "https" && n == 443) {
			port = ""
		}
	}

	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host += ":" + port
	}
	return scheme + "://" + host, true
}

// OriginAllowed reports whether origin exactly matches one of the allowlisted
// origins after canonicalization. An invalid origin never matches. An empty
// allowlist denies every origin.
func OriginAllowed(origin string, allowedOrigins []string) bool {
	canonical, ok := CanonicalOrigin(origin)
	if !ok {
		return false
	}
	for _, allowed := range allowedOrigins {
		allowedCanonical, ok := CanonicalOrigin(allowed)
		if ok && allowedCanonical == canonical {
			return true
		}
	}
	return false
}

func validOriginScheme(scheme string) bool {
	if len(scheme) == 0 || scheme[0] < 'a' || scheme[0] > 'z' {
		return false
	}
	for i := 1; i < len(scheme); i++ {
		c := scheme[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '+' || c == '-' || c == '.' {
			continue
		}
		return false
	}
	return true
}
