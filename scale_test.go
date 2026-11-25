package handshake

import (
	"testing"

	"prescreen.local/ps-802b9bdc43/bdd"
)

func TestFortyEightCapabilitiesStaySymbolic(t *testing.T) {
	const width = 48
	manager, err := bdd.NewManager(width)
	if err != nil {
		t.Fatal(err)
	}
	variables := make([]bdd.BDD, width)
	for index := range variables {
		variables[index], _ = manager.Variable(index)
	}

	// A function over all 48 bits with only 49 unique nodes: parity-like chain.
	root := variables[0]
	for index := 1; index < width; index++ {
		root = manager.Xor(root, variables[index])
	}
	if manager.NumNodes() > 2*width*width {
		t.Fatalf("ROBDD should stay polynomial without enumerating 2^%d, got %d nodes", width, manager.NumNodes())
	}
	minimal := manager.MinSolution(root)
	if minimal == nil || len(minimal) != width {
		t.Fatalf("invalid minimal solution: %v", minimal)
	}
	ones := 0
	for _, bit := range minimal {
		if bit {
			ones++
		}
	}
	if ones != 1 || !minimal[width-1] {
		t.Fatalf("odd-parity minimum should set only the last bit: %v", minimal)
	}

	accept := RuleSet{Rules: []Rule{{When: bdd.One, Outcome: AuthOutcome(3)}}}
	weak := RuleSet{Rules: []Rule{{When: bdd.One, Outcome: AuthOutcome(1)}}}
	guarded := RuleSet{Rules: []Rule{
		{When: root, Outcome: AuthOutcome(3)},
		{When: bdd.One, Outcome: RejectOutcome()},
	}}
	nodes := []NodeConfig{
		{NodeID: 1, Old: accept, New: guarded},
		{NodeID: 2, Old: weak, New: accept},
	}
	topology, err := NewTopology(manager, nodes, []int{1, 2}, bdd.One, nil)
	if err != nil {
		t.Fatal(err)
	}
	violation, err := topology.SmallestViolation(NewSwitchSet().With(1), 0)
	if err != nil {
		t.Fatal(err)
	}
	if violation == nil {
		t.Fatal("expected a weaker-than-both-endpoints violation")
	}
	if violation.Kind != "weaker-than-both-endpoints" {
		t.Fatalf("unexpected kind %q", violation.Kind)
	}
}

func TestEveryTicketStartIsSymbolicallyChecked(t *testing.T) {
	const width = 4
	manager, err := bdd.NewManager(width)
	if err != nil {
		t.Fatal(err)
	}
	vars := make([]bdd.BDD, width)
	for index := range vars {
		vars[index], _ = manager.Variable(index)
	}
	nodes := []NodeConfig{
		{
			NodeID: 1,
			Old:    RuleSet{Rules: []Rule{{When: vars[0], Outcome: AuthOutcome(2)}}},
			New:    RuleSet{Rules: []Rule{{When: vars[1], Outcome: AuthOutcome(2)}}},
		},
		{
			NodeID: 2,
			Old:    RuleSet{Rules: []Rule{{When: vars[2], Outcome: AuthOutcome(2)}}},
			New:    RuleSet{Rules: []Rule{{When: vars[3], Outcome: AuthOutcome(2)}}},
		},
	}
	topology, err := NewTopology(manager, nodes, []int{1, 2}, bdd.One, nil)
	if err != nil {
		t.Fatal(err)
	}
	states := []SwitchSet{NewSwitchSet(), NewSwitchSet().With(1), NewSwitchSet().With(2), SwitchSetAll(topology)}
	violation, err := topology.CheckStates(states)
	if err != nil {
		t.Fatal(err)
	}

	// Independent exhaustive oracle over every state, start and vector.
	var found *Violation
	for _, state := range states {
		for start := 0; start < 2; start++ {
			for value := 0; value < 1<<width; value++ {
				values := bitsOf(value, width)
				oldOutcome, _ := topology.Evaluate(NewSwitchSet(), start, values)
				newOutcome, _ := topology.Evaluate(SwitchSetAll(topology), start, values)
				midOutcome, _ := topology.Evaluate(state, start, values)
				bad := (DefaultStrength(midOutcome) < DefaultStrength(oldOutcome) &&
					DefaultStrength(midOutcome) < DefaultStrength(newOutcome)) ||
					(!oldOutcome.IsReject() && !newOutcome.IsReject() && midOutcome.IsReject())
				if bad && (found == nil || lessBitOrder(values, found.Capabilities)) {
					found = &Violation{Capabilities: values, State: state, Start: start}
				}
			}
		}
	}
	if (violation == nil) != (found == nil) {
		t.Fatalf("symbolic/exhaustive disagreement: %+v vs %+v", violation, found)
	}
	if violation != nil && !lessBitOrder(violation.Capabilities, found.Capabilities) &&
		!equalBools(violation.Capabilities, found.Capabilities) {
		t.Fatalf("symbolic counterexample %v is not the exhaustive minimum %v", violation.Capabilities, found.Capabilities)
	}
}

func bitsOf(value, width int) []bool {
	result := make([]bool, width)
	for index := range result {
		result[index] = value&(1<<(width-1-index)) != 0
	}
	return result
}

func equalBools(left, right []bool) bool {
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
