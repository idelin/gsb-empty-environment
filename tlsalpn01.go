package netsec

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"
)

var (
	// ErrTLSALPN01Protocol is returned when acme-tls/1 was not negotiated.
	ErrTLSALPN01Protocol = errors.New("netsec: acme-tls/1 was not negotiated")
	// ErrTLSALPN01Certificate is returned when the challenge certificate does
	// not prove control of the IP identifier.
	ErrTLSALPN01Certificate = errors.New("netsec: invalid tls-alpn-01 certificate")
	// ErrTLSALPN01KeyAuthorization is returned when the acmeIdentifier value
	// does not match the expected key authorization.
	ErrTLSALPN01KeyAuthorization = errors.New("netsec: tls-alpn-01 key authorization mismatch")
)

var acmeIdentifierOID = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 1, 31}

// NewTLSALPN01Config returns a TLS client configuration for an RFC 8738
// tls-alpn-01 validation.
//
// The SNI HostName is the reverse DNS mapping of the IP address, as required
// because RFC 6066 does not allow IP literals in SNI. The challenge certificate
// is self-signed, so normal PKI verification is disabled and replaced by the
// ACME-specific checks in VerifyConnection.
func NewTLSALPN01Config(id Identifier, keyAuthorization string) (*tls.Config, error) {
	if err := id.ValidateChallenge(ChallengeTLSALPN01); err != nil {
		return nil, err
	}
	if keyAuthorization == "" {
		return nil, fmt.Errorf("%w: empty key authorization", ErrTLSALPN01KeyAuthorization)
	}
	serverName, err := id.ReverseDNSName()
	if err != nil {
		return nil, err
	}

	config := &tls.Config{
		ServerName:         serverName,
		NextProtos:         []string{TLSALPN01Protocol},
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, // Replaced by VerifyConnection below.
	}
	config.VerifyConnection = func(state tls.ConnectionState) error {
		return verifyTLSALPN01State(state, id, keyAuthorization)
	}
	return config, nil
}

// TLSALPN01Validator performs a full tls-alpn-01 validation:
//   - connect directly to the identifier address on port 443,
//   - send the reverse DNS name as SNI,
//   - negotiate acme-tls/1,
//   - verify the certificate's single iPAddress SAN, and
//   - verify the critical acmeIdentifier extension against SHA-256(keyAuthorization).
type TLSALPN01Validator struct {
	// DialContext is optional. It is useful for custom networking and tests.
	// The dial address is always the identifier's literal IP and port 443.
	DialContext func(ctx context.Context, network, address string) (net.Conn, error)
	// Timeout defaults to 30 seconds when zero.
	Timeout time.Duration
}

// Validate performs the tls-alpn-01 challenge.
func (v *TLSALPN01Validator) Validate(ctx context.Context, id Identifier, keyAuthorization string) error {
	config, err := NewTLSALPN01Config(id, keyAuthorization)
	if err != nil {
		return err
	}
	addr, err := id.IPAddr()
	if err != nil {
		return err
	}
	address := net.JoinHostPort(addr.String(), tlsALPN01DefaultPort)

	timeout := v.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	dialContext := v.DialContext
	if dialContext == nil {
		dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
		dialContext = dialer.DialContext
	}
	rawConn, err := dialContext(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("netsec: dialing tls-alpn-01 address: %w", err)
	}

	conn := tls.Client(rawConn, config)
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if err := conn.HandshakeContext(ctx); err != nil {
		return fmt.Errorf("netsec: tls-alpn-01 handshake: %w", err)
	}
	return nil
}

// ValidateTLSALPN01 is the package-level convenience form of
// TLSALPN01Validator.Validate.
func ValidateTLSALPN01(ctx context.Context, id Identifier, keyAuthorization string, validators ...*TLSALPN01Validator) error {
	validator := &TLSALPN01Validator{}
	if len(validators) > 0 && validators[0] != nil {
		validator = validators[0]
	}
	return validator.Validate(ctx, id, keyAuthorization)
}

// VerifyTLSALPN01Certificate verifies the parts of a challenge certificate
// that are specific to an IP identifier.
func VerifyTLSALPN01Certificate(cert *x509.Certificate, addr netip.Addr, keyAuthorization string) error {
	if cert == nil {
		return fmt.Errorf("%w: no certificate", ErrTLSALPN01Certificate)
	}
	if !addr.IsValid() || addr.Is4In6() {
		return fmt.Errorf("%w: invalid identifier address", ErrTLSALPN01Certificate)
	}
	if keyAuthorization == "" {
		return fmt.Errorf("%w: empty key authorization", ErrTLSALPN01KeyAuthorization)
	}

	if len(cert.IPAddresses) != 1 || len(cert.DNSNames) != 0 ||
		len(cert.EmailAddresses) != 0 || len(cert.URIs) != 0 {
		return fmt.Errorf("%w: certificate must contain exactly one iPAddress SAN", ErrTLSALPN01Certificate)
	}
	certAddr, err := netip.ParseAddr(cert.IPAddresses[0].String())
	if err != nil || certAddr != addr {
		return fmt.Errorf("%w: SAN address does not match %s", ErrTLSALPN01Certificate, addr)
	}

	for _, extension := range cert.Extensions {
		if !extension.Id.Equal(acmeIdentifierOID) {
			continue
		}
		if !extension.Critical {
			return fmt.Errorf("%w: acmeIdentifier extension is not critical", ErrTLSALPN01Certificate)
		}
		var digest []byte
		rest, err := asn1.Unmarshal(extension.Value, &digest)
		if err != nil {
			return fmt.Errorf("%w: malformed acmeIdentifier extension: %v", ErrTLSALPN01Certificate, err)
		}
		if len(rest) != 0 {
			return fmt.Errorf("%w: trailing data in acmeIdentifier extension", ErrTLSALPN01Certificate)
		}
		want := sha256.Sum256([]byte(keyAuthorization))
		if len(digest) != len(want) || subtle.ConstantTimeCompare(digest, want[:]) != 1 {
			return ErrTLSALPN01KeyAuthorization
		}
		return nil
	}
	return fmt.Errorf("%w: missing acmeIdentifier extension", ErrTLSALPN01Certificate)
}

func verifyTLSALPN01State(state tls.ConnectionState, id Identifier, keyAuthorization string) error {
	if state.NegotiatedProtocol != TLSALPN01Protocol {
		return fmt.Errorf("%w: negotiated %q", ErrTLSALPN01Protocol, state.NegotiatedProtocol)
	}
	if len(state.PeerCertificates) == 0 {
		return fmt.Errorf("%w: server sent no certificate", ErrTLSALPN01Certificate)
	}
	addr, err := id.IPAddr()
	if err != nil {
		return err
	}
	return VerifyTLSALPN01Certificate(state.PeerCertificates[0], addr, keyAuthorization)
}
