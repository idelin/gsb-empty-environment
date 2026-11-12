package netsec

import (
	"math/big"
)

// Poly1305 one-time authenticator as defined in RFC 8439 section 2.5.
//
// Arithmetic is performed over the prime field 2^130 - 5 using math/big. This
// favors clarity and auditability over speed; the cipher's security boundary
// is exercised by the RFC 8439 test vectors in the test suite.

var poly1305Prime, _ = new(big.Int).SetString("3fffffffffffffffffffffffffffffffb", 16) // 2^130 - 5

// poly1305MAC returns the 16-byte tag for msg under the 32-byte one-time key.
func poly1305MAC(key, msg []byte) []byte {
	r := leToBig(clampR(key[:16]))
	s := leToBig(key[16:32])

	acc := new(big.Int)
	for len(msg) > 0 {
		block := msg
		if len(block) > 16 {
			block = block[:16]
		}
		// The block number is the little-endian block interpreted as an
		// integer, with the hi bit 2^(8 * block length) added.
		n := leToBig(block)
		hibit := new(big.Int).Lsh(big.NewInt(1), uint(8*len(block)))
		n.Add(n, hibit)
		acc.Add(acc, n)
		acc.Mul(acc, r)
		acc.Mod(acc, poly1305Prime)
		msg = msg[len(block):]
	}

	acc.Add(acc, s)
	tag := make([]byte, 16)
	// acc is little-endian on the wire; big.Int.Bytes is big-endian and omits
	// leading zero bytes, so right-align then reverse.
	be := acc.Bytes()
	if len(be) > 16 {
		be = be[len(be)-16:]
	}
	for i, b := range be {
		tag[15-i] = b
	}
	return tag
}

// clampR applies the Poly1305 clamping mask to the 16-byte r component.
func clampR(rb []byte) []byte {
	r := make([]byte, 16)
	copy(r, rb)
	r[3] &= 15
	r[7] &= 15
	r[11] &= 15
	r[15] &= 15
	r[4] &= 252
	r[8] &= 252
	r[12] &= 252
	return r
}

// leBytes reverses the input so that the little-endian wire bytes become
// big-endian bytes suitable for big.Int. Inputs shorter than 16 bytes are
// interpreted with zero-padding on the high side.
func leBytes(b []byte) []byte {
	out := make([]byte, len(b))
	for i := range b {
		out[len(b)-1-i] = b[i]
	}
	return out
}

// leToBig interprets b as a little-endian unsigned integer.
func leToBig(b []byte) *big.Int {
	return new(big.Int).SetBytes(leBytes(b))
}
