package admission

import (
	"math/big"
	"testing"
)

// Two draw branches merge into one shared decision node. The shared successor
// must receive the union of branch masses (here 1) without double counting,
// and total probability must be conserved for every admissible rule.
func TestSharedSuccessorMassConservation(t *testing.T) {
	m := &Model{
		Participants:   []Participant{{Subject: "s", VisibleMessages: []MessageType{"m"}}},
		PublicMessages: []MessageType{"m"},
		Root:           "r",
		MaxRetries:     0,
		Ideal:          map[string]*big.Rat{"A": rat("1")},
		Nodes: []Node{
			{ID: "r", Kind: KindEntry, Out: []Edge{{To: "g"}}},
			{ID: "g", Kind: KindDraw, Draw: &DrawSpec{
				Name:  "g",
				Table: ProbabilityTable{"x": rat("1/3"), "y": rat("2/3")},
			}, Out: []Edge{
				{To: "d", OnOutcome: "x"},
				{To: "d", OnOutcome: "y"},
			}},
			{ID: "d", Kind: KindDecision, Decision: &DecisionSpec{
				Subject:          "s",
				ObservedMessages: []MessageType{"m"},
				Actions:          []string{"go", "abstain"},
				AbstainAction:    "abstain",
			}, Out: []Edge{
				{To: "ta", OnAction: "go"},
				{To: "fail", OnAction: "abstain"},
			}},
			{ID: "ta", Kind: KindTerminal, Terminal: &TerminalSpec{Category: "A"}},
			{ID: "fail", Kind: KindTerminal, Terminal: &TerminalSpec{Category: FailureCategory}},
		},
	}
	a := mustCompile(t, m)
	res, err := a.Enumerate()
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	for _, action := range []string{"go", "abstain"} {
		d, err := a.Evaluate(Policy{"c00": action})
		if err != nil {
			t.Fatalf("Evaluate %s: %v", action, err)
		}
		total := new(big.Rat).Add(d.CompletionMass, d.FailureMass)
		if total.Cmp(rat("1")) != 0 {
			t.Fatalf("action %s violates mass conservation: %s", action, total)
		}
	}
	if res.PolicyCount != 2 {
		t.Fatalf("PolicyCount = %d, want 2", res.PolicyCount)
	}
}
