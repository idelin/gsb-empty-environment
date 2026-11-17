package netsec

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// ErrInvalidOrigin reports an origin that cannot safely identify a credential
// scope.
var ErrInvalidOrigin = errors.New("netsec: invalid origin")

// Origin is an HTTP origin after normalization: lowercase scheme and host,
// canonical IP spelling, no userinfo, and an explicit effective port. Comparing
// Origin values therefore distinguishes different ports and schemes.
//
// A zero Port in a caller-created Origin means the scheme's default port. Use
// ParseOrigin when starting from a URL string.
type Origin struct {
	Scheme string
	Host   string
	Port   int
}

// ParseOrigin parses rawURL into a normalized Origin. Userinfo is rejected:
// credentials belong in caller configuration, never in a source URL.
func ParseOrigin(rawURL string) (Origin, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return Origin{}, fmt.Errorf("%w: %v", ErrInvalidOrigin, err)
	}
	if u.User != nil {
		return Origin{}, fmt.Errorf("%w: userinfo is not allowed", ErrInvalidOrigin)
	}
	return OriginFromURL(u)
}

// NormalizeOrigin is an alias for ParseOrigin.
func NormalizeOrigin(rawURL string) (Origin, error) {
	return ParseOrigin(rawURL)
}

// MustParseOrigin is like ParseOrigin but panics on invalid input.
func MustParseOrigin(rawURL string) Origin {
	o, err := ParseOrigin(rawURL)
	if err != nil {
		panic(err)
	}
	return o
}

// OriginFromURL derives the normalized origin of u. Userinfo is ignored;
// callers that build requests should clear it before sending.
func OriginFromURL(u *url.URL) (Origin, error) {
	if u == nil {
		return Origin{}, fmt.Errorf("%w: nil URL", ErrInvalidOrigin)
	}
	if u.Opaque != "" {
		return Origin{}, fmt.Errorf("%w: opaque URL", ErrInvalidOrigin)
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return Origin{}, fmt.Errorf("%w: unsupported scheme %q", ErrInvalidOrigin, u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return Origin{}, fmt.Errorf("%w: missing host", ErrInvalidOrigin)
	}
	if hasEmptyPort(u.Host) {
		return Origin{}, fmt.Errorf("%w: empty port", ErrInvalidOrigin)
	}

	port := 0
	if rawPort := u.Port(); rawPort != "" {
		n, err := strconv.Atoi(rawPort)
		if err != nil || n < 1 || n > 65535 {
			return Origin{}, fmt.Errorf("%w: invalid port %q", ErrInvalidOrigin, rawPort)
		}
		port = n
	}

	return Origin{Scheme: scheme, Host: host, Port: port}.Canonical()
}

// Canonical validates o and returns its normalized form.
func (o Origin) Canonical() (Origin, error) {
	scheme := strings.ToLower(strings.TrimSpace(o.Scheme))
	if scheme != "http" && scheme != "https" {
		return Origin{}, fmt.Errorf("%w: unsupported scheme %q", ErrInvalidOrigin, o.Scheme)
	}

	host, err := canonicalHost(o.Host)
	if err != nil {
		return Origin{}, err
	}

	port := o.Port
	if port == 0 {
		port = defaultPort(scheme)
	}
	if port < 1 || port > 65535 {
		return Origin{}, fmt.Errorf("%w: invalid port %d", ErrInvalidOrigin, o.Port)
	}

	return Origin{Scheme: scheme, Host: host, Port: port}, nil
}

// IsZero reports whether o is the zero Origin.
func (o Origin) IsZero() bool {
	return o.Scheme == "" && o.Host == "" && o.Port == 0
}

// Equal reports whether o and other identify the same normalized origin.
func (o Origin) Equal(other Origin) bool {
	a, errA := o.Canonical()
	b, errB := other.Canonical()
	return errA == nil && errB == nil && a == b
}

// String returns the canonical scheme://host[:port] form.
func (o Origin) String() string {
	if o.IsZero() {
		return ""
	}
	c, err := o.Canonical()
	if err != nil {
		return fmt.Sprintf("%s://%s:%d", o.Scheme, o.Host, o.Port)
	}

	host := c.Host
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if c.Port == defaultPort(c.Scheme) {
		return c.Scheme + "://" + host
	}
	return c.Scheme + "://" + net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

func defaultPort(scheme string) int {
	if scheme == "https" {
		return 443
	}
	return 80
}

func hasEmptyPort(host string) bool {
	return strings.HasSuffix(host, ":")
}

func canonicalHost(raw string) (string, error) {
	host := strings.TrimSpace(raw)
	if len(host) >= 2 && host[0] == '[' && host[len(host)-1] == ']' {
		host = host[1 : len(host)-1]
	}
	if host == "" {
		return "", fmt.Errorf("%w: missing host", ErrInvalidOrigin)
	}
	for i := 0; i < len(host); i++ {
		if host[i] <= ' ' || host[i] == 0x7f {
			return "", fmt.Errorf("%w: invalid host %q", ErrInvalidOrigin, raw)
		}
	}
	if strings.ContainsAny(host, " /?#@\\") {
		return "", fmt.Errorf("%w: invalid host %q", ErrInvalidOrigin, raw)
	}
	if strings.Contains(host, "%") {
		addr, err := netip.ParseAddr(host)
		if err != nil {
			return "", fmt.Errorf("%w: invalid host %q", ErrInvalidOrigin, raw)
		}
		return addr.Unmap().String(), nil
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.Unmap().String(), nil
	}
	if strings.Contains(host, ":") {
		return "", fmt.Errorf("%w: invalid IPv6 address %q", ErrInvalidOrigin, raw)
	}

	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return "", fmt.Errorf("%w: missing host", ErrInvalidOrigin)
	}
	if strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") || strings.Contains(host, "..") {
		return "", fmt.Errorf("%w: invalid host %q", ErrInvalidOrigin, raw)
	}
	return host, nil
}
