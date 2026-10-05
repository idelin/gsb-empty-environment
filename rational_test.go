package rowconv

import (
	"math/big"
	"math/rand"
	"testing"
)

// refRescale is the independent reference: it expresses the rescale as a
// pure rational-number computation using math/big.Rat and rounds the
// resulting rational coefficient half-to-even. It shares no code with
// RescaleDecimal.
func refRescale(coef int64, from, to int32) (q *big.Int, exact bool) {
	r := big.NewRat(coef, 1)
	p := new(big.Rat).SetInt(pow10(absDiff(to, from)))
	if to >= from {
		r.Mul(r, p)
	} else {
		r.Quo(r, p)
	}
	num, den := r.Num(), r.Denom() // den > 0 always
	qq, rem := new(big.Int), new(big.Int)
	qq.QuoRem(num, den, rem)
	if rem.Sign() == 0 {
		return qq, true
	}
	// Round |num/den| half to even by comparing 2*|rem| against den.
	twice := new(big.Int).Mul(new(big.Int).Abs(rem), big.NewInt(2))
	cmp := twice.Cmp(den)
	up := cmp > 0 || (cmp == 0 && new(big.Int).Abs(qq).Bit(0) == 1)
	if up {
		if num.Sign() < 0 {
			qq.Sub(qq, big.NewInt(1))
		} else {
			qq.Add(qq, big.NewInt(1))
		}
	}
	return qq, false
}

func absDiff(a, b int32) int32 {
	if a >= b {
		return a - b
	}
	return b - a
}

// TestRescaleAgainstRational cross-checks RescaleDecimal against the
// independent rational reference over deterministic pseudo-random cases,
// including negative coefficients, ties, and overflow boundaries.
func TestRescaleAgainstRational(t *testing.T) {
	rng := rand.New(rand.NewSource(20241005))
	magnitudes := []int64{5, 99, 12345, 1 << 20, 1 << 40, 1 << 60, 1<<62 + 97531}
	const iterations = 20000
	for i := 0; i < iterations; i++ {
		mag := magnitudes[rng.Intn(len(magnitudes))]
		coef := rng.Int63n(mag) - mag/2
		if rng.Intn(2) == 0 {
			coef = -coef
		}
		from := int32(rng.Intn(10))
		to := int32(rng.Intn(10))

		want, exact := refRescale(coef, from, to)

		// Reject mode: succeeds iff the reference says the value is exact
		// and fits int64; exact-but-too-big must report overflow.
		got, err := RescaleDecimal(coef, from, to, ModeReject)
		if exact {
			if !fitsInt64(want) {
				if err == nil || err.Code != CodeOverflow {
					t.Fatalf("coef=%d from=%d to=%d: exact but overflows, got (%d, %v)",
						coef, from, to, got, err)
				}
			} else {
				if err != nil {
					t.Fatalf("coef=%d from=%d to=%d: exact but rejected: %v", coef, from, to, err)
				}
				if big.NewInt(got).Cmp(want) != 0 {
					t.Fatalf("coef=%d from=%d to=%d: got %d, reference %s", coef, from, to, got, want)
				}
			}
		} else if err == nil || err.Code != CodeInexact {
			t.Fatalf("coef=%d from=%d to=%d: inexact but accepted (%d, %v)", coef, from, to, got, err)
		}

		// Half-even mode: must match the reference, or overflow exactly
		// when the reference does not fit int64.
		got, err = RescaleDecimal(coef, from, to, ModeHalfEven)
		if !fitsInt64(want) {
			if err == nil || err.Code != CodeOverflow {
				t.Fatalf("coef=%d from=%d to=%d: reference %s overflows, got (%d, %v)",
					coef, from, to, want, got, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("coef=%d from=%d to=%d: unexpected error %v", coef, from, to, err)
		}
		if big.NewInt(got).Cmp(want) != 0 {
			t.Fatalf("coef=%d from=%d to=%d: got %d, reference %s", coef, from, to, got, want)
		}

		// Value preservation: |got*10^-to - coef*10^-from| <= 0.5*10^-to.
		if !exact {
			orig := new(big.Rat).SetInt64(coef)
			orig.Quo(orig, new(big.Rat).SetInt(pow10(from)))
			rounded := new(big.Rat).SetInt64(got)
			rounded.Quo(rounded, new(big.Rat).SetInt(pow10(to)))
			diff := new(big.Rat).Sub(rounded, orig)
			diff.Abs(diff)
			half := new(big.Rat).SetFrac64(1, 2)
			half.Quo(half, new(big.Rat).SetInt(pow10(to)))
			if diff.Cmp(half) > 0 {
				t.Fatalf("coef=%d from=%d to=%d: rounding error %s exceeds half ulp", coef, from, to, diff)
			}
		}
	}
}
