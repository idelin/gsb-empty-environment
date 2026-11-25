package replayhandoff

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

// Root is a SHA-256 root or digest.
type Root [32]byte

// ZeroRoot is the empty-log/chained predecessor root.
var ZeroRoot Root

// RootBytes returns a copy of the root bytes.
func RootBytes(r Root) []byte {
	out := make([]byte, len(r))
	copy(out, r[:])
	return out
}

// RootFromBytes validates and copies a 32-byte root.
func RootFromBytes(b []byte) (Root, error) {
	if len(b) != sha256.Size {
		return Root{}, fmt.Errorf("replayhandoff: root must be %d bytes, got %d", sha256.Size, len(b))
	}
	var r Root
	copy(r[:], b)
	return r, nil
}

// String returns lowercase hexadecimal.
func (r Root) String() string {
	return hex.EncodeToString(r[:])
}

func hashParts(parts ...[]byte) Root {
	h := sha256.New()
	for _, p := range parts {
		writeLengthPrefixed(h, p)
	}
	var out Root
	copy(out[:], h.Sum(nil))
	return out
}

type byteWriter interface {
	Write([]byte) (int, error)
}

func writeLengthPrefixed(w byteWriter, b []byte) {
	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], uint64(len(b)))
	_, _ = w.Write(lenBuf[:])
	_, _ = w.Write(b)
}
