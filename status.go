package netsec

import (
	"crypto"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// Certificate status values. The numeric values match the conventional
// values used by OCSP implementations (good=0, revoked=1, unknown=2).
const (
	Good         = 0
	Revoked      = 1
	Unknown      = 2
	ServerFailed = 3
)

// Status aliases make the constants self-describing without changing their
// values.
const (
	StatusGood         = Good
	StatusRevoked      = Revoked
	StatusUnknown      = Unknown
	StatusServerFailed = ServerFailed
)

// Revocation reasons from RFC 5280, section 5.3.1.
const (
	Unspecified          = 0
	KeyCompromise        = 1
	CACompromise         = 2
	AffiliationChanged   = 3
	Superseded           = 4
	CessationOfOperation = 5
	CertificateHold      = 6
	RemoveFromCRL        = 8
	PrivilegeWithdrawn   = 9
	AACompromise         = 10
)

var (
	// ErrConflictingStatus is returned when two SingleResponse entries
	// describe the same certificate with different statuses.
	ErrConflictingStatus = errors.New("ocsp: conflicting certificate status")
	// ErrStatusConflict is a convenience alias for ErrConflictingStatus.
	ErrStatusConflict = ErrConflictingStatus

	// ErrCertificateNotFound means the response has no entry matching the
	// requested certificate and issuer.
	ErrCertificateNotFound = errors.New("ocsp: no SingleResponse for certificate")
	// ErrNoSingleResponse means the response contained no SingleResponse entries.
	ErrNoSingleResponse = errors.New("ocsp: response has no SingleResponse entries")

	// ErrResponseNotSuccessful means the OCSP response status was not successful.
	ErrResponseNotSuccessful = errors.New("ocsp: response status is not successful")
	// ErrMalformedResponse means the response did not match the OCSP ASN.1
	// profile closely enough to be used safely.
	ErrMalformedResponse = errors.New("ocsp: malformed response")
	// ErrUnsupportedResponseType means the response was not a basic OCSP response.
	ErrUnsupportedResponseType = errors.New("ocsp: unsupported response type")

	// ErrInvalidSignature means the BasicOCSPResponse signature did not verify
	// against a trusted responder certificate.
	ErrInvalidSignature = errors.New("ocsp: invalid response signature")
	// ErrUntrustedResponder means the response signer is neither the issuer
	// nor an authorized delegated OCSP responder.
	ErrUntrustedResponder = errors.New("ocsp: untrusted OCSP responder")
	// ErrUnsupportedSignatureAlgorithm means the response signature algorithm
	// is not supported by this package.
	ErrUnsupportedSignatureAlgorithm = errors.New("ocsp: unsupported signature algorithm")
	// ErrUnsupportedHashAlgorithm means a CertID uses a hash algorithm this
	// package cannot compute.
	ErrUnsupportedHashAlgorithm = errors.New("ocsp: unsupported hash algorithm")

	// ErrResponseExpired means thisUpdate/nextUpdate do not show that the
	// response was fresh at the requested verification time.
	ErrResponseExpired = errors.New("ocsp: response is not fresh")
	// ErrInvalidResponseTime means a response timestamp is in the future
	// relative to the verification time and permitted clock skew.
	ErrInvalidResponseTime = errors.New("ocsp: invalid response time")

	// ErrCertificateRevoked is returned by the Validate helpers when the
	// certificate status is revoked. It is not returned by ParseResponse,
	// ParseResponseForCert, VerifyResponse, or CheckStatus, which expose the
	// Revoked status to the caller.
	ErrCertificateRevoked = errors.New("ocsp: certificate is revoked")
	// ErrUnknownStatus is returned by the Validate helpers when the response
	// reports a certificate as unknown.
	ErrUnknownStatus = errors.New("ocsp: certificate status is unknown")
)

// RevokedError describes a revoked certificate. It unwraps to
// ErrCertificateRevoked.
type RevokedError struct {
	SerialNumber     *big.Int
	RevokedAt        time.Time
	RevocationReason int
}

func (e *RevokedError) Error() string {
	if e == nil {
		return ErrCertificateRevoked.Error()
	}
	return fmt.Sprintf("%v: serial %v, revoked at %s", ErrCertificateRevoked, e.SerialNumber, e.RevokedAt.UTC().Format(time.RFC3339))
}

func (e *RevokedError) Unwrap() error { return ErrCertificateRevoked }

// CertID identifies the certificate and issuer covered by a SingleResponse.
type CertID struct {
	HashAlgorithm    crypto.Hash
	HashAlgorithmOID asn1.ObjectIdentifier
	IssuerNameHash   []byte
	IssuerKeyHash    []byte
	SerialNumber     *big.Int
}

// SingleResponse is one entry from BasicOCSPResponse.responseData.responses.
type SingleResponse struct {
	CertID           CertID
	Status           int
	ThisUpdate       time.Time
	NextUpdate       time.Time
	RevokedAt        time.Time
	RevocationReason int
	Extensions       []pkix.Extension
}

// ResponderID is the ResponderID CHOICE from a BasicOCSPResponse.
// Exactly one of ByName or ByKey is populated for a valid response.
type ResponderID struct {
	ByName []byte
	ByKey  []byte
}

// BasicResponse is a parsed and structurally validated BasicOCSPResponse.
type BasicResponse struct {
	ProducedAt   time.Time
	ResponderID  ResponderID
	Responses    []SingleResponse
	Certificates []*x509.Certificate
	Raw          []byte

	tbsDER             []byte
	signatureAlgorithm pkix.AlgorithmIdentifier
	signature          asn1.BitString
}

// Response is the status selected for one certificate. When a response has
// multiple agreeing entries for that certificate, Responses contains all of
// them and Status is the shared status.
type Response struct {
	Status           int
	SerialNumber     *big.Int
	ProducedAt       time.Time
	ThisUpdate       time.Time
	NextUpdate       time.Time
	RevokedAt        time.Time
	RevocationReason int
	Certificate      *x509.Certificate
	Responses        []SingleResponse
}

// StatusString returns a human-readable name for an OCSP status.
func StatusString(status int) string {
	switch status {
	case Good:
		return "good"
	case Revoked:
		return "revoked"
	case Unknown:
		return "unknown"
	case ServerFailed:
		return "server failed"
	default:
		return fmt.Sprintf("status(%d)", status)
	}
}
