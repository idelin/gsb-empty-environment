package admission

import (
	"math/big"
	"testing"
)

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rational: " + s)
	}
	return r
}

func mustCompile(t *testing.T, m *Model) *Analyzer {
	t.Helper()
	a, err := m.Compile()
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return a
}

// After a hidden coin, only the "a" branch reaches a public admission
// decision (publish vs. abstain); the "b" branch always completes. This is a
// conditional abstention whose failure mass is exactly the selectable branch
// (1/2), so completed rounds are a selectively truncated sample. No retry.
func conditionalAbstentionModel() *Model {
	return &Model{
		Participants: []Participant{{
			Subject:         "alice",
			VisibleMessages: []MessageType{"m1"},
		}},
		PublicMessages: []MessageType{"m1"},
		Root:           "root",
		MaxRetries:     0,
		Ideal: map[string]*big.Rat{
			"A": rat("1/2"),
			"B": rat("1/2"),
		},
		Nodes: []Node{
			{ID: "root", Kind: KindEntry, Out: []Edge{{To: "coin"}}},
			{ID: "coin", Kind: KindDraw, Draw: &DrawSpec{
				Name:  "coin",
				Table: ProbabilityTable{"a": rat("1/2"), "b": rat("1/2")},
			}, Out: []Edge{
				{To: "da", OnOutcome: "a"},
				{To: "tb", OnOutcome: "b"},
			}},
			{ID: "da", Kind: KindDecision, Decision: &DecisionSpec{
				Subject:          "alice",
				ObservedMessages: []MessageType{"m1"},
				Actions:          []string{"publish", "abstain"},
				AbstainAction:    "abstain",
			}, Out: []Edge{
				{To: "ta", OnAction: "publish"},
				{To: "failA", OnAction: "abstain"},
			}},
			{ID: "ta", Kind: KindTerminal, Terminal: &TerminalSpec{Category: "A"}},
			{ID: "tb", Kind: KindTerminal, Terminal: &TerminalSpec{Category: "B"}},
			{ID: "failA", Kind: KindTerminal, Terminal: &TerminalSpec{Category: FailureCategory}},
		},
	}
}

func TestConditionalAbstention(t *testing.T) {
	a := mustCompile(t, conditionalAbstentionModel())
	res, err := a.Enumerate()
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if res.PolicyCount != 2 {
		t.Fatalf("PolicyCount = %d, want 2", res.PolicyCount)
	}
	if res.FailureBound.Cmp(rat("1/2")) != 0 {
		t.Fatalf("FailureBound = %s, want 1/2", res.FailureBound.RatString())
	}
	if res.MinCompletion.Cmp(rat("1/2")) != 0 {
		t.Fatalf("MinCompletion = %s, want 1/2", res.MinCompletion.RatString())
	}
	for _, b := range res.CategoryBounds {
		wantCond := map[string]string{"A": "1/2", "B": "1/2"}
		wantMass := map[string]string{"A": "1/2", "B": "0"}
		if b.WorstConditionalDeviation.Cmp(rat(wantCond[b.Category])) != 0 {
			t.Fatalf("category %s cond deviation = %s, want %s", b.Category, b.WorstConditionalDeviation.RatString(), wantCond[b.Category])
		}
		if b.WorstMassDeviation.Cmp(rat(wantMass[b.Category])) != 0 {
			t.Fatalf("category %s mass deviation = %s, want %s", b.Category, b.WorstMassDeviation.RatString(), wantMass[b.Category])
		}
	}

	// Under publish, conditional shares equal the ideal 1/2-1/2 and nothing
	// is lost.
	dist, err := a.Evaluate(Policy{"c00": "publish"})
	if err != nil {
		t.Fatalf("Evaluate publish: %v", err)
	}
	if dist.CompletionMass.Cmp(rat("1")) != 0 || dist.FailureMass.Sign() != 0 {
		t.Fatalf("publish distribution wrong: completion=%s failure=%s", dist.CompletionMass, dist.FailureMass)
	}
	for _, cat := range []string{"A", "B"} {
		if dist.ConditionalShare[cat].Cmp(rat("1/2")) != 0 {
			t.Fatalf("conditional share %s = %s, want 1/2", cat, dist.ConditionalShare[cat])
		}
	}

	// Under abstention, branch "a" is dropped (failure 1/2). The remaining
	// completed sample is entirely category B: normalization over the
	// selectively surviving rounds yields B=1, A=0.
	abst, err := a.Evaluate(Policy{"c00": "abstain"})
	if err != nil {
		t.Fatalf("Evaluate abstain: %v", err)
	}
	if abst.FailureMass.Cmp(rat("1/2")) != 0 || abst.CompletionMass.Cmp(rat("1/2")) != 0 {
		t.Fatalf("abstain failure=%s completion=%s, want 1/2 and 1/2", abst.FailureMass, abst.CompletionMass)
	}
	if abst.ConditionalShare["A"].Sign() != 0 || abst.ConditionalShare["B"].Cmp(rat("1")) != 0 {
		t.Fatalf("abstain conditional shares = A:%s B:%s, want 0 and 1", abst.ConditionalShare["A"], abst.ConditionalShare["B"])
	}
}

// A deterministic model with unequal outcome probabilities exercises exact
// normalization over successful (non-failed) rounds.
func normalizationModel() *Model {
	return &Model{
		Participants: []Participant{{
			Subject:         "alice",
			VisibleMessages: []MessageType{"m1"},
		}},
		PublicMessages: []MessageType{"m1"},
		Root:           "root",
		MaxRetries:     0,
		Ideal: map[string]*big.Rat{
			"A": rat("1/2"),
			"B": rat("1/2"),
		},
		Nodes: []Node{
			{ID: "root", Kind: KindEntry, Out: []Edge{{To: "draw"}}},
			{ID: "draw", Kind: KindDraw, Draw: &DrawSpec{
				Name:  "draw",
				Table: ProbabilityTable{"a": rat("1/2"), "b": rat("1/3"), "f": rat("1/6")},
			}, Out: []Edge{
				{To: "ta", OnOutcome: "a"},
				{To: "tb", OnOutcome: "b"},
				{To: "fail", OnOutcome: "f"},
			}},
			{ID: "ta", Kind: KindTerminal, Terminal: &TerminalSpec{Category: "A"}},
			{ID: "tb", Kind: KindTerminal, Terminal: &TerminalSpec{Category: "B"}},
			{ID: "fail", Kind: KindTerminal, Terminal: &TerminalSpec{Category: FailureCategory}},
		},
	}
}

func TestNormalizationOverSuccessfulRounds(t *testing.T) {
	a := mustCompile(t, normalizationModel())
	res, err := a.Enumerate()
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	// No decisions: exactly one rule, failure 1/6, completion 5/6.
	if res.PolicyCount != 1 {
		t.Fatalf("PolicyCount = %d, want 1", res.PolicyCount)
	}
	if res.FailureBound.Cmp(rat("1/6")) != 0 {
		t.Fatalf("FailureBound = %s, want 1/6", res.FailureBound)
	}
	dist, err := a.Evaluate(Policy{})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if dist.SuccessMass["A"].Cmp(rat("1/2")) != 0 || dist.SuccessMass["B"].Cmp(rat("1/3")) != 0 {
		t.Fatalf("unconditional masses = A:%s B:%s", dist.SuccessMass["A"], dist.SuccessMass["B"])
	}
	// Conditional normalization: (1/2)/(5/6)=3/5 and (1/3)/(5/6)=2/5.
	if dist.ConditionalShare["A"].Cmp(rat("3/5")) != 0 || dist.ConditionalShare["B"].Cmp(rat("2/5")) != 0 {
		t.Fatalf("conditional shares = A:%s B:%s, want 3/5 and 2/5", dist.ConditionalShare["A"], dist.ConditionalShare["B"])
	}
	condSum := new(big.Rat).Add(dist.ConditionalShare["A"], dist.ConditionalShare["B"])
	if condSum.Cmp(rat("1")) != 0 {
		t.Fatalf("conditional shares sum to %s, want 1", condSum)
	}
	if res.CategoryBounds[0].WorstConditionalDeviation.Cmp(rat("1/10")) != 0 {
		t.Fatalf("A conditional deviation = %s, want 1/10", res.CategoryBounds[0].WorstConditionalDeviation)
	}
	if res.CategoryBounds[1].WorstConditionalDeviation.Cmp(rat("1/10")) != 0 {
		t.Fatalf("B conditional deviation = %s, want 1/10", res.CategoryBounds[1].WorstConditionalDeviation)
	}
}

// Two secret states (hidden coin outcomes) lead to two decision nodes that
// carry the identical public prefix. They must collapse to one decision
// variable, so the subject cannot choose differently on hidden branches.
func sharedObservationModel() *Model {
	return &Model{
		Participants: []Participant{{
			Subject:         "bob",
			VisibleMessages: []MessageType{"m1"},
		}},
		PublicMessages: []MessageType{"m1"},
		Root:           "root",
		MaxRetries:     0,
		Ideal: map[string]*big.Rat{
			"A": rat("1/2"),
			"B": rat("1/2"),
		},
		Nodes: []Node{
			{ID: "root", Kind: KindEntry, Out: []Edge{{To: "hidden"}}},
			{ID: "hidden", Kind: KindDraw, Draw: &DrawSpec{
				Name:  "hidden",
				Table: ProbabilityTable{"s0": rat("1/2"), "s1": rat("1/2")},
			}, Out: []Edge{
				{To: "d0", OnOutcome: "s0"},
				{To: "d1", OnOutcome: "s1"},
			}},
			{ID: "d0", Kind: KindDecision, Decision: &DecisionSpec{
				Subject:          "bob",
				ObservedMessages: []MessageType{"m1"},
				Actions:          []string{"publish", "abstain"},
				AbstainAction:    "abstain",
			}, Out: []Edge{
				{To: "split", OnAction: "publish"},
				{To: "fail0", OnAction: "abstain"},
			}},
			{ID: "d1", Kind: KindDecision, Decision: &DecisionSpec{
				Subject:          "bob",
				ObservedMessages: []MessageType{"m1"},
				Actions:          []string{"publish", "abstain"},
				AbstainAction:    "abstain",
			}, Out: []Edge{
				{To: "split", OnAction: "publish"},
				{To: "fail1", OnAction: "abstain"},
			}},
			// Shared successor: the round tree merges secret states again.
			{ID: "split", Kind: KindDraw, Draw: &DrawSpec{
				Name:  "split",
				Table: ProbabilityTable{"a": rat("1/2"), "b": rat("1/2")},
			}, Out: []Edge{
				{To: "ta", OnOutcome: "a"},
				{To: "tb", OnOutcome: "b"},
			}},
			{ID: "ta", Kind: KindTerminal, Terminal: &TerminalSpec{Category: "A"}},
			{ID: "tb", Kind: KindTerminal, Terminal: &TerminalSpec{Category: "B"}},
			{ID: "fail0", Kind: KindTerminal, Terminal: &TerminalSpec{Category: FailureCategory}},
			{ID: "fail1", Kind: KindTerminal, Terminal: &TerminalSpec{Category: FailureCategory}},
		},
	}
}

func TestSameObservationMergesSecretStates(t *testing.T) {
	a := mustCompile(t, sharedObservationModel())
	if len(a.vars) != 1 {
		t.Fatalf("decision variables = %d, want 1 for identical public prefixes", len(a.vars))
	}
	if a.nodeVar["d0"] != a.nodeVar["d1"] {
		t.Fatalf("d0 and d1 map to different variables: %d vs %d", a.nodeVar["d0"], a.nodeVar["d1"])
	}
	res, err := a.Enumerate()
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if res.PolicyCount != 2 {
		t.Fatalf("PolicyCount = %d, want 2 (hidden branching forbidden)", res.PolicyCount)
	}
	// The single variable governs both secret states: abstaining aborts both,
	// so the failure bound is one and no rule can keep s0 while dropping s1.
	if res.FailureBound.Cmp(rat("1")) != 0 {
		t.Fatalf("FailureBound = %s, want 1", res.FailureBound)
	}
	publish, err := a.Evaluate(Policy{"c00": "publish"})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if publish.CompletionMass.Cmp(rat("1")) != 0 {
		t.Fatalf("publish completion = %s, want 1", publish.CompletionMass)
	}
	abstain, err := a.Evaluate(Policy{"c00": "abstain"})
	if err != nil {
		t.Fatalf("Evaluate abstain: %v", err)
	}
	if abstain.FailureMass.Cmp(rat("1")) != 0 {
		t.Fatalf("abstain failure = %s, want 1 across both secret states", abstain.FailureMass)
	}
}
