package fp

import (
	"math/big"
	"testing"
)

func TestNaNAndSpecials(t *testing.T) {
	// qNaN passes through with quiet set; payload truncated to the
	// destination trailing-significand width (lower MantBits-1 bits).
	qnanF32 := Binary32.pack(false, uint64(1)<<Binary32.ExpBits-1,
		Binary32.quietBit()|0x12345)
	r := Convert(Binary16, Binary32, qnanF32, ToNearestEven)
	if r.Bits != Binary16.qnanBits(false, 0x345) {
		t.Fatalf("qNaN truncation = %04x want %04x", r.Bits, Binary16.qnanBits(false, 0x345))
	}
	if r.Flags != 0 {
		t.Fatalf("qNaN must not signal invalid, got %v", r.Flags)
	}

	// sNaN signals invalid and is quieted; payload preserved as truncated.
	snanF32 := Binary32.pack(false, uint64(1)<<Binary32.ExpBits-1, 0x12345)
	r = Convert(Binary16, Binary32, snanF32, ToNearestEven)
	if r.Bits != Binary16.qnanBits(false, 0x345) {
		t.Fatalf("sNaN quieting = %04x", r.Bits)
	}
	if r.Flags != FlagInvalid {
		t.Fatalf("sNaN must signal invalid, got %v", r.Flags)
	}

	// Payload that needs high bits dropped across f64 -> f16.
	snan64 := Binary64.pack(false, uint64(1)<<Binary64.ExpBits-1, 0xdeadbeef)
	r = Convert(Binary16, Binary64, snan64, ToNearestEven)
	if r.Bits != Binary16.qnanBits(false, 0x0ef) {
		t.Fatalf("f64 sNaN -> f16 = %04x", r.Bits)
	}

	// Sign is preserved on NaN/Inf.
	r = Convert(Binary16, Binary32, qnanF32|uint64(1)<<31, ToNearestEven)
	if r.Bits&(uint64(1)<<15) == 0 {
		t.Fatalf("NaN sign lost: %04x", r.Bits)
	}
	ninf := Convert(Binary16, Binary32, 0xff800000, ToNearestEven)
	if ninf.Bits != 0xfc00 {
		t.Fatalf("-Inf = %04x", ninf.Bits)
	}

	// Signed zeros survive in every mode.
	for _, mode := range allModes() {
		if z := Convert(Binary16, Binary32, 0x00000000, mode); z.Bits != 0 {
			t.Fatalf("+zero mode %d = %04x", mode, z.Bits)
		}
		if z := Convert(Binary16, Binary32, 0x80000000, mode); z.Bits != 0x8000 {
			t.Fatalf("-zero mode %d = %04x", mode, z.Bits)
		}
	}
}

// TestTinyExactNoUnderflow pins the underflow definition: tininess alone is
// not enough; an exact tiny result must not flag underflow.
func TestTinyExactNoUnderflow(t *testing.T) {
	// Smallest binary16 subnormal expressed decimally is 2^-24; give it
	// exactly as a power of two through the exact constructor.
	x := RationalExact(false, big.NewInt(1), -24)
	r := ConvertValue(Binary16, x, ToNearestEven)
	if r.Bits != 0x0001 || r.Flags != 0 {
		t.Fatalf("exact min subnormal bits=%04x flags=%v", r.Bits, r.Flags)
	}
	// One quantum below in magnitude rounds to zero with U|X.
	y := RationalExact(false, big.NewInt(15), -28) // 15*2^-28 = 0.9375*2^-24
	r = ConvertValue(Binary16, y, ToNearestEven)
	if r.Bits != 0x0001 {
		t.Fatalf("near min = %04x", r.Bits)
	}
	z := RationalExact(false, big.NewInt(15), -29) // 15*2^-29 < 0.5*2^-24 -> zero
	r = ConvertValue(Binary16, z, ToNearestEven)
	if r.Bits != 0x0000 || r.Flags != FlagUnderflow|FlagInexact {
		t.Fatalf("sub-zero bits=%04x flags=%v", r.Bits, r.Flags)
	}
}
