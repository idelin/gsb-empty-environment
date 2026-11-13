package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// Digest is a fixed-size block or parent digest.
type Digest [32]byte

// DigestOf returns the SHA-256 digest of data.
func DigestOf(data []byte) Digest {
	return Digest(sha256.Sum256(data))
}

// DigestFromBytes copies a 32-byte digest.
func DigestFromBytes(b []byte) (Digest, error) {
	var d Digest
	if len(b) != len(d) {
		return d, fmt.Errorf("netsec: digest must be %d bytes, got %d", len(d), len(b))
	}
	copy(d[:], b)
	return d, nil
}

// ParseDigest parses a lower- or upper-case hexadecimal digest.
func ParseDigest(s string) (Digest, error) {
	var d Digest
	if len(s) != hex.EncodedLen(len(d)) {
		return d, fmt.Errorf("netsec: digest must be %d hex characters", hex.EncodedLen(len(d)))
	}
	if _, err := hex.Decode(d[:], []byte(s)); err != nil {
		return d, fmt.Errorf("netsec: parse digest: %w", err)
	}
	return d, nil
}

// MustParseDigest is ParseDigest for compile-time-like constants.
func MustParseDigest(s string) Digest {
	d, err := ParseDigest(s)
	if err != nil {
		panic(err)
	}
	return d
}

// IsZero reports whether d is the all-zero digest.
func (d Digest) IsZero() bool {
	return d == Digest{}
}

// String returns the lower-case hexadecimal representation.
func (d Digest) String() string {
	return hex.EncodeToString(d[:])
}

// MarshalText implements encoding.TextMarshaler.
func (d Digest) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *Digest) UnmarshalText(text []byte) error {
	parsed, err := ParseDigest(string(text))
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// MarshalJSON encodes a digest as a hexadecimal string.
func (d Digest) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

// UnmarshalJSON accepts either a hexadecimal string or a JSON array of bytes.
func (d *Digest) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*d = Digest{}
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		return d.UnmarshalText([]byte(text))
	}
	var raw []byte
	if err := json.Unmarshal(data, &raw); err != nil {
		return errors.New("netsec: digest must be a hex string or byte array")
	}
	parsed, err := DigestFromBytes(raw)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}
