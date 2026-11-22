package layout

import (
	"math/big"
	"testing"
)

func bigFalling(n, k int) *big.Int {
	out := big.NewInt(1)
	for i := 0; i < k; i++ {
		out.Mul(out, big.NewInt(int64(n-i)))
	}
	return out
}

func TestLargeRankUnrankEndpoints(t *testing.T) {
	cfg := Config{Pages: 48}
	for i := 0; i < 8; i++ {
		cfg.Regions = append(cfg.Regions, Region{Name: string(rune('a' + i)), Length: 1, Alignment: 1})
	}
	p := mustPlanner(t, cfg)
	first := Layout{Starts: []int{0, 1, 2, 3, 4, 5, 6, 7}}
	last := Layout{Starts: []int{47, 46, 45, 44, 43, 42, 41, 40}}
	r, err := p.Rank(first)
	if err != nil || r.Sign() != 0 {
		t.Fatalf("first rank = %v, %v", r, err)
	}
	r, err = p.Rank(last)
	if err != nil {
		t.Fatal(err)
	}
	one := big.NewInt(1)
	wantLast := new(big.Int).Sub(p.Count(), one)
	if r.Cmp(wantLast) != 0 {
		t.Fatalf("last rank = %v, want %v", r, wantLast)
	}
	l, err := p.Unrank(wantLast)
	if err != nil || !l.Equal(last) {
		t.Fatalf("Unrank(last) = %v, %v", l, err)
	}
}

func TestLargeSingleConstraint(t *testing.T) {
	cfg := Config{Pages: 48}
	for i := 0; i < 8; i++ {
		cfg.Regions = append(cfg.Regions, Region{Name: string(rune('a' + i)), Length: 1, Alignment: 1})
	}
	cfg.Constraints = []Constraint{{A: 0, B: 1, Modulus: 3, Allowed: []uint64{0}}}
	p := mustPlanner(t, cfg)

	// Each residue class mod 3 has 16 pages. There are 3*(16*16-16)
	// ordered pairs of distinct starts with equal residues. The remaining
	// six regions use distinct starts from the other 46 pages.
	pairs := big.NewInt(int64(3 * (16*16 - 16)))
	want := pairs.Mul(pairs, bigFalling(46, 6))
	if got := p.Count(); got.Cmp(want) != 0 {
		t.Fatalf("Count() = %v, want %v", got, want)
	}
}

func TestLargeRedundantDenseConstraints(t *testing.T) {
	cfg := Config{Pages: 48}
	for i := 0; i < 8; i++ {
		cfg.Regions = append(cfg.Regions, Region{Name: string(rune('a' + i)), Length: 1, Alignment: 1})
	}
	// Unit-length intervals overlap exactly when their starts are equal.
	// This complete constraint graph therefore adds no restriction, and
	// pruning should keep the fast subset/page DP.
	allowed := make([]uint64, 0, 47)
	for r := uint64(1); r < 48; r++ {
		allowed = append(allowed, r)
	}
	for i := 0; i < 8; i++ {
		for j := i + 1; j < 8; j++ {
			cfg.Constraints = append(cfg.Constraints, Constraint{A: i, B: j, Modulus: 48, Allowed: allowed})
		}
	}
	p := mustPlanner(t, cfg)
	for i := 0; i < 8; i++ {
		if p.modAdj[i] != 0 {
			t.Fatalf("redundant constraints were not pruned: modAdj[%d]=%08b", i, p.modAdj[i])
		}
	}
	if got, want := p.Count(), bigFalling(48, 8); got.Cmp(want) != 0 {
		t.Fatalf("Count() = %v, want %v", got, want)
	}
}
