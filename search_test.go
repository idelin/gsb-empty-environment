package netsec

import (
	"math/big"
	"testing"
)

// bruteForceBest independently enumerates every trail between the start
// and target sets and returns the maximal score together with the
// lexicographically smallest difference sequence attaining it.
func bruteForceBest(c *Cipher, starts, targets []uint16) (*big.Rat, []uint16, int) {
	tset := make(map[uint16]struct{}, len(targets))
	for _, t := range targets {
		tset[t] = struct{}{}
	}
	var best *big.Rat
	var bestSeq []uint16
	count := 0
	var rec func(depth int, d uint16, score *big.Rat, path []uint16)
	rec = func(depth int, d uint16, score *big.Rat, path []uint16) {
		if depth == c.rounds {
			if _, ok := tset[d]; !ok {
				return
			}
			count++
			if best == nil || score.Cmp(best) > 0 ||
				(score.Cmp(best) == 0 && lessSeq(path, bestSeq)) {
				best = new(big.Rat).Set(score)
				bestSeq = append([]uint16(nil), path...)
			}
			return
		}
		var opts [NibbleCount][]nibChoice
		for nib := 0; nib < NibbleCount; nib++ {
			a := (d >> (4 * nib)) & 0xF
			for b := 0; b < 16; b++ {
				if cnt := c.ddt[a][b]; cnt > 0 {
					opts[nib] = append(opts[nib], nibChoice{b: uint16(b), cnt: int64(cnt)})
				}
			}
		}
		for _, c0 := range opts[0] {
			for _, c1 := range opts[1] {
				for _, c2 := range opts[2] {
					for _, c3 := range opts[3] {
						u := c0.b | c1.b<<4 | c2.b<<8 | c3.b<<12
						nd := c.permTab[u]
						prod := c0.cnt * c1.cnt * c2.cnt * c3.cnt
						rec(depth+1, nd,
							new(big.Rat).Mul(score, big.NewRat(prod, StateSize)),
							append(append([]uint16(nil), path...), nd))
					}
				}
			}
		}
	}
	for _, s := range starts {
		rec(0, s, big.NewRat(1, 1), []uint16{s})
	}
	return best, bestSeq, count
}

func TestSearchIdentityCipher(t *testing.T) {
	c := mustCipher(t, identitySBox(), identityPerm(), 2)
	res, err := c.SearchTrails(SearchRequest{Starts: []uint16{1}, Targets: []uint16{1}})
	if err != nil {
		t.Fatalf("SearchTrails: %v", err)
	}
	if !res.Optimal {
		t.Fatalf("identity search must be complete")
	}
	if res.Best == nil {
		t.Fatalf("expected a trail")
	}
	if res.Best.Score.Cmp(big.NewRat(1, 1)) != 0 {
		t.Fatalf("identity trail score = %s, want 1", res.Best.Score)
	}
	want := []uint16{1, 1, 1}
	for i, d := range want {
		if res.Best.Differences[i] != d {
			t.Fatalf("trail = %v, want %v", res.Best.Differences, want)
		}
	}
}

func TestSearchUnreachableTarget(t *testing.T) {
	c := mustCipher(t, identitySBox(), identityPerm(), 2)
	res, err := c.SearchTrails(SearchRequest{Starts: []uint16{1}, Targets: []uint16{2}})
	if err != nil {
		t.Fatalf("SearchTrails: %v", err)
	}
	if res.Best != nil {
		t.Fatalf("expected no trail, got %v", res.Best)
	}
	if !res.Optimal {
		t.Fatalf("search must be complete even when nothing is reachable")
	}
	if res.UpperBound.Sign() != 0 {
		t.Fatalf("upper bound = %s, want 0 for an exhausted empty search", res.UpperBound)
	}

	// Zero output difference is unreachable from any non-zero start
	// because the S-box is a bijection.
	p := mustCipher(t, presentSBox(), presentPerm16(), 3)
	res, err = p.SearchTrails(SearchRequest{Starts: []uint16{5}, Targets: []uint16{0}})
	if err != nil {
		t.Fatalf("SearchTrails: %v", err)
	}
	if res.Best != nil || !res.Optimal {
		t.Fatalf("target 0 must be unreachable, got best=%v optimal=%v", res.Best, res.Optimal)
	}
}

func TestSearchValidation(t *testing.T) {
	c := mustCipher(t, identitySBox(), identityPerm(), 1)
	if _, err := c.SearchTrails(SearchRequest{Starts: nil, Targets: []uint16{1}}); err == nil {
		t.Fatalf("empty start set must be rejected")
	}
	if _, err := c.SearchTrails(SearchRequest{Starts: []uint16{0}, Targets: []uint16{1}}); err == nil {
		t.Fatalf("zero start difference must be rejected")
	}
	if _, err := c.SearchTrails(SearchRequest{Starts: []uint16{1}, Targets: nil}); err == nil {
		t.Fatalf("empty target set must be rejected")
	}
}

func TestSearchTieBreak(t *testing.T) {
	c := mustCipher(t, identitySBox(), identityPerm(), 1)
	res, err := c.SearchTrails(SearchRequest{Starts: []uint16{2, 1}, Targets: []uint16{2, 1}})
	if err != nil {
		t.Fatalf("SearchTrails: %v", err)
	}
	if res.Best == nil || res.Ties != 2 {
		t.Fatalf("best=%v ties=%d, want 2 tied trails", res.Best, res.Ties)
	}
	want := []uint16{1, 1}
	for i, d := range want {
		if res.Best.Differences[i] != d {
			t.Fatalf("tie-break picked %v, want lexicographically smallest %v", res.Best.Differences, want)
		}
	}
}

func TestSearchMatchesBruteForce(t *testing.T) {
	for _, rounds := range []int{1, 2} {
		c := mustCipher(t, presentSBox(), presentPerm16(), rounds)
		starts := []uint16{0x0007, 0x00F0}
		targets := []uint16{0x0001, 0x0011, 0x0101, 0x1111, 0x00FF}
		res, err := c.SearchTrails(SearchRequest{Starts: starts, Targets: targets})
		if err != nil {
			t.Fatalf("rounds=%d: SearchTrails: %v", rounds, err)
		}
		if !res.Optimal {
			t.Fatalf("rounds=%d: unlimited search must be complete", rounds)
		}
		best, seq, n := bruteForceBest(c, starts, targets)
		if n == 0 {
			t.Fatalf("rounds=%d: brute force found no trails", rounds)
		}
		if res.Best == nil {
			t.Fatalf("rounds=%d: search found no trail but brute force found %d", rounds, n)
		}
		if res.Best.Score.Cmp(best) != 0 {
			t.Fatalf("rounds=%d: search score %s != brute force %s", rounds, res.Best.Score, best)
		}
		if res.UpperBound.Cmp(best) != 0 {
			t.Fatalf("rounds=%d: closed upper bound %s != best %s", rounds, res.UpperBound, best)
		}
		if len(res.Best.Differences) != len(seq) {
			t.Fatalf("rounds=%d: trail length mismatch", rounds)
		}
		for i := range seq {
			if res.Best.Differences[i] != seq[i] {
				t.Fatalf("rounds=%d: tie-break trail %v != brute force %v", rounds, res.Best.Differences, seq)
			}
		}
	}
}

func TestSearchBudgetExhaustion(t *testing.T) {
	c := mustCipher(t, presentSBox(), presentPerm16(), 4)
	req := SearchRequest{
		Starts:  []uint16{0x0001},
		Targets: []uint16{0x0001, 0x0010, 0x0100, 0x1000},
		Budget:  3,
	}
	res, err := c.SearchTrails(req)
	if err != nil {
		t.Fatalf("SearchTrails: %v", err)
	}
	if res.Optimal {
		t.Fatalf("budget 3 must not close a 4-round search")
	}
	if res.Expanded > req.Budget {
		t.Fatalf("expanded %d nodes, budget was %d", res.Expanded, req.Budget)
	}
	if res.UpperBound.Sign() <= 0 {
		t.Fatalf("unexcluded upper bound must be positive, got %s", res.UpperBound)
	}
	if res.Best != nil && res.Best.Score.Cmp(res.UpperBound) > 0 {
		t.Fatalf("best %s exceeds upper bound %s", res.Best.Score, res.UpperBound)
	}

	// The not-yet-excluded upper bound must remain valid for the true
	// optimum: run a much larger budget and compare.
	req.Budget = 200000
	full, err := c.SearchTrails(req)
	if err != nil {
		t.Fatalf("SearchTrails: %v", err)
	}
	if full.Best != nil && full.Best.Score.Cmp(res.UpperBound) > 0 {
		t.Fatalf("true best %s exceeds earlier upper bound %s", full.Best.Score, res.UpperBound)
	}
	if full.Optimal && full.Best != nil && full.UpperBound.Cmp(full.Best.Score) != 0 {
		t.Fatalf("closed search must report upper bound equal to best score")
	}
}
