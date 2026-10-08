package groupstats

import (
	"math/big"
	"math/rand"
	"testing"
)

// refStats recomputes count, mean and population variance directly from
// raw samples using exact rational arithmetic.
func refStats(samples []int64) (*big.Int, *big.Rat, *big.Rat) {
	n := big.NewInt(int64(len(samples)))
	if len(samples) == 0 {
		return n, nil, nil
	}
	sum := new(big.Int)
	sumSq := new(big.Int)
	for _, x := range samples {
		bx := big.NewInt(x)
		sum.Add(sum, bx)
		sumSq.Add(sumSq, new(big.Int).Mul(bx, bx))
	}
	mean := new(big.Rat).SetFrac(sum, n)
	num := new(big.Int).Mul(sumSq, n)
	num.Sub(num, new(big.Int).Mul(sum, sum))
	variance := new(big.Rat).SetFrac(num, new(big.Int).Mul(n, n))
	return n, mean, variance
}

func checkAgainstRaw(t *testing.T, s *Summary, raw []int64) {
	t.Helper()
	n, mean, variance := refStats(raw)
	if s.Count().Cmp(n) != 0 {
		t.Fatalf("count: got %v want %v", s.Count(), n)
	}
	if len(raw) == 0 {
		if _, err := s.Mean(); err != ErrEmpty {
			t.Fatalf("Mean on empty: got %v want ErrEmpty", err)
		}
		if _, err := s.Variance(); err != ErrEmpty {
			t.Fatalf("Variance on empty: got %v want ErrEmpty", err)
		}
		return
	}
	gotMean, err := s.Mean()
	if err != nil {
		t.Fatalf("Mean: %v", err)
	}
	if gotMean.Cmp(mean) != 0 {
		t.Fatalf("mean: got %v want %v", gotMean, mean)
	}
	gotVar, err := s.Variance()
	if err != nil {
		t.Fatalf("Variance: %v", err)
	}
	if gotVar.Cmp(variance) != 0 {
		t.Fatalf("variance: got %v want %v", gotVar, variance)
	}
}

func flatten(groups [][]int64) []int64 {
	var out []int64
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

func TestMergeMatchesRawSamples(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 50; trial++ {
		var groups [][]int64
		s := NewSummary()
		var raw []int64
		for g := 0; g < 5; g++ {
			m := rng.Intn(20)
			grp := make([]int64, m)
			for i := range grp {
				grp[i] = int64(rng.Intn(2001) - 1000)
			}
			groups = append(groups, grp)
			s.Merge(SummaryOf(grp...))
			raw = append(raw, grp...)
		}
		checkAgainstRaw(t, s, raw)
	}
}

func TestHugeCloseValues(t *testing.T) {
	// Values clustered around 2^62: naive float variance would
	// catastrophically cancel; exact rational arithmetic must not.
	base := int64(1) << 62
	samples := []int64{base, base + 1, base + 2, base - 3, base + 3, base}
	checkAgainstRaw(t, SummaryOf(samples...), samples)

	// Hand-checked: {B, B+2} has mean B+1 and variance 1.
	s := SummaryOf(base, base+2)
	mean, err := s.Mean()
	if err != nil {
		t.Fatal(err)
	}
	if mean.Cmp(new(big.Rat).SetInt64(base+1)) != 0 {
		t.Fatalf("mean: got %v", mean)
	}
	variance, err := s.Variance()
	if err != nil {
		t.Fatal(err)
	}
	if variance.Cmp(big.NewRat(1, 1)) != 0 {
		t.Fatalf("variance: got %v want 1", variance)
	}
}

func TestEmptyGroup(t *testing.T) {
	s := NewSummary()
	if _, err := s.Mean(); err != ErrEmpty {
		t.Fatalf("Mean on empty: got %v want ErrEmpty", err)
	}
	if _, err := s.Variance(); err != ErrEmpty {
		t.Fatalf("Variance on empty: got %v want ErrEmpty", err)
	}

	// Merging empty groups is a no-op.
	s.Merge(NewSummary())
	s.Merge(SummaryOf())
	s.Merge(SummaryOf(7, -9))
	s.Merge(NewSummary())
	checkAgainstRaw(t, s, []int64{7, -9})

	// Undoing an empty summary is a no-op.
	if err := s.Undo(NewSummary()); err != nil {
		t.Fatalf("undo empty: %v", err)
	}
	checkAgainstRaw(t, s, []int64{7, -9})
}

func TestMergeOrderInvariance(t *testing.T) {
	groups := [][]int64{
		{1, 2, 3},
		{},
		{-5, 17, 1000, -1000},
		{42},
		{8, 8, 8, 8, 8},
	}
	raw := flatten(groups)
	orders := [][]int{
		{0, 1, 2, 3, 4},
		{4, 3, 2, 1, 0},
		{2, 0, 4, 1, 3},
		{1, 3, 0, 4, 2},
	}
	for _, order := range orders {
		s := NewSummary()
		for _, i := range order {
			s.Merge(SummaryOf(groups[i]...))
		}
		checkAgainstRaw(t, s, raw)
	}
}

func TestReducedRationals(t *testing.T) {
	s := SummaryOf(0, 0, 0, 1)
	mean, _ := s.Mean()
	if mean.Cmp(big.NewRat(1, 4)) != 0 {
		t.Fatalf("mean: got %v want 1/4", mean)
	}
	variance, _ := s.Variance()
	if variance.Cmp(big.NewRat(3, 16)) != 0 {
		t.Fatalf("variance: got %v want 3/16", variance)
	}
	// Denominators must be in lowest terms (big.Rat invariant).
	if mean.Denom().Cmp(big.NewInt(4)) != 0 || variance.Denom().Cmp(big.NewInt(16)) != 0 {
		t.Fatalf("not reduced: mean=%v var=%v", mean, variance)
	}
}

func TestUndo(t *testing.T) {
	a := SummaryOf(1, 2, 3)
	b := SummaryOf(-10, 20)
	c := SummaryOf(7)

	s := NewSummary()
	s.Merge(a)
	s.Merge(b)
	s.Merge(c)
	checkAgainstRaw(t, s, []int64{1, 2, 3, -10, 20, 7})

	if err := s.Undo(b); err != nil {
		t.Fatalf("undo b: %v", err)
	}
	checkAgainstRaw(t, s, []int64{1, 2, 3, 7})

	if err := s.Undo(a); err != nil {
		t.Fatalf("undo a: %v", err)
	}
	if err := s.Undo(c); err != nil {
		t.Fatalf("undo c: %v", err)
	}
	if s.Count().Sign() != 0 {
		t.Fatalf("count after undoing all: got %v", s.Count())
	}
	if _, err := s.Mean(); err != ErrEmpty {
		t.Fatalf("Mean after undoing all: got %v want ErrEmpty", err)
	}
}

func TestUndoPartialGroup(t *testing.T) {
	// Undoing a sub-summary of an added group is legal.
	s := SummaryOf(4, 4, 4, 10)
	if err := s.Undo(SummaryOf(4, 4)); err != nil {
		t.Fatalf("undo partial: %v", err)
	}
	checkAgainstRaw(t, s, []int64{4, 10})
}

func TestInvalidUndoRejected(t *testing.T) {
	cases := []struct {
		name string
		base []int64
		undo []int64
	}{
		{"never added", []int64{1, 2}, []int64{5}},
		{"too many samples", []int64{1, 2}, []int64{1, 2, 3}},
		{"undo from empty", nil, []int64{1}},
		{"count ok but wrong values", []int64{1, 3}, []int64{2, 2}},
		{"impossible residual spread", []int64{0, 10}, []int64{5, 5}},
		{"leftover mass at zero count", []int64{1, 3}, []int64{1, 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := SummaryOf(tc.base...)
			before := s.Clone()
			if err := s.Undo(SummaryOf(tc.undo...)); err != ErrInvalidUndo {
				t.Fatalf("undo: got %v want ErrInvalidUndo", err)
			}
			// State must be unchanged.
			checkAgainstRaw(t, s, tc.base)
			if s.Count().Cmp(before.Count()) != 0 {
				t.Fatalf("count mutated on failed undo")
			}
			if s.Count().Sign() < 0 {
				t.Fatalf("negative count")
			}
		})
	}
}

func TestDoubleUndoRejected(t *testing.T) {
	b := SummaryOf(3, 9)
	s := NewSummary()
	s.Merge(b)
	if err := s.Undo(b); err != nil {
		t.Fatalf("first undo: %v", err)
	}
	if err := s.Undo(b); err != ErrInvalidUndo {
		t.Fatalf("second undo: got %v want ErrInvalidUndo", err)
	}
	if s.Count().Sign() != 0 {
		t.Fatalf("count: got %v want 0", s.Count())
	}
}

func TestUndoRandomizedAgainstRaw(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 50; trial++ {
		var groups [][]int64
		for g := 0; g < 6; g++ {
			grp := make([]int64, rng.Intn(10))
			for i := range grp {
				grp[i] = int64(rng.Intn(41) - 20)
			}
			groups = append(groups, grp)
		}
		s := NewSummary()
		for _, g := range groups {
			s.Merge(SummaryOf(g...))
		}
		// Undo a random subset in random order.
		perm := rng.Perm(len(groups))
		cut := rng.Intn(len(groups) + 1)
		undone := make(map[int]bool)
		for _, i := range perm[:cut] {
			if err := s.Undo(SummaryOf(groups[i]...)); err != nil {
				t.Fatalf("undo group %d: %v", i, err)
			}
			undone[i] = true
		}
		var remaining []int64
		for i, g := range groups {
			if !undone[i] {
				remaining = append(remaining, g...)
			}
		}
		checkAgainstRaw(t, s, remaining)
	}
}
