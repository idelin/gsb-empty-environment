package netsec

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

var (
	// ErrInvalidIssuer is returned when a credential issuer does not identify a
	// host that can be compared with a status list URI.
	ErrInvalidIssuer = errors.New("netsec: invalid credential issuer")

	// ErrInvalidStatusListURI is returned when a status list URI is not an
	// absolute URI with a host.
	ErrInvalidStatusListURI = errors.New("netsec: invalid status list URI")

	// ErrStatusListHostMismatch is returned when a status list is hosted on a
	// different host than the credential issuer. Such a list must not be used
	// to make a revocation decision.
	ErrStatusListHostMismatch = errors.New("netsec: status list host does not match credential issuer host")
)

// IssuerHost returns the canonical host of a credential issuer.
//
// The issuer may be an absolute URI (for example https://issuer.example) or a
// did:web identifier (for example did:web:issuer.example). A bare host name is
// also accepted for callers that already extracted the issuer host.
func IssuerHost(issuer string) (string, error) {
	host, err := issuerHost(issuer)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidIssuer, err)
	}
	return host, nil
}

// StatusListHost returns the canonical host of a status list URI.
func StatusListHost(statusListURI string) (string, error) {
	u, err := parseAbsoluteURI(statusListURI)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidStatusListURI, err)
	}
	host, err := canonicalHost(u.Hostname())
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidStatusListURI, err)
	}
	return host, nil
}

// SameHost reports whether the status list URI has the same host as the
// credential issuer.
//
// Host comparison is case-insensitive. Ports, schemes, paths, queries, and
// fragments are not part of the host and are deliberately ignored. The
// comparison is conservative: malformed identifiers return false.
func SameHost(issuer, statusListURI string) bool {
	issuerHost, err := IssuerHost(issuer)
	if err != nil {
		return false
	}
	listHost, err := StatusListHost(statusListURI)
	if err != nil {
		return false
	}
	return issuerHost == listHost
}

// StatusListURIAllowed reports whether a status list URI may be used for a
// credential issued by issuer.
func StatusListURIAllowed(issuer, statusListURI string) bool {
	return SameHost(issuer, statusListURI)
}

// ValidateStatusListURI enforces that the status list URI is served by the
// same host as the credential issuer. A list from any other host is not
// trusted and must not participate in a revocation decision.
func ValidateStatusListURI(issuer, statusListURI string) error {
	issuerHost, err := IssuerHost(issuer)
	if err != nil {
		return err
	}
	listHost, err := StatusListHost(statusListURI)
	if err != nil {
		return err
	}
	if issuerHost != listHost {
		return fmt.Errorf("%w: issuer host %q, status list host %q", ErrStatusListHostMismatch, issuerHost, listHost)
	}
	return nil
}

// ValidateStatusListSource is an alias for ValidateStatusListURI.
func ValidateStatusListSource(issuer, statusListURI string) error {
	return ValidateStatusListURI(issuer, statusListURI)
}

func issuerHost(issuer string) (string, error) {
	issuer = strings.TrimSpace(issuer)
	if issuer == "" {
		return "", errors.New("empty issuer")
	}

	if strings.HasPrefix(strings.ToLower(issuer), "did:") {
		return didWebHost(issuer)
	}
	if strings.Contains(issuer, "://") {
		u, err := parseAbsoluteURI(issuer)
		if err != nil {
			return "", err
		}
		return canonicalHost(u.Hostname())
	}

	// Accept a bare host (optionally with a port) for convenience. Reject
	// values that contain URI delimiters so that an incomplete URL cannot be
	// mistaken for an issuer host.
	if strings.ContainsAny(issuer, "/?#@") {
		return "", fmt.Errorf("issuer %q is not an absolute URI", issuer)
	}
	u, err := url.Parse("//" + issuer)
	if err != nil {
		return "", err
	}
	return canonicalHost(u.Hostname())
}

func didWebHost(did string) (string, error) {
	const method = "did:web:"
	if len(did) < len(method) || !strings.EqualFold(did[:len(method)], method) {
		return "", fmt.Errorf("unsupported DID method in %q", did)
	}

	rest := did[len(method):]
	if rest == "" {
		return "", errors.New("did:web identifier has no host")
	}

	hostPart := rest
	if i := strings.IndexByte(hostPart, ':'); i >= 0 {
		hostPart = hostPart[:i]
	}
	if i := strings.IndexAny(hostPart, "/?#"); i >= 0 {
		return "", fmt.Errorf("invalid did:web identifier %q", did)
	}

	decoded, err := url.PathUnescape(hostPart)
	if err != nil {
		return "", fmt.Errorf("invalid did:web host: %v", err)
	}
	if host, _, err := net.SplitHostPort(decoded); err == nil {
		decoded = host
	}
	return canonicalHost(decoded)
}

func parseAbsoluteURI(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, errors.New("empty URI")
	}
	if raw != strings.TrimSpace(raw) {
		return nil, errors.New("URI contains surrounding whitespace")
	}
	if strings.ContainsAny(raw, "\r\n\t") {
		return nil, errors.New("URI contains control characters")
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "" {
		return nil, errors.New("URI has no scheme")
	}
	if u.Opaque != "" {
		return nil, errors.New("URI is not hierarchical")
	}
	if u.Host == "" || u.Hostname() == "" {
		return nil, errors.New("URI has no host")
	}
	return u, nil
}

func canonicalHost(host string) (string, error) {
	if host != strings.TrimSpace(host) {
		return "", errors.New("host contains surrounding whitespace")
	}
	if strings.ContainsAny(host, "/?#@\\ \t\r\n") {
		return "", errors.New("host contains an invalid delimiter")
	}
	if len(host) >= 2 && host[0] == '[' && host[len(host)-1] == ']' {
		host = host[1 : len(host)-1]
	}
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return "", errors.New("empty host")
	}

	host = strings.ToLower(host)
	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), nil
	}
	return host, nil
}
