package netsec

import (
	"fmt"
	"net/url"
	"strings"
)

// OAuthMetadata is the subset of RFC 8414 / RFC 8705 metadata needed for mTLS
// endpoint selection.
type OAuthMetadata struct {
	TokenEndpoint      string            `json:"token_endpoint"`
	MTLEndpointAliases map[string]string `json:"mtls_endpoint_aliases"`
}

// TokenEndpointMTLSAlias returns the metadata's mTLS alias for the token
// endpoint, if one is advertised.
func (m OAuthMetadata) TokenEndpointMTLSAlias() (string, bool) {
	alias, ok := m.MTLEndpointAliases["token_endpoint"]
	return alias, ok && alias != ""
}

// IsMTLSTokenEndpoint reports whether requestURL identifies the token
// endpoint's mtls_endpoint_aliases entry. Query strings and fragments are
// ignored; scheme, host, and path must match.
func (m OAuthMetadata) IsMTLSTokenEndpoint(requestURL string) (bool, error) {
	alias, ok := m.TokenEndpointMTLSAlias()
	if !ok {
		return false, nil
	}
	return sameEndpointURL(requestURL, alias)
}

// TokenEndpointCertificateAuth is the result of certificate authentication at
// the token endpoint.
type TokenEndpointCertificateAuth struct {
	Authenticated bool
	Fingerprint   string
}

// AuthenticateTokenEndpointCertificate authenticates a client by its TLS
// client certificate. Certificate authentication is accepted only when
// requestURL is the metadata token_endpoint value inside
// mtls_endpoint_aliases. A certificate presented at the regular token endpoint
// (or any other endpoint) is rejected.
func AuthenticateTokenEndpointCertificate(meta OAuthMetadata, requestURL string, conn Connection) (TokenEndpointCertificateAuth, error) {
	if !conn.HasClientCertificate() {
		return TokenEndpointCertificateAuth{}, ErrNoClientCertificate
	}

	isAlias, err := meta.IsMTLSTokenEndpoint(requestURL)
	if err != nil {
		return TokenEndpointCertificateAuth{}, err
	}
	if !isAlias {
		return TokenEndpointCertificateAuth{}, fmt.Errorf("%w: request URL %q is not the mTLS token endpoint alias", ErrCertificateAuthNotAllowed, requestURL)
	}

	fingerprint, err := conn.CertificateFingerprint()
	if err != nil {
		return TokenEndpointCertificateAuth{}, err
	}
	return TokenEndpointCertificateAuth{Authenticated: true, Fingerprint: fingerprint}, nil
}

func sameEndpointURL(left, right string) (bool, error) {
	leftURL, err := url.Parse(left)
	if err != nil {
		return false, fmt.Errorf("netsec: invalid request URL %q: %w", left, err)
	}
	rightURL, err := url.Parse(right)
	if err != nil {
		return false, fmt.Errorf("netsec: invalid metadata endpoint URL %q: %w", right, err)
	}
	return strings.EqualFold(leftURL.Scheme, rightURL.Scheme) &&
		strings.EqualFold(leftURL.Host, rightURL.Host) &&
		leftURL.Path == rightURL.Path, nil
}
