package admission

import (
	"math/big"
	"testing"
)

// Build a wide layered DAG with thousands of shared-state nodes but only a
// handful of public decision points, mirroring the stated operating regime.
func largeDAGModel(width, layers int) *Model {
	rootEdges := make([]Edge, 0, width)
	for col := 0; col < width; col++ {
		rootEdges = append(rootEdges, Edge{To: nodeName(0, col), OnOutcome: "o" + itoa(col)})
	}
	nodes := []Node{
		{ID: "r", Kind: KindEntry, Out: []Edge{{To: "fan"}}},
		{ID: "fan", Kind: KindDraw, Draw: &DrawSpec{
			Name:  "fan",
			Table: uniformTable(width),
		}, Out: rootEdges},
	}
	for layer := 0; layer < layers; layer++ {
		for col := 0; col < width; col++ {
			id := nodeName(layer, col)
			edges := []Edge{}
			if layer+1 < layers {
				edges = append(edges,
					Edge{To: nodeName(layer+1, col), OnOutcome: "x"},
					Edge{To: nodeName(layer+1, (col+1)%width), OnOutcome: "y"},
				)
			} else {
				// Last layer fans into two shared terminals.
				edges = append(edges,
					Edge{To: "d", OnOutcome: "x"},
					Edge{To: "d", OnOutcome: "y"},
				)
			}
			nodes = append(nodes, Node{
				ID:   id,
				Kind: KindDraw,
				Draw: &DrawSpec{
					Name:  id,
					Table: ProbabilityTable{"x": rat("1/2"), "y": rat("1/2")},
				},
				Out: edges,
			})
		}
	}
	nodes = append(nodes,
		Node{ID: "d", Kind: KindDecision, Decision: &DecisionSpec{
			Subject:          "s",
			ObservedMessages: []MessageType{"m"},
			Actions:          []string{"go", "abstain"},
			AbstainAction:    "abstain",
		}, Out: []Edge{
			{To: "ta", OnAction: "go"},
			{To: "fail", OnAction: "abstain"},
		}},
		Node{ID: "ta", Kind: KindTerminal, Terminal: &TerminalSpec{Category: "A"}},
		Node{ID: "fail", Kind: KindTerminal, Terminal: &TerminalSpec{Category: FailureCategory}},
	)
	return &Model{
		Participants:   []Participant{{Subject: "s", VisibleMessages: []MessageType{"m"}}},
		PublicMessages: []MessageType{"m"},
		Root:           "r",
		MaxRetries:     0,
		Ideal:          map[string]*big.Rat{"A": rat("1")},
		Nodes:          nodes,
	}
}

func nodeName(layer, col int) string {
	return "g" + itoa(layer) + "-" + itoa(col)
}

func uniformTable(n int) ProbabilityTable {
	t := ProbabilityTable{}
	for i := 0; i < n; i++ {
		t["o"+itoa(i)] = new(big.Rat).Quo(big.NewRat(1, 1), big.NewRat(int64(n), 1))
	}
	return t
}

func TestThousandStateEnumeration(t *testing.T) {
	m := largeDAGModel(40, 60) // 2400 draw nodes + entry/decision/terminals
	if len(m.Nodes) < 2000 {
		t.Fatalf("model only has %d nodes", len(m.Nodes))
	}
	a := mustCompile(t, m)
	if len(a.vars) != 1 {
		t.Fatalf("decision variables = %d, want 1", len(a.vars))
	}
	res, err := a.Enumerate()
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if res.PolicyCount != 2 {
		t.Fatalf("PolicyCount = %d, want 2", res.PolicyCount)
	}
	// The merged decision is reached with total mass one; either rule
	// conserves all probability.
	if res.FailureBound.Cmp(rat("1")) != 0 {
		t.Fatalf("FailureBound = %s, want 1 (abstain rule)", res.FailureBound)
	}
	if res.MinCompletion.Sign() != 0 {
		t.Fatalf("MinCompletion = %s, want 0", res.MinCompletion)
	}
	goDist, err := a.Evaluate(Policy{"c00": "go"})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if goDist.CompletionMass.Cmp(big.NewRat(1, 1)) != 0 || goDist.FailureMass.Sign() != 0 {
		t.Fatalf("go rule conservation broken: completion=%s failure=%s",
			goDist.CompletionMass, goDist.FailureMass)
	}
}
