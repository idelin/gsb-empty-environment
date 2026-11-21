package admission

import "testing"

// TestSeparatorCoverage builds a minimal two-block instance where the
// separator batches are genuinely shared, and checks the tree-DP verdict
// against exhaustive enumeration.
func TestSeparatorCoverage(t *testing.T) {
	req := PublishRequest{
		Batches: batches(
			"L0", "L", "c", "L1", "L", "c",
			"R0", "R", "c", "R1", "R", "c",
		),
		Edges: []Edge{
			// Block 0.
			{L: "L0", R: "R0", JointCategory: "A"},
			{L: "L1", R: "R0", JointCategory: "B"},
			// Block 1.
			{L: "L0", R: "R1", JointCategory: "B"},
			{L: "L1", R: "R1", JointCategory: "A"},
		},
		Quota:            Quota{Total: 2},
		PrivacyThreshold: 2,
		Decomposition: &Decomposition{Blocks: []Block{
			{ID: "b0", EdgeKeys: []string{"L0|R0", "L1|R0"}},
			{ID: "b1", EdgeKeys: []string{"L0|R1", "L1|R1"}},
		}},
	}
	report, err := Admit(req)
	if err != nil {
		t.Fatal(err)
	}
	p, err := build(req, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	brute := bruteForce(p)
	feasible := report.Decision != DecisionInfeasible
	s := newSolver(p)
	for b, info := range s.infos {
		t.Logf("block %d boundary=%v parentMask=%b children=%v childMask=%v",
			b, info.boundary, info.parentMask, info.children, info.childMask)
		for mask, states := range info.local {
			t.Logf("  local mask=%b n=%d", mask, len(states))
		}
	}
	if brute != feasible {
		t.Fatalf("feasibility mismatch: brute=%v solver=%v boundary=%v", brute, feasible, p.boundaryBatches)
	}
}
