package netsec

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// endpoint is a parsed and normalized host endpoint.
type endpoint struct {
	raw    string
	scheme string
	// host holds the normalized host:port authority, always with an
	// explicit default port.
	host string
	// name is the normalized bare host name (lower-cased, trailing dot
	// removed), without port.
	name string
	port string
}

// parseEndpoint parses a server or host URL and normalizes its authority so
// that case differences, an absent explicit port and a trailing DNS dot do
// not affect same-domain comparison.
func parseEndpoint(raw string) (*endpoint, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("empty endpoint")
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("parse %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("parse %q: unsupported scheme %q", raw, u.Scheme)
	}
	name := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if name == "" {
		return nil, fmt.Errorf("parse %q: missing host", raw)
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "http" {
			port = "80"
		} else {
			port = "443"
		}
	}
	host := net.JoinHostPort(name, port)
	return &endpoint{
		raw:    raw,
		scheme: u.Scheme,
		host:   host,
		name:   name,
		port:   port,
	}, nil
}

// sameDomain reports whether two endpoints belong to the same domain, using
// normalized host name and port (case, default port and trailing dot are
// folded away).
func sameDomain(a, b *endpoint) bool {
	return a.host == b.host
}

// isLoopback reports whether the endpoint addresses the local host, either
// via the "localhost" name (including *.localhost) or a loopback IP.
func (e *endpoint) isLoopback() bool {
	if e.name == "localhost" || strings.HasSuffix(e.name, ".localhost") {
		return true
	}
	if ip := net.ParseIP(e.name); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// canonicalKey normalizes a policy host reference (host or host:port) into
// the same host:port form used by endpoints.
func canonicalKey(raw string) (string, error) {
	e, err := parseEndpoint(raw)
	if err != nil {
		return "", err
	}
	return e.host, nil
}
