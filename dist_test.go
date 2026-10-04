package conv

import (
	"math/big"
	"testing"
)

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rat: " + s)
	}
	return r
}

func rats(ss ...string) []*big.Rat {
	out := make([]*big.Rat, len(ss))
	for i, s := range ss {
		out[i] = rat(s)
	}
	return out
}

// enumerate computes the sum distribution by explicitly walking every
// combination of the variables' values.
func enumerate(vars []*Dist) map[int64]*big.Rat {
	result := make(map[int64]*big.Rat)
	var rec func(k int, sum int64, prob *big.Rat)
	rec = func(k int, sum int64, prob *big.Rat) {
		if k == len(vars) {
			if s, ok := result[sum]; ok {
				s.Add(s, prob)
			} else {
				result[sum] = new(big.Rat).Set(prob)
			}
			return
		}
		for _, e := range vars[k].entries {
			rec(k+1, sum+e.Val, new(big.Rat).Mul(prob, e.Prob))
		}
	}
	rec(0, 0, big.NewRat(1, 1))
	return result
}

func checkAgainstEnumeration(t *testing.T, got *Dist, vars []*Dist) {
	t.Helper()
	want := enumerate(vars)
	entries := got.Entries()
	if len(entries) != len(want) {
		t.Fatalf("support size %d, want %d", len(entries), len(want))
	}
	for _, e := range entries {
		w, ok := want[e.Val]
		if !ok {
			t.Fatalf("unexpected value %d in convolution result", e.Val)
		}
		if e.Prob.Cmp(w) != 0 {
			t.Fatalf("P(X=%d) = %s, want %s", e.Val, e.Prob, w)
		}
	}
	// Check CDF and tails at and around every support point.
	for v := range want {
		for _, u := range []int64{v - 1, v, v + 1} {
			var le, lt, ge, gt *big.Rat
			le, lt, ge, gt = new(big.Rat), new(big.Rat), new(big.Rat), new(big.Rat)
			for wv, wp := range want {
				switch {
				case wv <= u:
					le.Add(le, wp)
				}
				if wv < u {
					lt.Add(lt, wp)
				}
				if wv >= u {
					ge.Add(ge, wp)
				}
				if wv > u {
					gt.Add(gt, wp)
				}
			}
			if got.ProbLE(u).Cmp(le) != 0 {
				t.Errorf("ProbLE(%d) = %s, want %s", u, got.ProbLE(u), le)
			}
			if got.ProbLT(u).Cmp(lt) != 0 {
				t.Errorf("ProbLT(%d) = %s, want %s", u, got.ProbLT(u), lt)
			}
			if got.ProbGE(u).Cmp(ge) != 0 {
				t.Errorf("ProbGE(%d) = %s, want %s", u, got.ProbGE(u), ge)
			}
			if got.ProbGT(u).Cmp(gt) != 0 {
				t.Errorf("ProbGT(%d) = %s, want %s", u, got.ProbGT(u), gt)
			}
		}
	}
}

func mustDist(t *testing.T, vals []int64, probs []*big.Rat) *Dist {
	t.Helper()
	d, err := NewDist(vals, probs)
	if err != nil {
		t.Fatalf("NewDist(%v): %v", vals, err)
	}
	return d
}

func TestNewDistMergesDuplicatesAndDropsZeros(t *testing.T) {
	d, err := NewDist([]int64{2, 1, 2, 3, 1}, rats("1/4", "1/8", "1/4", "0", "3/8"))
	if err != nil {
		t.Fatal(err)
	}
	entries := d.Entries()
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[0].Val != 1 || entries[0].Prob.Cmp(rat("1/2")) != 0 {
		t.Errorf("entry 0 = %+v, want {1, 1/2}", entries[0])
	}
	if entries[1].Val != 2 || entries[1].Prob.Cmp(rat("1/2")) != 0 {
		t.Errorf("entry 1 = %+v, want {2, 1/2}", entries[1])
	}
}

func TestNewDistRejectsBadInput(t *testing.T) {
	if _, err := NewDist([]int64{1, 2}, rats("1/2", "1/3")); err == nil {
		t.Error("expected error for probabilities not summing to 1")
	}
	if _, err := NewDist([]int64{1}, rats("0")); err == nil {
		t.Error("expected error for all-zero probabilities")
	}
	if _, err := NewDist([]int64{1, 2}, rats("3/2", "-1/2")); err == nil {
		t.Error("expected error for negative probability")
	}
	if _, err := NewDist(nil, nil); err == nil {
		t.Error("expected error for empty distribution")
	}
}

func TestConvolveMatchesEnumeration(t *testing.T) {
	// Includes negative values and a degenerate variable.
	a := mustDist(t, []int64{-3, -1, 2}, rats("1/6", "1/3", "1/2"))
	b := mustDist(t, []int64{-2, 0, 1, 4}, rats("1/8", "3/8", "1/4", "1/4"))
	c := Degenerate(-5)
	d := mustDist(t, []int64{0, 1}, rats("1/2", "1/2"))
	vars := []*Dist{a, b, c, d}

	got := Convolve(Convolve(Convolve(a, b), c), d)
	checkAgainstEnumeration(t, got, vars)

	s, err := NewSum(vars)
	if err != nil {
		t.Fatal(err)
	}
	checkAgainstEnumeration(t, s.Dist(), vars)
}

func TestQuantileExactBoundary(t *testing.T) {
	d := mustDist(t, []int64{-2, 0, 3}, rats("1/4", "1/4", "1/2"))
	cases := []struct {
		p    *big.Rat
		want int64
	}{
		{rat("1/4"), -2}, // p exactly at first cumulative boundary
		{rat("1/2"), 0},  // p exactly at second cumulative boundary
		{new(big.Rat).Add(rat("1/4"), rat("1/1000000")), 0},
		{new(big.Rat).Add(rat("1/2"), rat("1/1000000")), 3},
		{rat("1"), 3},
		{rat("1/1000000"), -2},
	}
	for _, c := range cases {
		got, err := d.Quantile(c.p)
		if err != nil {
			t.Fatalf("Quantile(%s): %v", c.p, err)
		}
		if got != c.want {
			t.Errorf("Quantile(%s) = %d, want %d", c.p, got, c.want)
		}
	}
	if _, err := d.Quantile(rat("0")); err == nil {
		t.Error("expected error for p = 0")
	}
	if _, err := d.Quantile(rat("3/2")); err == nil {
		t.Error("expected error for p > 1")
	}
}

func TestQuantileAgainstEnumeration(t *testing.T) {
	a := mustDist(t, []int64{-1, 2}, rats("2/5", "3/5"))
	b := mustDist(t, []int64{0, 1, 3}, rats("1/6", "1/3", "1/2"))
	vars := []*Dist{a, b}
	got := Convolve(a, b)
	want := enumerate(vars)

	// Probe every cumulative boundary of the enumerated distribution.
	sorted := got.Entries()
	cum := new(big.Rat)
	for _, e := range sorted {
		cum.Add(cum, e.Prob)
		q, err := got.Quantile(cum)
		if err != nil {
			t.Fatal(err)
		}
		if q != e.Val {
			t.Errorf("Quantile(cum=%s) = %d, want %d", cum, q, e.Val)
		}
		// Just below the boundary must give the previous value.
		eps := new(big.Rat).Sub(cum, e.Prob)
		if eps.Sign() > 0 {
			q, _ = got.Quantile(eps)
			// smallest v with CDF(v) >= cum - mass(v) is the previous support point
			var prev int64
			first := true
			for wv := range want {
				if wv < e.Val && (first || wv > prev) {
					prev = wv
					first = false
				}
			}
			if first {
				t.Fatalf("no support point below %d", e.Val)
			}
			if q != prev {
				t.Errorf("Quantile(%s) = %d, want %d", eps, q, prev)
			}
		}
	}
}

func TestSumReuseAfterSet(t *testing.T) {
	a := mustDist(t, []int64{-2, 1}, rats("1/3", "2/3"))
	b := mustDist(t, []int64{0, 5}, rats("1/4", "3/4"))
	c := Degenerate(7)
	d := mustDist(t, []int64{-1, 0, 1}, rats("1/2", "1/4", "1/4"))
	e := mustDist(t, []int64{2, 3}, rats("3/8", "5/8"))
	vars := []*Dist{a, b, c, d, e}

	s, err := NewSum(vars)
	if err != nil {
		t.Fatal(err)
	}
	checkAgainstEnumeration(t, s.Dist(), vars)

	// Replace variable 1 and verify correctness against enumeration.
	b2 := mustDist(t, []int64{-4, 0, 2}, rats("1/5", "3/10", "1/2"))
	if err := s.Set(1, b2); err != nil {
		t.Fatal(err)
	}
	vars2 := []*Dist{a, b2, c, d, e}
	checkAgainstEnumeration(t, s.Dist(), vars2)

	// Replace variable 4 and confirm the subtree over vars {0..3},
	// which does not contain variable 4, is reused rather than recomputed.
	leftBefore := s.root.left
	distBefore := leftBefore.dist
	e2 := mustDist(t, []int64{10}, rats("1"))
	if err := s.Set(4, e2); err != nil {
		t.Fatal(err)
	}
	if s.root.left != leftBefore {
		t.Error("left subtree node was replaced; expected reuse")
	}
	if s.root.left.dist != distBefore {
		t.Error("left subtree convolution was recomputed; expected reuse")
	}
	checkAgainstEnumeration(t, s.Dist(), []*Dist{a, b2, c, d, e2})
}
