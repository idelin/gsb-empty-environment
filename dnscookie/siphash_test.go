package dnscookie

import "testing"

// Reference vectors from the SipHash-2-4 paper (Aumasson & Bernstein):
// key = 00 01 02 ... 0f, message = first i bytes of 00 01 02 ...
var sipHashVectors = []uint64{
	0x726fdb47dd0e0e31, 0x74f839c593dc67fd, 0x0d6c8009d9a94f5a,
	0x85676696d7fb7e2d, 0xcf2794e0277187b7, 0x18765564cd99a68d,
	0xcbc9466e58fee3ce, 0xab0200f58b01d137, 0x93f5f5799a932462,
	0x9e0082df0ba9e4b0, 0x7a5dbbc594ddb9f3, 0xf4b32f46226bada7,
	0x751e8fbc860ee5fb, 0x14ea5627c0843d90, 0xf723ca908e7af2ee,
	0xa129ca6149be45e5,
}

func TestSipHash24ReferenceVectors(t *testing.T) {
	var key [16]byte
	for i := range key {
		key[i] = byte(i)
	}
	msg := make([]byte, 64)
	for i := range msg {
		msg[i] = byte(i)
	}
	for i, want := range sipHashVectors {
		if got := sipHash24(&key, msg[:i]); got != want {
			t.Errorf("siphash24(len=%d) = %016x, want %016x", i, got, want)
		}
	}
}
