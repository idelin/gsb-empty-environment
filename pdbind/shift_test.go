package pdbind

import "testing"

func TestShifts(t *testing.T) {
	one := [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	if got := shiftLeft128(one, 8); got != [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0} {
		t.Fatalf("one<<8 = %x", got)
	}
	two := [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}
	if got := shiftRight128(two, 1); got != one {
		t.Fatalf("two>>1 = %x want %x", got, one)
	}
	// 0x0100 >> 8 == 1
	v := [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0}
	if got := shiftRight128(v, 8); got != one {
		t.Fatalf("0x0100>>8 = %x want %x", got, one)
	}
	// 0x0100 >> 72 == 0
	if got := shiftRight128(v, 72); got != [16]byte{} {
		t.Fatalf("0x0100>>72 = %x want zero", got)
	}
}
