package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	sha256Algorithm = "sha256"
	sha256HexLength = 64
)

// Digest is an OCI content digest. This package only accepts sha256 digests
// written as "sha256:<64 lowercase hexadecimal characters>".
type Digest string

// ParseDigest validates and returns a digest.
func ParseDigest(s string) (Digest, error) {
	if err := validateDigest(s); err != nil {
		return "", err
	}
	return Digest(s), nil
}

// Validate reports whether d is a well-formed sha256 digest.
func (d Digest) Validate() error {
	return validateDigest(string(d))
}

// Algorithm returns the digest algorithm, or an empty string when d is
// malformed.
func (d Digest) Algorithm() string {
	if err := d.Validate(); err != nil {
		return ""
	}
	return sha256Algorithm
}

// Hex returns the hexadecimal digest value, or an empty string when d is
// malformed.
func (d Digest) Hex() string {
	if err := d.Validate(); err != nil {
		return ""
	}
	return string(d)[len(sha256Algorithm)+1:]
}

// Matches reports whether payload has the digest d.
func (d Digest) Matches(payload []byte) bool {
	if err := d.Validate(); err != nil {
		return false
	}
	sum := sha256.Sum256(payload)
	return "sha256:"+hex.EncodeToString(sum[:]) == string(d)
}

func (d Digest) String() string {
	return string(d)
}

func validateDigest(s string) error {
	const prefix = sha256Algorithm + ":"
	if len(s) != len(prefix)+sha256HexLength {
		return fmt.Errorf("%w: got length %d, want %d", ErrInvalidDigest, len(s), len(prefix)+sha256HexLength)
	}
	if !strings.HasPrefix(s, prefix) {
		return fmt.Errorf("%w: only sha256 digests are supported", ErrInvalidDigest)
	}
	for _, c := range s[len(prefix):] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return fmt.Errorf("%w: digest must use lowercase hexadecimal characters", ErrInvalidDigest)
		}
	}
	return nil
}
