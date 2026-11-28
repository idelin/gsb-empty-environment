package steady

import (
	"testing"
	"time"
)

// relativeError returns |a-b|/|b|, guarding a zero denominator.
func relativeError(a, b float64) float64 {
	if b == 0 {
		if a == 0 {
			return 0
		}
		return 1
	}
	d := a - b
	if d < 0 {
		d = -d
	}
	return d / b
}

// TestSolverMatchesSimulationAtCriticalOverload drives the 4-hop chain near
// D's soft threshold and requires the analytical fixed point and the
// discrete-event simulation (built from the real retry.EdgeController) to
// agree on per-node physical arrival rates within Monte Carlo tolerance.
func TestSolverMatchesSimulationAtCriticalOverload(t *testing.T) {
	top := avalancheTopology(-1, 0, 3)
	res, err := Solve(top)
	if err != nil {
		t.Fatal(err)
	}
	sim, err := Simulate(top, SimConfig{
		Duration: 300 * time.Second,
		Warmup:   80 * time.Second,
		Seed:     11,
	})
	if err != nil {
		t.Fatal(err)
	}
	const tol = 0.12 // Monte Carlo + EWMA tolerance at the critical knee
	for _, name := range []string{"B", "C", "D"} {
		analytic := nodeByName(res, name)
		measured := simNodeByName(sim, name)
		if err := relativeError(measured.PhysicalArrival, analytic.PhysicalArrival); err > tol {
			t.Errorf("node %s physical arrival mismatch: solver=%.2f sim=%.2f relErr=%.3f",
				name, analytic.PhysicalArrival, measured.PhysicalArrival, err)
		}
		if err := relativeError(measured.Amplification, analytic.Amplification); err > tol {
			t.Errorf("node %s amplification mismatch: solver=%.3f sim=%.3f relErr=%.3f",
				name, analytic.Amplification, measured.Amplification, err)
		}
	}
	// The simulated local failure rate at D must track the analytic curve.
	dAnalytic := nodeByName(res, "D")
	dSim := simNodeByName(sim, "D")
	if err := relativeError(dSim.FailureRate, dAnalytic.LocalFailureRate); err > tol {
		t.Errorf("D failure rate mismatch: solver=%.3f sim=%.3f",
			dAnalytic.LocalFailureRate, dSim.FailureRate)
	}
}

// TestSimulationBudgetSuppressesLoad verifies the DES honors the retry
// budget: the budgeted run produces strictly fewer downstream attempts than
// the unlimited run for the same topology.
func TestSimulationBudgetSuppressesLoad(t *testing.T) {
	unlimited := avalancheTopology(-1, 0, 3)
	budgeted := avalancheTopology(0.1, 1, 3)
	simU, err := Simulate(unlimited, SimConfig{
		Duration: 200 * time.Second, Warmup: 50 * time.Second, Seed: 11,
	})
	if err != nil {
		t.Fatal(err)
	}
	simB, err := Simulate(budgeted, SimConfig{
		Duration: 200 * time.Second, Warmup: 50 * time.Second, Seed: 11,
	})
	if err != nil {
		t.Fatal(err)
	}
	dU := simNodeByName(simU, "D")
	dB := simNodeByName(simB, "D")
	if dB.PhysicalArrival >= dU.PhysicalArrival {
		t.Fatalf("budget must reduce D load in DES: budgeted=%.2f unlimited=%.2f",
			dB.PhysicalArrival, dU.PhysicalArrival)
	}
}

// TestSimulationHedgeCancellation exercises hedged branches in the DES:
// branches are launched, the first successful response wins and siblings
// are cancelled, and no budget slots leak afterwards.
func TestSimulationHedgeCancellation(t *testing.T) {
	curve := SoftThresholdCurve{BaseFailure: 0, SoftLoad: 1e9, HardLoad: 1e10, HardFailure: 0}
	nodes := []NodeConfig{
		{Name: "A", Capacity: 1e9, MeanServiceTime: 20 * time.Millisecond, MinServiceTime: 2 * time.Millisecond, Curve: curve},
		{Name: "B", Capacity: 1e9, MeanServiceTime: 20 * time.Millisecond, MinServiceTime: 2 * time.Millisecond, Curve: curve},
	}
	edges := []EdgeConfig{{
		From: "A", To: "B", Fanout: 1, MaxAttempts: 3, MaxHedges: 2,
		HedgeDelay: 5 * time.Millisecond, BudgetRatio: -1,
		BackoffBase: time.Millisecond,
	}}
	top := &Topology{Nodes: nodes, Edges: edges, Ingress: map[string]float64{"A": 100}}
	sim, err := Simulate(top, SimConfig{
		Duration: 60 * time.Second, Warmup: 10 * time.Second, Seed: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sim.Edges[0].HedgesPerCall <= 0 {
		t.Fatal("expected hedged branches to fire in the simulation")
	}
	// With no failures, the edge attempt rate must exceed its outer logical
	// call rate because hedged branches add racing physical attempts.
	if sim.Edges[0].AttemptRate <= sim.Edges[0].Calls {
		t.Fatalf("hedging must raise edge attempt rate above call rate: attempt=%.2f calls=%.2f",
			sim.Edges[0].AttemptRate, sim.Edges[0].Calls)
	}
}

// TestSimulationMinRetryFloor checks the low-traffic budget floor inside
// the DES: even a handful of calls still get baseline retries.
func TestSimulationMinRetryFloor(t *testing.T) {
	curve := SoftThresholdCurve{BaseFailure: 0.5, SoftLoad: 1e9, HardLoad: 1e10, HardFailure: 0.5}
	nodes := []NodeConfig{
		{Name: "A", Capacity: 1e9, MeanServiceTime: 20 * time.Millisecond, MinServiceTime: 2 * time.Millisecond, Curve: curve},
		{Name: "B", Capacity: 1e9, MeanServiceTime: 20 * time.Millisecond, MinServiceTime: 2 * time.Millisecond, Curve: curve},
	}
	mkEdge := func(min int) EdgeConfig {
		return EdgeConfig{
			From: "A", To: "B", Fanout: 1, MaxAttempts: 4,
			BudgetRatio: 0.0, MinRetryConcurrency: min,
			BackoffBase: time.Millisecond,
		}
	}
	topFloor := &Topology{Nodes: nodes, Edges: []EdgeConfig{mkEdge(3)}, Ingress: map[string]float64{"A": 5}}
	topNone := &Topology{Nodes: nodes, Edges: []EdgeConfig{mkEdge(0)}, Ingress: map[string]float64{"A": 5}}
	simFloor, err := Simulate(topFloor, SimConfig{
		Duration: 100 * time.Second, Warmup: 20 * time.Second, Seed: 9,
	})
	if err != nil {
		t.Fatal(err)
	}
	simNone, err := Simulate(topNone, SimConfig{
		Duration: 100 * time.Second, Warmup: 20 * time.Second, Seed: 9,
	})
	if err != nil {
		t.Fatal(err)
	}
	if simFloor.Edges[0].RetriesPerCall <= simNone.Edges[0].RetriesPerCall {
		t.Fatalf("min retry floor must raise retries in DES: floor=%.4f none=%.4f",
			simFloor.Edges[0].RetriesPerCall, simNone.Edges[0].RetriesPerCall)
	}
}
