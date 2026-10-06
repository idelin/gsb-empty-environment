package flowsolve

import (
	"math/rand"
	"testing"
)

func mustSolve(t *testing.T, n *Network) ([]int64, *Certificate) {
	t.Helper()
	flow, cert, err := Solve(n)
	if err != nil {
		t.Fatalf("Solve returned error: %v", err)
	}
	if (flow == nil) == (cert == nil) {
		t.Fatalf("Solve must return exactly one of flow or certificate")
	}
	return flow, cert
}

func TestParallelEdges(t *testing.T) {
	n := &Network{
		NumNodes: 2,
		Demand:   []int64{5, -5},
		Edges: []Edge{
			{From: 1, To: 0, Lo: 0, Hi: 3},
			{From: 1, To: 0, Lo: 0, Hi: 3},
			{From: 1, To: 0, Lo: 1, Hi: 3},
		},
	}
	flow, cert := mustSolve(t, n)
	if cert != nil {
		t.Fatalf("expected feasible, got certificate %+v", cert)
	}
	if err := VerifyFlow(n, flow); err != nil {
		t.Fatalf("invalid flow: %v", err)
	}
}

func TestIsolatedNode(t *testing.T) {
	feasible := &Network{NumNodes: 2, Demand: []int64{0, 0}}
	if _, cert := mustSolve(t, feasible); cert != nil {
		t.Fatalf("isolated node with zero demand must be feasible")
	}
	infeasible := &Network{NumNodes: 2, Demand: []int64{1, -1}}
	flow, cert := mustSolve(t, infeasible)
	if flow != nil {
		t.Fatalf("isolated node with nonzero demand must be infeasible")
	}
	if err := VerifyCertificate(infeasible, cert); err != nil {
		t.Fatalf("invalid certificate: %v", err)
	}
}

func TestSelfLoop(t *testing.T) {
	// Self-loop forces flow 2 through itself but must not affect conservation.
	n := &Network{
		NumNodes: 2,
		Demand:   []int64{1, -1},
		Edges: []Edge{
			{From: 0, To: 0, Lo: 2, Hi: 4},
			{From: 1, To: 0, Lo: 0, Hi: 5},
		},
	}
	flow, cert := mustSolve(t, n)
	if cert != nil {
		t.Fatalf("expected feasible, got certificate %+v", cert)
	}
	if flow[0] < 2 || flow[0] > 4 {
		t.Fatalf("self-loop flow %d outside [2, 4]", flow[0])
	}
	if err := VerifyFlow(n, flow); err != nil {
		t.Fatalf("invalid flow: %v", err)
	}

	// A self-loop alone can never make a zero-demand node infeasible.
	only := &Network{
		NumNodes: 1,
		Demand:   []int64{0},
		Edges:    []Edge{{From: 0, To: 0, Lo: 3, Hi: 7}},
	}
	if _, cert := mustSolve(t, only); cert != nil {
		t.Fatalf("self-loop on balanced node must be feasible")
	}
}

func TestCertificateKnownCut(t *testing.T) {
	// Node 0 must absorb 10 but the only incoming edge carries at most 4.
	n := &Network{
		NumNodes: 2,
		Demand:   []int64{10, -10},
		Edges:    []Edge{{From: 1, To: 0, Lo: 0, Hi: 4}},
	}
	flow, cert := mustSolve(t, n)
	if flow != nil {
		t.Fatalf("expected infeasible")
	}
	if err := VerifyCertificate(n, cert); err != nil {
		t.Fatalf("invalid certificate: %v", err)
	}
}

func TestInputValidation(t *testing.T) {
	if _, _, err := Solve(&Network{NumNodes: 1, Demand: []int64{1}}); err != ErrDemandSum {
		t.Fatalf("want ErrDemandSum, got %v", err)
	}
	bad := &Network{NumNodes: 1, Demand: []int64{0}, Edges: []Edge{{From: 0, To: 0, Lo: 2, Hi: 1}}}
	if _, _, err := Solve(bad); err != ErrBadBounds {
		t.Fatalf("want ErrBadBounds, got %v", err)
	}
	if _, err := BruteForce(bad, 1000); err != ErrBadBounds {
		t.Fatalf("want ErrBadBounds from BruteForce, got %v", err)
	}
}

// randomNetwork builds a small random instance with parallel edges,
// self-loops and possibly isolated nodes.
func randomNetwork(rng *rand.Rand) *Network {
	numNodes := 1 + rng.Intn(5)
	demand := make([]int64, numNodes)
	for i := 0; i+1 < numNodes; i++ {
		demand[i] = int64(rng.Intn(7) - 3)
		demand[numNodes-1] -= demand[i]
	}
	numEdges := rng.Intn(9)
	edges := make([]Edge, numEdges)
	for i := range edges {
		edges[i] = Edge{
			From: rng.Intn(numNodes),
			To:   rng.Intn(numNodes),
			Lo:   int64(rng.Intn(3)),
		}
		edges[i].Hi = edges[i].Lo + int64(rng.Intn(3))
	}
	return &Network{NumNodes: numNodes, Demand: demand, Edges: edges}
}

func TestRandomAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 2000; trial++ {
		n := randomNetwork(rng)
		wantFeasible, err := BruteForce(n, 1<<20)
		if err != nil {
			t.Fatalf("trial %d: brute force: %v", trial, err)
		}
		flow, cert, err := Solve(n)
		if err != nil {
			t.Fatalf("trial %d: Solve: %v", trial, err)
		}
		if wantFeasible {
			if flow == nil {
				t.Fatalf("trial %d: brute force feasible but Solve returned certificate %+v\nnetwork: %+v", trial, cert, n)
			}
			if err := VerifyFlow(n, flow); err != nil {
				t.Fatalf("trial %d: %v\nnetwork: %+v\nflow: %v", trial, err, n, flow)
			}
		} else {
			if cert == nil {
				t.Fatalf("trial %d: brute force infeasible but Solve returned flow %v\nnetwork: %+v", trial, flow, n)
			}
			if err := VerifyCertificate(n, cert); err != nil {
				t.Fatalf("trial %d: %v\nnetwork: %+v", trial, err, n)
			}
		}
	}
}
