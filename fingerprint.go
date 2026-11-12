package netsec

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
)

// Fingerprint is the SHA-256 digest of a certificate's advertised SubjectPublicKeyInfo.
type Fingerprint [sha256.Size]byte

// SPKIFingerprint returns the SHA-256 digest of cert.RawSubjectPublicKeyInfo.
// It returns the zero fingerprint when cert is nil.
func SPKIFingerprint(cert *x509.Certificate) Fingerprint {
	if cert == nil {
		return Fingerprint{}
	}
	return Fingerprint(sha256.Sum256(cert.RawSubjectPublicKeyInfo))
}

// SPKIFingerprintRaw returns the SHA-256 digest of DER-encoded SPKI bytes.
func SPKIFingerprintRaw(spki []byte) Fingerprint {
	return Fingerprint(sha256.Sum256(spki))
}

// FingerprintOf is an alias for SPKIFingerprint.
func FingerprintOf(cert *x509.Certificate) Fingerprint {
	return SPKIFingerprint(cert)
}

// String returns the fingerprint as lower-case hexadecimal.
func (f Fingerprint) String() string {
	return hex.EncodeToString(f[:])
}

// IsZero reports whether f is the zero fingerprint.
func (f Fingerprint) IsZero() bool {
	return f == Fingerprint{}
}

// Equal reports whether f and other contain the same digest.
func (f Fingerprint) Equal(other Fingerprint) bool {
	return f == other
}
