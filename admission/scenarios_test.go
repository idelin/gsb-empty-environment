package admission

import "testing"

// TestScenarioLocallyTwoGloballyOne covers "each local view offers two
// candidates, but the global constraints leave only one feasible joint
// category". Every protected batch has two incident edges, yet quotas make a
// single matching the unique globally feasible one.
func TestScenarioLocallyTwoGloballyOne(t *testing.T) {
	req := PublishRequest{
		Batches: batches(
			"L1", "L", "x", "L2", "L", "x", "L3", "L", "x",
			"R1", "R", "y", "R2", "R", "z", "R3", "R", "w",
		),
		Edges: []Edge{
			{L: "L1", R: "R1", JointCategory: "A"},
			{L: "L1", R: "R2", JointCategory: "B"},
			{L: "L2", R: "R1", JointCategory: "B"},
			{L: "L2", R: "R2", JointCategory: "A"},
			{L: "L3", R: "R3", JointCategory: "A"},
			{L: "L3", R: "R1", JointCategory: "B"},
		},
		// Perfect matchings differ in how often joint category A occurs.
		// The cap eliminates every matching except one, while each protected
		// batch still sees two locally incident candidate edges.
		Quota: Quota{
			MaxPerCategory: map[string]int{"A": 2},
			Total:          3,
		},
		ProtectedBatches: []string{"L1", "L2", "L3", "R1", "R2", "R3"},
		PrivacyThreshold: 2,
	}

	report, err := Admit(req)
	if err != nil {
		t.Fatalf("Admit returned error: %v", err)
	}
	if report.Decision != DecisionBelowThreshold {
		t.Fatalf("decision = %q, want %q", report.Decision, DecisionBelowThreshold)
	}
	for _, b := range report.Batches {
		if b.PossibleJointCategories != 1 {
			t.Errorf("batch %s possible joint categories = %d, want 1 (local edges are not the answer)",
				b.BatchID, b.PossibleJointCategories)
		}
	}

	// Independent enumeration must reach the same verdict and counts.
	p, err := build(req, DefaultOptions())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !bruteForce(p) {
		t.Fatal("brute force found no feasible matching although solver did")
	}
	for _, id := range req.ProtectedBatches {
		joint, _ := bruteAmbiguity(p, id)
		if joint != 1 {
			t.Errorf("brute force joint categories for %s = %d, want 1", id, joint)
		}
	}
}

// TestScenarioTwoTablesNoCommonPairing covers "each table is pairable on its
// own, but the two tables admit no common feasible pairing". The gateway
// admits the first table, rejects the second against the union, and keeps its
// prior commitment untouched.
func TestScenarioTwoTablesNoCommonPairing(t *testing.T) {
	gw := NewGateway()

	first := PublishRequest{
		Batches: batches("L1", "L", "x", "L2", "L", "x", "R1", "R", "y", "R2", "R", "y"),
		Edges: []Edge{
			{L: "L1", R: "R1", JointCategory: "A"},
			{L: "L1", R: "R2", JointCategory: "B"},
			{L: "L2", R: "R1", JointCategory: "B"},
			{L: "L2", R: "R2", JointCategory: "A"},
		},
		ProtectedBatches: []string{"L1"},
		PrivacyThreshold: 2,
	}
	r1, err := gw.Publish(first)
	if err != nil {
		t.Fatalf("first publish: %v", err)
	}
	if r1.Decision != DecisionPublishable {
		t.Fatalf("first decision = %q, want publishable", r1.Decision)
	}

	// On its own this release is a balanced 2x2 table with feasible perfect
	// matchings. The cross edges connect it to the committed table; the B
	// quota rules out every perfect matching of the merged 4x4 problem, so
	// the two tables admit no common feasible pairing.
	second := PublishRequest{
		Batches: batches("L3", "L", "x", "L4", "L", "x", "R3", "R", "y", "R4", "R", "y"),
		Edges: []Edge{
			{L: "L3", R: "R3", JointCategory: "B"},
			{L: "L3", R: "R4", JointCategory: "A"},
			{L: "L4", R: "R3", JointCategory: "A"},
			{L: "L4", R: "R4", JointCategory: "B"},
			// Cross edges turn the union into one connected pairing problem.
			{L: "L1", R: "R3", JointCategory: "B"},
			{L: "L3", R: "R1", JointCategory: "B"},
		},
		Quota:            Quota{MaxPerCategory: map[string]int{"A": 3, "B": 1}},
		ProtectedBatches: []string{"L3"},
		PrivacyThreshold: 2,
	}

	// Independent confirmation: the second table considered alone is feasible.
	standalone := PublishRequest{
		Batches: batches("L3", "L", "x", "L4", "L", "x", "R3", "R", "y", "R4", "R", "y"),
		Edges: []Edge{
			{L: "L3", R: "R3", JointCategory: "B"},
			{L: "L3", R: "R4", JointCategory: "A"},
			{L: "L4", R: "R3", JointCategory: "A"},
			{L: "L4", R: "R4", JointCategory: "B"},
		},
		Quota:            Quota{MaxPerCategory: map[string]int{"A": 3, "B": 1}},
		PrivacyThreshold: 2,
	}
	if r, err := Admit(standalone); err != nil || r.Decision == DecisionInfeasible {
		t.Fatalf("second table must be feasible on its own, got decision=%v err=%v", r.Decision, err)
	}

	r2, err := gw.Publish(second)
	if err != nil {
		t.Fatalf("second publish: %v", err)
	}
	if r2.Decision != DecisionInfeasible {
		t.Fatalf("merged decision = %q, want infeasible", r2.Decision)
	}

	// Rejection must not change the commitment: snapshot is still table one.
	snap := gw.Snapshot()
	if len(snap.Batches) != 4 || len(snap.Edges) != 4 {
		t.Fatalf("gateway state mutated after rejection: %d batches %d edges",
			len(snap.Batches), len(snap.Edges))
	}
}

// TestScenarioJointMoreDeterminedThanColumns covers "the joint category is
// easier to pin down than either per-column category". Both sides retain two
// possible counterpart synthetic categories, but every feasible pairing
// produces the same joint category.
func TestScenarioJointMoreDeterminedThanColumns(t *testing.T) {
	req := PublishRequest{
		Batches: batches(
			"L1", "L", "x", "L2", "L", "x",
			"R1", "R", "y", "R2", "R", "z",
		),
		Edges: []Edge{
			{L: "L1", R: "R1", JointCategory: "J"},
			{L: "L1", R: "R2", JointCategory: "J"},
			{L: "L2", R: "R1", JointCategory: "J"},
			{L: "L2", R: "R2", JointCategory: "J"},
		},
		ProtectedBatches: []string{"L1", "L2"},
		PrivacyThreshold: 2,
	}
	report, err := Admit(req)
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if report.Decision != DecisionBelowThreshold {
		t.Fatalf("decision = %q, want below_threshold", report.Decision)
	}
	for _, b := range report.Batches {
		if b.PossibleJointCategories != 1 {
			t.Errorf("%s joint categories = %d, want 1", b.BatchID, b.PossibleJointCategories)
		}
		if b.PossibleCounterpartCategories != 2 {
			t.Errorf("%s counterpart categories = %d, want 2", b.BatchID, b.PossibleCounterpartCategories)
		}
		if b.PossibleJointCategories >= b.PossibleCounterpartCategories {
			t.Errorf("joint category should be more determined than per-column category: joint=%d counterpart=%d",
				b.PossibleJointCategories, b.PossibleCounterpartCategories)
		}
	}

	p, err := build(req, DefaultOptions())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !bruteForce(p) {
		t.Fatal("brute force disagrees on feasibility")
	}
	for _, id := range req.ProtectedBatches {
		joint, counterpart := bruteAmbiguity(p, id)
		if joint != 1 || counterpart != 2 {
			t.Errorf("brute force mismatch for %s: joint=%d counterpart=%d", id, joint, counterpart)
		}
	}
}

func batches(parts ...string) []Batch {
	if len(parts)%3 != 0 {
		panic("batches requires id/side/category triples")
	}
	out := make([]Batch, 0, len(parts)/3)
	for i := 0; i < len(parts); i += 3 {
		out = append(out, Batch{ID: parts[i], Side: parts[i+1], Category: parts[i+2]})
	}
	return out
}
