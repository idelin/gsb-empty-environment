package floatconv

import (
	"math/big"
	"testing"
)

// exactFromBits decodes a finite (or zero) bit pattern into an exactValue.
func exactFromBits(f Format, bits uint64) exactValue {
	d := decode(f, bits)
	if d.class != classFinite && d.class != classZero {
		panic("not finite")
	}
	num := d.num
	if num == nil {
		num = new(big.Int)
	}
	return exactValue{sign: d.sign, num: num, den: big.NewInt(1), exp2: d.exp2}
}

// midpointOf returns the exact arithmetic midpoint of two exact values
// (both den == 1).
func midpointOf(a, b exactValue) exactValue {
	e := a.exp2
	if b.exp2 < e {
		e = b.exp2
	}
	num := new(big.Int).Lsh(a.num, uint(a.exp2-e))
	num.Add(num, new(big.Int).Lsh(b.num, uint(b.exp2-e)))
	return exactValue{num: num, den: big.NewInt(1), exp2: e - 1}
}

// shifted returns v + k * 2^(v.exp2-40): a value microscopically above or
// below v, still exactly representable with integers.
func shifted(v exactValue, k int64) exactValue {
	num := new(big.Int).Lsh(v.num, 40)
	num.Add(num, big.NewInt(k))
	return exactValue{num: num, den: big.NewInt(1), exp2: v.exp2 - 40}
}

func mustRound(t *testing.T, f Format, v exactValue, mode RoundingMode) (uint64, Flags) {
	t.Helper()
	return roundToFormat(f, v, mode)
}

// evenOf returns the bit pattern among lo, hi whose mantissa field is even.
func evenOf(f Format, lo, hi uint64) uint64 {
	if lo&f.mantMask()&1 == 0 {
		return lo
	}
	return hi
}

// TestMidpoints walks every adjacent pair of positive finite binary16
// values (including the subnormal range), builds the exact midpoint and
// values microscopically above and below it, and checks all four rounding
// modes. A sample of negative pairs is checked too.
func TestMidpoints(t *testing.T) {
	f := Binary16
	const maxFinite = 0x7BFF
	for lo := uint64(1); lo < maxFinite; lo++ {
		hi := lo + 1
		vLo, vHi := exactFromBits(f, lo), exactFromBits(f, hi)
		mid := midpointOf(vLo, vHi)
		above := shifted(mid, 1)
		below := shifted(mid, -1)

		even := evenOf(f, lo, hi)
		type exp struct {
			mid, above, below uint64
		}
		expect := map[RoundingMode]exp{
			RoundNearestEven:    {even, hi, lo},
			RoundTowardZero:     {lo, lo, lo},
			RoundTowardPositive: {hi, hi, hi},
			RoundTowardNegative: {lo, lo, lo},
		}
		for mode, want := range expect {
			if got, fl := mustRound(t, f, mid, mode); got != want.mid || fl&FlagInexact == 0 {
				t.Fatalf("mid lo=%#x hi=%#x mode=%v: got (%#x,%v), want %#x + inexact", lo, hi, mode, got, fl, want.mid)
			}
			if got, _ := mustRound(t, f, above, mode); got != want.above {
				t.Fatalf("above lo=%#x hi=%#x mode=%v: got %#x, want %#x", lo, hi, mode, got, want.above)
			}
			if got, _ := mustRound(t, f, below, mode); got != want.below {
				t.Fatalf("below lo=%#x hi=%#x mode=%v: got %#x, want %#x", lo, hi, mode, got, want.below)
			}
		}

		// Sampled negative side: values are -mag, so ordering flips.
		if lo%16 == 0 {
			nLo := lo | 0x8000 // smaller magnitude, greater value
			nHi := hi | 0x8000 // larger magnitude, smaller value
			vLo, vHi := exactFromBits(f, nLo), exactFromBits(f, nHi)
			mid := midpointOf(vLo, vHi)
			mid.sign = 1
			even := evenOf(f, nLo, nHi)
			negExpect := map[RoundingMode]uint64{
				RoundNearestEven:    even,
				RoundTowardZero:     nLo,
				RoundTowardPositive: nLo,
				RoundTowardNegative: nHi,
			}
			for mode, want := range negExpect {
				if got, _ := mustRound(t, f, mid, mode); got != want {
					t.Fatalf("neg mid lo=%#x hi=%#x mode=%v: got %#x, want %#x", nLo, nHi, mode, got, want)
				}
			}
		}
	}
}

// TestOverflowBoundary checks the exact midpoint between the largest
// finite binary16 value (65504) and the would-be next value 2^16, plus
// neighbors, in all rounding modes.
func TestOverflowBoundary(t *testing.T) {
	f := Binary16
	const maxFinite = 0x7BFF
	const inf = 0x7C00
	vLo := exactFromBits(f, maxFinite)
	vHi := exactValue{num: big.NewInt(1), den: big.NewInt(1), exp2: 16} // 2^16
	mid := midpointOf(vLo, vHi)                                         // 65520
	above := shifted(mid, 1)
	below := shifted(mid, -1)

	const ov = FlagOverflow | FlagInexact
	const ix = FlagInexact
	cases := []struct {
		name string
		v    exactValue
		mode RoundingMode
		want uint64
		fl   Flags
	}{
		{"mid nearest", mid, RoundNearestEven, inf, ov},
		{"mid towardZero", mid, RoundTowardZero, maxFinite, ix},
		{"mid towardPos", mid, RoundTowardPositive, inf, ov},
		{"mid towardNeg", mid, RoundTowardNegative, maxFinite, ix},
		{"above nearest", above, RoundNearestEven, inf, ov},
		{"above towardZero", above, RoundTowardZero, maxFinite, ix},
		{"above towardPos", above, RoundTowardPositive, inf, ov},
		{"above towardNeg", above, RoundTowardNegative, maxFinite, ix},
		{"below nearest", below, RoundNearestEven, maxFinite, ix},
		{"below towardZero", below, RoundTowardZero, maxFinite, ix},
		{"below towardPos", below, RoundTowardPositive, inf, ov},
		{"below towardNeg", below, RoundTowardNegative, maxFinite, ix},
	}
	for _, c := range cases {
		got, fl := roundToFormat(f, c.v, c.mode)
		if got != c.want || fl != c.fl {
			t.Errorf("%s: got (%#x,%v), want (%#x,%v)", c.name, got, fl, c.want, c.fl)
		}
	}
}

// TestUnderflowBoundary checks the midpoint between zero and the smallest
// subnormal, and the midpoint between the largest subnormal and the
// smallest normal, exercising the "tiny after rounding and inexact"
// underflow definition.
func TestUnderflowBoundary(t *testing.T) {
	f := Binary16
	const minSub = 0x0001
	const maxSub = 0x03FF
	const minNorm = 0x0400
	const ui = FlagUnderflow | FlagInexact
	const ix = FlagInexact

	// Pair (+0, minSubnormal): midpoint 2^-25.
	zero := exactFromBits(f, 0)
	vSub := exactFromBits(f, minSub)
	mid := midpointOf(zero, vSub)
	above := shifted(mid, 1)
	below := shifted(mid, -1)
	cases := []struct {
		name string
		v    exactValue
		mode RoundingMode
		want uint64
		fl   Flags
	}{
		{"mid nearest ties to zero", mid, RoundNearestEven, 0, ui},
		{"mid towardZero", mid, RoundTowardZero, 0, ui},
		{"mid towardPos", mid, RoundTowardPositive, minSub, ui},
		{"mid towardNeg", mid, RoundTowardNegative, 0, ui},
		{"above nearest", above, RoundNearestEven, minSub, ui},
		{"above towardZero", above, RoundTowardZero, 0, ui},
		{"below nearest", below, RoundNearestEven, 0, ui},
		{"below towardPos", below, RoundTowardPositive, minSub, ui},
	}
	for _, c := range cases {
		got, fl := roundToFormat(f, c.v, c.mode)
		if got != c.want || fl != c.fl {
			t.Errorf("%s: got (%#x,%v), want (%#x,%v)", c.name, got, fl, c.want, c.fl)
		}
	}

	// Pair (maxSubnormal, minNormal): midpoint 2047*2^-25. Rounding up to
	// the smallest normal is NOT tiny after rounding, so no underflow.
	vMaxSub := exactFromBits(f, maxSub)
	vMinNorm := exactFromBits(f, minNorm)
	mid2 := midpointOf(vMaxSub, vMinNorm)
	above2 := shifted(mid2, 1)
	below2 := shifted(mid2, -1)
	cases2 := []struct {
		name string
		v    exactValue
		mode RoundingMode
		want uint64
		fl   Flags
	}{
		{"mid nearest ties to minNormal", mid2, RoundNearestEven, minNorm, ix},
		{"mid towardZero", mid2, RoundTowardZero, maxSub, ui},
		{"mid towardPos", mid2, RoundTowardPositive, minNorm, ix},
		{"mid towardNeg", mid2, RoundTowardNegative, maxSub, ui},
		{"above nearest", above2, RoundNearestEven, minNorm, ix},
		{"below nearest", below2, RoundNearestEven, maxSub, ui},
		{"below towardPos", below2, RoundTowardPositive, minNorm, ix},
	}
	for _, c := range cases2 {
		got, fl := roundToFormat(f, c.v, c.mode)
		if got != c.want || fl != c.fl {
			t.Errorf("%s: got (%#x,%v), want (%#x,%v)", c.name, got, fl, c.want, c.fl)
		}
	}
}
