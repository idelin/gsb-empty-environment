package distconv

import (
	"math/big"
	"testing"
)

func benchDists(n int) []*Dist {
	dists := make([]*Dist, n)
	for i := 0; i < n; i++ {
		v := int64(i%5) - 2 // values in -2..2, varying per index
		dists[i] = Must([]Pair{
			{v, big.NewRat(1, 3)},
			{v + 1, big.NewRat(1, 6)},
			{v + 3, big.NewRat(1, 2)},
		})
	}
	return dists
}

func TestConvolverReuseAfterSingleUpdate(t *testing.T) {
	const n = 8
	dists := benchDists(n)
	c, err := NewConvolver(dists)
	if err != nil {
		t.Fatal(err)
	}
	first := c.Sum()
	checkAgainstEnumeration(t, first, enumerate(dists))
	if got := c.ComputedPairs(); got != n-1 {
		t.Fatalf("initial Sum performed %d pairwise convolutions, want %d", got, n-1)
	}

	// Replace one variable with a distribution including negative mass
	// support and a degenerate one in turn.
	replacements := []*Dist{
		Must([]Pair{{-11, big.NewRat(2, 7)}, {0, big.NewRat(3, 7)}, {5, big.NewRat(2, 7)}}),
		Degenerate(4),
	}
	for idx, repl := range replacements {
		before := c.ComputedPairs()
		c.Set(3, repl)
		dists[3] = repl
		got := c.Sum()
		checkAgainstEnumeration(t, got, enumerate(dists))
		// A balanced tree over 8 leaves has depth 3; only the nodes on
		// the leaf-to-root path may be recomputed.
		if recomputed := c.ComputedPairs() - before; recomputed != 3 {
			t.Errorf("update %d recomputed %d nodes, want 3 (tree depth)", idx, recomputed)
		}
	}
}

func TestConvolverNonPowerOfTwo(t *testing.T) {
	const n = 5
	dists := benchDists(n)
	c, err := NewConvolver(dists)
	if err != nil {
		t.Fatal(err)
	}
	checkAgainstEnumeration(t, c.Sum(), enumerate(dists))
	if got := c.ComputedPairs(); got != n-1 {
		t.Fatalf("initial Sum performed %d pairwise convolutions, want %d", got, n-1)
	}

	// Every single-leaf update must keep the result exact.
	for i := 0; i < n; i++ {
		repl := Must([]Pair{
			{int64(-i), big.NewRat(1, 4)},
			{int64(2 * i), big.NewRat(3, 4)},
		})
		c.Set(i, repl)
		dists[i] = repl
		checkAgainstEnumeration(t, c.Sum(), enumerate(dists))
	}
}

func TestConvolverSingleVariable(t *testing.T) {
	d := Must([]Pair{{-4, big.NewRat(1, 5)}, {9, big.NewRat(4, 5)}})
	c, err := NewConvolver([]*Dist{d})
	if err != nil {
		t.Fatal(err)
	}
	got := c.Sum()
	if got.Prob(-4).Cmp(big.NewRat(1, 5)) != 0 || got.Prob(9).Cmp(big.NewRat(4, 5)) != 0 {
		t.Errorf("single-variable sum = %s", got)
	}
	c.Set(0, Degenerate(-1))
	if got := c.Sum(); got.Prob(-1).Cmp(big.NewRat(1, 1)) != 0 {
		t.Errorf("after Set, sum = %s", got)
	}
}

func TestConvolverErrors(t *testing.T) {
	if _, err := NewConvolver(nil); err == nil {
		t.Error("expected error for no distributions")
	}
	if _, err := NewConvolver([]*Dist{Degenerate(0), nil}); err == nil {
		t.Error("expected error for nil distribution")
	}
}

func TestConvolveAllSingle(t *testing.T) {
	d := Must([]Pair{{2, big.NewRat(1, 1)}})
	got, err := ConvolveAll([]*Dist{d})
	if err != nil {
		t.Fatal(err)
	}
	if got.Prob(2).Cmp(big.NewRat(1, 1)) != 0 {
		t.Errorf("single convolution = %s", got)
	}
	if _, err := ConvolveAll(nil); err == nil {
		t.Error("expected error for empty input")
	}
}
