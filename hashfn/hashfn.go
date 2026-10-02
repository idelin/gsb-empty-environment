// Package hashfn provides the 64-bit hash functions evaluated by the
// analyzer: MD5 (folded to 64 bits), MurmurHash3 (x64_128, low 64 bits)
// and FNV-1a (64-bit). Only the Go standard library is used.
package hashfn

import (
	"crypto/md5"
	"encoding/binary"
	"math/bits"
)

// Hash64 maps an arbitrary key to a 64-bit hash value.
type Hash64 func(key []byte) uint64

// MD5Hash hashes the key with MD5 and folds the first 8 digest bytes
// into a 64-bit value. MD5 has excellent avalanche behaviour, so any
// contiguous bit window of the result is uniformly distributed.
func MD5Hash(key []byte) uint64 {
	sum := md5.Sum(key)
	return binary.LittleEndian.Uint64(sum[:8])
}

// FNV1aHash implements the 64-bit FNV-1a hash. It is implemented
// directly (instead of via hash/fnv) to avoid per-call allocations;
// a unit test cross-checks it against the standard library.
//
// FNV-1a mixes each input byte with a single XOR followed by one
// multiply. The multiply propagates entropy towards the high bits only,
// so entropy contained in the last input byte(s) never reaches the top
// output bits. This avalanche deficiency is exactly what the analyzer
// is built to detect.
func FNV1aHash(key []byte) uint64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	h := uint64(offset64)
	for _, b := range key {
		h ^= uint64(b)
		h *= prime64
	}
	return h
}

// Murmur3Hash implements MurmurHash3 x64_128 (seed 0) and returns the
// low 64 bits of the 128-bit result. The fmix64 finalizer gives the
// output full avalanche behaviour.
func Murmur3Hash(key []byte) uint64 {
	const (
		c1 = 0x87c37b91114253d5
		c2 = 0x4cf5ad432745937f
	)
	var h1, h2 uint64
	n := len(key)

	nblocks := n / 16
	for i := 0; i < nblocks; i++ {
		k1 := binary.LittleEndian.Uint64(key[i*16:])
		k2 := binary.LittleEndian.Uint64(key[i*16+8:])

		k1 *= c1
		k1 = bits.RotateLeft64(k1, 31)
		k1 *= c2
		h1 ^= k1

		h1 = bits.RotateLeft64(h1, 27)
		h1 += h2
		h1 = h1*5 + 0x52dce729

		k2 *= c2
		k2 = bits.RotateLeft64(k2, 33)
		k2 *= c1
		h2 ^= k2

		h2 = bits.RotateLeft64(h2, 31)
		h2 += h1
		h2 = h2*5 + 0x38495ab5
	}

	tail := key[nblocks*16:]
	var k1, k2 uint64
	switch n & 15 {
	case 15:
		k2 ^= uint64(tail[14]) << 48
		fallthrough
	case 14:
		k2 ^= uint64(tail[13]) << 40
		fallthrough
	case 13:
		k2 ^= uint64(tail[12]) << 32
		fallthrough
	case 12:
		k2 ^= uint64(tail[11]) << 24
		fallthrough
	case 11:
		k2 ^= uint64(tail[10]) << 16
		fallthrough
	case 10:
		k2 ^= uint64(tail[9]) << 8
		fallthrough
	case 9:
		k2 ^= uint64(tail[8])
		k2 *= c2
		k2 = bits.RotateLeft64(k2, 33)
		k2 *= c1
		h2 ^= k2
		fallthrough
	case 8:
		k1 ^= uint64(tail[7]) << 56
		fallthrough
	case 7:
		k1 ^= uint64(tail[6]) << 48
		fallthrough
	case 6:
		k1 ^= uint64(tail[5]) << 40
		fallthrough
	case 5:
		k1 ^= uint64(tail[4]) << 32
		fallthrough
	case 4:
		k1 ^= uint64(tail[3]) << 24
		fallthrough
	case 3:
		k1 ^= uint64(tail[2]) << 16
		fallthrough
	case 2:
		k1 ^= uint64(tail[1]) << 8
		fallthrough
	case 1:
		k1 ^= uint64(tail[0])
		k1 *= c1
		k1 = bits.RotateLeft64(k1, 31)
		k1 *= c2
		h1 ^= k1
	}

	length := uint64(n)
	h1 ^= length
	h2 ^= length
	h1 += h2
	h2 += h1
	h1 = fmix64(h1)
	h2 = fmix64(h2)
	h1 += h2
	h2 += h1
	return h1
}

// fmix64 is the MurmurHash3 64-bit finalization mixer.
func fmix64(k uint64) uint64 {
	k ^= k >> 33
	k *= 0xff51afd7ed558ccd
	k ^= k >> 33
	k *= 0xc4ceb9fe1a85ec53
	k ^= k >> 33
	return k
}
