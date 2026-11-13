package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
)

// Digest is a host-assigned, opaque content identifier. Hosts should normally
// use NewDigest, which produces a SHA-256 digest, but short fixture identifiers
// are accepted when they are non-empty and contain no whitespace or control
// characters.
type Digest string

// NewDigest returns the SHA-256 digest of content in a stable text form.
func NewDigest(content []byte) Digest {
	sum := sha256.Sum256(content)
	return Digest("sha256:" + hex.EncodeToString(sum[:]))
}

// ParseDigest validates and returns an opaque digest.
func ParseDigest(value string) (Digest, error) {
	d := Digest(value)
	if err := d.Validate(); err != nil {
		return "", err
	}
	return d, nil
}

// Validate checks the minimal invariants required for a digest to be used as a
// policy and package-map key.
func (d Digest) Validate() error {
	if d == "" {
		return fmt.Errorf("netsec: empty package digest")
	}
	if len(d) > 256 {
		return fmt.Errorf("netsec: package digest is too long")
	}
	for _, r := range string(d) {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("netsec: package digest contains whitespace or control characters")
		}
	}
	return nil
}

func (d Digest) String() string { return string(d) }

func validateOpaqueID(kind, id string) error {
	if id == "" {
		return fmt.Errorf("netsec: empty %s ID", kind)
	}
	if len(id) > 256 {
		return fmt.Errorf("netsec: %s ID is too long", kind)
	}
	for _, r := range id {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("netsec: %s ID contains whitespace or control characters", kind)
		}
	}
	if strings.ContainsRune(id, '\x00') {
		return fmt.Errorf("netsec: %s ID contains NUL", kind)
	}
	return nil
}
