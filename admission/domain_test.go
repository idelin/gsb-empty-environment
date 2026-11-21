package admission

import (
	"math/big"
	"strings"
	"testing"
)

func domainModel() *Model {
	m := conditionalAbstentionModel()
	m.Participants[0].FailureDomain = "zone-1"
	m.HostConditions = []HostCondition{{
		Domain:           "zone-1",
		ObservedMessages: []MessageType{"m1"},
		Rule:             "host-policy-7",
		Action:           "publish",
	}}
	return m
}

func TestFailureDomainHostCondition(t *testing.T) {
	a := mustCompile(t, domainModel())
	conds := a.Conditions()
	if len(conds) != 1 || !conds[0].HostFixed || conds[0].HostAction != "publish" {
		t.Fatalf("condition not host-fixed: %+v", conds)
	}
	// Enumeration must respect the host condition: the abstain action is
	// inadmissible for this variable.
	if _, err := a.Evaluate(Policy{"c00": "abstain"}); err == nil {
		t.Fatal("host-fixed domain accepted an overridden action")
	}
	res, err := a.Enumerate()
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if res.PolicyCount != 1 || res.FailureBound.Sign() != 0 {
		t.Fatalf("policies=%d failure=%s, want 1 and 0", res.PolicyCount, res.FailureBound)
	}
}

func TestFailureDomainRequiresCondition(t *testing.T) {
	m := domainModel()
	m.HostConditions = nil
	if _, err := m.Compile(); err == nil {
		t.Fatal("Compile accepted failure domain without a host condition")
	}
}

func TestInvisibleMessageRejected(t *testing.T) {
	m := conditionalAbstentionModel()
	m.Participants[0].VisibleMessages = nil
	if _, err := m.Compile(); err == nil {
		t.Fatal("Compile accepted decision over an invisible message")
	}
}

func TestBadProbabilityTableRejected(t *testing.T) {
	m := conditionalAbstentionModel()
	for i := range m.Nodes {
		if m.Nodes[i].ID == "coin" {
			m.Nodes[i].Draw.Table = ProbabilityTable{"a": rat("1/3"), "b": rat("1/3")}
		}
	}
	if _, err := m.Compile(); err == nil {
		t.Fatal("Compile accepted probabilities summing to 2/3")
	}
}

func TestDecisionPointCap(t *testing.T) {
	m := &Model{
		Participants: []Participant{{Subject: "s"}},
		Root:         "r",
		MaxRetries:   0,
		Ideal:        map[string]*big.Rat{"A": rat("1")},
	}
	n := MaxDecisionPoints + 1
	visible := make([]MessageType, 0, n)
	messages := make([]MessageType, 0, n)
	for i := 0; i < n; i++ {
		msg := MessageType("m" + itoa(i))
		visible = append(visible, msg)
		messages = append(messages, msg)
	}
	m.Participants[0].VisibleMessages = visible
	m.PublicMessages = messages

	nodes := []Node{{ID: "r", Kind: KindEntry, Out: []Edge{{To: "d0"}}}}
	for i := 0; i < n; i++ {
		next := "ta"
		if i+1 < n {
			next = decisionID(i + 1)
		}
		nodes = append(nodes, Node{
			ID: decisionID(i), Kind: KindDecision,
			Decision: &DecisionSpec{
				Subject:          "s",
				ObservedMessages: []MessageType{MessageType("m" + itoa(i))},
				Actions:          []string{"go", "abstain"},
				AbstainAction:    "abstain",
			},
			Out: []Edge{
				{To: next, OnAction: "go"},
				{To: "fail", OnAction: "abstain"},
			},
		})
	}
	nodes = append(nodes,
		Node{ID: "ta", Kind: KindTerminal, Terminal: &TerminalSpec{Category: "A"}},
		Node{ID: "fail", Kind: KindTerminal, Terminal: &TerminalSpec{Category: FailureCategory}},
	)
	m.Nodes = nodes
	_, err := m.Compile()
	if err == nil || !strings.Contains(err.Error(), "exceed cap") {
		t.Fatalf("expected decision cap error, got %v", err)
	}
}

func decisionID(i int) string { return "d" + itoa(i) }

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [12]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
