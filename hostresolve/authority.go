package hostresolve

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// parseAuthority parses a single authority value of the form host[:port],
// where host is an IP literal (including bracketed IPv6) or a DNS reg-name.
//
// It rejects values that contain userinfo ("user@host"), path, query or
// fragment components, whitespace, control characters, or a non-numeric /
// out-of-range port. The returned key is canonical for use as both a routing
// key and a cache key: DNS names are lower-cased and IP literals are rendered
// in their canonical form, with a non-empty port reattached via
// net.JoinHostPort so IPv6 keys are unambiguous.
func parseAuthority(value string) (key string, err error) {
	if value == "" {
		return "", fmt.Errorf("%w: empty authority", ErrInvalidHost)
	}
	for _, r := range value {
		if r <= 0x20 || r == 0x7f {
			return "", fmt.Errorf("%w: whitespace or control character in %q", ErrInvalidHost, value)
		}
	}

	host := value
	var port string
	if strings.HasPrefix(value, "[") {
		// Bracketed IP-literal, optionally followed by :port.
		end := strings.IndexByte(value, ']')
		if end < 0 {
			return "", fmt.Errorf("%w: unterminated IP literal in %q", ErrInvalidHost, value)
		}
		host = value[1:end]
		rest := value[end+1:]
		if rest != "" {
			if rest[0] != ':' {
				return "", fmt.Errorf("%w: unexpected text after IP literal in %q", ErrInvalidHost, value)
			}
			port = rest[1:]
		}
	} else {
		// Any slash, question mark or fragment marker makes this something
		// other than a bare authority; an @ sign means userinfo was supplied.
		if strings.ContainsAny(value, "/?#@") {
			return "", fmt.Errorf("%w: userinfo or path characters in %q", ErrInvalidHost, value)
		}
		if idx := strings.LastIndexByte(value, ':'); idx >= 0 {
			host = value[:idx]
			port = value[idx+1:]
			// More than one colon outside brackets is not a valid host:port.
			if strings.Contains(host, ":") {
				return "", fmt.Errorf("%w: unbracketed IPv6 literal in %q", ErrInvalidHost, value)
			}
		}
	}

	if host == "" {
		return "", fmt.Errorf("%w: empty host in %q", ErrInvalidHost, value)
	}
	if strings.Contains(host, "@") {
		return "", fmt.Errorf("%w: userinfo in %q", ErrInvalidHost, value)
	}

	canonicalHost, err := canonicalHost(host)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidHost, err)
	}
	if port != "" {
		p, err := strconv.Atoi(port)
		if err != nil || p < 0 || p > 65535 || strconv.Itoa(p) != port {
			return "", fmt.Errorf("%w: invalid port %q", ErrInvalidHost, port)
		}
		return net.JoinHostPort(canonicalHost, port), nil
	}
	return canonicalHost, nil
}

// canonicalHost validates host as either an IP literal or an RFC 3986
// reg-name and returns a stable, lower-cased representation. Percent-encoded
// names are not accepted at an HTTP entry point: they are a normalization
// ambiguity that would let one tenant craft another tenant's key.
func canonicalHost(host string) (string, error) {
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.String(), nil
	}
	// Fall through to the RFC 3986 reg-name grammar for non-IP hosts.
	if !isValidRegName(host) {
		return "", fmt.Errorf("not an IP literal or valid DNS name: %q", host)
	}
	return strings.ToLower(host), nil
}

// isValidRegName reports whether host matches the RFC 3986 reg-name grammar
// (unreserved / pct-encoded / sub-delims), with percent encoding refused.
func isValidRegName(host string) bool {
	if host == "" {
		return false
	}
	if host[0] == '.' || strings.Contains(host, "..") {
		return false
	}
	for i := 0; i < len(host); i++ {
		c := host[i]
		switch {
		case c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9',
			c == '-', c == '.', c == '_', c == '~', c == '!', c == '$',
			c == '&', c == '\'', c == '(', c == ')', c == '*', c == '+',
			c == ',', c == ';', c == '=':
			continue
		default:
			return false
		}
	}
	// A trailing dot is legal in DNS but is rejected rather than trimmed:
	// trimming would collapse "example.com." onto the "example.com" key and
	// let one tenant address another tenant's cache partition.
	if strings.HasSuffix(host, ".") {
		return false
	}
	return true
}
