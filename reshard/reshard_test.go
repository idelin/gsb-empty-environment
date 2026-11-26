package reshard

import (
	"math/big"
	"testing"
)

func intBig(n int64) *big.Int { return big.NewInt(n) }

// TestLocalSliceBeforeSlowAxis covers shrinking the per-card bytes with a free
// LocalSlice before the expensive slow-axis reduction. The slow axis holds a
// Partial contribution, so AllToAll cannot touch it; the choice is between a
// full-size and a pre-shrunk reduction.
func TestLocalSliceBeforeSlowAxis(t *testing.T) {
	// d1 is replicated and shardable on the Partial slow axis a=1; the fast
	// axis a=0 is free. Target clears the Partial and shards d1 on a=1.
	// LocalSlice cannot run on a Partial axis, so the free shrink first shards
	// d0 on a=0, halving the bytes fed into the slow-axis ReduceScatter.
	p := Problem{
		Shape:           []int64{4, 8},
		BytesPerElement: 1,
		Mesh:            Mesh{Ranks: []int64{2, 2}, Weights: []int64{1, 100}},
		Source:          Layout{Shard: []int{-1, -1}, Partial: []bool{false, true}},
		Target:          Layout{Shard: []int{-1, 1}, Partial: []bool{false, false}},
	}
	sol, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	// LocalSlice must come before the slow-axis communication.
	sawSlowComm := false
	localSlices := 0
	for _, op := range sol.Ops {
		if op.A == 1 {
			sawSlowComm = true
		}
		if op.Type == LocalSlice {
			localSlices++
			if sawSlowComm {
				t.Fatalf("LocalSlice after slow-axis communication: %v", sol.Ops)
			}
		}
	}
	if localSlices != 1 || !sawSlowComm {
		t.Fatalf("expected LocalSlice then slow-axis reduction, got %v", sol.Ops)
	}
	if got := simulatedCost(t, p, sol.Ops); got.Cmp(sol.Cost) != 0 {
		t.Fatalf("simulation cost %s != solver cost %s", got, sol.Cost)
	}

	// Bad ordering: AllReduce at full per-card bytes, then LocalSlice on the
	// now-free slow axis. The full-size reduction is 2 * 1/2 * 32 * 100 = 3200.
	badSeq := []Op{
		{Type: AllReduce, A: 1},
		{Type: LocalSlice, A: 1, D2: 1},
	}
	end, badCost, err := simulate(p, badSeq)
	if err != nil {
		t.Fatalf("bad ordering simulation: %v", err)
	}
	if !newState(end).equal(newState(p.Target)) {
		t.Fatalf("bad ordering does not reach the target: %v", end)
	}
	if badCost.Cmp(sol.Cost) <= 0 {
		t.Fatalf("bad ordering cost %s should exceed optimal %s", badCost, sol.Cost)
	}
}

// TestRejectNonDivisibleSharding covers rejection of shardings that split a
// tensor dimension into non-integral pieces, both at validation and via edges.
func TestRejectNonDivisibleSharding(t *testing.T) {
	p := Problem{
		Shape:           []int64{3, 8},
		BytesPerElement: 1,
		Mesh:            Mesh{Ranks: []int64{2, 2}, Weights: []int64{1, 1}},
		Source:          Layout{Shard: []int{0, -1}, Partial: []bool{false, false}},
		Target:          Layout{Shard: []int{0, -1}, Partial: []bool{false, false}},
	}
	if _, err := Solve(p); err == nil {
		t.Fatal("expected rejection of size-3 dimension sharded by axis of size 2")
	}

	odd := Problem{
		Shape:           []int64{3, 8},
		BytesPerElement: 1,
		Mesh:            Mesh{Ranks: []int64{2, 2}, Weights: []int64{1, 1}},
		Source:          Layout{Shard: []int{-1, 1}, Partial: []bool{true, false}},
		Target:          Layout{Shard: []int{0, 1}, Partial: []bool{false, false}},
	}
	if _, err := Solve(odd); err == nil {
		t.Fatal("expected unreachable target needing a non-divisible shard")
	}

	solver := newSolver(odd)
	for _, e := range solver.edges(newState(odd.Source)) {
		for d, a := range e.next.shard {
			if a >= 0 && odd.Shape[d]%odd.Mesh.Ranks[a] != 0 {
				t.Fatalf("edge %v produced non-divisible sharding", e.op)
			}
		}
	}
}

// TestReduceScatterBeatsAllReduceThenSlice covers fusing the reduction with
// the new sharding: one ReduceScatter beats AllReduce plus a free LocalSlice.
func TestReduceScatterBeatsAllReduceThenSlice(t *testing.T) {
	p := Problem{
		Shape:           []int64{4, 8},
		BytesPerElement: 1,
		Mesh:            Mesh{Ranks: []int64{2, 2}, Weights: []int64{1, 1}},
		Source:          Layout{Shard: []int{-1, -1}, Partial: []bool{true, false}},
		Target:          Layout{Shard: []int{0, -1}, Partial: []bool{false, false}},
	}
	sol, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if sol.Steps != 1 || sol.Ops[0].Type != ReduceScatter {
		t.Fatalf("want single ReduceScatter, got %v", sol.Ops)
	}
	// RS rational: (2-1)/2 * per-card 32 * 1 = 16; scaled by lcm 2 -> 32.
	if sol.ScaledCost.Cmp(intBig(32)) != 0 {
		t.Fatalf("want scaled cost 32, got %s", sol.ScaledCost)
	}
	if got := simulatedCost(t, p, sol.Ops); got.Cmp(sol.Cost) != 0 {
		t.Fatalf("simulation cost %s != solver cost %s", got, sol.Cost)
	}

	// AllReduce alone already costs 2 * 16 = 32, before the free LocalSlice.
	alt, err := Solve(Problem{
		Shape: p.Shape, BytesPerElement: p.BytesPerElement, Mesh: p.Mesh,
		Source: p.Source,
		Target: Layout{Shard: []int{-1, -1}, Partial: []bool{false, false}},
	})
	if err != nil {
		t.Fatalf("Solve AllReduce variant: %v", err)
	}
	if alt.ScaledCost.Cmp(sol.ScaledCost) <= 0 {
		t.Fatalf("AllReduce+LocalSlice cost %s should exceed ReduceScatter %s",
			alt.ScaledCost, sol.ScaledCost)
	}
}

// TestAllToAllBeatsAllGatherThenSlice covers a one-step sharding-axis change.
func TestAllToAllBeatsAllGatherThenSlice(t *testing.T) {
	p := Problem{
		Shape:           []int64{4, 8},
		BytesPerElement: 1,
		Mesh:            Mesh{Ranks: []int64{2, 2}, Weights: []int64{1, 1}},
		Source:          Layout{Shard: []int{0, -1}, Partial: []bool{false, false}},
		Target:          Layout{Shard: []int{-1, 1}, Partial: []bool{false, false}},
	}
	sol, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if sol.Steps != 1 || sol.Ops[0].Type != AllToAll {
		t.Fatalf("want single AllToAll, got %v", sol.Ops)
	}
	if sol.Ops[0].A != 0 || sol.Ops[0].D1 != 0 || sol.Ops[0].D2 != 1 {
		t.Fatalf("unexpected AllToAll arguments: %v", sol.Ops[0])
	}
	// A2A rational: (2-1)/2 * per-card 16 = 8; scaled by lcm 2 -> 16.
	if sol.ScaledCost.Cmp(intBig(16)) != 0 {
		t.Fatalf("want scaled cost 16, got %s", sol.ScaledCost)
	}
	if got := simulatedCost(t, p, sol.Ops); got.Cmp(sol.Cost) != 0 {
		t.Fatalf("simulation cost %s != solver cost %s", got, sol.Cost)
	}

	// AllGather alone costs (2-1)/2 * full 32 = 16, then LocalSlice is free.
	agOnly, err := Solve(Problem{
		Shape: p.Shape, BytesPerElement: p.BytesPerElement, Mesh: p.Mesh,
		Source: p.Source,
		Target: Layout{Shard: []int{-1, -1}, Partial: []bool{false, false}},
	})
	if err != nil {
		t.Fatalf("Solve AllGather variant: %v", err)
	}
	if agOnly.ScaledCost.Cmp(sol.ScaledCost) <= 0 {
		t.Fatalf("AllGather+LocalSlice (%s, %d steps) should lose to AllToAll (%s, %d)",
			agOnly.ScaledCost, agOnly.Steps, sol.ScaledCost, sol.Steps)
	}
}
