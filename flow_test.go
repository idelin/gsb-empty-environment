package flowbounds

import (
	"math/rand"
	"testing"
)

func mustSolve(t *testing.T, n *Network) Result {
	t.Helper()
	res, err := Solve(n)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	return res
}

func TestSimpleFeasible(t *testing.T) {
	// 0 -> 1 must carry exactly 5.
	n := &Network{
		NumNodes: 2,
		Edges:    []Edge{{From: 0, To: 1, Lower: 3, Upper: 7}},
		Demand:   []int64{-5, 5},
	}
	res := mustSolve(t, n)
	if !res.Feasible {
		t.Fatalf("expected feasible, got cut %+v", res.Cut)
	}
	if res.Flow[0] != 5 {
		t.Fatalf("flow = %d, want 5", res.Flow[0])
	}
	if err := CheckFlow(n, res.Flow); err != nil {
		t.Fatalf("CheckFlow: %v", err)
	}
}

func TestLowerBoundForcesInfeasible(t *testing.T) {
	// Edge 0->1 forces at least 4 into node 1, but node 1 demands only 1
	// and cannot push anything back.
	n := &Network{
		NumNodes: 2,
		Edges:    []Edge{{From: 0, To: 1, Lower: 4, Upper: 9}},
		Demand:   []int64{-1, 1},
	}
	res := mustSolve(t, n)
	if res.Feasible {
		t.Fatalf("expected infeasible, got flow %v", res.Flow)
	}
	if err := CheckCertificate(n, res.Cut); err != nil {
		t.Fatalf("CheckCertificate: %v", err)
	}
	// Either {0} (forced outflow exceeds demand) or {1} (demand exceeds
	// possible inflow) is a valid violated set; CheckCertificate above
	// already verified the violation is real.
	if !res.Cut.InSet[0] && !res.Cut.InSet[1] {
		t.Fatalf("certificate set is empty: %v", res.Cut.InSet)
	}
}

func TestParallelEdges(t *testing.T) {
	// Two parallel arcs 0->1, each capped at 3; demand 5 needs both.
	n := &Network{
		NumNodes: 2,
		Edges: []Edge{
			{From: 0, To: 1, Lower: 0, Upper: 3},
			{From: 0, To: 1, Lower: 0, Upper: 3},
		},
		Demand: []int64{-5, 5},
	}
	res := mustSolve(t, n)
	if !res.Feasible {
		t.Fatalf("expected feasible, got cut %+v", res.Cut)
	}
	if err := CheckFlow(n, res.Flow); err != nil {
		t.Fatalf("CheckFlow: %v", err)
	}
	if res.Flow[0]+res.Flow[1] != 5 {
		t.Fatalf("parallel flows sum to %d, want 5", res.Flow[0]+res.Flow[1])
	}
}

func TestSelfLoopOnlyBoundedByRange(t *testing.T) {
	// The self-loop cannot help node 0 meet its demand; only the
	// incoming arc matters. Self-loop flow just needs to stay in range.
	n := &Network{
		NumNodes: 2,
		Edges: []Edge{
			{From: 0, To: 0, Lower: 2, Upper: 4},
			{From: 1, To: 0, Lower: 0, Upper: 10},
		},
		Demand: []int64{6, -6},
	}
	res := mustSolve(t, n)
	if !res.Feasible {
		t.Fatalf("expected feasible, got cut %+v", res.Cut)
	}
	if res.Flow[0] < 2 || res.Flow[0] > 4 {
		t.Fatalf("self-loop flow %d outside [2, 4]", res.Flow[0])
	}
	if err := CheckFlow(n, res.Flow); err != nil {
		t.Fatalf("CheckFlow: %v", err)
	}
}

func TestSelfLoopCannotSatisfyDemand(t *testing.T) {
	// Node 0 demands 3 but its only edge is a self-loop: infeasible.
	n := &Network{
		NumNodes: 2,
		Edges:    []Edge{{From: 0, To: 0, Lower: 0, Upper: 100}},
		Demand:   []int64{3, -3},
	}
	res := mustSolve(t, n)
	if res.Feasible {
		t.Fatalf("expected infeasible, got flow %v", res.Flow)
	}
	if err := CheckCertificate(n, res.Cut); err != nil {
		t.Fatalf("CheckCertificate: %v", err)
	}
}

func TestIsolatedNodes(t *testing.T) {
	// Node 2 is isolated with zero demand: fine. Then flip its demand
	// and expect infeasibility certified.
	ok := &Network{NumNodes: 3, Demand: []int64{0, 0, 0}}
	if res := mustSolve(t, ok); !res.Feasible {
		t.Fatalf("isolated zero-demand node should be feasible, got %+v", res.Cut)
	}
	bad := &Network{NumNodes: 3, Demand: []int64{1, -1, 0}}
	// Move the imbalance onto the isolated node instead.
	bad.Demand = []int64{0, 0, 0}
	bad.Demand[2] = 1
	bad.Demand[1] = -1
	res := mustSolve(t, bad)
	if res.Feasible {
		t.Fatalf("isolated node with demand must be infeasible")
	}
	if err := CheckCertificate(bad, res.Cut); err != nil {
		t.Fatalf("CheckCertificate: %v", err)
	}
}

func TestInvalidInstances(t *testing.T) {
	if _, err := Solve(&Network{NumNodes: 2, Demand: []int64{1, 0}}); err == nil {
		t.Fatal("expected error for demands not summing to zero")
	}
	if _, err := Solve(&Network{
		NumNodes: 1,
		Edges:    []Edge{{From: 0, To: 0, Lower: 5, Upper: 2}},
		Demand:   []int64{0},
	}); err == nil {
		t.Fatal("expected error for Lower > Upper")
	}
	if _, err := Solve(&Network{
		NumNodes: 1,
		Edges:    []Edge{{From: 0, To: 3, Lower: 0, Upper: 1}},
		Demand:   []int64{0},
	}); err == nil {
		t.Fatal("expected error for endpoint out of range")
	}
}

// randomNetwork builds a small random instance with parallel edges,
// self-loops and possibly isolated nodes. If feasibleDemands is set, the
// demands are derived from a random in-range flow, guaranteeing
// feasibility.
func randomNetwork(rng *rand.Rand, feasibleDemands bool) *Network {
	numNodes := 1 + rng.Intn(5)
	numEdges := rng.Intn(7)
	n := &Network{NumNodes: numNodes, Demand: make([]int64, numNodes)}
	for i := 0; i < numEdges; i++ {
		e := Edge{From: rng.Intn(numNodes), To: rng.Intn(numNodes)}
		e.Lower = int64(rng.Intn(3))
		e.Upper = e.Lower + int64(rng.Intn(3))
		n.Edges = append(n.Edges, e)
	}
	if feasibleDemands {
		for _, e := range n.Edges {
			if e.From == e.To {
				continue
			}
			f := e.Lower
			if e.Upper > e.Lower {
				f += int64(rng.Intn(int(e.Upper - e.Lower + 1)))
			}
			n.Demand[e.To] += f
			n.Demand[e.From] -= f
		}
	} else {
		for v := 0; v+1 < numNodes; v++ {
			n.Demand[v] = int64(rng.Intn(7) - 3)
			n.Demand[numNodes-1] -= n.Demand[v]
		}
	}
	return n
}

func TestSolveMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	const maxCombos = 200000
	feasible, infeasible := 0, 0
	for trial := 0; trial < 4000; trial++ {
		n := randomNetwork(rng, trial%2 == 0)
		want, err := BruteForceFeasible(n, maxCombos)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		res, err := Solve(n)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		if res.Feasible != want {
			t.Fatalf("trial %d: Solve feasible=%v, brute force=%v; network %+v", trial, res.Feasible, want, n)
		}
		if res.Feasible {
			feasible++
			if err := CheckFlow(n, res.Flow); err != nil {
				t.Fatalf("trial %d: %v; network %+v", trial, err, n)
			}
		} else {
			infeasible++
			if err := CheckCertificate(n, res.Cut); err != nil {
				t.Fatalf("trial %d: %v; network %+v", trial, err, n)
			}
		}
	}
	t.Logf("trials: %d feasible, %d infeasible", feasible, infeasible)
}

// TestCheckFlowRejectsBadFlow guards the verifier itself.
func TestCheckFlowRejectsBadFlow(t *testing.T) {
	n := &Network{
		NumNodes: 2,
		Edges:    []Edge{{From: 0, To: 1, Lower: 0, Upper: 5}},
		Demand:   []int64{-3, 3},
	}
	if err := CheckFlow(n, []int64{6}); err == nil {
		t.Fatal("expected range violation")
	}
	if err := CheckFlow(n, []int64{2}); err == nil {
		t.Fatal("expected conservation violation")
	}
}

// TestCheckCertificateRejectsBogus guards the certificate verifier.
func TestCheckCertificateRejectsBogus(t *testing.T) {
	n := &Network{
		NumNodes: 2,
		Edges:    []Edge{{From: 0, To: 1, Lower: 0, Upper: 5}},
		Demand:   []int64{-3, 3},
	}
	// Demand 3 <= upper-in 5: not a violation.
	bogus := &CutCertificate{InSet: []bool{false, true}, DemandSum: 3, UpperIn: 5, LowerOut: 0}
	if err := CheckCertificate(n, bogus); err == nil {
		t.Fatal("expected rejection of non-violated certificate")
	}
	// Violated, but aggregates do not match the network.
	wrong := &CutCertificate{InSet: []bool{false, true}, DemandSum: 3, UpperIn: 1, LowerOut: 0}
	if err := CheckCertificate(n, wrong); err == nil {
		t.Fatal("expected rejection of mismatched aggregates")
	}
}
