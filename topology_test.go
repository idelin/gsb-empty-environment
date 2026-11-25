package handshake

import (
	"testing"

	"prescreen.local/ps-802b9bdc43/bdd"
)

func buildTestTopology(t *testing.T, width int) (*bdd.Manager, *Topology, []bdd.BDD) {
	t.Helper()
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
			Old: RuleSet{Name: "old-1", Rules: []Rule{
				{Name: "auth", When: vars[0], Outcome: AuthOutcome(1)},
			}},
			New: RuleSet{Name: "new-1", Rules: []Rule{
				{Name: "auth", When: vars[1], Outcome: AuthOutcome(2)},
			}},
		},
		{
			NodeID: 2,
			Old: RuleSet{Name: "old-2", Rules: []Rule{
				{Name: "auth", When: vars[2], Outcome: AuthOutcome(3)},
			}},
			New: RuleSet{Name: "new-2", Rules: []Rule{
				{Name: "strong", When: vars[0], Outcome: AuthOutcome(2)},
				{Name: "auth", When: vars[2], Outcome: AuthOutcome(2)},
			}},
		},
	}
	topology, err := NewTopology(manager, nodes, []int{1, 2}, bdd.One, nil)
	if err != nil {
		t.Fatal(err)
	}
	return manager, topology, vars
}

func TestSymbolicResultMatchesExhaustiveFallback(t *testing.T) {
	const width = 3
	manager, topology, _ := buildTestTopology(t, width)
	states := []SwitchSet{
		NewSwitchSet(),
		NewSwitchSet().With(1),
		NewSwitchSet().With(2),
		NewSwitchSet().With(1, 2),
	}
	for _, state := range states {
		for start := 0; start < 2; start++ {
			result, err := topology.SymbolicResult(state, start)
			if err != nil {
				t.Fatal(err)
			}
			enumerateValues(width, func(values []bool) {
				direct, err := topology.Evaluate(state, start, values)
				if err != nil {
					t.Fatal(err)
				}
				root, ok := result[direct]
				if !ok {
					t.Fatalf("state %v start %d: outcome %s missing from symbolic partition", state.IDs(), start, direct)
				}
				matched, err := manager.Eval(root, values)
				if err != nil {
					t.Fatal(err)
				}
				if !matched {
					t.Fatalf("state %v start %d vector %v: symbolic BDD does not cover %s", state.IDs(), start, values, direct)
				}
			})
		}
	}
}

func TestRejectFallbackAndTicketRevalidation(t *testing.T) {
	const width = 3
	_, topology, _ := buildTestTopology(t, width)

	// Vector (false,false,true): node 1 rejects in every state, node 2 accepts.
	values := []bool{false, false, true}
	outcome, err := topology.Evaluate(NewSwitchSet(), 0, values)
	if err != nil || outcome != AuthOutcome(3) {
		t.Fatalf("fallback expected AUTH(3), got %s err=%v", outcome, err)
	}

	// A ticket pinned to node 2 starts at index 1 and is revalidated there.
	outcome, err = topology.Evaluate(NewSwitchSet().With(2), 1, values)
	if err != nil || outcome != AuthOutcome(2) {
		t.Fatalf("ticket revalidation expected AUTH(2), got %s err=%v", outcome, err)
	}

	// A ticket pinned to node 1 cannot reach node 2's earlier fallback position.
	outcome, err = topology.Evaluate(NewSwitchSet(), 1, []bool{false, false, false})
	if err != nil || !outcome.IsReject() {
		t.Fatalf("ticket start expected reject, got %s err=%v", outcome, err)
	}

	violation, err := topology.SmallestViolation(NewSwitchSet().With(1), 0)
	if err != nil {
		t.Fatal(err)
	}
	if violation == nil {
		t.Fatal("expected a reject-while-both-endpoints-connect capability counterexample")
	}
	if violation.Kind != "reject-while-both-endpoints-connect" {
		t.Fatalf("unexpected violation kind %q", violation.Kind)
	}
	wantVector := []bool{true, false, false}
	for index := range wantVector {
		if violation.Capabilities[index] != wantVector[index] {
			t.Fatalf("counterexample = %v, want %v", violation.Capabilities, wantVector)
		}
	}

	// The all-old and all-new endpoint states must themselves be safe.
	for _, state := range []SwitchSet{NewSwitchSet(), SwitchSetAll(topology)} {
		for start := 0; start < 2; start++ {
			violation, err := topology.SmallestViolation(state, start)
			if err != nil {
				t.Fatal(err)
			}
			if violation != nil {
				t.Fatalf("endpoint state %v start %d unexpectedly unsafe: %+v", state.IDs(), start, violation)
			}
		}
	}
}

func TestZeroLegalClientsIsVacuouslySafe(t *testing.T) {
	manager, err := bdd.NewManager(2)
	if err != nil {
		t.Fatal(err)
	}
	x0, _ := manager.Variable(0)
	nodes := []NodeConfig{{
		NodeID: 7,
		Old:    RuleSet{Rules: []Rule{{When: bdd.One, Outcome: AuthOutcome(1)}}},
		New:    RuleSet{Rules: []Rule{{When: bdd.One, Outcome: RejectOutcome()}}},
	}}
	legal := manager.And(x0, manager.Not(x0))
	topology, err := NewTopology(manager, nodes, []int{7}, legal, nil)
	if err != nil {
		t.Fatal(err)
	}
	if topology.HasLegalClient() {
		t.Fatal("empty legal capability set expected")
	}
	violation, err := topology.CheckStates([]SwitchSet{NewSwitchSet(), NewSwitchSet().With(7)})
	if err != nil {
		t.Fatal(err)
	}
	if violation != nil {
		t.Fatalf("no legal clients must be vacuously safe, got %+v", violation)
	}
}
