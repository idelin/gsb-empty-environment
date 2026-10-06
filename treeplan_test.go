package treeplan

import (
	"fmt"
	"math/rand"
)

// randomSpec builds a random tree spec with the given number of groups.
// Every group gets 0-2 leaves; costs, penalties and memory are small so
// brute-force checkers stay tractable.
func randomSpec(r *rand.Rand, numGroups int) *Spec {
	groups := []*Group{{Name: "g0"}}
	leafCount := 0
	for len(groups) < numGroups {
		parent := groups[r.Intn(len(groups))]
		g := &Group{Name: fmt.Sprintf("g%d", len(groups))}
		parent.Groups = append(parent.Groups, g)
		groups = append(groups, g)
	}
	var leaves []string
	for _, g := range groups {
		g.ExpandCost = int64(r.Intn(9))
		g.CollapseCost = int64(r.Intn(4))
		g.Memory = int64(r.Intn(7))
		for i := 0; i < r.Intn(3); i++ {
			name := fmt.Sprintf("l%d", leafCount)
			leafCount++
			g.Leaves = append(g.Leaves, Leaf{
				Name:      name,
				Penalty:   int64(r.Intn(10)),
				Memory:    int64(r.Intn(6)),
				ServeCost: int64(r.Intn(3)),
			})
			leaves = append(leaves, name)
		}
	}
	if len(leaves) == 0 {
		groups[0].Leaves = append(groups[0].Leaves, Leaf{Name: "l0"})
		leaves = append(leaves, "l0")
	}
	r.Shuffle(len(leaves), func(i, j int) { leaves[i], leaves[j] = leaves[j], leaves[i] })
	numTargets := 1 + r.Intn(len(leaves))
	if numTargets > 4 {
		numTargets = 4
	}
	return &Spec{Root: groups[0], Targets: leaves[:numTargets]}
}

func mustBuild(t interface{ Fatalf(string, ...interface{}) }, spec *Spec) *Tree {
	tr, err := BuildTree(spec)
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}
	return tr
}
