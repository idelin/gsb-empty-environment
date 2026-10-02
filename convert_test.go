package floatwire

import (
	"math"
	"math/rand"
	"testing"
)

// Every one of the 65536 float16 patterns, widened to float32 and
// float64, must agree with the exact big.Int reference, and narrowing
// back must be the identity (bit-preserving contract). No host float is
// involved anywhere, so NaN payloads cannot be lost.
func TestAllFloat16PatternsAgainstReference(t *testing.T) {
	for i := 0; i < 1<<16; i++ {
		b := uint64(i)

		got32, fl32, err := ConvertFlags(Float16, Float32, b, ModeBitPreserving)
		if err != nil {
			t.Fatal(err)
		}
		ref32, rfl32 := refConvert(Float16, Float32, b)
		if got32 != ref32 || fl32 != rfl32 {
			t.Fatalf("f16->f32 %04x: got %08x/%v want %08x/%v", i, got32, fl32, ref32, rfl32)
		}

		got64, fl64, err := ConvertFlags(Float16, Float64, b, ModeBitPreserving)
		if err != nil {
			t.Fatal(err)
		}
		ref64, rfl64 := refConvert(Float16, Float64, b)
		if got64 != ref64 || fl64 != rfl64 {
			t.Fatalf("f16->f64 %04x: got %016x/%v want %016x/%v", i, got64, fl64, ref64, rfl64)
		}

		// Widening is lossless: narrowing back restores the exact bits.
		back32, err := Convert(Float32, Float16, got32, ModeBitPreserving)
		if err != nil || back32 != b {
			t.Fatalf("f16->f32->f16 %04x -> %08x -> %04x (err=%v)", i, got32, back32, err)
		}
		back64, err := Convert(Float64, Float16, got64, ModeBitPreserving)
		if err != nil || back64 != b {
			t.Fatalf("f16->f64->f16 %04x -> %016x -> %04x (err=%v)", i, got64, back64, err)
		}

		// Widening finite values is exact: no exception flags, ever.
		if Classify(Float16, b) != NaN && (fl32 != (Flags{}) || fl64 != (Flags{})) {
			t.Fatalf("widening %04x raised flags %v/%v", i, fl32, fl64)
		}
	}
}

// float16MantExp decomposes a finite float16 pattern into mant * 2^e2.
func float16MantExp(h uint64) (mant uint64, e2 int) {
	exp := (h >> 10) & 0x1F
	man := h & 0x3FF
	if exp == 0 {
		return man, 1 - 15 - 10
	}
	return man | 0x400, int(exp) - 15 - 10
}

// float32BitsOfNormal builds the float32 pattern of mant * 2^e2 for a
// positive value that is surely normal in float32, using only integers.
func float32BitsOfNormal(mant uint64, e2 int) uint32 {
	// Normalize to 24 significant bits.
	bl := 64 - clz64(mant)
	shift := bl - 24
	keep := mant
	if shift > 0 {
		keep = mant >> uint(shift)
	} else {
		keep = mant << uint(-shift)
	}
	e2 += shift
	expUnb := e2 + 23
	field := uint32(expUnb + 127)
	return field<<23 | uint32(keep&0x7FFFFF)
}

func clz64(v uint64) int {
	n := 0
	for i := 63; i >= 0; i-- {
		if v&(uint64(1)<<uint(i)) != 0 {
			break
		}
		n++
	}
	return n
}

// For every adjacent pair of finite float16 values, the exact midpoint
// (representable in float32) must round to the even neighbor, and the
// float32 values one ulp below/above the midpoint must round down/up.
// This pins the round-to-nearest-ties-to-even behavior on 32-bit
// midpoint boundaries without ever using host floats.
func TestAllFloat32MidpointBoundaries(t *testing.T) {
	for h := uint64(0); h < 0x7BFF; h++ { // positive finite pairs
		mantLo, e2Lo := float16MantExp(h)
		mantHi, e2Hi := float16MantExp(h + 1)
		// Bring both to the lower exponent (e2Hi >= e2Lo for adjacent
		// patterns, including across binade boundaries).
		mid2 := mantLo + mantHi<<uint(e2Hi-e2Lo) // = 2*midpoint at e2Lo
		midBits := float32BitsOfNormal(mid2, e2Lo-1)

		even := h
		if h&1 == 1 {
			even = h + 1
		}
		for _, sign := range []uint64{0, 0x8000} {
			lo, hi := sign|h, sign|(h+1)
			ev := sign | even
			mid := uint64(midBits) | sign<<16 // sign to bit 31
			if sign != 0 {
				mid = uint64(midBits) | 0x80000000
			}

			got, flags, err := ConvertFlags(Float32, Float16, mid, ModeNumeric)
			if err != nil {
				t.Fatal(err)
			}
			if got != ev {
				t.Fatalf("midpoint of %04x/%04x (%08x) -> %04x, want even %04x", lo, hi, mid, got, ev)
			}
			if !flags.Inexact || flags.Overflow {
				t.Fatalf("midpoint %08x flags = %v, want inexact without overflow", mid, flags)
			}
			// The exact reference must agree.
			ref, rfl := refConvert(Float32, Float16, mid)
			if ref != got || rfl != flags {
				t.Fatalf("reference mismatch at %08x: %04x/%v vs %04x/%v", mid, ref, rfl, got, flags)
			}

			// One float32 ulp below/above the midpoint.
			below, _, _ := ConvertFlags(Float32, Float16, mid-1, ModeNumeric)
			if below != lo {
				t.Fatalf("midpoint-1ulp %08x -> %04x, want %04x", mid-1, below, lo)
			}
			above, _, _ := ConvertFlags(Float32, Float16, mid+1, ModeNumeric)
			if above != hi {
				t.Fatalf("midpoint+1ulp %08x -> %04x, want %04x", mid+1, above, hi)
			}
		}
	}
}

// Differential test against the exact reference on random patterns for
// every conversion pair.
func TestRandomPatternsAgainstReference(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	pairs := [][2]Width{
		{Float32, Float16}, {Float16, Float32},
		{Float64, Float32}, {Float32, Float64},
		{Float64, Float16}, {Float16, Float64},
	}
	for _, pair := range pairs {
		from, to := pair[0], pair[1]
		n := 200000
		if from == Float16 {
			n = 1 << 16 // exhaustive
		}
		for i := 0; i < n; i++ {
			var b uint64
			if from == Float16 {
				b = uint64(i)
			} else {
				b = rng.Uint64()
			}
			for _, mode := range []Mode{ModeBitPreserving, ModeNumeric} {
				got, flags, err := ConvertFlags(from, to, b, mode)
				if err != nil {
					t.Fatal(err)
				}
				ref, rfl := refConvert(from, to, MaskBits(from, b))
				if got != ref || flags != rfl {
					t.Fatalf("%d->%d %016x: got %x/%v want %x/%v",
						from, to, b, got, flags, ref, rfl)
				}
			}
		}
	}
}

// Exception flags on hand-picked boundaries.
func TestConversionFlags(t *testing.T) {
	type tc struct {
		from, to Width
		bits     uint64
		want     uint64
		flags    Flags
	}
	f32 := func(f float32) uint64 { return uint64(math.Float32bits(f)) }
	cases := []tc{
		// Exact normal conversion: no flags.
		{Float32, Float16, f32(1.5), 0x3E00, Flags{}},
		// Inexact normal.
		{Float32, Float16, f32(1.1), 0x3C66, Flags{Inexact: true}},
		// Overflow to infinity: Overflow + Inexact.
		{Float32, Float16, f32(3.0e38), 0x7C00, Flags{Overflow: true, Inexact: true}},
		// Largest finite float32.
		{Float32, Float16, 0x7F7FFFFF, 0x7C00, Flags{Overflow: true, Inexact: true}},
		// Overflow boundary: 65520 rounds up to Inf, 65519 stays finite.
		{Float32, Float16, f32(65520), 0x7C00, Flags{Overflow: true, Inexact: true}},
		{Float32, Float16, f32(65519), 0x7BFF, Flags{Inexact: true}},
		// Exact subnormal result: no flags at all.
		{Float32, Float16, f32(0.00006103515625), 0x0400, Flags{}},  // 2^-14, smallest normal
		{Float32, Float16, f32(0.000030517578125), 0x0200, Flags{}}, // 2^-15, exact subnormal
		// Inexact subnormal: Underflow + Inexact.
		{Float32, Float16, f32(1e-6), 0x0011, Flags{Underflow: true, Inexact: true}},
		// Underflow to zero.
		{Float32, Float16, f32(1e-20), 0x0000, Flags{Underflow: true, Inexact: true}},
		// Signaling NaN raises Invalid; payload policy keeps it NaN.
		{Float32, Float16, 0x7F800001, 0x7C01, Flags{Invalid: true}},
		// Quiet NaN: no flags.
		{Float32, Float16, 0x7FC00001, 0x7E00, Flags{}},
	}
	for _, c := range cases {
		got, flags, err := ConvertFlags(c.from, c.to, c.bits, ModeNumeric)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want || flags != c.flags {
			t.Errorf("convert %08x: got %04x/%v, want %04x/%v", c.bits, got, flags, c.want, c.flags)
		}
	}
}

// NaN payload policy: top bits kept on narrowing, payload placed at the
// top on widening, signaling bit preserved, never decay to non-NaN.
func TestNaNPayloadPolicy(t *testing.T) {
	// Widening keeps the payload in the most significant mantissa bits.
	up, err := Convert(Float16, Float64, 0x7E55, ModeBitPreserving) // qNaN payload 0x155
	if err != nil {
		t.Fatal(err)
	}
	if up != 0x7FF0000000000000|(0x255<<42) {
		t.Fatalf("f16->f64 NaN payload = %016x", up)
	}
	// Narrowing truncates from the bottom, keeping top payload bits.
	down, err := Convert(Float64, Float16, 0x7FF8DEADBEEFCAFE, ModeBitPreserving)
	if err != nil {
		t.Fatal(err)
	}
	want := uint64(0x7C00) | (0x8DEADBEEFCAFE >> 42)
	if down != want {
		t.Fatalf("f64->f16 NaN payload = %04x, want %04x", down, want)
	}
	// A quiet NaN whose payload truncates away keeps the quiet bit.
	quiet, err := Convert(Float64, Float16, 0x7FF8000000000001, ModeBitPreserving)
	if err != nil {
		t.Fatal(err)
	}
	if quiet != 0x7E00 {
		t.Fatalf("quiet NaN = %04x, want 7E00", quiet)
	}
	// A payload living entirely in truncated bits must not become Inf:
	// the lowest payload bit is set instead.
	tiny, err := Convert(Float64, Float16, 0x7FF0000000000001, ModeBitPreserving)
	if err != nil {
		t.Fatal(err)
	}
	if tiny != 0x7C01 {
		t.Fatalf("truncated-to-zero payload = %04x, want 7C01", tiny)
	}
	if !IsNaN(Float16, tiny) {
		t.Fatal("NaN decayed to non-NaN")
	}
	// Signaling bit survives narrowing and widening.
	s, err := Convert(Float64, Float16, 0x7FF4000000000001, ModeBitPreserving)
	if err != nil {
		t.Fatal(err)
	}
	if !IsSignaling(Float16, s) {
		t.Fatalf("signaling NaN quieted: %04x", s)
	}
	s2, err := Convert(Float16, Float64, s, ModeBitPreserving)
	if err != nil {
		t.Fatal(err)
	}
	if !IsSignaling(Float64, s2) {
		t.Fatalf("signaling NaN quieted on widening: %016x", s2)
	}
	// NaN sign is preserved.
	neg, err := Convert(Float32, Float16, 0xFFC00001, ModeBitPreserving)
	if err != nil {
		t.Fatal(err)
	}
	if neg != 0xFE00 {
		t.Fatalf("negative NaN = %04x, want FE00", neg)
	}
}

// Subnormal policy: gradual underflow, never flush-to-zero, exact
// subnormals convert without flags.
func TestSubnormalPolicy(t *testing.T) {
	// float16 subnormals widen exactly to float32 normals.
	for h := uint64(1); h < 0x400; h++ {
		up, flags, err := ConvertFlags(Float16, Float32, h, ModeNumeric)
		if err != nil {
			t.Fatal(err)
		}
		if flags != (Flags{}) {
			t.Fatalf("subnormal %04x widening raised %v", h, flags)
		}
		if Classify(Float32, up) != Normal {
			t.Fatalf("subnormal %04x widened to non-normal %08x", h, up)
		}
		back, _ := Convert(Float32, Float16, up, ModeNumeric)
		if back != h {
			t.Fatalf("subnormal round trip %04x -> %08x -> %04x", h, up, back)
		}
	}
	// The smallest float16 subnormal is exactly representable in float32.
	up, _, _ := ConvertFlags(Float16, Float32, 0x0001, ModeNumeric)
	if up != 0x33800000 { // 2^-24
		t.Fatalf("min subnormal widened to %08x, want 33800000", up)
	}
	// float32 subnormals narrow to float16 zero with Underflow+Inexact,
	// never silently flushed without flags.
	down, flags, _ := ConvertFlags(Float32, Float16, 0x00000001, ModeNumeric)
	if down != 0 || !flags.Underflow || !flags.Inexact {
		t.Fatalf("tiny f32 -> %04x/%v, want 0000/UX", down, flags)
	}
	// Sign of an underflowed zero is kept.
	downNeg, _, _ := ConvertFlags(Float32, Float16, 0x80000001, ModeNumeric)
	if downNeg != 0x8000 {
		t.Fatalf("negative tiny -> %04x, want 8000", downNeg)
	}
}

// Infinities convert to infinities of the same sign with no flags.
func TestInfinityConversion(t *testing.T) {
	inf16, flags, _ := ConvertFlags(Float32, Float16, 0x7F800000, ModeNumeric)
	if inf16 != 0x7C00 || flags != (Flags{}) {
		t.Fatalf("+Inf -> %04x/%v", inf16, flags)
	}
	ninf64, flags, _ := ConvertFlags(Float16, Float64, 0xFC00, ModeNumeric)
	if ninf64 != 0xFFF0000000000000 || flags != (Flags{}) {
		t.Fatalf("-Inf -> %016x/%v", ninf64, flags)
	}
}
