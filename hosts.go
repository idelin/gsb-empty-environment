package netsec

import (
	"fmt"
	"net"
	"net/textproto"
	"net/url"
	"strings"
)

// endpoint is a parsed and normalized host endpoint.
type endpoint struct {
	url        *url.URL
	hostname   string
	normalized string
}

// parseEndpoint parses an absolute http(s) URL, rejecting userinfo and
// missing hosts, and produces the normalized host:port identity used for
// same-domain comparisons.
func parseEndpoint(raw string) (*endpoint, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %q: %v", ErrInvalidEndpoint, raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("%w: %q", ErrInvalidEndpoint, raw)
	}
	if u.Host == "" || u.Hostname() == "" {
		return nil, fmt.Errorf("%w: %q", ErrInvalidEndpoint, raw)
	}
	if u.User != nil {
		return nil, fmt.Errorf("%w: %q", ErrUserInfoNotAllowed, raw)
	}

	// Normalization: lower-case, strip a trailing root dot, and fill in
	// the scheme default port so equivalent spellings compare equal.
	hostname := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return &endpoint{
		url:        u,
		hostname:   hostname,
		normalized: net.JoinHostPort(hostname, port),
	}, nil
}

// IsLoopback reports whether host identifies a loopback endpoint. Host
// may be a bare hostname or IP; case and a trailing dot are normalized
// before the check.
func IsLoopback(host string) bool {
	name := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if name == "localhost" {
		return true
	}
	if ip := net.ParseIP(name); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// SameDomain reports whether two endpoint URLs refer to the same host
// identity after hostname normalization (case, trailing dot, default
// port). Paths and query strings are irrelevant.
func SameDomain(a, b string) (bool, error) {
	ea, err := parseEndpoint(a)
	if err != nil {
		return false, err
	}
	eb, err := parseEndpoint(b)
	if err != nil {
		return false, err
	}
	return ea.normalized == eb.normalized, nil
}

// checkScheme enforces https everywhere except policy-allowed loopback
// http endpoints.
func checkScheme(ep *endpoint, policy Policy) error {
	if ep.url.Scheme == "https" {
		return nil
	}
	if policy.AllowLoopbackHTTP && IsLoopback(ep.hostname) {
		return nil
	}
	return ErrPlaintextNotAllowed
}

// hasAuthorization reports whether the header table carries a non-empty
// Authorization header, matching header-name case-insensitively.
func hasAuthorization(header map[string]string) bool {
	for key, value := range header {
		if textproto.CanonicalMIMEHeaderKey(key) == "Authorization" && value != "" {
			return true
		}
	}
	return false
}

// HasCapability reports whether the host advertises want.
func (h *HostEntry) HasCapability(want Capability) bool {
	for _, c := range h.Capabilities {
		if c == want {
			return true
		}
	}
	return false
}

// IsResolveOnly reports whether the host is restricted to the resolve
// capability and therefore must never serve blob pulls. A host without
// an explicit capabilities list gets containerd's default of all
// capabilities and is not resolve-only.
func (h *HostEntry) IsResolveOnly() bool {
	if len(h.Capabilities) == 0 {
		return false
	}
	hasResolve := false
	for _, c := range h.Capabilities {
		switch c {
		case CapabilityResolve:
			hasResolve = true
		case CapabilityPull, CapabilityPush:
			return false
		}
	}
	return hasResolve
}

// HostsForPull validates cfg and returns the ordered subset of hosts
// that are allowed to serve pulls. Resolve-only mirrors are excluded so
// a pull request never reaches a host that may only resolve names.
func HostsForPull(cfg *Config, policy Policy) ([]HostEntry, error) {
	if err := Validate(cfg, policy); err != nil {
		return nil, err
	}
	var pullHosts []HostEntry
	for _, host := range cfg.Hosts {
		if len(host.Capabilities) == 0 || host.HasCapability(CapabilityPull) {
			pullHosts = append(pullHosts, host)
		}
	}
	if len(pullHosts) == 0 {
		return nil, ErrNoPullHost
	}
	return pullHosts, nil
}
