package groupstats

import (
	"math/big"
	"math/rand"
	"testing"
)

// rawStats recomputes count, mean and population variance directly from the
// raw samples, independently of the Summary implementation.
func rawStats(samples []*big.Int) (count *big.Int, mean, variance *big.Rat) {
	count = big.NewInt(int64(len(samples)))
	if len(samples) == 0 {
		return count, nil, nil
	}
	sum := new(big.Int)
	for _, x := range samples {
		sum.Add(sum, x)
	}
	mean = new(big.Rat).SetFrac(sum, count)
	acc := new(big.Rat)
	for _, x := range samples {
		d := new(big.Rat).SetInt(x)
		d.Sub(d, mean)
		acc.Add(acc, new(big.Rat).Mul(d, d))
	}
	variance = new(big.Rat).Quo(acc, new(big.Rat).SetInt(count))
	return count, mean, variance
}

func checkAgainstRaw(t *testing.T, s *Summary, samples []*big.Int) {
	t.Helper()
	count, mean, variance := rawStats(samples)
	if s.Count().Cmp(count) != 0 {
		t.Fatalf("count: got %v, want %v", s.Count(), count)
	}
	if mean == nil {
		if s.Mean() != nil {
			t.Fatalf("mean: got %v, want nil", s.Mean())
		}
	} else if s.Mean() == nil || s.Mean().Cmp(mean) != 0 {
		t.Fatalf("mean: got %v, want %v", s.Mean(), mean)
	}
	if variance == nil {
		if s.Variance() != nil {
			t.Fatalf("variance: got %v, want nil", s.Variance())
		}
	} else if s.Variance() == nil || s.Variance().Cmp(variance) != 0 {
		t.Fatalf("variance: got %v, want %v", s.Variance(), variance)
	}
}

func summarize(samples []*big.Int) *Summary {
	s := New()
	for _, x := range samples {
		s.Add(x)
	}
	return s
}

func huge(base string, offset int64) *big.Int {
	n, _ := new(big.Int).SetString(base, 10)
	return n.Add(n, big.NewInt(offset))
}

func TestRandomSamplesAgainstRaw(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	var samples []*big.Int
	for i := 0; i < 500; i++ {
		samples = append(samples, big.NewInt(rng.Int63n(2000001)-1000000))
	}
	// Split into groups, summarize each, then merge.
	groups := [][]*big.Int{samples[:100], samples[100:260], samples[260:261], samples[261:]}
	merged := New()
	for _, g := range groups {
		merged.Merge(summarize(g))
	}
	checkAgainstRaw(t, merged, samples)
}

func TestHugeNearEqualValues(t *testing.T) {
	base := "10000000000000000000000000000000000000000" // 10^40
	var samples []*big.Int
	for i := int64(0); i < 200; i++ {
		samples = append(samples, huge(base, i%7-3)) // offsets in [-3, 3]
	}
	// Two halves merged must equal the whole, and both must match raw math.
	half := len(samples) / 2
	merged := summarize(samples[:half])
	merged.Merge(summarize(samples[half:]))
	checkAgainstRaw(t, merged, samples)

	// 200 samples with offsets cycling -3..3: 28 full cycles (sum 0) plus
	// offsets -3,-2,-1,0, so the mean is exactly base - 3/100.
	wantMean, _ := new(big.Rat).SetString(base)
	wantMean.Sub(wantMean, big.NewRat(3, 100))
	if merged.Mean().Cmp(wantMean) != 0 {
		t.Fatalf("mean: got %v, want %v", merged.Mean(), wantMean)
	}
	// Variance of the offsets: E[x^2] = 798/200, mean offset = -3/100,
	// so var = 798/200 - 9/10000 = 39891/10000. Tiny next to the 10^40
	// base, and must be exact (catastrophic cancellation would lose it).
	if merged.Variance().Cmp(big.NewRat(39891, 10000)) != 0 {
		t.Fatalf("variance: got %v, want 39891/10000", merged.Variance())
	}
}

func TestEmptyGroups(t *testing.T) {
	e := New()
	if e.Count().Sign() != 0 || e.Mean() != nil || e.Variance() != nil {
		t.Fatalf("empty group: got count=%v mean=%v var=%v", e.Count(), e.Mean(), e.Variance())
	}
	samples := []*big.Int{big.NewInt(3), big.NewInt(-7), big.NewInt(11)}
	s := summarize(samples)

	// Merging an empty group changes nothing.
	s.Merge(New())
	checkAgainstRaw(t, s, samples)

	// Undoing an empty group is a valid no-op.
	if err := s.Unmerge(New()); err != nil {
		t.Fatalf("unmerge empty: %v", err)
	}
	checkAgainstRaw(t, s, samples)

	// Merging empties stays empty.
	e.Merge(New())
	if e.Mean() != nil || e.Variance() != nil {
		t.Fatalf("merged empties should stay empty")
	}
}

func TestMergeOrders(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	var samples []*big.Int
	for i := 0; i < 120; i++ {
		samples = append(samples, big.NewInt(rng.Int63n(100)))
	}
	// Different groupings of the same samples.
	g1 := []*Summary{summarize(samples[:30]), summarize(samples[30:90]), summarize(samples[90:])}
	g2 := []*Summary{summarize(samples[:60]), summarize(samples[60:])}
	g3 := []*Summary{summarize(samples)}

	orders := [][]int{{0, 1, 2}, {2, 0, 1}, {1, 2, 0}}
	var results []*Summary
	for _, ord := range orders {
		m := New()
		for _, i := range ord {
			m.Merge(g1[i].Clone())
		}
		results = append(results, m)
	}
	m2 := New()
	m2.Merge(g2[1].Clone())
	m2.Merge(g2[0].Clone())
	results = append(results, m2, g3[0])

	for _, r := range results {
		checkAgainstRaw(t, r, samples)
	}
}

func TestUnmergeRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	var groups [][]*big.Int
	for g := 0; g < 5; g++ {
		var grp []*big.Int
		for i := 0; i < 40; i++ {
			grp = append(grp, big.NewInt(rng.Int63n(10000)-5000))
		}
		groups = append(groups, grp)
	}
	sums := make([]*Summary, len(groups))
	total := New()
	var all []*big.Int
	for i, g := range groups {
		sums[i] = summarize(g)
		total.Merge(sums[i])
		all = append(all, g...)
	}
	checkAgainstRaw(t, total, all)

	// Undo groups 1 and 3 (out of order); remainder must match raw samples
	// of groups 0, 2, 4.
	if err := total.Unmerge(sums[1]); err != nil {
		t.Fatalf("unmerge: %v", err)
	}
	if err := total.Unmerge(sums[3]); err != nil {
		t.Fatalf("unmerge: %v", err)
	}
	var rest []*big.Int
	rest = append(rest, groups[0]...)
	rest = append(rest, groups[2]...)
	rest = append(rest, groups[4]...)
	checkAgainstRaw(t, total, rest)

	// Undo everything; the result must be exactly empty.
	for _, i := range []int{0, 2, 4} {
		if err := total.Unmerge(sums[i]); err != nil {
			t.Fatalf("unmerge: %v", err)
		}
	}
	checkAgainstRaw(t, total, nil)
}

func TestUnmergeInvalid(t *testing.T) {
	s := summarize([]*big.Int{big.NewInt(1), big.NewInt(2), big.NewInt(3)})
	before := s.Clone()

	cases := map[string]*Summary{
		"larger count":        summarize([]*big.Int{big.NewInt(1), big.NewInt(2), big.NewInt(3), big.NewInt(4)}),
		"never added values":  summarize([]*big.Int{big.NewInt(100)}),
		"impossible variance": summarize([]*big.Int{big.NewInt(1), big.NewInt(10)}),
	}
	for name, o := range cases {
		if err := s.Unmerge(o); err != ErrInvalidUnmerge {
			t.Fatalf("%s: got err=%v, want ErrInvalidUnmerge", name, err)
		}
		// Failed undo must not mutate, and must never yield a negative count.
		if s.Count().Cmp(before.Count()) != 0 ||
			s.Mean().Cmp(before.Mean()) != 0 ||
			s.Variance().Cmp(before.Variance()) != 0 {
			t.Fatalf("%s: summary mutated by rejected unmerge", name)
		}
		if s.Count().Sign() < 0 {
			t.Fatalf("%s: negative count", name)
		}
	}

	// Undoing the exact contents twice: second undo must be rejected.
	exact := summarize([]*big.Int{big.NewInt(1), big.NewInt(2), big.NewInt(3)})
	if err := s.Unmerge(exact); err != nil {
		t.Fatalf("first exact unmerge: %v", err)
	}
	checkAgainstRaw(t, s, nil)
	if err := s.Unmerge(exact); err != ErrInvalidUnmerge {
		t.Fatalf("second exact unmerge: got %v, want ErrInvalidUnmerge", err)
	}
}

func TestReducedRationals(t *testing.T) {
	s := summarize([]*big.Int{big.NewInt(2), big.NewInt(4), big.NewInt(6)})
	if got := s.Mean().RatString(); got != "4" {
		t.Fatalf("mean: got %s, want 4", got)
	}
	if got := s.Variance().RatString(); got != "8/3" {
		t.Fatalf("variance: got %s, want 8/3", got)
	}
}
