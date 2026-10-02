package hashring

import (
	"crypto/md5"
	"encoding/binary"
	"math/bits"
)

// HashFunc maps an arbitrary key to a 32-bit position on the hash ring.
type HashFunc func(data []byte) uint32

// NamedHash bundles a hash function with its display name.
type NamedHash struct {
	Name string
	Func HashFunc
}

// MD5Hash uses the first 4 bytes (little-endian) of the MD5 digest.
func MD5Hash(data []byte) uint32 {
	sum := md5.Sum(data)
	return binary.LittleEndian.Uint32(sum[:4])
}

// Murmur3Hash is a from-scratch MurmurHash3 x86_32 implementation (seed 0).
func Murmur3Hash(data []byte) uint32 {
	const (
		c1 = 0xcc9e2d51
		c2 = 0x1b873593
	)
	h1 := uint32(0)
	nblocks := len(data) / 4
	for i := 0; i < nblocks; i++ {
		k1 := binary.LittleEndian.Uint32(data[i*4:])
		k1 *= c1
		k1 = bits.RotateLeft32(k1, 15)
		k1 *= c2
		h1 ^= k1
		h1 = bits.RotateLeft32(h1, 13)
		h1 = h1*5 + 0xe6546b64
	}
	var k1 uint32
	tail := data[nblocks*4:]
	switch len(tail) {
	case 3:
		k1 ^= uint32(tail[2]) << 16
		fallthrough
	case 2:
		k1 ^= uint32(tail[1]) << 8
		fallthrough
	case 1:
		k1 ^= uint32(tail[0])
		k1 *= c1
		k1 = bits.RotateLeft32(k1, 15)
		k1 *= c2
		h1 ^= k1
	}
	h1 ^= uint32(len(data))
	h1 ^= h1 >> 16
	h1 *= 0x85ebca6b
	h1 ^= h1 >> 13
	h1 *= 0xc2b2ae35
	h1 ^= h1 >> 16
	return h1
}

// FNV1aHash is the 32-bit FNV-1a hash.
func FNV1aHash(data []byte) uint32 {
	const (
		offsetBasis = 2166136261
		prime       = 16777619
	)
	h := uint32(offsetBasis)
	for _, b := range data {
		h ^= uint32(b)
		h *= prime
	}
	return h
}

// DefaultHashes is the comparison set used by the analyzer CLI and tests.
func DefaultHashes() []NamedHash {
	return []NamedHash{
		{Name: "MD5-32", Func: MD5Hash},
		{Name: "MurmurHash3-x86_32", Func: Murmur3Hash},
		{Name: "FNV-1a-32", Func: FNV1aHash},
	}
}
