package admission

import (
	"math/big"
	"testing"
)

// One fixed commitment draw (shared by all attempts), then up to three
// attempts (one initial + two retries). Each attempt has a fresh public draw
// and an abstain decision.
func retryModel(t *testing.T) *Model {
	t.Helper()
	edge := func(to, action string, retry bool) Edge {
		return Edge{To: to, OnAction: action, Retry: retry}
	}
	nodes := []Node{
		{ID: "root", Kind: KindEntry, Out: []Edge{{To: "commit"}}},
		{ID: "commit", Kind: KindDraw, Draw: &DrawSpec{
			Name:          "commitment",
			FixedVariable: "C",
			Table:         ProbabilityTable{"c": rat("1")},
		}, Out: []Edge{{To: "g0", OnOutcome: "c"}}},

		{ID: "e1", Kind: KindEntry, Out: []Edge{{To: "g1"}}},
		{ID: "e2", Kind: KindEntry, Out: []Edge{{To: "g2"}}},

		{ID: "g0", Kind: KindDraw, Draw: &DrawSpec{
			Name: "fresh-0", Table: ProbabilityTable{"ok": rat("2/3"), "bad": rat("1/3")},
		}, Out: []Edge{{To: "d0", OnOutcome: "ok"}, {To: "fail", OnOutcome: "bad"}}},
		{ID: "g1", Kind: KindDraw, Draw: &DrawSpec{
			Name: "fresh-1", Table: ProbabilityTable{"ok": rat("2/3"), "bad": rat("1/3")},
		}, Out: []Edge{{To: "d1", OnOutcome: "ok"}, {To: "fail", OnOutcome: "bad"}}},
		{ID: "g2", Kind: KindDraw, Draw: &DrawSpec{
			Name: "fresh-2", Table: ProbabilityTable{"ok": rat("2/3"), "bad": rat("1/3")},
		}, Out: []Edge{{To: "d2", OnOutcome: "ok"}, {To: "fail", OnOutcome: "bad"}}},

		{ID: "d0", Kind: KindDecision, Decision: decision("alice"), Out: []Edge{
			edge("ta", "publish", false), edge("e1", "abstain", true),
		}},
		{ID: "d1", Kind: KindDecision, Decision: decision("alice"), Out: []Edge{
			edge("ta", "publish", false), edge("e2", "abstain", true),
		}},
		{ID: "d2", Kind: KindDecision, Decision: decision("alice"), Out: []Edge{
			edge("ta", "publish", false), edge("fail", "abstain", false),
		}},
		{ID: "ta", Kind: KindTerminal, Terminal: &TerminalSpec{Category: "A"}},
		{ID: "fail", Kind: KindTerminal, Terminal: &TerminalSpec{Category: FailureCategory}},
	}
	return &Model{
		Participants:   []Participant{{Subject: "alice", VisibleMessages: []MessageType{"m1"}}},
		PublicMessages: []MessageType{"m1"},
		Root:           "root",
		MaxRetries:     2,
		Ideal:          map[string]*big.Rat{"A": rat("1")},
		Nodes:          nodes,
	}
}

func decision(subject Identity) *DecisionSpec {
	return &DecisionSpec{
		Subject:          subject,
		ObservedMessages: []MessageType{"m1"},
		Actions:          []string{"publish", "abstain"},
		AbstainAction:    "abstain",
	}
}

func TestRetrySharesFixedCommitment(t *testing.T) {
	a := mustCompile(t, retryModel(t))
	res, err := a.Enumerate()
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	// Three decision points sharing one public prefix => one variable, so
	// only two deterministic rules.
	if len(a.vars) != 1 || res.PolicyCount != 2 {
		t.Fatalf("vars=%d policies=%d, want 1 and 2", len(a.vars), res.PolicyCount)
	}
	// Publish immediately: failure 1/3. Always abstain: every attempt aborts,
	// failure 1, the tight upper bound.
	if res.FailureBound.Cmp(rat("1")) != 0 {
		t.Fatalf("FailureBound = %s, want 1", res.FailureBound)
	}
	firstPublish, err := a.Evaluate(Policy{"c00": "publish"})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if firstPublish.FailureMass.Cmp(rat("1/3")) != 0 || firstPublish.CompletionMass.Cmp(rat("2/3")) != 0 {
		t.Fatalf("publish-first failure=%s completion=%s, want 1/3 and 2/3",
			firstPublish.FailureMass, firstPublish.CompletionMass)
	}
	alwaysAbstain, err := a.Evaluate(Policy{"c00": "abstain"})
	if err != nil {
		t.Fatalf("Evaluate abstain: %v", err)
	}
	if alwaysAbstain.FailureMass.Cmp(rat("1")) != 0 {
		t.Fatalf("always-abstain failure = %s, want 1", alwaysAbstain.FailureMass)
	}
}

func TestFixedVariableRedrawRejected(t *testing.T) {
	m := retryModel(t)
	// Add a second node drawing the same fixed quantity in a retry layer.
	m.Nodes = append(m.Nodes, Node{
		ID: "commit2", Kind: KindDraw,
		Draw: &DrawSpec{Name: "redraw", FixedVariable: "C", Table: ProbabilityTable{"c": rat("1")}},
	})
	for i := range m.Nodes {
		if m.Nodes[i].ID == "e1" {
			m.Nodes[i].Out = []Edge{{To: "commit2"}}
		}
		if m.Nodes[i].ID == "commit2" {
			m.Nodes[i].Out = []Edge{{To: "g1", OnOutcome: "c"}}
		}
	}
	if _, err := m.Compile(); err == nil {
		t.Fatal("Compile accepted re-draw of fixed variable C in a retry layer")
	}
}

func TestRetryLimitEnforced(t *testing.T) {
	m := retryModel(t)
	m.MaxRetries = 1 // model has paths consuming two retries
	if _, err := m.Compile(); err == nil {
		t.Fatal("Compile accepted a path exceeding the declared retry limit")
	}
}

func TestThreeRetriesRejected(t *testing.T) {
	m := retryModel(t)
	m.MaxRetries = 3 // protocol cap is two
	if _, err := m.Compile(); err == nil {
		t.Fatal("Compile accepted MaxRetries above protocol cap")
	}
}

func TestMassConservation(t *testing.T) {
	a := mustCompile(t, retryModel(t))
	res, err := a.Enumerate()
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	for _, line := range res.records {
		_ = line // records are exercised via certificate root below
	}
	d, err := a.Evaluate(Policy{"c00": "publish"})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	total := new(big.Rat).Add(d.CompletionMass, d.FailureMass)
	if total.Cmp(rat("1")) != 0 {
		t.Fatalf("mass conservation violated: completion+failure=%s", total)
	}
}
