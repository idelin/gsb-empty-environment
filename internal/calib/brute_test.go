package calib

import (
	"math/big"
	"math/rand"
	"testing"
)

// TestEventIntervalAgainstBruteForce verifies the "extreme offsets suffice"
// argument by enumerating every feasible integer offset, not just the two
// extremes, on randomized feasible instances.
func TestEventIntervalAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 300; trial++ {
		// Random ground-truth parameters.
		a0 := big.NewRat(int64(rng.Intn(21)-10), int64(rng.Intn(4)+1))
		b0 := int64(rng.Intn(11) - 5)

		n := 2 + rng.Intn(4)
		var cs []Constraint
		for i := 0; i < n; i++ {
			tm := big.NewRat(int64(rng.Intn(41)-20), int64(rng.Intn(3)+1))
			g := new(big.Rat).Add(new(big.Rat).Mul(a0, tm), big.NewRat(b0, 1))
			w := big.NewRat(int64(rng.Intn(5)), 1) // half-width
			cs = append(cs, Constraint{
				ID: "x", T: tm,
				Lo: new(big.Rat).Sub(g, w),
				Hi: new(big.Rat).Add(g, w),
			})
		}
		reg := Solve(cs, Interval{})
		if !reg.IntFeasible {
			t.Fatalf("trial %d: constructed feasible instance is infeasible", trial)
		}
		if reg.BIntLo == nil || reg.BIntHi == nil {
			continue // unbounded offsets: covered by TestEventIntervalUnbounded
		}
		if reg.BIntHi.Int64()-reg.BIntLo.Int64() > 1000 {
			t.Fatalf("trial %d: offset range too wide for brute force", trial)
		}

		tau := big.NewRat(int64(rng.Intn(61)-30), int64(rng.Intn(3)+1))
		got := EventInterval(cs, Interval{}, reg, tau)

		// Brute force over every integer offset in range.
		var wantLo, wantHi *big.Rat
		for b := new(big.Int).Set(reg.BIntLo); b.Cmp(reg.BIntHi) <= 0; b.Add(b, big.NewInt(1)) {
			if v, ok := eventBoundAtOffset(cs, Interval{}, b, tau, true); ok && v != nil {
				if wantLo == nil || v.Cmp(wantLo) < 0 {
					wantLo = v
				}
			}
			if v, ok := eventBoundAtOffset(cs, Interval{}, b, tau, false); ok && v != nil {
				if wantHi == nil || v.Cmp(wantHi) > 0 {
					wantHi = v
				}
			}
		}
		if (got.Lo == nil) != (wantLo == nil) || (got.Lo != nil && got.Lo.Cmp(wantLo) != 0) {
			t.Fatalf("trial %d: lo got %v want %v", trial, RatStr(got.Lo), RatStr(wantLo))
		}
		if (got.Hi == nil) != (wantHi == nil) || (got.Hi != nil && got.Hi.Cmp(wantHi) != 0) {
			t.Fatalf("trial %d: hi got %v want %v", trial, RatStr(got.Hi), RatStr(wantHi))
		}
	}
}

// TestSolveSamplingConsistency checks that sampled feasible parameters always
// satisfy the reported ranges, and that points slightly outside the reported
// drift interval are indeed infeasible.
func TestSolveSamplingConsistency(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 200; trial++ {
		a0 := big.NewRat(int64(rng.Intn(11)-5), int64(rng.Intn(3)+1))
		b0 := int64(rng.Intn(9) - 4)
		n := 2 + rng.Intn(4)
		var cs []Constraint
		for i := 0; i < n; i++ {
			tm := big.NewRat(int64(rng.Intn(31)-15), int64(rng.Intn(2)+1))
			g := new(big.Rat).Add(new(big.Rat).Mul(a0, tm), big.NewRat(b0, 1))
			w := big.NewRat(int64(rng.Intn(4)), 1)
			cs = append(cs, Constraint{
				ID: "x", T: tm,
				Lo: new(big.Rat).Sub(g, w),
				Hi: new(big.Rat).Add(g, w),
			})
		}
		reg := Solve(cs, Interval{})
		if !reg.IntFeasible {
			t.Fatalf("trial %d: infeasible", trial)
		}
		// Ground truth must lie inside the reported ranges.
		if reg.A.Lo != nil && a0.Cmp(reg.A.Lo) < 0 || reg.A.Hi != nil && a0.Cmp(reg.A.Hi) > 0 {
			t.Fatalf("trial %d: a0=%v outside [%v, %v]", trial, a0, RatStr(reg.A.Lo), RatStr(reg.A.Hi))
		}
		b0r := big.NewRat(b0, 1)
		if reg.B.Lo != nil && b0r.Cmp(reg.B.Lo) < 0 || reg.B.Hi != nil && b0r.Cmp(reg.B.Hi) > 0 {
			t.Fatalf("trial %d: b0=%v outside [%v, %v]", trial, b0r, RatStr(reg.B.Lo), RatStr(reg.B.Hi))
		}
		// Sampled feasible points must respect the offset range.
		for s := 0; s < 50; s++ {
			a := randomAIn(rng, reg.A)
			if a == nil {
				continue
			}
			lo, hi := boundsAt(cs, a)
			if lo.Cmp(hi) > 0 {
				t.Fatalf("trial %d: a=%v in reported range but infeasible", trial, a)
			}
			if reg.B.Lo != nil && lo.Cmp(reg.B.Lo) < 0 {
				t.Fatalf("trial %d: L(a)=%v below B.Lo=%v", trial, lo, reg.B.Lo)
			}
			if reg.B.Hi != nil && hi.Cmp(reg.B.Hi) > 0 {
				t.Fatalf("trial %d: U(a)=%v above B.Hi=%v", trial, hi, reg.B.Hi)
			}
		}
		// Just outside the drift range must be infeasible.
		eps := big.NewRat(1, 997)
		if reg.A.Lo != nil {
			a := new(big.Rat).Sub(reg.A.Lo, eps)
			if lo, hi := boundsAt(cs, a); lo.Cmp(hi) <= 0 {
				t.Fatalf("trial %d: a=%v below range still feasible", trial, a)
			}
		}
		if reg.A.Hi != nil {
			a := new(big.Rat).Add(reg.A.Hi, eps)
			if lo, hi := boundsAt(cs, a); lo.Cmp(hi) <= 0 {
				t.Fatalf("trial %d: a=%v above range still feasible", trial, a)
			}
		}
	}
}

func randomAIn(rng *rand.Rand, iv Interval) *big.Rat {
	if iv.Lo == nil || iv.Hi == nil {
		return nil
	}
	// a = Lo + r*(Hi-Lo), r in [0,1]
	num := int64(rng.Intn(1001))
	r := big.NewRat(num, 1000)
	span := new(big.Rat).Sub(iv.Hi, iv.Lo)
	return new(big.Rat).Add(iv.Lo, new(big.Rat).Mul(r, span))
}

// boundsAt returns L(a), U(a): the feasible offset window for a fixed drift.
func boundsAt(cs []Constraint, a *big.Rat) (lo, hi *big.Rat) {
	for i, c := range cs {
		v := new(big.Rat).Sub(c.Lo, new(big.Rat).Mul(a, c.T))
		if i == 0 || v.Cmp(lo) > 0 {
			lo = v
		}
		u := new(big.Rat).Sub(c.Hi, new(big.Rat).Mul(a, c.T))
		if i == 0 || u.Cmp(hi) < 0 {
			hi = u
		}
	}
	return lo, hi
}

// TestEventIntervalContainsSamples verifies that randomly sampled feasible
// (a, b) pairs with integer b always produce event times inside the reported
// interval, including on instances with unbounded offsets.
func TestEventIntervalContainsSamples(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for trial := 0; trial < 200; trial++ {
		a0 := big.NewRat(int64(rng.Intn(11)-5), int64(rng.Intn(3)+1))
		b0 := int64(rng.Intn(9) - 4)
		n := 1 + rng.Intn(4)
		var cs []Constraint
		for i := 0; i < n; i++ {
			tm := big.NewRat(int64(rng.Intn(31)-15), int64(rng.Intn(2)+1))
			g := new(big.Rat).Add(new(big.Rat).Mul(a0, tm), big.NewRat(b0, 1))
			w := big.NewRat(int64(rng.Intn(4)), 1)
			cs = append(cs, Constraint{
				ID: "x", T: tm,
				Lo: new(big.Rat).Sub(g, w),
				Hi: new(big.Rat).Add(g, w),
			})
		}
		reg := Solve(cs, Interval{})
		if !reg.IntFeasible {
			t.Fatalf("trial %d: infeasible", trial)
		}
		tau := big.NewRat(int64(rng.Intn(61)-30), int64(rng.Intn(3)+1))
		iv := EventInterval(cs, Interval{}, reg, tau)

		// Sample feasible integer offsets near the ground truth.
		for db := int64(-3); db <= 3; db++ {
			b := big.NewInt(b0 + db)
			// Find a feasible a for this b by scanning rationals.
			aLo, aHi := aRangeForOffset(cs, b)
			if aLo == nil || aHi == nil || aLo.Cmp(aHi) > 0 {
				continue
			}
			for s := 0; s <= 4; s++ {
				frac := big.NewRat(int64(s), 4)
				a := new(big.Rat).Add(aLo, new(big.Rat).Mul(frac, new(big.Rat).Sub(aHi, aLo)))
				g := new(big.Rat).Add(new(big.Rat).Mul(a, tau), new(big.Rat).SetInt(b))
				if iv.Lo != nil && g.Cmp(iv.Lo) < 0 {
					t.Fatalf("trial %d: sample g=%v below Lo=%v (a=%v b=%v)", trial, g, iv.Lo, a, b)
				}
				if iv.Hi != nil && g.Cmp(iv.Hi) > 0 {
					t.Fatalf("trial %d: sample g=%v above Hi=%v (a=%v b=%v)", trial, g, iv.Hi, a, b)
				}
			}
		}
	}
}

// aRangeForOffset computes the feasible drift interval for a fixed integer
// offset, bounded on both sides (nil if unbounded or infeasible).
func aRangeForOffset(cs []Constraint, b *big.Int) (lo, hi *big.Rat) {
	bRat := new(big.Rat).SetInt(b)
	for _, c := range cs {
		if c.T.Sign() == 0 {
			if bRat.Cmp(c.Lo) < 0 || bRat.Cmp(c.Hi) > 0 {
				return nil, nil
			}
			continue
		}
		l := new(big.Rat).Quo(new(big.Rat).Sub(c.Lo, bRat), c.T)
		u := new(big.Rat).Quo(new(big.Rat).Sub(c.Hi, bRat), c.T)
		if c.T.Sign() < 0 {
			l, u = u, l
		}
		if lo == nil || l.Cmp(lo) > 0 {
			lo = l
		}
		if hi == nil || u.Cmp(hi) < 0 {
			hi = u
		}
	}
	return lo, hi
}
