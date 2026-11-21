package admission

import (
	"math/big"
	"testing"
)

// Every rule aborts the round: conditional allocation is undefined and the
// package must return the trivial tight deviation bound of one.
func TestNeverCompletingRound(t *testing.T) {
	m := &Model{
		Participants:   []Participant{{Subject: "s", VisibleMessages: []MessageType{"m"}}},
		PublicMessages: []MessageType{"m"},
		Root:           "r",
		MaxRetries:     0,
		Ideal:          map[string]*big.Rat{"A": rat("1")},
		Nodes: []Node{
			{ID: "r", Kind: KindEntry, Out: []Edge{{To: "d"}}},
			{ID: "d", Kind: KindDecision, Decision: &DecisionSpec{
				Subject:          "s",
				ObservedMessages: []MessageType{"m"},
				// Both admissible actions lead to the failure terminal.
				Actions:       []string{"abstain", "withhold"},
				AbstainAction: "abstain",
			}, Out: []Edge{
				{To: "fail", OnAction: "abstain"},
				{To: "fail", OnAction: "withhold"},
			}},
			// The only reachable terminal is a failure: no rule completes.
			{ID: "fail", Kind: KindTerminal, Terminal: &TerminalSpec{Category: FailureCategory}},
		},
	}
	a := mustCompile(t, m)
	res, err := a.Enumerate()
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if res.PolicyCount != 2 {
		t.Fatalf("PolicyCount = %d, want 2", res.PolicyCount)
	}
	if res.FailureBound.Cmp(rat("1")) != 0 || res.MinCompletion.Sign() != 0 {
		t.Fatalf("failure=%s minCompletion=%s, want 1 and 0", res.FailureBound, res.MinCompletion)
	}
	if res.CategoryBounds[0].WorstConditionalDeviation.Cmp(rat("1")) != 0 {
		t.Fatalf("undefined allocation must bound deviation by 1, got %s",
			res.CategoryBounds[0].WorstConditionalDeviation)
	}
	d, err := a.Evaluate(Policy{"c00": "withhold"})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if d.ConditionalShare != nil {
		t.Fatal("ConditionalShare must be nil when no round completes")
	}
}

// A three-action decision point enumerates exactly three deterministic rules.
func TestThreeActionEnumeration(t *testing.T) {
	m := &Model{
		Participants:   []Participant{{Subject: "s", VisibleMessages: []MessageType{"m"}}},
		PublicMessages: []MessageType{"m"},
		Root:           "r",
		MaxRetries:     0,
		Ideal: map[string]*big.Rat{
			"A": rat("1/2"),
			"B": rat("1/2"),
		},
		Nodes: []Node{
			{ID: "r", Kind: KindEntry, Out: []Edge{{To: "d"}}},
			{ID: "d", Kind: KindDecision, Decision: &DecisionSpec{
				Subject:          "s",
				ObservedMessages: []MessageType{"m"},
				Actions:          []string{"publish", "abstain", "defer"},
				AbstainAction:    "abstain",
			}, Out: []Edge{
				{To: "g", OnAction: "publish"},
				{To: "fail", OnAction: "abstain"},
				{To: "g", OnAction: "defer"},
			}},
			{ID: "g", Kind: KindDraw, Draw: &DrawSpec{
				Name:  "g",
				Table: ProbabilityTable{"a": rat("1/2"), "b": rat("1/2")},
			}, Out: []Edge{
				{To: "ta", OnOutcome: "a"},
				{To: "tb", OnOutcome: "b"},
			}},
			{ID: "ta", Kind: KindTerminal, Terminal: &TerminalSpec{Category: "A"}},
			{ID: "tb", Kind: KindTerminal, Terminal: &TerminalSpec{Category: "B"}},
			{ID: "fail", Kind: KindTerminal, Terminal: &TerminalSpec{Category: FailureCategory}},
		},
	}
	a := mustCompile(t, m)
	res, err := a.Enumerate()
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if res.PolicyCount != 3 {
		t.Fatalf("PolicyCount = %d, want 3", res.PolicyCount)
	}
	if res.FailureBound.Cmp(rat("1")) != 0 {
		t.Fatalf("FailureBound = %s, want 1 via the abstain rule", res.FailureBound)
	}
}
