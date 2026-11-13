// Package netsec validates TLS Certificate messages for a TLS stack.
//
// The rules enforced here mirror RFC 8446 §4.4.2:
//
//   - When a certificate is required, the certificate list must contain
//     at least one entry; an empty list fails the handshake.
//   - When a client certificate is not required, an empty list is only
//     acceptable if no CertificateRequest was sent.
//   - A non-empty list is accepted when its certification path is
//     acceptable to the configured verifier.
//   - A client that receives a CertificateRequest but has no certificate
//     to offer replies with a Certificate message carrying an empty list.
package netsec

import (
	"errors"
	"fmt"
)

var (
	// ErrCertificateRequired is returned when a certificate is mandatory
	// but the peer sent an empty certificate list.
	ErrCertificateRequired = errors.New("netsec: certificate required but list is empty")

	// ErrEmptyCertificateList is returned when an empty certificate list
	// is not permitted by the handshake context.
	ErrEmptyCertificateList = errors.New("netsec: empty certificate list not allowed")
)

// PathVerifier reports whether a certification path is acceptable.
// rawCerts holds the presented chain in wire order, leaf first.
// A nil error means the path is acceptable.
type PathVerifier func(rawCerts [][]byte) error

// ListPolicy describes the handshake context in which a received
// Certificate message is validated.
type ListPolicy struct {
	// CertificateRequested reports whether a CertificateRequest message
	// was sent to the peer.
	CertificateRequested bool

	// RequireCertificate reports whether the peer must authenticate with
	// a certificate (e.g. the server requires client authentication).
	RequireCertificate bool

	// VerifyPath optionally checks the presented certification path.
	// When nil, any non-empty list is accepted.
	VerifyPath PathVerifier
}

// ValidateCertificateList validates the certificate list of a received
// TLS Certificate message against the handshake policy. It returns nil
// when the handshake may continue.
func ValidateCertificateList(rawCerts [][]byte, policy ListPolicy) error {
	if len(rawCerts) == 0 {
		if policy.RequireCertificate {
			return ErrCertificateRequired
		}
		if policy.CertificateRequested {
			return fmt.Errorf("%w: certificate request was sent", ErrEmptyCertificateList)
		}
		return nil
	}
	if policy.VerifyPath != nil {
		if err := policy.VerifyPath(rawCerts); err != nil {
			return fmt.Errorf("netsec: unacceptable certification path: %w", err)
		}
	}
	return nil
}

// ClientCertificateList builds the certificate list for the client's
// Certificate message.
//
// When no CertificateRequest was received, it returns nil and no
// Certificate message should be sent. When a certificate was requested
// but none is available, it returns an empty, non-nil list: the client
// must still answer with a Certificate message carrying an empty list.
func ClientCertificateList(requested bool, available [][]byte) [][]byte {
	if !requested {
		return nil
	}
	if len(available) == 0 {
		return [][]byte{}
	}
	return available
}
