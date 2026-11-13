package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

const (
	// HashSize is the number of bytes in every sparse Merkle tree digest.
	HashSize = sha256.Size

	// MaxHeight is the largest supported tree height.
	MaxHeight uint8 = 64

	// MaxQueries is the largest number of keys in one multi-proof.
	MaxQueries = 256

	// ServiceHeight is the height used by the key directory service.
	ServiceHeight uint8 = 64
)

// Digest is a SHA-256 digest. Its zero value is not an empty-tree sentinel.
type Digest [HashSize]byte

func (d Digest) String() string {
	return hex.EncodeToString(d[:])
}

// emptyRoots[d] is the root of an empty subtree with d levels below its root.
// Level zero is therefore the absent leaf.
var emptyRoots [MaxHeight + 1]Digest

func init() {
	emptyRoots[0] = sha256.Sum256([]byte{0x02})
	for depth := uint8(1); depth <= MaxHeight; depth++ {
		emptyRoots[depth] = InternalHash(emptyRoots[depth-1], emptyRoots[depth-1])
	}
}

// EmptyRoot returns the root of an entirely empty tree of the given height.
func EmptyRoot(height uint8) (Digest, error) {
	if !validHeight(height) {
		return Digest{}, ErrHeight
	}
	return emptyRoots[height], nil
}

// LeafHash returns SHA256(00 || key_be64 || valueLen_be64 || value).
func LeafHash(key uint64, value []byte) Digest {
	h := sha256.New()
	_, _ = h.Write([]byte{0x00})

	var header [16]byte
	binary.BigEndian.PutUint64(header[:8], key)
	binary.BigEndian.PutUint64(header[8:], uint64(len(value)))
	_, _ = h.Write(header[:])
	_, _ = h.Write(value)

	var out Digest
	copy(out[:], h.Sum(nil))
	return out
}

// AbsentLeafHash returns SHA256(02).
func AbsentLeafHash() Digest {
	return emptyRoots[0]
}

// InternalHash returns SHA256(01 || left || right).
func InternalHash(left, right Digest) Digest {
	var buf [1 + 2*HashSize]byte
	buf[0] = 0x01
	copy(buf[1:1+HashSize], left[:])
	copy(buf[1+HashSize:], right[:])
	return Digest(sha256.Sum256(buf[:]))
}

// PrefixAtDepth returns the numeric prefix formed by the first depth bits of
// key, where bit height-1 is visited first. The prefix occupies the low depth
// bits of the result.
func PrefixAtDepth(key uint64, height, depth uint8) (uint64, error) {
	if !validHeight(height) {
		return 0, ErrHeight
	}
	if !keyInRange(key, height) {
		return 0, fmt.Errorf("%w: key %d at height %d", ErrQueryKey, key, height)
	}
	if depth > height {
		return 0, ErrDepth
	}
	if depth == 0 {
		return 0, nil
	}
	return key >> (height - depth), nil
}

func validHeight(height uint8) bool {
	return height >= 1 && height <= MaxHeight
}

func keyInRange(key uint64, height uint8) bool {
	if height == MaxHeight {
		return true
	}
	return key < uint64(1)<<height
}

func prefixFits(prefix uint64, depth uint8) bool {
	if depth == MaxHeight {
		return true
	}
	return prefix < uint64(1)<<depth
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	out := make([]byte, len(value))
	copy(out, value)
	return out
}
