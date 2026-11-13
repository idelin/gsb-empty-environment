package netsec

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// SHA256FingerprintLength is the number of bytes in a SHA-256 digest.
const SHA256FingerprintLength = sha256.Size

// CertificateFingerprint returns the RFC 8705 x5t#S256 value for DER-encoded
// certificate bytes: the unpadded base64url encoding of SHA-256(DER).
//
// The DER is hashed as supplied. Callers normally pass the peer certificate's
// x509.Certificate.Raw field from the TLS connection.
func CertificateFingerprint(der []byte) (string, error) {
	if len(der) == 0 {
		return "", fmt.Errorf("%w: empty certificate DER", ErrInvalidFingerprint)
	}
	sum := sha256.Sum256(der)
	return base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

// ValidateX5TS256 checks that value is the canonical, unpadded base64url
// encoding of exactly 32 bytes. Padding ('=') and the standard-base64
// characters '+' and '/' are rejected even if a permissive decoder would
// accept them.
func ValidateX5TS256(value string) error {
	_, err := decodeFingerprint(value)
	return err
}

// decodeFingerprint validates and decodes an x5t#S256 or jkt value. Both
// claims use the same SHA-256 base64url format.
func decodeFingerprint(value string) ([]byte, error) {
	if value == "" {
		return nil, fmt.Errorf("%w: value is empty", ErrInvalidFingerprint)
	}
	if strings.ContainsRune(value, '=') {
		return nil, fmt.Errorf("%w: padding is not allowed", ErrInvalidFingerprint)
	}
	if strings.ContainsAny(value, "+/") {
		return nil, fmt.Errorf("%w: standard base64 characters '+' and '/' are not allowed", ErrInvalidFingerprint)
	}
	if strings.ContainsAny(value, " \t\r\n") {
		return nil, fmt.Errorf("%w: whitespace is not allowed", ErrInvalidFingerprint)
	}

	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidFingerprint, err)
	}
	if len(decoded) != SHA256FingerprintLength {
		return nil, fmt.Errorf("%w: digest must be %d bytes, got %d", ErrInvalidFingerprint, SHA256FingerprintLength, len(decoded))
	}
	// Strict decoding checks trailing padding bits, and this round trip
	// rejects any other non-canonical spelling.
	if base64.RawURLEncoding.EncodeToString(decoded) != value {
		return nil, fmt.Errorf("%w: value is not canonical base64url", ErrInvalidFingerprint)
	}
	return decoded, nil
}

// fingerprintsEqual compares two canonical fingerprint values by decoding
// them and comparing the digest bytes in constant time.
func fingerprintsEqual(a, b string) (bool, error) {
	left, err := decodeFingerprint(a)
	if err != nil {
		return false, err
	}
	right, err := decodeFingerprint(b)
	if err != nil {
		return false, err
	}
	if subtle.ConstantTimeCompare(left, right) != 1 {
		return false, nil
	}
	return true, nil
}

// SameFingerprint reports whether two well-formed x5t#S256 values identify
// the same digest. It is exported for callers comparing refresh-token records
// against fingerprints computed from a TLS peer certificate.
func SameFingerprint(a, b string) (bool, error) {
	return fingerprintsEqual(a, b)
}

// IsInvalidFingerprint reports whether err was caused by a malformed
// fingerprint value.
func IsInvalidFingerprint(err error) bool {
	return errors.Is(err, ErrInvalidFingerprint)
}
