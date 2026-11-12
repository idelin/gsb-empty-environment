package netsec

import (
	"errors"
	"net"
	"strings"
)

// ErrInvalidOrigin is returned when an authorization origin cannot be parsed
// into the strict "scheme://host[:port]" form, or when a promised request
// targets an origin that cannot be canonicalized for comparison.
var ErrInvalidOrigin = errors.New("netsec: invalid origin")

// Origin is a canonical RFC 6454 origin: a scheme together with a host and an
// explicit, normalized port. The zero value is invalid (see Origin.Valid).
//
// Origins are comparable, so they may be used as map keys and compared with
// the == operator. The Origin type intentionally carries no path, query, or
// userinfo component: those are never part of an origin and therefore never
// part of an authorization decision.
type Origin struct {
	scheme string
	host   string
	port   int
}

// ParseOrigin parses a serialized origin of the form "scheme://host[:port]".
// It is stricter than generic URL parsing: paths, query strings, fragments,
// and userinfo are rejected, because including them would let a caller smuggle
// authority beyond the origin boundary.
func ParseOrigin(raw string) (Origin, error) {
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok {
		return Origin{}, ErrInvalidOrigin
	}
	if scheme == "" || !isScheme(scheme) {
		return Origin{}, ErrInvalidOrigin
	}
	if rest == "" {
		return Origin{}, ErrInvalidOrigin
	}
	if strings.ContainsAny(rest, "/?#") || strings.Contains(rest, "@") {
		return Origin{}, ErrInvalidOrigin
	}
	return canonicalOrigin(scheme, rest)
}

// Valid reports whether o was produced from a well-formed origin.
func (o Origin) Valid() bool {
	return o.scheme != "" && o.host != "" && o.port != 0
}

// Scheme returns the lower-cased scheme.
func (o Origin) Scheme() string { return o.scheme }

// Host returns the normalized host (lower-cased, without brackets or port).
func (o Origin) Host() string { return o.host }

// Port returns the explicit, normalized port number (including the scheme
// default port when none was supplied at parse time).
func (o Origin) Port() int { return o.port }

// String renders the canonical "scheme://host[:port]" form, adding brackets
// around IPv6 literals.
func (o Origin) String() string {
	if !o.Valid() {
		return ""
	}
	host := o.host
	if strings.Count(host, ":") >= 2 {
		host = "[" + host + "]"
	}
	var b strings.Builder
	b.WriteString(o.scheme)
	b.WriteString("://")
	b.WriteString(host)
	b.WriteByte(':')
	b.WriteString(itoa(o.port))
	return b.String()
}

// canonicalOrigin turns a scheme and an authority ("host[:port]") into a
// canonical Origin. It is shared by ParseOrigin and by promise validation so
// that authorization comparisons always use identical normalization.
func canonicalOrigin(scheme, authority string) (Origin, error) {
	host, port, err := splitAuthority(authority)
	if err != nil {
		return Origin{}, err
	}
	scheme = strings.ToLower(scheme)
	switch scheme {
	case "http":
		if port == 0 {
			port = 80
		}
	case "https":
		if port == 0 {
			port = 443
		}
	}
	// For any other scheme the well-known default port is unknown, so an
	// explicit port must have been present in the authority.
	if port == 0 && scheme != "http" && scheme != "https" {
		return Origin{}, ErrInvalidOrigin
	}
	return Origin{scheme: scheme, host: host, port: port}, nil
}

// splitAuthority splits an HTTP authority into a host and a port. A port is
// optional, but if present it must be a decimal value in 1..65535. Host
// validation deliberately rejects whitespace, control characters, and the
// URI delimiters that could confuse an upstream parser.
func splitAuthority(authority string) (string, int, error) {
	if authority == "" {
		return "", 0, ErrInvalidOrigin
	}
	hostPart := authority
	portPart := ""
	if strings.HasPrefix(authority, "[") {
		closeBracket := strings.Index(authority, "]")
		if closeBracket < 0 {
			return "", 0, ErrInvalidOrigin
		}
		hostPart = authority[1:closeBracket]
		rest := authority[closeBracket+1:]
		if rest != "" {
			if rest[0] != ':' || strings.Contains(rest[1:], ":") {
				return "", 0, ErrInvalidOrigin
			}
			portPart = rest[1:]
		}
	} else if at := strings.LastIndex(authority, ":"); at >= 0 {
		if strings.Contains(authority[at+1:], ":") {
			return "", 0, ErrInvalidOrigin
		}
		hostPart = authority[:at]
		portPart = authority[at+1:]
	}
	if !validHost(hostPart) {
		return "", 0, ErrInvalidOrigin
	}
	port := 0
	if portPart != "" {
		parsed, ok := parsePort(portPart)
		if !ok {
			return "", 0, ErrInvalidOrigin
		}
		port = parsed
	}
	return strings.ToLower(hostPart), port, nil
}

func validHost(host string) bool {
	if host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return true
	}
	for i := 0; i < len(host); i++ {
		c := host[i]
		switch {
		case c <= 0x20 || c == 0x7f:
			return false
		case c == ' ' || c == '\t' || c == '@' || c == '/' || c == '\\' ||
			c == '?' || c == '#' || c == '[' || c == ']' || c == '"' || c == '<' ||
			c == '>' || c == '^' || c == '`' || c == '{' || c == '}' || c == '|':
			return false
		}
	}
	return true
}

func parsePort(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
		if n > 65535 {
			return 0, false
		}
	}
	return n, n > 0
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
