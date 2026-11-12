package netsec

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// Upstream describes one resolver endpoint and its trust anchors.
//
// For DoH and DoT, AuthName is the DNS name (or IP SAN) that the TLS certificate
// must authenticate. It is deliberately independent of Bootstrap and Address:
// bootstrap hints only influence where the connection is opened.
//
// URL is the HTTPS DoH endpoint. Address and Bootstrap are ordered dial hints:
// either may be an IP or a host name, with a port omitted when the transport's
// default port should be used. For DoT and plaintext DNS, Address is the first
// hint. Timeout overrides the selector's default per-upstream timeout.
//
// SPKIPins contains caller-supplied SHA-256 hashes of the SubjectPublicKeyInfo,
// encoded as standard base64. A "sha256/" prefix and hexadecimal encodings are
// also accepted. When set, at least one certificate in the verified chain must
// match one pin.
type Upstream struct {
	Name      string
	Protocol  Protocol
	URL       string
	Address   string
	Bootstrap []string
	AuthName  string
	SPKIPins  []string
	RootCAs   *x509.CertPool
	Timeout   time.Duration
}

// DoHUpstream builds a DoH upstream. The URL's hostname may be resolved through
// bootstrap only as a connection hint; authName remains the certificate name.
func DoHUpstream(name, endpoint, authName string, pins []string, bootstrap ...string) Upstream {
	return Upstream{
		Name:      name,
		Protocol:  DoH,
		URL:       endpoint,
		Bootstrap: bootstrap,
		AuthName:  authName,
		SPKIPins:  pins,
	}
}

// DoTUpstream builds a DoT upstream. Bootstrap entries are dial hints and must
// not be used for certificate verification.
func DoTUpstream(name, authName string, pins []string, bootstrap ...string) Upstream {
	return Upstream{
		Name:      name,
		Protocol:  DoT,
		Bootstrap: bootstrap,
		AuthName:  authName,
		SPKIPins:  pins,
	}
}

// PlainUpstream builds a plaintext DNS upstream.
func PlainUpstream(name, address string) Upstream {
	return Upstream{
		Name:     name,
		Protocol: PlainDNS,
		Address:  address,
	}
}

// SPKIPinForCert returns the base64 SHA-256 SPKI pin for a certificate.
func SPKIPinForCert(cert *x509.Certificate) (string, error) {
	if cert == nil {
		return "", fmt.Errorf("%w: nil certificate", ErrInvalidSPKIPin)
	}
	der, err := x509.MarshalPKIXPublicKey(cert.PublicKey)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidSPKIPin, err)
	}
	sum := sha256.Sum256(der)
	return base64.StdEncoding.EncodeToString(sum[:]), nil
}

type preparedUpstream struct {
	upstream    Upstream
	index       int
	authName    string
	pins        [][]byte
	timeout     time.Duration
	doHURL      *url.URL
	dotTargets  []string
	plainTarget string
	tlsConfig   *tls.Config
}

func prepareUpstream(mode Mode, upstream Upstream, defaultTimeout time.Duration) (preparedUpstream, error) {
	prepared := preparedUpstream{
		upstream: upstream,
		timeout:  upstream.Timeout,
	}
	if prepared.timeout <= 0 {
		prepared.timeout = defaultTimeout
	}
	if upstream.Name == "" {
		prepared.upstream.Name = defaultUpstreamName(upstream)
	}

	switch upstream.Protocol {
	case DoH:
		parsed, err := url.Parse(upstream.URL)
		if err != nil {
			return preparedUpstream{}, fmt.Errorf("netsec: DoH upstream %q: %w", prepared.upstream.Name, err)
		}
		if parsed.Scheme != "https" || parsed.Host == "" {
			return preparedUpstream{}, fmt.Errorf("netsec: DoH upstream %q: %w", prepared.upstream.Name, ErrInsecureDoHURL)
		}
		prepared.doHURL = parsed
		prepared.authName = upstream.AuthName
		if prepared.authName == "" {
			if mode == Strict {
				return preparedUpstream{}, fmt.Errorf("netsec: DoH upstream %q: %w", prepared.upstream.Name, ErrMissingAuthName)
			}
			prepared.authName = parsed.Hostname()
		}
	case DoT:
		prepared.authName = upstream.AuthName
		if prepared.authName == "" {
			return preparedUpstream{}, fmt.Errorf("netsec: DoT upstream %q: %w", prepared.upstream.Name, ErrMissingAuthName)
		}
		hints := upstreamHints(upstream)
		if len(hints) == 0 {
			hints = []string{net.JoinHostPort(prepared.authName, "853")}
		}
		for _, hint := range hints {
			prepared.dotTargets = append(prepared.dotTargets, normalizeAddress(hint, "853"))
		}
	case PlainDNS:
		hints := upstreamHints(upstream)
		if len(hints) == 0 {
			return preparedUpstream{}, fmt.Errorf("netsec: plain DNS upstream %q requires an address", prepared.upstream.Name)
		}
		prepared.plainTarget = normalizeAddress(hints[0], "53")
	default:
		return preparedUpstream{}, fmt.Errorf("netsec: upstream %q has unknown protocol %q", prepared.upstream.Name, upstream.Protocol)
	}

	if upstream.Protocol.secure() {
		if mode == Strict && len(upstream.SPKIPins) == 0 {
			return preparedUpstream{}, fmt.Errorf("netsec: upstream %q: %w", prepared.upstream.Name, ErrMissingSPKIPin)
		}
		for _, pin := range upstream.SPKIPins {
			decoded, err := decodeSPKIPin(pin)
			if err != nil {
				return preparedUpstream{}, fmt.Errorf("netsec: upstream %q: %w", prepared.upstream.Name, err)
			}
			prepared.pins = append(prepared.pins, decoded)
		}
		prepared.tlsConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: prepared.authName,
			RootCAs:    upstream.RootCAs,
		}
		if len(prepared.pins) > 0 {
			prepared.tlsConfig.VerifyConnection = verifySPKI(prepared.pins)
		}
	}

	return prepared, nil
}

func defaultUpstreamName(upstream Upstream) string {
	switch upstream.Protocol {
	case DoH:
		return "doh:" + upstream.URL
	case DoT:
		if len(upstream.Bootstrap) > 0 {
			return "dot:" + upstream.Bootstrap[0]
		}
		return "dot:" + upstream.AuthName
	case PlainDNS:
		if upstream.Address != "" {
			return "plain:" + upstream.Address
		}
		if len(upstream.Bootstrap) > 0 {
			return "plain:" + upstream.Bootstrap[0]
		}
	}
	return string(upstream.Protocol)
}

func upstreamHints(upstream Upstream) []string {
	hints := make([]string, 0, len(upstream.Bootstrap)+1)
	if upstream.Address != "" {
		hints = append(hints, upstream.Address)
	}
	hints = append(hints, upstream.Bootstrap...)
	return hints
}

func normalizeAddress(address, defaultPort string) string {
	address = strings.TrimSpace(address)
	if address == "" {
		return address
	}
	if _, _, err := net.SplitHostPort(address); err == nil {
		return address
	}
	if strings.HasPrefix(address, "[") && strings.HasSuffix(address, "]") {
		address = strings.TrimSuffix(strings.TrimPrefix(address, "["), "]")
	}
	return net.JoinHostPort(address, defaultPort)
}

func decodeSPKIPin(pin string) ([]byte, error) {
	value := strings.TrimSpace(pin)
	if value == "" {
		return nil, fmt.Errorf("%w: empty pin", ErrInvalidSPKIPin)
	}
	if strings.HasPrefix(strings.ToLower(value), "sha256/") {
		value = value[len("sha256/"):]
	}
	if decoded, err := base64.StdEncoding.DecodeString(value); err == nil && len(decoded) == sha256.Size {
		return decoded, nil
	}
	if decoded, err := base64.RawStdEncoding.DecodeString(value); err == nil && len(decoded) == sha256.Size {
		return decoded, nil
	}
	if decoded, err := base64.URLEncoding.DecodeString(value); err == nil && len(decoded) == sha256.Size {
		return decoded, nil
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(value); err == nil && len(decoded) == sha256.Size {
		return decoded, nil
	}
	if decoded, err := hex.DecodeString(value); err == nil && len(decoded) == sha256.Size {
		return decoded, nil
	}
	return nil, fmt.Errorf("%w: %q is not a base64, raw-base64, URL-base64, or hex SHA-256 hash", ErrInvalidSPKIPin, pin)
}

func verifySPKI(pins [][]byte) func(tls.ConnectionState) error {
	return func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) == 0 {
			return fmt.Errorf("%w: no peer certificate", ErrSPKIPinMismatch)
		}
		for _, cert := range state.PeerCertificates {
			der, err := x509.MarshalPKIXPublicKey(cert.PublicKey)
			if err != nil {
				continue
			}
			sum := sha256.Sum256(der)
			for _, pin := range pins {
				if len(pin) == sha256.Size && subtle.ConstantTimeCompare(sum[:], pin) == 1 {
					return nil
				}
			}
		}
		return ErrSPKIPinMismatch
	}
}
