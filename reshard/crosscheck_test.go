package reshard

import (
	"math/big"
	"math/rand"
	"testing"
)

// randomLayout draws a uniformly structured valid layout for the cross-check.
func randomLayout(rng *rand.Rand, shape, ranks []int64) (Layout, bool) {
	meshAxes := len(ranks)
	shard := make([]int, len(shape))
	used := make([]bool, meshAxes)
	for d := range shape {
		if rng.Intn(2) == 0 {
			shard[d] = -1
			continue
		}
		candidates := []int{}
		for a := 0; a < meshAxes; a++ {
			if !used[a] && shape[d]%ranks[a] == 0 {
				candidates = append(candidates, a)
			}
		}
		if len(candidates) == 0 {
			shard[d] = -1
			continue
		}
		a := candidates[rng.Intn(len(candidates))]
		shard[d] = a
		used[a] = true
	}
	partial := make([]bool, meshAxes)
	for a := 0; a < meshAxes; a++ {
		if !used[a] && rng.Intn(2) == 1 {
			partial[a] = true
		}
	}
	return Layout{Shard: shard, Partial: partial}, true
}

// TestSmallGraphShortestPathCrossCheck runs 200 small random problems and
// compares Dijkstra against the independent Bellman-Ford oracle, including
// reachability, exact scaled cost, and simulated semantic equivalence.
func TestSmallGraphShortestPathCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(20261126))
	meshSizes := [][]int64{{2, 2}, {2, 3}, {3, 2}, {2, 2, 2}}
	shapePool := [][]int64{
		{2}, {4}, {6},
		{2, 2}, {2, 4}, {4, 6}, {6, 4},
		{2, 2, 2}, {2, 4, 6}, {4, 4, 4},
		{2, 2, 2, 2}, {2, 4, 6, 4},
	}
	reachable := 0
	for iter := 0; iter < 200; iter++ {
		ranks := meshSizes[rng.Intn(len(meshSizes))]
		shape := shapePool[rng.Intn(len(shapePool))]
		weights := []int64{int64(1 + rng.Intn(5)), int64(1 + rng.Intn(5))}
		if len(ranks) == 3 {
			weights = append(weights, int64(1+rng.Intn(5)))
		}
		src, _ := randomLayout(rng, shape, ranks)
		dst, _ := randomLayout(rng, shape, ranks)
		p := Problem{
			Shape:           shape,
			BytesPerElement: int64(1 + rng.Intn(4)),
			Mesh:            Mesh{Ranks: ranks, Weights: weights},
			Source:          src,
			Target:          dst,
		}
		sol, err := Solve(p)
		bfCost, bfReachable := bellmanFord(p, newState(src), newState(dst))
		if err != nil {
			if bfReachable {
				t.Fatalf("iter %d: Solve said unreachable, Bellman-Ford reached cost %s",
					iter, bfCost)
			}
			continue
		}
		if !bfReachable {
			t.Fatalf("iter %d: Solve returned a plan but Bellman-Ford found none", iter)
		}
		if sol.ScaledCost.Cmp(bfCost) != 0 {
			t.Fatalf("iter %d: Dijkstra cost %s != Bellman-Ford cost %s",
				iter, sol.ScaledCost, bfCost)
		}
		if got := simulatedCost(t, p, sol.Ops); got.Cmp(sol.Cost) != 0 {
			t.Fatalf("iter %d: simulation cost %s != solver cost %s", iter, got, sol.Cost)
		}
		if rat := new(big.Rat).SetFrac(sol.ScaledCost, big.NewInt(sol.Scale)); rat.Cmp(sol.Cost) != 0 {
			t.Fatalf("iter %d: Cost/ScaledCost inconsistency", iter)
		}
		reachable++
	}
	if reachable == 0 {
		t.Fatal("cross-check never exercised a reachable problem")
	}
	t.Logf("cross-check finished with %d reachable problems", reachable)
}

// TestExactRationalCost checks rat conversion where the primitive volume is
// fractional before scaling (AllReduce factor of 2 on a size-3 axis).
func TestExactRationalCost(t *testing.T) {
	p := Problem{
		Shape:           []int64{3},
		BytesPerElement: 1,
		Mesh:            Mesh{Ranks: []int64{3, 2}, Weights: []int64{1, 1}},
		Source:          Layout{Shard: []int{-1}, Partial: []bool{true, false}},
		Target:          Layout{Shard: []int{-1}, Partial: []bool{false, false}},
	}
	sol, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	// AllReduce: 2 * (3-1)/3 * per-card 3 = 4.
	if sol.Cost.Cmp(big.NewRat(4, 1)) != 0 {
		t.Fatalf("want rational 4, got %s", sol.Cost)
	}
	// Scaled by lcm(3,2)=6: 24/6.
	if sol.Scale != 6 || sol.ScaledCost.Cmp(intBig(24)) != 0 {
		t.Fatalf("want scaled 24/6, got %s/%d", sol.ScaledCost, sol.Scale)
	}
	if got := simulatedCost(t, p, sol.Ops); got.Cmp(sol.Cost) != 0 {
		t.Fatalf("simulation cost %s != solver cost %s", got, sol.Cost)
	}
}

// TestLexicographicTieBreak verifies the dictionary-smallest plan among
// equal-cost, equal-length zero-communication choices.
func TestLexicographicTieBreak(t *testing.T) {
	p := Problem{
		Shape:           []int64{4, 4},
		BytesPerElement: 1,
		Mesh:            Mesh{Ranks: []int64{2, 2}, Weights: []int64{1, 1}},
		Source:          Layout{Shard: []int{-1, -1}, Partial: []bool{false, false}},
		Target:          Layout{Shard: []int{0, 1}, Partial: []bool{false, false}},
	}
	sol, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	want := []Op{
		{Type: LocalSlice, A: 0, D2: 0},
		{Type: LocalSlice, A: 1, D2: 1},
	}
	if len(sol.Ops) != len(want) {
		t.Fatalf("want %v, got %v", want, sol.Ops)
	}
	for i := range want {
		if compareOp(sol.Ops[i], want[i]) != 0 {
			t.Fatalf("op %d: want %v got %v (full %v)", i, want[i], sol.Ops[i], sol.Ops)
		}
	}
	if sol.ScaledCost.Sign() != 0 {
		t.Fatalf("all-LocalSlice plan must be free, got %s", sol.ScaledCost)
	}
	if got := simulatedCost(t, p, sol.Ops); got.Cmp(sol.Cost) != 0 {
		t.Fatalf("simulation cost %s != solver cost %s", got, sol.Cost)
	}
}
