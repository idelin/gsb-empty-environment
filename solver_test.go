package plsolver

import (
	"math/big"
	"math/rand"
	"sort"
	"testing"
)

func rat(n, d int64) *big.Rat { return big.NewRat(n, d) }

// bruteForce is an independent reference implementation. It enumerates
// every breakpoint (duplicates merged here as well), evaluates f
// exactly at each breakpoint and at each segment midpoint, and derives
// flat segments from midpoint values rather than from slopes. The
// minimum over a linear segment is attained at its endpoints, so the
// sample set {breakpoints} ∪ {midpoints} determines the optimum; a
// segment is fully optimal iff its midpoint attains the minimum (the
// midpoint is the average of the endpoints, so this forces the segment
// to be flat at the minimum).
func bruteForce(s *Solver) (*big.Rat, []Interval) {
	raw := []*big.Rat{new(big.Rat).Set(s.lo), new(big.Rat).Set(s.hi)}
	for _, t := range s.terms {
		if t.kind == absTerm && t.a.Cmp(s.lo) > 0 && t.a.Cmp(s.hi) < 0 {
			raw = append(raw, new(big.Rat).Set(t.a))
		}
	}
	sort.Slice(raw, func(i, j int) bool { return raw[i].Cmp(raw[j]) < 0 })
	pts := raw[:1]
	for _, p := range raw[1:] {
		if p.Cmp(pts[len(pts)-1]) != 0 {
			pts = append(pts, p)
		}
	}

	n := len(pts)
	vals := make([]*big.Rat, n)
	for i, p := range pts {
		vals[i] = s.Eval(p)
	}
	midVals := make([]*big.Rat, n-1)
	for k := range midVals {
		mid := new(big.Rat).Add(pts[k], pts[k+1])
		mid.Quo(mid, big.NewRat(2, 1))
		midVals[k] = s.Eval(mid)
	}

	min := new(big.Rat).Set(vals[0])
	for _, v := range vals[1:] {
		if v.Cmp(min) < 0 {
			min.Set(v)
		}
	}
	for _, v := range midVals {
		if v.Cmp(min) < 0 {
			min.Set(v)
		}
	}

	optPoint := make([]bool, n)
	for i, v := range vals {
		optPoint[i] = v.Cmp(min) == 0
	}
	optSeg := make([]bool, n-1)
	for k, v := range midVals {
		optSeg[k] = v.Cmp(min) == 0
	}

	var ivs []Interval
	for i := 0; i < n; {
		if !optPoint[i] {
			i++
			continue
		}
		j := i
		for j < n-1 && optSeg[j] {
			j++
		}
		ivs = append(ivs, Interval{Lo: pts[i], Hi: pts[j]})
		i = j + 1
	}
	return min, ivs
}

func checkIntervals(t *testing.T, got []Interval, want [][2]*big.Rat) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("intervals: got %v, want %v", got, want)
	}
	for i, iv := range got {
		if iv.Lo.Cmp(want[i][0]) != 0 || iv.Hi.Cmp(want[i][1]) != 0 {
			t.Fatalf("intervals: got %v, want %v", got, want)
		}
	}
}

// checkConsistency verifies the slope certificates against exact
// evaluations and cross-checks the optimum against bruteForce.
func checkConsistency(t *testing.T, s *Solver, res *Result) {
	t.Helper()

	refMin, refIvs := bruteForce(s)
	if res.Min.Cmp(refMin) != 0 {
		t.Fatalf("min: got %s, brute force %s", res.Min, refMin)
	}
	if len(res.Intervals) != len(refIvs) {
		t.Fatalf("intervals: got %v, brute force %v", res.Intervals, refIvs)
	}
	for i := range res.Intervals {
		if res.Intervals[i].Lo.Cmp(refIvs[i].Lo) != 0 ||
			res.Intervals[i].Hi.Cmp(refIvs[i].Hi) != 0 {
			t.Fatalf("intervals: got %v, brute force %v", res.Intervals, refIvs)
		}
	}

	// Slope certificate: f(hi) - f(lo) == slope * (hi - lo) per segment.
	for _, seg := range res.Segments {
		lhs := new(big.Rat).Sub(s.Eval(seg.Hi), s.Eval(seg.Lo))
		rhs := new(big.Rat).Sub(seg.Hi, seg.Lo)
		rhs.Mul(rhs, seg.Slope)
		if lhs.Cmp(rhs) != 0 {
			t.Fatalf("slope certificate violated on (%s, %s)", seg.Lo, seg.Hi)
		}
	}

	// Every reported optimal point attains the minimum; optimal
	// intervals are sorted and disjoint.
	for _, iv := range res.Intervals {
		if s.Eval(iv.Lo).Cmp(res.Min) != 0 || s.Eval(iv.Hi).Cmp(res.Min) != 0 {
			t.Fatalf("interval %v does not attain min %s", iv, res.Min)
		}
	}
	for i := 1; i < len(res.Intervals); i++ {
		if res.Intervals[i-1].Hi.Cmp(res.Intervals[i].Lo) >= 0 {
			t.Fatalf("intervals not sorted/disjoint: %v", res.Intervals)
		}
	}
}

func TestFlatMedianSegment(t *testing.T) {
	s := NewSolver(rat(0, 1), rat(10, 1))
	for _, a := range []int64{1, 2, 3, 4} {
		s.AddAbs(rat(1, 1), rat(a, 1))
	}
	res := s.Solve()
	if res.Min.Cmp(rat(4, 1)) != 0 {
		t.Fatalf("min = %s, want 4", res.Min)
	}
	checkIntervals(t, res.Intervals, [][2]*big.Rat{{rat(2, 1), rat(3, 1)}})
	checkConsistency(t, s, res)
}

func TestNegativeWeightEndpointOptima(t *testing.T) {
	// f = -|x| on [-3, 3]: concave spike, minima at both endpoints.
	s := NewSolver(rat(-3, 1), rat(3, 1))
	s.AddAbs(rat(-1, 1), rat(0, 1))
	res := s.Solve()
	if res.Min.Cmp(rat(-3, 1)) != 0 {
		t.Fatalf("min = %s, want -3", res.Min)
	}
	checkIntervals(t, res.Intervals, [][2]*big.Rat{
		{rat(-3, 1), rat(-3, 1)},
		{rat(3, 1), rat(3, 1)},
	})
	checkConsistency(t, s, res)
}

func TestLinearTiltFlatRay(t *testing.T) {
	// f = |x| + x on [-3, 5]: slope 0 on [-3, 0], slope 2 on [0, 5].
	s := NewSolver(rat(-3, 1), rat(5, 1))
	s.AddAbs(rat(1, 1), rat(0, 1))
	s.AddLinear(rat(1, 1), rat(0, 1))
	res := s.Solve()
	if res.Min.Cmp(rat(0, 1)) != 0 {
		t.Fatalf("min = %s, want 0", res.Min)
	}
	checkIntervals(t, res.Intervals, [][2]*big.Rat{{rat(-3, 1), rat(0, 1)}})
	checkConsistency(t, s, res)
}

func TestDuplicateBreakpointsMerged(t *testing.T) {
	// 2|x-1| + 3|x-1| behaves as 5|x-1|; breakpoint 1 must appear once.
	s := NewSolver(rat(0, 1), rat(4, 1))
	s.AddAbs(rat(2, 1), rat(1, 1))
	s.AddAbs(rat(3, 1), rat(1, 1))
	res := s.Solve()
	if res.Min.Cmp(rat(0, 1)) != 0 {
		t.Fatalf("min = %s, want 0", res.Min)
	}
	checkIntervals(t, res.Intervals, [][2]*big.Rat{{rat(1, 1), rat(1, 1)}})
	count := 0
	for _, p := range res.Points {
		if p.X.Cmp(rat(1, 1)) == 0 {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("breakpoint 1 appears %d times, want 1", count)
	}
	checkConsistency(t, s, res)
}

func TestEndpointOptimumLinear(t *testing.T) {
	s := NewSolver(rat(1, 1), rat(2, 1))
	s.AddLinear(rat(1, 1), rat(0, 1))
	res := s.Solve()
	if res.Min.Cmp(rat(1, 1)) != 0 {
		t.Fatalf("min = %s, want 1", res.Min)
	}
	checkIntervals(t, res.Intervals, [][2]*big.Rat{{rat(1, 1), rat(1, 1)}})
	checkConsistency(t, s, res)
}

func TestNonConvexFlatAndPointComponents(t *testing.T) {
	// f = -2|x| + |x-2| + |x+2| on [-3, 3]: optimal set is the union of
	// the flat segment [-3,-2] and the flat segment [2,3], separated by
	// a non-convex bump.
	s := NewSolver(rat(-3, 1), rat(3, 1))
	s.AddAbs(rat(-2, 1), rat(0, 1))
	s.AddAbs(rat(1, 1), rat(2, 1))
	s.AddAbs(rat(1, 1), rat(-2, 1))
	res := s.Solve()
	if res.Min.Cmp(rat(0, 1)) != 0 {
		t.Fatalf("min = %s, want 0", res.Min)
	}
	checkIntervals(t, res.Intervals, [][2]*big.Rat{
		{rat(-3, 1), rat(-2, 1)},
		{rat(2, 1), rat(3, 1)},
	})
	checkConsistency(t, s, res)
}

func TestEmptyObjective(t *testing.T) {
	s := NewSolver(rat(-1, 1), rat(2, 1))
	res := s.Solve()
	if res.Min.Cmp(rat(0, 1)) != 0 {
		t.Fatalf("min = %s, want 0", res.Min)
	}
	checkIntervals(t, res.Intervals, [][2]*big.Rat{{rat(-1, 1), rat(2, 1)}})
	checkConsistency(t, s, res)
}

func TestDegenerateDomain(t *testing.T) {
	s := NewSolver(rat(3, 2), rat(3, 2))
	s.AddAbs(rat(-7, 3), rat(0, 1))
	s.AddLinear(rat(2, 1), rat(-1, 1))
	res := s.Solve()
	want := s.Eval(rat(3, 2))
	if res.Min.Cmp(want) != 0 {
		t.Fatalf("min = %s, want %s", res.Min, want)
	}
	checkIntervals(t, res.Intervals, [][2]*big.Rat{{rat(3, 2), rat(3, 2)}})
	checkConsistency(t, s, res)
}

func TestAddRemove(t *testing.T) {
	s := NewSolver(rat(-5, 1), rat(5, 1))
	id := s.AddAbs(rat(2, 1), rat(1, 1))
	s.AddAbs(rat(-1, 1), rat(-2, 1))
	s.AddLinear(rat(1, 2), rat(3, 1))
	before := s.Solve()
	checkConsistency(t, s, before)

	if !s.Remove(id) {
		t.Fatal("Remove reported missing id")
	}
	if s.Remove(id) {
		t.Fatal("Remove succeeded twice")
	}

	fresh := NewSolver(rat(-5, 1), rat(5, 1))
	fresh.AddAbs(rat(-1, 1), rat(-2, 1))
	fresh.AddLinear(rat(1, 2), rat(3, 1))
	got, want := s.Solve(), fresh.Solve()
	if got.Min.Cmp(want.Min) != 0 {
		t.Fatalf("after removal: min %s, fresh solver %s", got.Min, want.Min)
	}
	if len(got.Intervals) != len(want.Intervals) {
		t.Fatalf("after removal: intervals %v, fresh %v", got.Intervals, want.Intervals)
	}
	for i := range got.Intervals {
		if got.Intervals[i].Lo.Cmp(want.Intervals[i].Lo) != 0 ||
			got.Intervals[i].Hi.Cmp(want.Intervals[i].Hi) != 0 {
			t.Fatalf("after removal: intervals %v, fresh %v", got.Intervals, want.Intervals)
		}
	}
	checkConsistency(t, s, got)
}

func TestRandomCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	// Small point set to force duplicate breakpoints; rationals with
	// denominators 1 and 2.
	points := []*big.Rat{
		rat(-3, 1), rat(-3, 1), rat(-1, 1), rat(0, 1), rat(0, 1),
		rat(1, 2), rat(1, 1), rat(2, 1), rat(4, 1), rat(7, 2),
	}
	for trial := 0; trial < 1000; trial++ {
		lo := int64(rng.Intn(6) - 6)
		hi := lo + int64(rng.Intn(9))
		s := NewSolver(rat(lo, 1), rat(hi, 1))
		nTerms := rng.Intn(9)
		for i := 0; i < nTerms; i++ {
			w := rat(int64(rng.Intn(9)-4), int64(rng.Intn(2)+1)) // may be negative or zero
			if rng.Intn(3) == 0 {
				s.AddLinear(w, rat(int64(rng.Intn(5)-2), 1))
			} else {
				s.AddAbs(w, points[rng.Intn(len(points))])
			}
		}
		checkConsistency(t, s, s.Solve())
	}
}
