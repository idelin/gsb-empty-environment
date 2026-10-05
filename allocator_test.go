package allocator

import (
	"math/rand"
	"reflect"
	"testing"
)

func counts(assignment []int) map[int]int {
	c := make(map[int]int)
	for _, owner := range assignment {
		c[owner]++
	}
	return c
}

func TestQuotasSumExactlyAndRespectWeights(t *testing.T) {
	nodes := []Node{{ID: 1, Weight: 5}, {ID: 2, Weight: 3}, {ID: 3, Weight: 2}}
	for shards := 0; shards <= 97; shards++ {
		q := Quotas(nodes, shards)
		sum := 0
		for _, id := range []int{1, 2, 3} {
			sum += q[id]
			// Hamilton property: quota is within one of the ideal share.
			ideal := float64(shards) * float64(nodes[id-1].Weight) / 10.0
			if diff := float64(q[id]) - ideal; diff >= 1 || diff <= -1 {
				t.Fatalf("shards=%d node %d: quota %d deviates from ideal %f", shards, id, q[id], ideal)
			}
		}
		if sum != shards {
			t.Fatalf("shards=%d: quotas sum to %d", shards, sum)
		}
	}
}

func TestQuotaRemainderTieBrokenByNodeID(t *testing.T) {
	// 1 shard, 3 equal-weight nodes: every remainder ties, node 1 wins.
	q := Quotas([]Node{{ID: 3, Weight: 1}, {ID: 1, Weight: 1}, {ID: 2, Weight: 1}}, 1)
	if q[1] != 1 || q[2] != 0 || q[3] != 0 {
		t.Fatalf("tie not broken by node ID: %v", q)
	}
	// 4 shards over 3 equal nodes: 2,1,1 in ID order.
	q = Quotas([]Node{{ID: 3, Weight: 1}, {ID: 1, Weight: 1}, {ID: 2, Weight: 1}}, 4)
	if q[1] != 2 || q[2] != 1 || q[3] != 1 {
		t.Fatalf("unexpected largest-remainder split: %v", q)
	}
}

func TestStableAssignmentDoesNotMove(t *testing.T) {
	nodes := []Node{{ID: 1, Weight: 1}, {ID: 2, Weight: 1}}
	old := []int{1, 1, 2, 2}
	plan := Reassign(4, nodes, old)
	if plan.Migrations != 0 || plan.LowerBound != 0 {
		t.Fatalf("stable assignment moved shards: %+v", plan)
	}
	if !reflect.DeepEqual(plan.Assignment, old) {
		t.Fatalf("assignment changed: %v", plan.Assignment)
	}
}

func TestNodeAdded(t *testing.T) {
	old := []int{1, 1, 1, 2, 2, 2}
	before := []Node{{ID: 1, Weight: 1}, {ID: 2, Weight: 1}}
	if p := Reassign(6, before, old); p.Migrations != 0 {
		t.Fatalf("precondition: expected stable, got %d migrations", p.Migrations)
	}
	after := []Node{{ID: 1, Weight: 1}, {ID: 2, Weight: 1}, {ID: 3, Weight: 1}}
	plan := Reassign(6, after, old)
	if plan.Quotas[3] != 2 {
		t.Fatalf("new node quota = %d, want 2", plan.Quotas[3])
	}
	if plan.Migrations != 2 || plan.LowerBound != 2 {
		t.Fatalf("migrations=%d lowerBound=%d, want 2/2", plan.Migrations, plan.LowerBound)
	}
	assertQuotasHeld(t, plan)
	// The two shards the new node receives must be the highest-ID surplus
	// shards: nodes keep their lowest-ID shards.
	want := []int{1, 1, 3, 2, 2, 3}
	if !reflect.DeepEqual(plan.Assignment, want) {
		t.Fatalf("assignment = %v, want %v", plan.Assignment, want)
	}
}

func TestNodeRemoved(t *testing.T) {
	old := []int{1, 1, 2, 2, 3, 3}
	nodes := []Node{{ID: 1, Weight: 1}, {ID: 2, Weight: 1}} // node 3 went offline
	plan := Reassign(6, nodes, old)
	if plan.Migrations != 2 || plan.LowerBound != 2 {
		t.Fatalf("migrations=%d lowerBound=%d, want 2/2", plan.Migrations, plan.LowerBound)
	}
	assertQuotasHeld(t, plan)
	for _, owner := range plan.Assignment {
		if owner == 3 {
			t.Fatalf("removed node still owns shards: %v", plan.Assignment)
		}
	}
	// Survivors keep their own shards; node 3's shards go to the
	// lowest-ID deficit node first.
	want := []int{1, 1, 2, 2, 1, 2}
	if !reflect.DeepEqual(plan.Assignment, want) {
		t.Fatalf("assignment = %v, want %v", plan.Assignment, want)
	}
}

func TestWeightZeroed(t *testing.T) {
	old := []int{1, 1, 2, 2, 3, 3}
	nodes := []Node{{ID: 1, Weight: 1}, {ID: 2, Weight: 0}, {ID: 3, Weight: 1}}
	plan := Reassign(6, nodes, old)
	if plan.Quotas[2] != 0 {
		t.Fatalf("zeroed node quota = %d, want 0", plan.Quotas[2])
	}
	if plan.Migrations != 2 || plan.LowerBound != 2 {
		t.Fatalf("migrations=%d lowerBound=%d, want 2/2", plan.Migrations, plan.LowerBound)
	}
	assertQuotasHeld(t, plan)
	want := []int{1, 1, 1, 3, 3, 3}
	if !reflect.DeepEqual(plan.Assignment, want) {
		t.Fatalf("assignment = %v, want %v", plan.Assignment, want)
	}
}

func TestAllWeightsZeroFallsBackToEven(t *testing.T) {
	nodes := []Node{{ID: 1}, {ID: 2}, {ID: 3}}
	q := Quotas(nodes, 7)
	if q[1] != 3 || q[2] != 2 || q[3] != 2 {
		t.Fatalf("degenerate weights not spread evenly by ID: %v", q)
	}
}

func TestUnassignedShardsAreFilled(t *testing.T) {
	old := []int{1, Unassigned, Unassigned, 2}
	nodes := []Node{{ID: 1, Weight: 1}, {ID: 2, Weight: 1}}
	plan := Reassign(4, nodes, old)
	assertQuotasHeld(t, plan)
	if plan.Migrations != plan.LowerBound {
		t.Fatalf("migrations=%d lowerBound=%d", plan.Migrations, plan.LowerBound)
	}
	for _, owner := range plan.Assignment {
		if owner == Unassigned {
			t.Fatalf("shard left unassigned: %v", plan.Assignment)
		}
	}
}

func TestDeterministic(t *testing.T) {
	nodes := []Node{{ID: 2, Weight: 3}, {ID: 1, Weight: 5}, {ID: 3, Weight: 2}}
	old := []int{1, 2, 3, 1, 2, 3, 1, 2, 3, 1}
	first := Reassign(10, nodes, old)
	for i := 0; i < 50; i++ {
		next := Reassign(10, nodes, old)
		if !reflect.DeepEqual(first, next) {
			t.Fatalf("non-deterministic plan:\n%+v\n%+v", first, next)
		}
	}
}

func TestProofDocumentsDeficits(t *testing.T) {
	old := []int{1, 1, 1, 1}
	nodes := []Node{{ID: 1, Weight: 1}, {ID: 2, Weight: 1}}
	plan := Reassign(4, nodes, old)
	if plan.Proof == "" {
		t.Fatal("missing lower-bound proof")
	}
	t.Logf("proof:\n%s", plan.Proof)
}

// TestBruteForceOptimal exhaustively checks small cases: no assignment
// satisfying the quotas migrates fewer shards than the plan.
func TestBruteForceOptimal(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 300; trial++ {
		shards := 1 + rng.Intn(8)
		nodeIDs := []int{1, 2, 3}
		nodes := make([]Node, 0, len(nodeIDs))
		for _, id := range nodeIDs {
			if rng.Intn(4) == 0 {
				continue // node offline
			}
			nodes = append(nodes, Node{ID: id, Weight: int64(rng.Intn(4))}) // may be zero
		}
		if len(nodes) == 0 {
			continue
		}
		old := make([]int, shards)
		for s := range old {
			old[s] = nodeIDs[rng.Intn(len(nodeIDs))]
		}
		plan := Reassign(shards, nodes, old)
		assertQuotasHeld(t, plan)
		if plan.Migrations != plan.LowerBound {
			t.Fatalf("trial %d: migrations=%d lowerBound=%d", trial, plan.Migrations, plan.LowerBound)
		}
		best := bruteForceMinMigrations(shards, nodes, plan.Quotas, old)
		if plan.Migrations != best {
			t.Fatalf("trial %d: plan migrates %d, brute-force optimum is %d (old=%v quotas=%v)",
				trial, plan.Migrations, best, old, plan.Quotas)
		}
	}
}

// bruteForceMinMigrations enumerates every assignment meeting the quotas
// and returns the minimum number of owner changes.
func bruteForceMinMigrations(shards int, nodes []Node, quotas map[int]int, old []int) int {
	remaining := make(map[int]int, len(quotas))
	for _, n := range nodes {
		remaining[n.ID] = quotas[n.ID]
	}
	best := shards + 1
	current := make([]int, shards)
	var search func(s int, changes int)
	search = func(s int, changes int) {
		if changes >= best {
			return
		}
		if s == shards {
			best = changes
			return
		}
		for _, n := range nodes {
			if remaining[n.ID] == 0 {
				continue
			}
			remaining[n.ID]--
			current[s] = n.ID
			delta := 0
			if n.ID != old[s] {
				delta = 1
			}
			search(s+1, changes+delta)
			remaining[n.ID]++
		}
	}
	search(0, 0)
	return best
}

func assertQuotasHeld(t *testing.T, plan Plan) {
	t.Helper()
	held := counts(plan.Assignment)
	delete(held, Unassigned)
	for id, q := range plan.Quotas {
		if held[id] != q {
			t.Fatalf("held %v != quotas %v", held, plan.Quotas)
		}
		delete(held, id)
	}
	if len(held) != 0 {
		t.Fatalf("held %v != quotas %v", held, plan.Quotas)
	}
}
