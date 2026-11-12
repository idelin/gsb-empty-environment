package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"crypto/x509"
)

// FingerprintSize is the length of a SHA-256 certificate fingerprint.
const FingerprintSize = sha256.Size

// Fingerprint identifies a DTLS client certificate. It is the SHA-256 digest
// of the certificate's DER encoding.
type Fingerprint [FingerprintSize]byte

// FingerprintOfCertificate returns the SHA-256 fingerprint of cert.Raw, which
// is the DER encoding parsed from the certificate. A nil certificate or one
// without a DER body yields the zero Fingerprint.
func FingerprintOfCertificate(cert *x509.Certificate) Fingerprint {
	if cert == nil || len(cert.Raw) == 0 {
		return Fingerprint{}
	}
	return sha256.Sum256(cert.Raw)
}

// FingerprintOfDER returns the SHA-256 fingerprint of a DER-encoded
// certificate. An empty DER body yields the zero Fingerprint.
func FingerprintOfDER(der []byte) Fingerprint {
	if len(der) == 0 {
		return Fingerprint{}
	}
	return sha256.Sum256(der)
}

// IsZero reports whether f is the zero value.
func (f Fingerprint) IsZero() bool {
	return f == Fingerprint{}
}

// String returns the lowercase hexadecimal fingerprint without separators.
func (f Fingerprint) String() string {
	return hex.EncodeToString(f[:])
}

// MarshalText implements encoding.TextMarshaler.
func (f Fingerprint) MarshalText() ([]byte, error) {
	if f.IsZero() {
		return nil, errors.New("netsec: cannot marshal an empty certificate fingerprint")
	}
	return []byte(f.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler. Colons and whitespace
// between hexadecimal bytes are accepted, as is a leading "sha256:" prefix.
func (f *Fingerprint) UnmarshalText(text []byte) error {
	parsed, err := ParseFingerprint(string(text))
	if err != nil {
		return err
	}
	*f = parsed
	return nil
}

// ParseFingerprint parses a SHA-256 certificate fingerprint. It accepts
// lowercase or uppercase hexadecimal, optional colon separators, and a
// leading "sha256:" prefix.
func ParseFingerprint(s string) (Fingerprint, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(strings.ToLower(s), "sha256:") {
		s = s[len("sha256:"):]
	}
	s = strings.ReplaceAll(s, ":", "")
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "\t", "")
	if len(s) != FingerprintSize*2 {
		return Fingerprint{}, fmt.Errorf("%w: fingerprint must contain %d hexadecimal digits", ErrInvalidFingerprint, FingerprintSize*2)
	}
	var f Fingerprint
	if _, err := hex.Decode(f[:], []byte(s)); err != nil {
		return Fingerprint{}, fmt.Errorf("%w: %v", ErrInvalidFingerprint, err)
	}
	if f.IsZero() {
		return Fingerprint{}, fmt.Errorf("%w: fingerprint must not be all zero", ErrInvalidFingerprint)
	}
	return f, nil
}
