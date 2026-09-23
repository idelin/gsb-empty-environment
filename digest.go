package blockpack

import (
	"crypto/sha256"
	"encoding/binary"
)

// Digest is a SHA-256 digest used for all commitments in the package.
type Digest = [32]byte

func hashParts(parts ...[]byte) Digest {
	h := sha256.New()
	for _, p := range parts {
		h.Write(p)
	}
	var d Digest
	copy(d[:], h.Sum(nil))
	return d
}

func u32(v uint32) []byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	return b[:]
}
