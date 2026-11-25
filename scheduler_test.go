package handshake

import (
	"testing"

	"prescreen.local/ps-802b9bdc43/bdd"
)

func atomicOnlyTopology(t *testing.T) (*bdd.Manager, *Topology) {
	t.Helper()
	manager, err := bdd.NewManager(3)
	if err != nil {
		t.Fatal(err)
	}
	x1, _ := manager.Variable(1)
	x2, _ := manager.Variable(2)
	strong := RuleSet{Rules: []Rule{{When: bdd.One, Outcome: AuthOutcome(3)}}}
	nodes := []NodeConfig{
		{
			NodeID: 1,
			Old:    strong,
			New: RuleSet{Rules: []Rule{
				{When: x1, Outcome: AuthOutcome(3)},
				{When: bdd.One, Outcome: RejectOutcome()},
			}},
		},
		{
			NodeID: 2,
			Old: RuleSet{Rules: []Rule{
				{When: x2, Outcome: AuthOutcome(3)},
				{When: bdd.One, Outcome: AuthOutcome(1)},
			}},
			New: RuleSet{Rules: []Rule{
				{When: x2, Outcome: AuthOutcome(3)},
				{When: bdd.One, Outcome: AuthOutcome(2)},
			}},
		},
	}
	topology, err := NewTopology(manager, nodes, []int{1, 2}, bdd.One, nil)
	if err != nil {
		t.Fatal(err)
	}
	return manager, topology
}

func TestAtomicBatchFeasibleOnlyWhenSwitchedTogether(t *testing.T) {
	_, topology := atomicOnlyTopology(t)
	windows := [][]int{{1, 2}, {1, 2}, {1, 2}}

	// Without an atomic group, switching node 1 alone creates the vector
	// (false,false,*): both endpoints connect, but the transition rejects. The
	// dependency removes the alternative safe ordering.
	split, err := Schedule(SchedulingInput{
		Topology:     topology,
		Dependencies: []Constraint{{Before: 1, After: 2}},
		WindowNodes:  windows,
	})
	if err != nil {
		t.Fatal(err)
	}
	if split.Plan != nil || split.Blocker == nil {
		t.Fatalf("split scheduling should be blocked, got %+v", split)
	}
	if err := VerifyBlocker(SchedulingInput{
		Topology:     topology,
		Dependencies: []Constraint{{Before: 1, After: 2}},
		WindowNodes:  windows,
	}, split.Blocker); err != nil {
		t.Fatalf("split blocker certificate failed verification: %v", err)
	}

	// The same release is feasible when both nodes form one atomic batch.
	together, err := Schedule(SchedulingInput{
		Topology:     topology,
		Dependencies: []Constraint{{Before: 1, After: 2}},
		AtomicGroups: [][]int{{1, 2}},
		WindowNodes:  windows,
	})
	if err != nil {
		t.Fatal(err)
	}
	if together.Blocker != nil || together.Plan == nil {
		t.Fatalf("atomic scheduling should succeed, got %+v", together)
	}
	if len(together.Plan.Batches) != 1 {
		t.Fatalf("expected one atomic batch, got %+v", together.Plan.Batches)
	}
	batch := together.Plan.Batches[0]
	if len(batch.Nodes) != 2 {
		t.Fatalf("unexpected atomic batch %+v", batch)
	}
	if err := VerifyPlan(SchedulingInput{
		Topology:     topology,
		Dependencies: []Constraint{{Before: 1, After: 2}},
		AtomicGroups: [][]int{{1, 2}},
		WindowNodes:  windows,
	}, together.Plan); err != nil {
		t.Fatalf("plan verification failed: %v", err)
	}

	// Independent exhaustive confirmation that the intermediate state is unsafe
	// only when the two nodes are switched apart.
	enumerateValues(3, func(values []bool) {
		splitOutcome, _ := topology.Evaluate(NewSwitchSet().With(1), 0, values)
		oldOutcome, _ := topology.Evaluate(NewSwitchSet(), 0, values)
		newOutcome, _ := topology.Evaluate(SwitchSetAll(topology), 0, values)
		weakened := DefaultStrength(splitOutcome) < DefaultStrength(oldOutcome) &&
			DefaultStrength(splitOutcome) < DefaultStrength(newOutcome)
		if values[1] || values[2] {
			if weakened {
				t.Fatalf("vector %v unexpectedly weak: old=%s new=%s mid=%s", values, oldOutcome, newOutcome, splitOutcome)
			}
			return
		}
		if !weakened {
			t.Fatalf("vector %v expected a weaker-than-both-endpoints intermediate result", values)
		}
	})
}

func TestMinimumWindowsAndLexicographicTieBreak(t *testing.T) {
	manager, err := bdd.NewManager(3)
	if err != nil {
		t.Fatal(err)
	}
	x0, _ := manager.Variable(0)
	x1, _ := manager.Variable(1)
	accept := RuleSet{Rules: []Rule{{When: bdd.One, Outcome: AuthOutcome(1)}}}
	nodes := []NodeConfig{
		{NodeID: 1, Old: accept, New: RuleSet{Rules: []Rule{{When: x0, Outcome: AuthOutcome(1)}}}},
		{NodeID: 2, Old: accept, New: RuleSet{Rules: []Rule{{When: x1, Outcome: AuthOutcome(1)}}}},
		{NodeID: 3, Old: accept, New: accept},
		{NodeID: 4, Old: accept, New: accept},
	}
	topology, err := NewTopology(manager, nodes, []int{1, 2, 3, 4}, bdd.One, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Every node is switchable in every window, so the minimum solution uses a
	// single window. Lexicographic ordering must select node sequence [1 2 3 4]
	// regardless of internal enumeration order.
	windows := [][]int{{1, 2, 3, 4}, {1, 2, 3, 4}}
	result, err := Schedule(SchedulingInput{Topology: topology, WindowNodes: windows})
	if err != nil {
		t.Fatal(err)
	}
	if result.Blocker != nil || result.Plan == nil {
		t.Fatalf("expected feasible plan, got blocker %+v", result.Blocker)
	}
	plan := result.Plan
	if len(plan.Batches) != 1 || plan.Batches[0].Window != 0 {
		t.Fatalf("expected one minimum-window batch, got %+v", plan.Batches)
	}
	if !equalInts(plan.Batches[0].Nodes, []int{1, 2, 3, 4}) {
		t.Fatalf("unexpected batch nodes %+v", plan.Batches[0].Nodes)
	}
	if err := VerifyPlan(SchedulingInput{Topology: topology, WindowNodes: windows}, plan); err != nil {
		t.Fatalf("plan verification failed: %v", err)
	}
}

func TestNoWindowBlockerIsVerifiable(t *testing.T) {
	_, topology := atomicOnlyTopology(t)
	result, err := Schedule(SchedulingInput{
		Topology:     topology,
		AtomicGroups: [][]int{{1, 2}},
		WindowNodes:  [][]int{{1}, {2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Blocker == nil || result.Blocker.Kind != "group-has-no-window" {
		t.Fatalf("expected group-has-no-window blocker, got %+v", result.Blocker)
	}
	if err := VerifyBlocker(SchedulingInput{
		Topology:     topology,
		AtomicGroups: [][]int{{1, 2}},
		WindowNodes:  [][]int{{1}, {2}},
	}, result.Blocker); err != nil {
		t.Fatalf("blocker verification failed: %v", err)
	}
}

func TestZeroLegalClientsScheduleIsAccepted(t *testing.T) {
	manager, err := bdd.NewManager(2)
	if err != nil {
		t.Fatal(err)
	}
	x0, _ := manager.Variable(0)
	accept := RuleSet{Rules: []Rule{{When: bdd.One, Outcome: AuthOutcome(1)}}}
	nodes := []NodeConfig{
		{NodeID: 1, Old: accept, New: RuleSet{Rules: []Rule{{When: bdd.Zero, Outcome: RejectOutcome()}}}},
	}
	legal := manager.And(x0, manager.Not(x0))
	topology, err := NewTopology(manager, nodes, []int{1}, legal, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Schedule(SchedulingInput{
		Topology:    topology,
		WindowNodes: [][]int{{1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.ZeroLegalClients || result.Plan == nil || result.Blocker != nil {
		t.Fatalf("zero-client release should schedule vacuously, got %+v", result)
	}
}

func equalInts(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func TestLexicographicTieBreakAcrossBatches(t *testing.T) {
	manager, err := bdd.NewManager(2)
	if err != nil {
		t.Fatal(err)
	}
	x0, _ := manager.Variable(0)
	x1, _ := manager.Variable(1)
	accept := RuleSet{Rules: []Rule{{When: bdd.One, Outcome: AuthOutcome(1)}}}
	nodes := []NodeConfig{
		{NodeID: 1, Old: accept, New: RuleSet{Rules: []Rule{{When: x0, Outcome: AuthOutcome(1)}}}},
		{NodeID: 2, Old: accept, New: RuleSet{Rules: []Rule{{When: x1, Outcome: AuthOutcome(1)}}}},
	}
	topology, err := NewTopology(manager, nodes, []int{1, 2}, bdd.One, nil)
	if err != nil {
		t.Fatal(err)
	}
	// A dependency forces two batches; both node orderings use two windows.
	// The first batch must be the smaller node id under lex ordering.
	result, err := Schedule(SchedulingInput{
		Topology:     topology,
		Dependencies: []Constraint{{Before: 1, After: 2}},
		WindowNodes:  [][]int{{1, 2}, {1, 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Blocker != nil || len(result.Plan.Batches) != 2 {
		t.Fatalf("expected two ordered batches, got %+v", result)
	}
	if !equalInts(result.Plan.Batches[0].Nodes, []int{1}) ||
		!equalInts(result.Plan.Batches[1].Nodes, []int{2}) {
		t.Fatalf("unexpected lex ordering %+v", result.Plan.Batches)
	}
}
