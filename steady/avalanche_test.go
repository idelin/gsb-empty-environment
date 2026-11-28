package steady

import (
	"math"
	"testing"
)

// TestAvalancheWithoutBudget verifies that unlimited retries on every hop
// amplify D's mild overload until hard capacity is broken, while the same
// chain under a 20%-style retry budget converges within capacity.
func TestAvalancheWithoutBudgetVsBudgetedConvergence(t *testing.T) {
	unlimited := avalancheTopology(-1, 0, 3)
	resNo, err := Solve(unlimited)
	if err != nil {
		t.Fatal(err)
	}
	if !resNo.Converged {
		t.Fatal("unlimited-retry fixed point still converges numerically, but must break capacity")
	}
	if len(resNo.AvalancheNodes) == 0 {
		t.Fatalf("expected avalanche nodes, got none: %+v", resNo.Nodes)
	}
	dNo := nodeByName(resNo, "D")
	if !dNo.OverCapacity || dNo.LoadToCapacity <= 1 {
		t.Fatalf("D must break capacity without budget, got load/cap=%.3f", dNo.LoadToCapacity)
	}
	// The chain amplification must be substantially above 1 (nested retries).
	if dNo.ChainAmplification <= 2 {
		t.Fatalf("D chain amplification %.3f too low; expected nested retry stacking", dNo.ChainAmplification)
	}

	budgeted := avalancheTopology(0.1, 1, 3)
	resYes, err := Solve(budgeted)
	if err != nil {
		t.Fatal(err)
	}
	if !resYes.Converged || len(resYes.AvalancheNodes) != 0 {
		t.Fatalf("budgeted chain must converge with no avalanche, got av=%v", resYes.AvalancheNodes)
	}
	dYes := nodeByName(resYes, "D")
	if dYes.OverCapacity {
		t.Fatalf("budgeted D must stay within capacity, got load/cap=%.3f", dYes.LoadToCapacity)
	}
	if dYes.PhysicalArrival >= dNo.PhysicalArrival {
		t.Fatalf("budget must reduce D physical load: budgeted=%.2f unlimited=%.2f",
			dYes.PhysicalArrival, dNo.PhysicalArrival)
	}
	// At least one edge must be demonstrably budget-limited.
	limited := false
	for _, e := range resYes.Edges {
		if e.RetryLimited {
			limited = true
		}
	}
	if !limited {
		t.Fatal("expected at least one edge to be retry-budget limited")
	}
}

// TestWorstCaseAmplification reproduces the headline 4^3 = 64x bound: with 3
// retries (4 attempts) per hop and a fully failing sink, each hop multiplies
// the physical load by 4 across three edges.
func TestWorstCaseAmplification64x(t *testing.T) {
	curveOK := SoftThresholdCurve{BaseFailure: 0, SoftLoad: 1e9, HardLoad: 1e10, HardFailure: 0}
	curveDead := SoftThresholdCurve{BaseFailure: 1, SoftLoad: 1e9, HardLoad: 1e10, HardFailure: 1}
	nodes := []NodeConfig{
		{Name: "A", Capacity: 1e9, MeanServiceTime: 1e6, MinServiceTime: 1e6, Curve: curveOK},
		{Name: "B", Capacity: 1e9, MeanServiceTime: 1e6, MinServiceTime: 1e6, Curve: curveOK},
		{Name: "C", Capacity: 1e9, MeanServiceTime: 1e6, MinServiceTime: 1e6, Curve: curveOK},
		{Name: "D", Capacity: 1e9, MeanServiceTime: 1e6, MinServiceTime: 1e6, Curve: curveDead},
	}
	edges := []EdgeConfig{}
	for _, pair := range [][2]string{{"A", "B"}, {"B", "C"}, {"C", "D"}} {
		edges = append(edges, EdgeConfig{
			From: pair[0], To: pair[1], Fanout: 1, MaxAttempts: 4,
			BudgetRatio: -1, BackoffBase: 1e3,
		})
	}
	top := &Topology{Nodes: nodes, Edges: edges, Ingress: map[string]float64{"A": 10}}
	res, err := Solve(top)
	if err != nil {
		t.Fatal(err)
	}
	d := nodeByName(res, "D")
	if math.Abs(d.PhysicalArrival-640) > 1e-6 {
		t.Fatalf("D physical = %.3f, want 640 (10 ingress * 4^3)", d.PhysicalArrival)
	}
	if math.Abs(d.ChainAmplification-64) > 1e-6 {
		t.Fatalf("D amplification = %.3f, want 64", d.ChainAmplification)
	}
}

// TestSafeBudgetRestoresSteadyState checks that the binary search returns a
// budget ratio which, when applied uniformly, removes every avalanche node.
func TestSafeBudgetRestoresSteadyState(t *testing.T) {
	top := avalancheTopology(-1, 0, 3)
	ratio, err := GlobalSafeRatio(top)
	if err != nil {
		t.Fatal(err)
	}
	if math.IsInf(ratio, 1) || ratio <= 0 {
		t.Fatalf("expected finite positive safe ratio, got %v", ratio)
	}
	cp := avalancheTopology(ratio, 0, 3)
	res, err := Solve(cp)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Converged || len(res.AvalancheNodes) != 0 {
		t.Fatalf("safe ratio %.4f did not restore steady state: av=%v", ratio, res.AvalancheNodes)
	}
	for _, n := range res.Nodes {
		if n.OverCapacity {
			t.Fatalf("node %s over capacity at safe ratio", n.Name)
		}
	}
}
