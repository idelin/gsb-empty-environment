package netsec

import "testing"

// Reference vectors from the SipHash paper (key = 00 01 ... 0f,
// message = 00 01 ... (len-1)).
func TestSipHash24ReferenceVectors(t *testing.T) {
	var key [16]byte
	for i := range key {
		key[i] = byte(i)
	}
	k0 := uint64(0x0706050403020100)
	k1 := uint64(0x0f0e0d0c0b0a0908)
	want := []uint64{
		0x726fdb47dd0e0e31,
		0x74f839c593dc67fd,
		0x0d6c8009d9a94f5a,
		0x85676696d7fb7e2d,
		0xcf2794e0277187b7,
		0x18765564cd99a68d,
		0xcbc9466e58fee3ce,
		0xab0200f58b01d137,
	}
	for n, w := range want {
		msg := key[:n]
		if got := siphash24(k0, k1, msg); got != w {
			t.Fatalf("siphash24(len=%d) = %#016x, want %#016x", n, got, w)
		}
	}
}
