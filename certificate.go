package netsec

import (
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
)

const signingDomain = "example.org/netsec/q-021/certificate/v1"

// Certificate is a deliberately small, application-specific delegation
// certificate. It is not an X.509 certificate.
//
// The byte string authenticated by Signature is defined by SigningBytes. The
// signature field itself is not part of that byte string.
type Certificate struct {
	// SubjectPublicKey is the Ed25519 key to which this certificate delegates.
	SubjectPublicKey ed25519.PublicKey

	// IssuerID names the signer. For a certificate issued directly by a
	// trusted root it must equal TrustRoot.ID. Together with Serial it is also
	// the revocation key.
	IssuerID string

	// Serial identifies this issuance. Issuers must not reuse a serial for the
	// same IssuerID.
	Serial uint64

	// AllowedOperations is the set of operations this certificate permits.
	// An empty set permits nothing.
	AllowedOperations []string

	// ResourcePrefixes is the set of slash-separated resource path prefixes
	// this certificate permits. An empty set permits nothing.
	ResourcePrefixes []string

	// MaxDelegationDepth is the number of further delegations allowed below
	// this certificate. Zero makes the certificate a leaf.
	MaxDelegationDepth uint32

	// NotBefore and NotAfter define the half-open validity interval
	// [NotBefore, NotAfter).
	NotBefore time.Time
	NotAfter  time.Time

	// Signature is an Ed25519 signature over SigningBytes.
	Signature []byte
}

// TrustRoot binds an issuer ID to a public key trusted by the host.
type TrustRoot struct {
	ID        string
	PublicKey ed25519.PublicKey
}

// Revocation identifies one issuance. Revoking a serial only affects the
// issuer identified by IssuerID.
type Revocation struct {
	IssuerID string
	Serial   uint64
}

// Request describes one concrete operation on one resource path.
type Request struct {
	Operation    string
	ResourcePath string

	// SubjectPublicKey optionally fixes the leaf of the returned chain. When
	// set, the last certificate in the chain must have this subject key.
	// Leaving it empty accepts any certificate that supports the request.
	SubjectPublicKey ed25519.PublicKey
}

var (
	// ErrTooManyCertificates is returned when a verification input exceeds
	// the hard 32-certificate limit.
	ErrTooManyCertificates = errors.New("netsec: too many certificates")

	// ErrInvalidCertificate reports malformed certificate fields.
	ErrInvalidCertificate = errors.New("netsec: invalid certificate")

	// ErrInvalidSignature reports a signature that does not authenticate the
	// certificate under the expected issuer key.
	ErrInvalidSignature = errors.New("netsec: invalid signature")

	// ErrInvalidTrustRoot reports malformed or empty trust-root configuration.
	ErrInvalidTrustRoot = errors.New("netsec: invalid trust root")

	// ErrInvalidRequest reports a malformed verification request.
	ErrInvalidRequest = errors.New("netsec: invalid request")

	// ErrNoTrustedPath reports that no single trusted, valid chain supports
	// the request.
	ErrNoTrustedPath = errors.New("netsec: no trusted path")
)

type normalizedCertificate struct {
	subjectPublicKey ed25519.PublicKey
	issuerID         string
	serial           uint64
	operations       []string
	prefixes         []string
	maxDepth         uint32
	notBefore        time.Time
	notAfter         time.Time
}

// SigningBytes returns the canonical, length-prefixed byte string covered by
// Certificate.Signature. Signature is intentionally not included.
//
// Set-valued fields are sorted and deduplicated for signing. Resource paths
// are normalized by requiring an absolute, already-clean path; only a
// trailing slash is accepted and removed.
func (c Certificate) SigningBytes() ([]byte, error) {
	normalized, err := normalizeCertificate(c, false)
	if err != nil {
		return nil, err
	}
	return normalized.signingBytes()
}

// SignCertificate signs cert with issuerPrivateKey and stores the signature in
// cert.Signature. The private key is the issuer's key; it need not correspond
// to cert.SubjectPublicKey.
func SignCertificate(cert *Certificate, issuerPrivateKey ed25519.PrivateKey) error {
	if cert == nil {
		return fmt.Errorf("%w: nil certificate", ErrInvalidCertificate)
	}
	if len(issuerPrivateKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("%w: issuer Ed25519 private key must be %d bytes", ErrInvalidSignature, ed25519.PrivateKeySize)
	}
	message, err := cert.SigningBytes()
	if err != nil {
		return err
	}
	cert.Signature = ed25519.Sign(issuerPrivateKey, message)
	return nil
}

// Sign is a short alias for SignCertificate.
func Sign(cert *Certificate, issuerPrivateKey ed25519.PrivateKey) error {
	return SignCertificate(cert, issuerPrivateKey)
}

// VerifyCertificateSignature checks cert.Signature using publicKey as the
// issuer key.
func VerifyCertificateSignature(cert Certificate, publicKey ed25519.PublicKey) error {
	normalized, err := normalizeCertificate(cert, true)
	if err != nil {
		return err
	}
	message, err := normalized.signingBytes()
	if err != nil {
		return err
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: issuer Ed25519 public key must be %d bytes", ErrInvalidSignature, ed25519.PublicKeySize)
	}
	if !ed25519.Verify(publicKey, message, cert.Signature) {
		return ErrInvalidSignature
	}
	return nil
}

func normalizeCertificate(c Certificate, requireSignature bool) (normalizedCertificate, error) {
	if len(c.SubjectPublicKey) != ed25519.PublicKeySize {
		return normalizedCertificate{}, fmt.Errorf("%w: subject Ed25519 public key must be %d bytes", ErrInvalidCertificate, ed25519.PublicKeySize)
	}
	if c.IssuerID == "" {
		return normalizedCertificate{}, fmt.Errorf("%w: issuer ID is empty", ErrInvalidCertificate)
	}
	if !c.NotBefore.Before(c.NotAfter) {
		return normalizedCertificate{}, fmt.Errorf("%w: validity interval must be non-empty", ErrInvalidCertificate)
	}
	if requireSignature && len(c.Signature) != ed25519.SignatureSize {
		return normalizedCertificate{}, fmt.Errorf("%w: Ed25519 signature must be %d bytes", ErrInvalidSignature, ed25519.SignatureSize)
	}

	operations, err := normalizeSet(c.AllowedOperations, func(value string) (string, error) {
		if value == "" {
			return "", errors.New("operation is empty")
		}
		return value, nil
	})
	if err != nil {
		return normalizedCertificate{}, fmt.Errorf("%w: %v", ErrInvalidCertificate, err)
	}

	prefixes, err := normalizeSet(c.ResourcePrefixes, CanonicalResourcePath)
	if err != nil {
		return normalizedCertificate{}, fmt.Errorf("%w: %v", ErrInvalidCertificate, err)
	}

	subject := append(ed25519.PublicKey(nil), c.SubjectPublicKey...)
	return normalizedCertificate{
		subjectPublicKey: subject,
		issuerID:         c.IssuerID,
		serial:           c.Serial,
		operations:       operations,
		prefixes:         prefixes,
		maxDepth:         c.MaxDelegationDepth,
		notBefore:        c.NotBefore,
		notAfter:         c.NotAfter,
	}, nil
}

func normalizeSet(values []string, normalize func(string) (string, error)) ([]string, error) {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value, err := normalize(value)
		if err != nil {
			return nil, err
		}
		normalized = append(normalized, value)
	}
	sort.Strings(normalized)

	write := 0
	for _, value := range normalized {
		if write > 0 && normalized[write-1] == value {
			continue
		}
		normalized[write] = value
		write++
	}
	return normalized[:write], nil
}

func (c normalizedCertificate) signingBytes() ([]byte, error) {
	var message []byte
	var err error

	if message, err = appendField(message, []byte(signingDomain)); err != nil {
		return nil, err
	}
	if message, err = appendField(message, c.subjectPublicKey); err != nil {
		return nil, err
	}
	if message, err = appendField(message, []byte(c.issuerID)); err != nil {
		return nil, err
	}

	var serial [8]byte
	binary.BigEndian.PutUint64(serial[:], c.serial)
	if message, err = appendField(message, serial[:]); err != nil {
		return nil, err
	}

	operations, err := encodeStringList(c.operations)
	if err != nil {
		return nil, err
	}
	if message, err = appendField(message, operations); err != nil {
		return nil, err
	}

	prefixes, err := encodeStringList(c.prefixes)
	if err != nil {
		return nil, err
	}
	if message, err = appendField(message, prefixes); err != nil {
		return nil, err
	}

	var depth [4]byte
	binary.BigEndian.PutUint32(depth[:], c.maxDepth)
	if message, err = appendField(message, depth[:]); err != nil {
		return nil, err
	}

	notBefore := encodeTime(c.notBefore)
	if message, err = appendField(message, notBefore); err != nil {
		return nil, err
	}
	notAfter := encodeTime(c.notAfter)
	if message, err = appendField(message, notAfter); err != nil {
		return nil, err
	}
	return message, nil
}

func encodeStringList(values []string) ([]byte, error) {
	if len(values) > math.MaxUint32 {
		return nil, errors.New("string list is too long")
	}
	var encoded []byte
	var count [4]byte
	binary.BigEndian.PutUint32(count[:], uint32(len(values)))
	encoded = append(encoded, count[:]...)
	for _, value := range values {
		var err error
		encoded, err = appendField(encoded, []byte(value))
		if err != nil {
			return nil, err
		}
	}
	return encoded, nil
}

func appendField(dst, value []byte) ([]byte, error) {
	if len(value) > math.MaxUint32 {
		return nil, errors.New("certificate field is too long")
	}
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(value)))
	dst = append(dst, length[:]...)
	dst = append(dst, value...)
	return dst, nil
}

func encodeTime(value time.Time) []byte {
	var encoded [12]byte
	binary.BigEndian.PutUint64(encoded[:8], uint64(value.Unix()))
	binary.BigEndian.PutUint32(encoded[8:], uint32(value.Nanosecond()))
	return encoded[:]
}
