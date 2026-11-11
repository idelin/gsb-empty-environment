package wafdecision

import (
	"math/rand"
	"testing"
)

// bruteWorst is an independent reference implementation that enumerates every
// legal next-leaf choice at every partial-assignment state. It intentionally
// duplicates no compiler code beyond the NNF analysis primitives, so agreement
// validates the minimax DP from a separate angle.
func bruteWorst(root node, byID map[int]int, cost []uint64, n int) uint64 {
	type state struct{ known, vals uint16 }
	memo := make(map[state]uint64)
	var rec func(known, vals uint16) uint64
	rec = func(known, vals uint16) uint64 {
		k := state{known, vals}
		if v, ok := memo[k]; ok {
			return v
		}
		if _, ok := decided(root, known, vals); ok {
			memo[k] = 0
			return 0
		}
		front := frontier(root, known, vals, nil)
		var best uint64 = ^uint64(0)
		for _, leafID := range front {
			bit := uint16(1) << uint(leafID)
			lo := rec(known|bit, vals)
			hi := rec(known|bit, vals|bit)
			cand := cost[byID[leafID]] + maxUint64(lo, hi)
			if cand < best {
				best = cand
			}
		}
		memo[k] = best
		return best
	}
	return rec(0, 0)
}

func randomFormula(rng *rand.Rand, depth, maxLeaves int) Formula {
	if depth <= 0 || maxLeaves <= 1 {
		return NewLeaf(rng.Intn(maxLeaves))
	}
	switch rng.Intn(5) {
	case 0:
		return NotOf(randomFormula(rng, depth-1, maxLeaves))
	case 1, 2:
		count := 2 + rng.Intn(3)
		kids := make([]Formula, count)
		for i := range kids {
			kids[i] = randomFormula(rng, depth-1, maxLeaves)
		}
		return AndOf(kids...)
	case 3, 4:
		count := 2 + rng.Intn(3)
		kids := make([]Formula, count)
		for i := range kids {
			kids[i] = randomFormula(rng, depth-1, maxLeaves)
		}
		return OrOf(kids...)
	default:
		return NewLeaf(rng.Intn(maxLeaves))
	}
}

func TestDPMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(20261111))
	for iter := 0; iter < 300; iter++ {
		n := 2 + rng.Intn(6) // 2..7 leaves
		f := randomCoveringFormula(rng, n)
		specs := make([]LeafSpec, n)
		for i := 0; i < n; i++ {
			lo := rng.Float64() * 0.5
			hi := lo + rng.Float64()*(1-lo)
			specs[i] = spec(i, uint64(1+rng.Intn(20)), lo, hi)
		}
		g, err := Compile(f, specs)
		if err != nil {
			t.Fatalf("iter %d: compile: %v", iter, err)
		}
		root, err := toNNF(f, false)
		if err != nil {
			t.Fatalf("nnf: %v", err)
		}
		root = simplify(root)
		byID := map[int]int{}
		cost := make([]uint64, n)
		for i, s := range specs {
			byID[s.ID] = i
			cost[i] = s.Cost
		}
		want := bruteWorst(root, byID, cost, n)
		if g.WorstCost != want {
			t.Fatalf("iter %d: DP worst=%d brute=%d", iter, g.WorstCost, want)
		}
		if _, err := g.Verify(); err != nil {
			t.Fatalf("iter %d: verify: %v", iter, err)
		}
	}
}

// randomCoveringFormula builds a random formula guaranteed to reference every
// leaf identifier, so the strict spec-vs-formula coverage check passes.
func randomCoveringFormula(rng *rand.Rand, n int) Formula {
	leaves := make([]Formula, n)
	for i := 0; i < n; i++ {
		leaves[i] = NewLeaf(i)
		if rng.Intn(3) == 0 {
			leaves[i] = NotOf(leaves[i])
		}
	}
	shuffled := rng.Perm(n)
	kids := make([]Formula, n)
	for i, idx := range shuffled {
		kids[i] = leaves[idx]
	}
	if rng.Intn(2) == 0 {
		return AndOf(kids...)
	}
	return OrOf(kids...)
}

func TestEmptyConnectivesBecomeConstants(t *testing.T) {
	// Empty OR is false, so AND(false, leaf) is a constant false formula with
	// no reachable leaf; constant-only rules are rejected as leaf-free.
	f := AndOf(NewLeaf(0), OrOf())
	specs := []LeafSpec{spec(0, 3, 0.5, 0.5)}
	if _, err := Compile(f, specs); err != ErrNoLeaves {
		t.Fatalf("constant-folded formula: got %v", err)
	}

	if _, err := Compile(AndOf(), specs); err != ErrNoLeaves {
		t.Fatalf("constant-only formula: got %v", err)
	}

	// Empty AND is true, so OR(true, leaf) is likewise constant true.
	if _, err := Compile(OrOf(NewLeaf(0), AndOf()), specs); err != ErrNoLeaves {
		t.Fatalf("constant-folded OR: got %v", err)
	}
}
