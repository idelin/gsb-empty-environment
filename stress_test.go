package handshake

import (
	"testing"

	"prescreen.local/ps-802b9bdc43/bdd"
)

func TestFourteenNodePlanAndUnsatCertificate(t *testing.T) {
	const nodeCount = 14
	manager, err := bdd.NewManager(6)
	if err != nil {
		t.Fatal(err)
	}
	vars := make([]bdd.BDD, 6)
	for index := range vars {
		vars[index], _ = manager.Variable(index)
	}
	accept := RuleSet{Rules: []Rule{{When: bdd.One, Outcome: AuthOutcome(2)}}}
	nodes := make([]NodeConfig, nodeCount)
	fallback := make([]int, nodeCount)
	for index := range nodes {
		nodes[index] = NodeConfig{
			NodeID: index + 1,
			Old:    accept,
			New: RuleSet{Rules: []Rule{
				{When: vars[index%6], Outcome: AuthOutcome(2)},
				{When: bdd.One, Outcome: AuthOutcome(1)},
			}},
		}
		fallback[index] = index + 1
	}
	topology, err := NewTopology(manager, nodes, fallback, bdd.One, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Every node has a window; a chain dependency forces an ordered rollout.
	windows := make([][]int, nodeCount+1)
	for index := range windows {
		windows[index] = fallback
	}
	var dependencies []Constraint
	for index := 1; index < nodeCount; index++ {
		dependencies = append(dependencies, Constraint{Before: index, After: index + 1})
	}
	input := SchedulingInput{
		Topology:     topology,
		Dependencies: dependencies,
		WindowNodes:  windows,
	}
	result, err := Schedule(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Blocker != nil || result.Plan == nil {
		t.Fatalf("expected feasible ordered rollout, got %+v", result.Blocker)
	}
	if err := VerifyPlan(input, result.Plan); err != nil {
		t.Fatalf("plan verification failed: %v", err)
	}

	// Remove node 14 from every window to force an independently checkable
	// static impossibility certificate.
	unsatWindows := make([][]int, nodeCount+1)
	for index := range unsatWindows {
		unsatWindows[index] = fallback[:nodeCount-1]
	}
	unsatInput := SchedulingInput{
		Topology:     topology,
		Dependencies: dependencies,
		WindowNodes:  unsatWindows,
	}
	unsat, err := Schedule(unsatInput)
	if err != nil {
		t.Fatal(err)
	}
	if unsat.Plan != nil || unsat.Blocker == nil {
		t.Fatalf("expected a blocker, got plan %+v", unsat.Plan)
	}
	if err := VerifyBlocker(unsatInput, unsat.Blocker); err != nil {
		t.Fatalf("blocker certificate failed: %v", err)
	}
}
