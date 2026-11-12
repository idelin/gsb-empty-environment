package netsec

import (
	"crypto/sha256"
	"encoding"
	"encoding/hex"
	"fmt"
	"regexp"
)

// Digest is an OCI content digest in the "<algorithm>:<encoded>" form.
//
// Only the sha256 algorithm is accepted, which is the algorithm required for
// in-toto subjects and OCI manifests handled by this package.
type Digest string

var _ encoding.TextUnmarshaler = (*Digest)(nil)

// digestPattern mirrors the OCI digest-spec grammar restricted to sha256.
var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// ParseDigest validates an OCI digest string. It returns ErrBadDigest for any
// digest that is not a well-formed sha256 digest.
func ParseDigest(s string) (Digest, error) {
	if !digestPattern.MatchString(s) {
		return "", fmt.Errorf("%w: %q", ErrBadDigest, s)
	}
	return Digest(s), nil
}

// String returns the canonical "<algorithm>:<hex>" representation.
func (d Digest) String() string { return string(d) }

// IsZero reports whether the digest is the empty value.
func (d Digest) IsZero() bool { return d == "" }

// Algorithm returns the digest algorithm component (for example "sha256").
func (d Digest) Algorithm() string {
	for i := 0; i < len(d); i++ {
		if d[i] == ':' {
			return string(d[:i])
		}
	}
	return ""
}

// Encoded returns the hex-encoded digest component.
func (d Digest) Encoded() string {
	for i := 0; i < len(d); i++ {
		if d[i] == ':' {
			return string(d[i+1:])
		}
	}
	return ""
}

// UnmarshalText implements encoding.TextUnmarshaler so malformed digests
// embedded in JSON documents are rejected while parsing.
func (d *Digest) UnmarshalText(text []byte) error {
	parsed, err := ParseDigest(string(text))
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// SHA256Digest computes the sha256 digest of data in OCI form.
func SHA256Digest(data []byte) Digest {
	sum := sha256.Sum256(data)
	return Digest("sha256:" + hex.EncodeToString(sum[:]))
}

// verifySHA256 returns the sha256 digest of data and, when want is non-empty,
// checks that it matches. mismatch selects the sentinel reported on a
// mismatch so callers can distinguish compressed, config and diffID failures.
func verifySHA256(data []byte, want Digest, mismatch error) (Digest, error) {
	got := SHA256Digest(data)
	if want != "" && got != want {
		return got, fmt.Errorf("%w: expected %s, got %s", mismatch, want, got)
	}
	return got, nil
}
