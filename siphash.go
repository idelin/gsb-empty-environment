package netsec

import "encoding/binary"

// siphash implements SipHash-2-4, a keyed hash with 128-bit key (k0, k1).
// The per-table secret key makes it infeasible for an attacker to
// precompute colliding keys for a specific table instance.
func siphash(k0, k1 uint64, p []byte) uint64 {
	v0 := k0 ^ 0x736f6d6570736575
	v1 := k1 ^ 0x646f72616e646f6d
	v2 := k0 ^ 0x6c7967656e657261
	v3 := k1 ^ 0x7465646279746573

	sipround := func() {
		v0 += v1
		v1 = v1<<13 | v1>>(64-13)
		v1 ^= v0
		v0 = v0<<32 | v0>>(64-32)
		v2 += v3
		v3 = v3<<16 | v3>>(64-16)
		v3 ^= v2
		v0 += v3
		v3 = v3<<21 | v3>>(64-21)
		v3 ^= v0
		v2 += v1
		v1 = v1<<17 | v1>>(64-17)
		v1 ^= v2
		v2 = v2<<32 | v2>>(64-32)
	}

	totalLen := len(p)
	for len(p) >= 8 {
		m := binary.LittleEndian.Uint64(p)
		v3 ^= m
		sipround()
		sipround()
		v0 ^= m
		p = p[8:]
	}
	b := uint64(totalLen) << 56
	for i := 0; i < len(p); i++ {
		b |= uint64(p[i]) << (8 * uint(i))
	}
	v3 ^= b
	sipround()
	sipround()
	v0 ^= b
	v2 ^= 0xff
	sipround()
	sipround()
	sipround()
	sipround()
	return v0 ^ v1 ^ v2 ^ v3
}
