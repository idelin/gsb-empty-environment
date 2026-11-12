package netsec

import (
	"net/url"
	"strconv"
	"strings"
)

// NormalizeServiceURL canonicalizes a CAS service URL for comparison.
//
// Both the URL stored when a ticket is issued and the URL supplied to
// validation are normalized. Normalization:
//
//   - lower-cases the scheme and host;
//   - removes an explicit default port (80 for http, 443 for https);
//   - normalizes percent escapes and empty paths;
//   - drops the fragment, which is never sent to the service;
//   - preserves path and query component order and case.
//
// Userinfo is rejected because it makes service identity ambiguous.
func NormalizeServiceURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ErrInvalidService
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return "", ErrInvalidService
		}
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", ErrInvalidService
	}
	if u.Opaque != "" || u.Scheme == "" || u.Host == "" {
		return "", ErrInvalidService
	}
	if u.User != nil {
		return "", ErrInvalidService
	}
	if strings.Contains(u.Path, `\`) || strings.Contains(u.RawPath, `\`) {
		return "", ErrInvalidService
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", ErrInvalidService
	}

	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", ErrInvalidService
	}
	// A trailing dot denotes the same DNS name.
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return "", ErrInvalidService
	}

	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", ErrInvalidService
		}
		if (scheme == "http" && n == 80) || (scheme == "https" && n == 443) {
			port = ""
		} else {
			port = strconv.Itoa(n)
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return "", ErrInvalidService
	}

	hostPort := host
	if strings.Contains(host, ":") {
		hostPort = "[" + host + "]"
	}
	if port != "" {
		hostPort += ":" + port
	}

	path, err := normalizeEscapedComponent(u.EscapedPath())
	if err != nil {
		return "", ErrInvalidService
	}
	if path == "" {
		path = "/"
	}
	if strings.Contains(path, `\`) {
		return "", ErrInvalidService
	}

	query, err := normalizeEscapedComponent(u.RawQuery)
	if err != nil {
		return "", ErrInvalidService
	}

	normalized := scheme + "://" + hostPort + path
	if query != "" {
		normalized += "?" + query
	}
	return normalized, nil
}

// CanonicalizeServiceURL is an alias for NormalizeServiceURL.
func CanonicalizeServiceURL(raw string) (string, error) {
	return NormalizeServiceURL(raw)
}

// NormalizeService is an alias for NormalizeServiceURL.
func NormalizeService(raw string) (string, error) {
	return NormalizeServiceURL(raw)
}

// ServiceURLsEqual reports whether two service URLs normalize to the same
// service identity.
func ServiceURLsEqual(a, b string) bool {
	na, err := NormalizeServiceURL(a)
	if err != nil {
		return false
	}
	nb, err := NormalizeServiceURL(b)
	if err != nil {
		return false
	}
	return na == nb
}

func normalizeEscapedComponent(s string) (string, error) {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			b.WriteByte(s[i])
			continue
		}
		if i+2 >= len(s) {
			return "", ErrInvalidService
		}
		hi, ok1 := fromHex(s[i+1])
		lo, ok2 := fromHex(s[i+2])
		if !ok1 || !ok2 {
			return "", ErrInvalidService
		}
		v := hi<<4 | lo
		if v == '\\' {
			return "", ErrInvalidService
		}
		// RFC 3986: percent-encoded unreserved characters are equivalent to
		// their literal forms. Everything else keeps a canonical uppercase
		// escape so that %2f and %2F compare equal.
		if isUnreserved(v) {
			b.WriteByte(v)
		} else {
			b.WriteByte('%')
			b.WriteByte("0123456789ABCDEF"[v>>4])
			b.WriteByte("0123456789ABCDEF"[v&0x0f])
		}
		i += 2
	}
	return b.String(), nil
}

func fromHex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

func isUnreserved(c byte) bool {
	return (c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') ||
		c == '-' || c == '.' || c == '_' || c == '~'
}
