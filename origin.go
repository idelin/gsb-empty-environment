package redirectplan

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

var (
	// ErrInvalidOrigin reports a URL that cannot be used as an HTTP origin.
	ErrInvalidOrigin = errors.New("redirectplan: invalid origin")
	// ErrUnsupportedScheme reports a redirect to a non-HTTP(S) URL.
	ErrUnsupportedScheme = errors.New("redirectplan: unsupported URL scheme")
)

// Origin is the security boundary used to select credentials.  Scheme, host,
// and effective port all participate in equality.  Host is lower-case and
// canonicalized; IP literals use their canonical textual form.  Port is the
// effective port, including the scheme default when the URL omits it.
type Origin struct {
	Scheme string
	Host   string
	Port   string
}

// IsZero reports whether the origin is unset.
func (o Origin) IsZero() bool {
	return o.Scheme == "" && o.Host == "" && o.Port == ""
}

// String returns the canonical origin, omitting a default port.
func (o Origin) String() string {
	if o.IsZero() {
		return ""
	}
	return o.Scheme + "://" + formatAuthority(o.Host, o.Port, o.Scheme)
}

// ParseOrigin parses rawURL and returns its canonical origin.  URL user
// information is deliberately ignored because it is not part of an origin.
func ParseOrigin(rawURL string) (Origin, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return Origin{}, fmt.Errorf("%w: %w", ErrInvalidOrigin, err)
	}
	_, origin, err := normalizeURL(u)
	return origin, err
}

// NormalizeOrigin returns the canonical origin of u.
func NormalizeOrigin(u *url.URL) (Origin, error) {
	_, origin, err := normalizeURL(u)
	return origin, err
}

// ParseTarget parses and normalizes a redirect target, retaining path and
// query while discarding user information.
func ParseTarget(rawURL string) (*url.URL, Origin, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, Origin{}, fmt.Errorf("%w: %w", ErrInvalidOrigin, err)
	}
	return normalizeURL(u)
}

func normalizeURL(u *url.URL) (*url.URL, Origin, error) {
	if u == nil {
		return nil, Origin{}, fmt.Errorf("%w: nil URL", ErrInvalidOrigin)
	}
	if u.Opaque != "" {
		return nil, Origin{}, fmt.Errorf("%w: opaque URL", ErrInvalidOrigin)
	}

	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	switch scheme {
	case "http", "https":
	default:
		return nil, Origin{}, fmt.Errorf("%w: %q", ErrUnsupportedScheme, u.Scheme)
	}

	host := strings.ToLower(u.Hostname())
	if host == "" {
		return nil, Origin{}, fmt.Errorf("%w: empty host", ErrInvalidOrigin)
	}
	if strings.ContainsAny(host, " \t\r\n/\\") {
		return nil, Origin{}, fmt.Errorf("%w: invalid host %q", ErrInvalidOrigin, host)
	}
	host = canonicalHost(host)

	port, err := canonicalPort(scheme, u.Port())
	if err != nil {
		return nil, Origin{}, err
	}

	canonical := *u
	canonical.Scheme = scheme
	canonical.Host = formatAuthority(host, port, scheme)
	// User information is never an origin component and must never be
	// converted into an Authorization header by net/http.
	canonical.User = nil

	return &canonical, Origin{Scheme: scheme, Host: host, Port: port}, nil
}

func canonicalHost(host string) string {
	host = strings.TrimSuffix(host, ".")
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.String()
	}
	return host
}

func canonicalPort(scheme, port string) (string, error) {
	if port == "" {
		return defaultPort(scheme), nil
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("%w: invalid port %q", ErrInvalidOrigin, port)
	}
	return strconv.Itoa(n), nil
}

func defaultPort(scheme string) string {
	switch scheme {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

func formatAuthority(host, port, scheme string) string {
	if host == "" {
		return ""
	}
	// Hostname() unescapes an IPv6 zone identifier.  A URL authority needs
	// the percent sign escaped again.
	host = strings.ReplaceAll(host, "%", "%25")
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port == "" || port == defaultPort(scheme) {
		return host
	}
	return net.JoinHostPort(strings.Trim(host, "[]"), port)
}
