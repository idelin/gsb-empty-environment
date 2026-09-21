package floatconv_test

import (
	"testing"

	"floatconv"
)

// Exhaustively verify that every 16-bit pattern survives a widening to
// binary32 followed by a narrowing back, bit-identically for finite values
// and zeros, and with the declared NaN canonicalization (quiet bit forced,
// payload left-aligned) for NaNs.
func testRoundTrip(t *testing.T, f16 floatconv.Format) {
	t.Helper()
	quiet := uint64(1) << (f16.MantBits - 1)
	expMask := uint64(1)<<f16.ExpBits - 1
	mantMask := uint64(1)<<f16.MantBits - 1
	for bits := uint64(0); bits < 1<<16; bits++ {
		up, flUp, err := floatconv.Convert(floatconv.Binary32, f16, bits, floatconv.RoundNearestEven)
		if err != nil {
			t.Fatal(err)
		}
		expField := bits >> f16.MantBits & expMask
		mant := bits & mantMask
		isNaN := expField == expMask && (mant != 0 || !f16.HasInf)
		isInf := expField == expMask && mant == 0 && f16.HasInf

		// Widening of finite values must be exact; sNaN raises invalid.
		signaling := isNaN && mant != 0 && mant&quiet == 0
		wantFlUp := floatconv.Flags(0)
		if signaling {
			wantFlUp = floatconv.FlagInvalid
		}
		if flUp != wantFlUp {
			t.Fatalf("%v widen %#x: flags %v, want %v", f16, bits, flUp, wantFlUp)
		}

		back, flBack, err := floatconv.Convert(f16, floatconv.Binary32, up, floatconv.RoundNearestEven)
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case isNaN:
			// Declared canonicalization: quiet bit set, payload preserved
			// through left-aligned truncation, sign preserved.
			want := bits | quiet
			if back != want {
				t.Fatalf("%v NaN round trip %#x -> %#x -> %#x, want %#x", f16, bits, up, back, want)
			}
			if flBack != 0 {
				t.Fatalf("%v NaN narrow %#x: flags %v, want none", f16, up, flBack)
			}
		case isInf:
			if back != bits || flBack != 0 {
				t.Fatalf("%v inf round trip %#x -> %#x -> %#x flags %v", f16, bits, up, back, flBack)
			}
		default:
			// Finite values and signed zeros: bit-identical, no flags.
			if back != bits || flBack != 0 {
				t.Fatalf("%v finite round trip %#x -> %#x -> %#x flags %v", f16, bits, up, back, flBack)
			}
		}
	}
}

func TestRoundTripBinary16ThroughBinary32(t *testing.T) {
	testRoundTrip(t, floatconv.Binary16)
}

func TestRoundTripBFloat16ThroughBinary32(t *testing.T) {
	testRoundTrip(t, floatconv.BFloat16)
}
