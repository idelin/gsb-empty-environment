package steady

import (
	"math"
	"testing"
	"time"
)

func abs(v float64) float64 { return math.Abs(v) }

// TestSolverDeadlineLimitsAttempts verifies that a tight edge deadline caps
// the number of attempts exactly like the runtime Deadline.EnoughFor
// pre-check, suppressing retry amplification.
func TestSolverDeadlineLimitsAttempts(t *testing.T) {
	curveDead := SoftThresholdCurve{BaseFailure: 1, SoftLoad: 1e9, HardLoad: 1e10, HardFailure: 1}
	curveOK := SoftThresholdCurve{BaseFailure: 0, SoftLoad: 1e9, HardLoad: 1e10, HardFailure: 0}
	nodes := []NodeConfig{
		{Name: "A", Capacity: 1e9, MeanServiceTime: time.Millisecond, MinServiceTime: time.Millisecond, Curve: curveOK},
		{Name: "B", Capacity: 1e9, MeanServiceTime: time.Millisecond, MinServiceTime: time.Millisecond, Curve: curveDead},
	}
	edge := EdgeConfig{
		From: "A", To: "B", Fanout: 1, MaxAttempts: 4,
		BudgetRatio: -1, BackoffBase: 10 * time.Millisecond,
	}
	topNoDeadline := &Topology{
		Nodes:   nodes,
		Edges:   []EdgeConfig{edge},
		Ingress: map[string]float64{"A": 100},
	}
	resFull, err := Solve(topNoDeadline)
	if err != nil {
		t.Fatal(err)
	}
	if got := resFull.Edges[0].AttemptsPerCall; got != 4 {
		t.Fatalf("no deadline: attempts/call = %.3f, want 4", got)
	}

	// Deadline 11ms: the first retry needs backoff 10ms + minProc 1ms = 11ms
	// and the pre-check is strictly greater-than, so only attempt 1 fits.
	tight := edge
	tight.RequestDeadline = 11 * time.Millisecond
	topTight := &Topology{
		Nodes: nodes, Edges: []EdgeConfig{tight},
		Ingress: map[string]float64{"A": 100},
	}
	resTight, err := Solve(topTight)
	if err != nil {
		t.Fatal(err)
	}
	if !resTight.Edges[0].DeadlineLimited {
		t.Fatal("tight deadline must mark the edge deadline-limited")
	}
	if got := resTight.Edges[0].AttemptsPerCall; got != 1 {
		t.Fatalf("tight deadline: attempts/call = %.3f, want 1", got)
	}
	if abs(resTight.Nodes[1].PhysicalArrival-100) > 1e-6 {
		t.Fatalf("deadline-truncated B load = %.3f, want 100", resTight.Nodes[1].PhysicalArrival)
	}
}

// TestMinRetryConcurrencyFloor checks that the budget floor still admits a
// baseline retry volume when the ratio-derived slot count rounds to zero.
func TestMinRetryConcurrencyFloor(t *testing.T) {
	curve := SoftThresholdCurve{BaseFailure: 0.5, SoftLoad: 1e9, HardLoad: 1e10, HardFailure: 0.5}
	nodes := []NodeConfig{
		{Name: "A", Capacity: 1e9, MeanServiceTime: 10 * time.Millisecond, MinServiceTime: time.Millisecond, Curve: curve},
		{Name: "B", Capacity: 1e9, MeanServiceTime: 10 * time.Millisecond, MinServiceTime: time.Millisecond, Curve: curve},
	}
	mkEdge := func(min int) EdgeConfig {
		return EdgeConfig{
			From: "A", To: "B", Fanout: 1, MaxAttempts: 4,
			BudgetRatio: 0.01, MinRetryConcurrency: min,
			BackoffBase: time.Millisecond,
		}
	}
	topNoFloor := &Topology{Nodes: nodes, Edges: []EdgeConfig{mkEdge(0)}, Ingress: map[string]float64{"A": 5}}
	resNo, err := Solve(topNoFloor)
	if err != nil {
		t.Fatal(err)
	}
	topFloor := &Topology{Nodes: nodes, Edges: []EdgeConfig{mkEdge(2)}, Ingress: map[string]float64{"A": 5}}
	resFloor, err := Solve(topFloor)
	if err != nil {
		t.Fatal(err)
	}
	if resFloor.Edges[0].ExtraPerCall <= resNo.Edges[0].ExtraPerCall {
		t.Fatalf("minRetryConcurrency floor must raise extra attempts: floor=%.4f nofloor=%.4f",
			resFloor.Edges[0].ExtraPerCall, resNo.Edges[0].ExtraPerCall)
	}
}

// TestHedgingAmplifiesAndIsBudgetCapped checks that hedged branches add
// expected load through the exponential service-time tail and that the
// retry budget caps the combined retry+hedge extra attempts.
func TestHedgingAmplifiesAndIsBudgetCapped(t *testing.T) {
	curve := SoftThresholdCurve{BaseFailure: 0.01, SoftLoad: 1e9, HardLoad: 1e10, HardFailure: 0.5}
	nodes := []NodeConfig{
		{Name: "A", Capacity: 1e9, MeanServiceTime: 20 * time.Millisecond, MinServiceTime: 2 * time.Millisecond, Curve: curve},
		{Name: "B", Capacity: 1e9, MeanServiceTime: 20 * time.Millisecond, MinServiceTime: 2 * time.Millisecond, Curve: curve},
	}
	mkEdge := func(budget float64) EdgeConfig {
		return EdgeConfig{
			From: "A", To: "B", Fanout: 1, MaxAttempts: 3, MaxHedges: 2,
			HedgeDelay: 5 * time.Millisecond, BudgetRatio: budget,
			BackoffBase: time.Millisecond,
		}
	}
	topUnlimited := &Topology{Nodes: nodes, Edges: []EdgeConfig{mkEdge(-1)}, Ingress: map[string]float64{"A": 100}}
	resU, err := Solve(topUnlimited)
	if err != nil {
		t.Fatal(err)
	}
	if resU.Edges[0].ExtraPerCall <= 0 {
		t.Fatal("hedging must add extra attempts per call")
	}
	topCapped := &Topology{Nodes: nodes, Edges: []EdgeConfig{mkEdge(0.0)}, Ingress: map[string]float64{"A": 100}}
	resC, err := Solve(topCapped)
	if err != nil {
		t.Fatal(err)
	}
	if resC.Edges[0].ExtraPerCall != 0 {
		t.Fatalf("zero budget with no floor must eliminate all extra attempts, got %.4f", resC.Edges[0].ExtraPerCall)
	}
	if resC.Nodes[1].PhysicalArrival >= resU.Nodes[1].PhysicalArrival {
		t.Fatal("budget cap must reduce hedged physical load")
	}
}
