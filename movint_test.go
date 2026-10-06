package movint

import (
	"math/big"
	"math/rand"
	"testing"
)

func rat(n, d int64) *big.Rat { return big.NewRat(n, d) }

func mi(p0, v0, p1, v1 int64) MovingInterval {
	return MovingInterval{P0: p0, V0: v0, P1: p1, V1: v1}
}

func ti(lo, hi *big.Rat) TimeInterval { return TimeInterval{Lo: lo, Hi: hi} }

func sameTimes(got, want []TimeInterval) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i].Lo.Cmp(want[i].Lo) != 0 || got[i].Hi.Cmp(want[i].Hi) != 0 {
			return false
		}
	}
	return true
}

func TestIntersectionTimes(t *testing.T) {
	cases := []struct {
		name string
		a, b MovingInterval
		T    *big.Rat
		want []TimeInterval
	}{
		{"static overlap", mi(0, 0, 10, 0), mi(5, 0, 15, 0), rat(10, 1),
			[]TimeInterval{ti(rat(0, 1), rat(10, 1))}},
		{"static disjoint", mi(0, 0, 1, 0), mi(5, 0, 6, 0), rat(10, 1),
			nil},
		{"enter and leave", mi(0, 0, 1, 0), mi(2, -1, 3, -1), rat(5, 1),
			[]TimeInterval{ti(rat(1, 1), rat(3, 1))}},
		{"instant touch", mi(0, 0, 0, 0), mi(1, -1, 1, -1), rat(2, 1),
			[]TimeInterval{ti(rat(1, 1), rat(1, 1))}},
		{"crossing endpoints two pieces", mi(0, 0, 1, 0), mi(-10, 2, 20, -1), rat(25, 1),
			[]TimeInterval{ti(rat(0, 1), rat(11, 2)), ti(rat(19, 1), rat(25, 1))}},
		{"touch at T", mi(0, 0, 0, 0), mi(-2, 1, -2, 1), rat(2, 1),
			[]TimeInterval{ti(rat(2, 1), rat(2, 1))}},
		{"touch at 0", mi(0, 0, 0, 0), mi(0, 1, 0, 1), rat(2, 1),
			[]TimeInterval{ti(rat(0, 1), rat(0, 1))}},
		{"zero T touch", mi(0, 0, 1, 0), mi(1, 0, 2, 0), rat(0, 1),
			[]TimeInterval{ti(rat(0, 1), rat(0, 1))}},
		{"zero T disjoint", mi(0, 0, 1, 0), mi(2, 0, 3, 0), rat(0, 1),
			nil},
		{"always contains", mi(0, 0, 10, 0), mi(3, 0, 4, 0), rat(7, 1),
			[]TimeInterval{ti(rat(0, 1), rat(7, 1))}},
		{"crossing keeps containing", mi(0, 0, 0, 0), mi(-1, 1, 1, -1), rat(2, 1),
			[]TimeInterval{ti(rat(0, 1), rat(2, 1))}},
		{"rational T", mi(0, 0, 1, 0), mi(2, -1, 3, -1), rat(7, 2),
			[]TimeInterval{ti(rat(1, 1), rat(3, 1))}},
		{"fractional root", mi(0, 0, 0, 0), mi(1, -3, 1, -3), rat(2, 1),
			[]TimeInterval{ti(rat(1, 3), rat(1, 3))}},
		{"meet at crossing point", mi(0, 0, 0, 0), mi(-1, 1, 1, -1), rat(1, 1),
			[]TimeInterval{ti(rat(0, 1), rat(1, 1))}},
	}
	for _, c := range cases {
		got := IntersectionTimes(c.a, c.b, c.T)
		if !sameTimes(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, formatTimes(got), formatTimes(c.want))
			continue
		}
		if err := Verify(c.a, c.b, c.T, got); err != nil {
			t.Errorf("%s: Verify failed: %v", c.name, err)
		}
	}
}

func formatTimes(ts []TimeInterval) string {
	s := "["
	for i, iv := range ts {
		if i > 0 {
			s += " "
		}
		s += "[" + iv.Lo.RatString() + "," + iv.Hi.RatString() + "]"
	}
	return s + "]"
}

func inAny(ts []TimeInterval, t *big.Rat) bool {
	for _, iv := range ts {
		if t.Cmp(iv.Lo) >= 0 && t.Cmp(iv.Hi) <= 0 {
			return true
		}
	}
	return false
}

// TestRandomDense cross-checks IntersectionTimes against direct evaluation
// on a dense rational grid, and runs the interior/exterior sampler.
func TestRandomDense(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	randInt := func(lo, hi int64) int64 { return lo + rng.Int63n(hi-lo+1) }
	for iter := 0; iter < 500; iter++ {
		a := mi(randInt(-8, 8), randInt(-4, 4), randInt(-8, 8), randInt(-4, 4))
		b := mi(randInt(-8, 8), randInt(-4, 4), randInt(-8, 8), randInt(-4, 4))
		T := rat(randInt(1, 20), randInt(1, 3))

		got := IntersectionTimes(a, b, T)
		if err := Verify(a, b, T, got); err != nil {
			t.Fatalf("iter %d: a=%+v b=%+v T=%v: %v (got %v)", iter, a, b, T, err, formatTimes(got))
		}

		// Dense per-sample comparison: classification at every grid point
		// must agree with membership in the returned closed intervals.
		const grid = 160
		for k := int64(0); k <= grid; k++ {
			ts := new(big.Rat).Mul(T, rat(k, grid))
			if a.IntersectsAt(b, ts) != inAny(got, ts) {
				t.Fatalf("iter %d: mismatch at t=%v: a=%+v b=%+v got %v",
					iter, ts, a, b, formatTimes(got))
			}
		}
	}
}

// TestIntersectAll checks that the batch helper agrees with per-pair calls.
func TestIntersectAll(t *testing.T) {
	as := []MovingInterval{mi(0, 0, 1, 0), mi(-3, 1, 5, -1), mi(2, 2, 2, 2)}
	bs := []MovingInterval{mi(4, -1, 6, -1), mi(0, 0, 0, 0)}
	T := rat(9, 2)

	results := IntersectAll(as, bs, T)
	if len(results) != len(as)*len(bs) {
		t.Fatalf("got %d results, want %d", len(results), len(as)*len(bs))
	}
	for _, r := range results {
		want := IntersectionTimes(as[r.A], bs[r.B], T)
		if !sameTimes(r.Times, want) {
			t.Errorf("pair (%d,%d): got %v, want %v", r.A, r.B, formatTimes(r.Times), formatTimes(want))
		}
		if err := Verify(as[r.A], bs[r.B], T, r.Times); err != nil {
			t.Errorf("pair (%d,%d): Verify failed: %v", r.A, r.B, err)
		}
	}
}
