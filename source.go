package netsec

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Source is a canonical HTTP origin: scheme plus authority, with the default
// port for the scheme omitted. Source values are immutable once constructed and
// are comparable, so they can be used as map keys.
type Source struct {
	scheme    string
	authority string
}

// NewSource constructs a canonical source. The authority may include an
// explicit port, including a default port, but must not contain user
// information, a path, a query, a fragment, or backslashes.
func NewSource(scheme, authority string) (Source, error) {
	scheme = strings.ToLower(scheme)
	if scheme != "http" && scheme != "https" {
		return Source{}, fmt.Errorf("%w: unsupported scheme %q", ErrInvalidSource, scheme)
	}
	if authority == "" {
		return Source{}, fmt.Errorf("%w: empty authority", ErrInvalidSource)
	}
	if strings.TrimSpace(authority) != authority {
		return Source{}, fmt.Errorf("%w: authority contains outer whitespace", ErrInvalidSource)
	}
	for _, r := range authority {
		if r <= 0x20 || r == 0x7f {
			return Source{}, fmt.Errorf("%w: authority contains whitespace or control characters", ErrInvalidSource)
		}
	}
	if strings.ContainsAny(authority, "/?#@\\") {
		return Source{}, fmt.Errorf("%w: authority contains a delimiter", ErrInvalidSource)
	}
	if strings.Contains(authority, "%") {
		return Source{}, fmt.Errorf("%w: authority contains percent-encoding", ErrInvalidSource)
	}
	if strings.HasSuffix(authority, ":") {
		return Source{}, fmt.Errorf("%w: authority has an empty port", ErrInvalidSource)
	}

	u, err := url.Parse("http://" + authority)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return Source{}, fmt.Errorf("%w: malformed authority %q", ErrInvalidSource, authority)
	}

	host := u.Hostname()
	if host == "" {
		return Source{}, fmt.Errorf("%w: empty host", ErrInvalidSource)
	}
	host = strings.ToLower(host)
	host = strings.TrimSuffix(host, ".")
	if host == "" || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") || strings.Contains(host, "..") {
		return Source{}, fmt.Errorf("%w: malformed host", ErrInvalidSource)
	}
	if strings.Contains(host, ":") {
		ip := net.ParseIP(host)
		if ip == nil {
			return Source{}, fmt.Errorf("%w: malformed IPv6 host", ErrInvalidSource)
		}
		host = ip.String()
	}

	port := u.Port()
	if port != "" {
		for _, r := range port {
			if r < '0' || r > '9' {
				return Source{}, fmt.Errorf("%w: invalid port %q", ErrInvalidSource, port)
			}
		}
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return Source{}, fmt.Errorf("%w: invalid port %q", ErrInvalidSource, port)
		}
		port = strconv.Itoa(n)
	}

	defaultPort := "80"
	if scheme == "https" {
		defaultPort = "443"
	}

	canonicalAuthority := host
	if strings.Contains(host, ":") {
		canonicalAuthority = "[" + host + "]"
	}
	if port != "" && port != defaultPort {
		canonicalAuthority = net.JoinHostPort(host, port)
	}

	return Source{scheme: scheme, authority: canonicalAuthority}, nil
}

// ParseSource parses an absolute origin such as "https://example.test:443".
// A single trailing slash is permitted.
func ParseSource(raw string) (Source, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return Source{}, fmt.Errorf("%w: malformed origin %q", ErrInvalidSource, raw)
	}
	return NewSource(u.Scheme, u.Host)
}

// Scheme returns the canonical, lower-case scheme.
func (s Source) Scheme() string { return s.scheme }

// Authority returns the canonical authority. Default ports are omitted.
func (s Source) Authority() string { return s.authority }

// String returns the canonical origin.
func (s Source) String() string {
	if s.scheme == "" || s.authority == "" {
		return ""
	}
	return s.scheme + "://" + s.authority
}

func (s Source) valid() bool {
	return (s.scheme == "http" || s.scheme == "https") && s.authority != ""
}
