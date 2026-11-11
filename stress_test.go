package netsec

import (
	"math/rand"
	"testing"
	"time"
)

// TestMaxSizeStress exercises the documented input limits to make sure the
// exact solver stays well within scheduling-host latency budgets.
func TestMaxSizeStress(t *testing.T) {
	rng := rand.New(rand.NewSource(99))

	requests := make([]Request, MaxRequests)
	tenants := []string{"t0", "t1", "t2", "t3"}
	purposes := []string{"sign", "wrap", "attest"}
	for i := range requests {
		// Spread sensitive approval requests so no tenant's conflict clique
		// exceeds the six available domains; capacity stays comfortably large.
		tenant := tenants[i%len(tenants)]
		requests[i] = Request{
			ID:           "r" + itoa(i),
			TenantID:     tenant,
			KeyPurpose:   purposes[rng.Intn(len(purposes))],
			Capacity:     1 + rng.Intn(4),
			Sensitive:    i%2 == 0,
			ApprovalRole: []string{"initiator", "approver"}[i%2],
		}
	}

	domains := make([]Domain, MaxDomains)
	for d := range domains {
		pools := make(map[string]int)
		for _, p := range purposes {
			// Generous capacity so feasibility is governed by the isolation
			// constraints, not by pool exhaustion.
			pools[p] = 100
		}
		domains[d] = Domain{ID: "d" + itoa(d), Purposes: pools}
	}

	cost := make([][]int, MaxRequests)
	for i := range cost {
		cost[i] = make([]int, MaxDomains)
		for d := range cost[i] {
			cost[i][d] = rng.Intn(50)
		}
	}

	in := Input{Requests: requests, Domains: domains, Cost: cost}

	start := time.Now()
	plan, cs, err := Solve(in)
	elapsed := time.Since(start)
	t.Logf("max-size solve took %s, plan=%+v conflict=%+v", elapsed, plan, cs)

	if err != nil {
		t.Fatalf("relaxed capacities should be feasible: %v", err)
	}
	if plan.EnabledCount < 1 || plan.EnabledCount > MaxDomains {
		t.Fatalf("illegal enabled count: %d", plan.EnabledCount)
	}
	if !planSatisfiesConstraints(t, in, plan) {
		t.Fatal("max-size plan violates constraints")
	}
	if elapsed > 250*time.Millisecond {
		t.Fatalf("solver too slow at max size: %s", elapsed)
	}
}

// TestMaxSizeInfeasible checks conflict-set extraction at the input limits.
func TestMaxSizeInfeasible(t *testing.T) {
	requests := make([]Request, MaxRequests)
	for i := range requests {
		requests[i] = Request{
			ID:           "r" + itoa(i),
			TenantID:     "t0",
			KeyPurpose:   "sign",
			Capacity:     1,
			Sensitive:    true,
			ApprovalRole: "approver",
		}
	}
	domains := make([]Domain, MaxDomains)
	for d := range domains {
		domains[d] = Domain{
			ID:       "d" + itoa(d),
			Purposes: map[string]int{"sign": 100},
		}
	}
	cost := make([][]int, MaxRequests)
	for i := range cost {
		cost[i] = make([]int, MaxDomains)
	}

	in := Input{Requests: requests, Domains: domains, Cost: cost}
	_, cs, err := Solve(in)
	if err != ErrInfeasible {
		t.Fatalf("expected ErrInfeasible, got %v", err)
	}

	// Six domains can separate at most six mutually conflicting requests, so
	// an inclusion-minimal infeasible subset has seven requests.
	if len(cs.RequestSubset) != MaxDomains+1 {
		t.Fatalf("expected minimal subset of %d requests, got %d",
			MaxDomains+1, len(cs.RequestSubset))
	}
	expectedPairs := (MaxDomains + 1) * MaxDomains / 2
	if len(cs.PairConflicts) != expectedPairs {
		t.Fatalf("expected %d pair conflicts, got %d",
			expectedPairs, len(cs.PairConflicts))
	}
}
