// Package dnscookie implements interoperable DNS Cookies (RFC 7873 /
// RFC 9018) generation, verification and anti-poisoning policy state
// machines for authoritative and recursive DNS hosts.
//
// The package is fully self-contained: it ships its own SipHash-2-4
// implementation and a minimal DNS wire-format parser, so it has no
// dependencies outside the Go standard library and never touches the
// network.
package dnscookie

import (
	"encoding/binary"
	"math/bits"
)

// sipHash24 computes SipHash-2-4 of msg with the 128-bit key, returning
// the 64-bit result. This is the reference construction by Aumasson and
// Bernstein, used by RFC 9018 for the Server Cookie hash.
func sipHash24(key *[16]byte, msg []byte) uint64 {
	k0 := binary.LittleEndian.Uint64(key[0:8])
	k1 := binary.LittleEndian.Uint64(key[8:16])

	v0 := k0 ^ 0x736f6d6570736575
	v1 := k1 ^ 0x646f72616e646f6d
	v2 := k0 ^ 0x6c7967656e657261
	v3 := k1 ^ 0x7465646279746573

	sipRound := func() {
		v0 += v1
		v1 = bits.RotateLeft64(v1, 13)
		v1 ^= v0
		v0 = bits.RotateLeft64(v0, 32)
		v2 += v3
		v3 = bits.RotateLeft64(v3, 16)
		v3 ^= v2
		v0 += v3
		v3 = bits.RotateLeft64(v3, 21)
		v3 ^= v0
		v2 += v1
		v1 = bits.RotateLeft64(v1, 17)
		v1 ^= v2
		v2 = bits.RotateLeft64(v2, 32)
	}

	i := 0
	for ; i+8 <= len(msg); i += 8 {
		m := binary.LittleEndian.Uint64(msg[i:])
		v3 ^= m
		sipRound()
		sipRound()
		v0 ^= m
	}

	// Final block: length byte followed by the remaining bytes.
	b := uint64(len(msg)) << 56
	for j := i; j < len(msg); j++ {
		b |= uint64(msg[j]) << (8 * uint(j-i))
	}
	v3 ^= b
	sipRound()
	sipRound()
	v0 ^= b

	v2 ^= 0xff
	sipRound()
	sipRound()
	sipRound()
	sipRound()

	return v0 ^ v1 ^ v2 ^ v3
}
