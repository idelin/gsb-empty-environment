package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
)

// Digest is the SHA-256 digest of a canonically encoded value.
type Digest [32]byte

// String returns the hexadecimal form of d.
func (d Digest) String() string {
	return hex.EncodeToString(d[:])
}

// IsZero reports whether d is the zero digest.
func (d Digest) IsZero() bool {
	return d == Digest{}
}

// digestOf hashes a domain separator and a sequence of length-prefixed parts.
// The length prefixes make concatenations unambiguous.
func digestOf(domain string, parts ...[]byte) Digest {
	h := sha256.New()
	h.Write([]byte(domain))
	h.Write([]byte{0})
	var length [8]byte
	for _, part := range parts {
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		h.Write(length[:])
		h.Write(part)
	}
	var out Digest
	copy(out[:], h.Sum(nil))
	return out
}

// canonical is a small deterministic binary encoder used for digest inputs.
type canonical struct {
	b []byte
}

func (c *canonical) u32(v uint32) {
	c.b = append(c.b, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func (c *canonical) u64(v uint64) {
	c.b = append(c.b,
		byte(v>>56), byte(v>>48), byte(v>>40), byte(v>>32),
		byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func (c *canonical) str(v string) {
	c.u64(uint64(len(v)))
	c.b = append(c.b, v...)
}

func (c *canonical) bytes(v []byte) {
	c.u64(uint64(len(v)))
	c.b = append(c.b, v...)
}
