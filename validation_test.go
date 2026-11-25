package handshake

import (
	"strings"
	"testing"

	"prescreen.local/ps-802b9bdc43/bdd"
)

func TestSchedulingInputValidation(t *testing.T) {
	manager, err := bdd.NewManager(2)
	if err != nil {
		t.Fatal(err)
	}
	accept := RuleSet{Rules: []Rule{{When: bdd.One, Outcome: AuthOutcome(1)}}}
	nodes := []NodeConfig{
		{NodeID: 1, Old: accept, New: accept},
		{NodeID: 2, Old: accept, New: accept},
	}
	topology, err := NewTopology(manager, nodes, []int{1, 2}, bdd.One, nil)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		input SchedulingInput
		want  string
	}{
		{
			name: "dependency cycle",
			input: SchedulingInput{
				Topology:     topology,
				Dependencies: []Constraint{{Before: 1, After: 2}, {Before: 2, After: 1}},
				WindowNodes:  [][]int{{1, 2}},
			},
			want: "cycle",
		},
		{
			name: "node in multiple groups",
			input: SchedulingInput{
				Topology:     topology,
				AtomicGroups: [][]int{{1, 2}, {2, 1}},
				WindowNodes:  [][]int{{1, 2}},
			},
			want: "groups",
		},
		{
			name: "unknown dependency node",
			input: SchedulingInput{
				Topology:     topology,
				Dependencies: []Constraint{{Before: 9, After: 1}},
				WindowNodes:  [][]int{{1, 2}},
			},
			want: "unknown node",
		},
		{
			name: "empty windows",
			input: SchedulingInput{
				Topology: topology,
			},
			want: "maintenance window",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Schedule(tc.input)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestTopologyValidation(t *testing.T) {
	manager, _ := bdd.NewManager(2)
	accept := RuleSet{Rules: []Rule{{When: bdd.One, Outcome: AuthOutcome(1)}}}
	if _, err := NewTopology(manager, []NodeConfig{{NodeID: 1, Old: accept, New: accept}}, []int{9}, bdd.One, nil); err == nil {
		t.Fatal("expected unknown fallback node error")
	}
	if _, err := NewTopology(manager, nil, []int{1}, bdd.One, nil); err == nil {
		t.Fatal("expected empty topology error")
	}
}
