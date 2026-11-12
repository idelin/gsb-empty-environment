package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// Digest is the SHA-256 digest used for evidence, issuers, and chain links.
type Digest [sha256.Size]byte

// DigestOf returns the SHA-256 digest of data.
func DigestOf(data []byte) Digest {
	return Digest(sha256.Sum256(data))
}

// String returns the lowercase hexadecimal representation of d.
func (d Digest) String() string {
	return hex.EncodeToString(d[:])
}

// IsZero reports whether d is the zero digest.
func (d Digest) IsZero() bool {
	return d == Digest{}
}

// MarshalText implements encoding.TextMarshaler.
func (d Digest) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *Digest) UnmarshalText(text []byte) error {
	if d == nil {
		return fmt.Errorf("netsec: nil digest receiver")
	}
	if len(text) != sha256.Size*2 {
		return fmt.Errorf("netsec: digest must be %d hexadecimal characters", sha256.Size*2)
	}
	var decoded [sha256.Size]byte
	if _, err := hex.Decode(decoded[:], text); err != nil {
		return fmt.Errorf("netsec: invalid digest: %w", err)
	}
	*d = Digest(decoded)
	return nil
}
