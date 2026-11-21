package admission

import "testing"

func TestGatewayRejectsConflictingBatchAndKeepsCommitment(t *testing.T) {
	gw := NewGateway()
	first := PublishRequest{
		Batches:          []Batch{{ID: "L1", Side: "L", Category: "x"}, {ID: "R1", Side: "R", Category: "y"}},
		Edges:            []Edge{{L: "L1", R: "R1", JointCategory: "J"}},
		PrivacyThreshold: 1,
	}
	if r, err := gw.Publish(first); err != nil || r.Decision != DecisionPublishable {
		t.Fatalf("first publish failed: %v %v", r, err)
	}

	conflict := PublishRequest{
		Batches: []Batch{{ID: "L1", Side: "L", Category: "different"}},
		Edges:   []Edge{{L: "L1", R: "R1", JointCategory: "J"}},
	}
	if _, err := gw.Publish(conflict); err == nil {
		t.Fatal("expected conflict error")
	}
	snap := gw.Snapshot()
	if len(snap.Batches) != 2 || snap.Batches[0].Category != "x" {
		t.Fatalf("commitment changed after rejected request: %+v", snap.Batches)
	}
}

func TestGatewayQuotaTightensAcrossReleases(t *testing.T) {
	gw := NewGateway()
	base := PublishRequest{
		Batches: []Batch{
			{ID: "L1", Side: "L", Category: "x"},
			{ID: "R1", Side: "R", Category: "y"},
		},
		Edges:            []Edge{{L: "L1", R: "R1", JointCategory: "J"}},
		PrivacyThreshold: 1,
		Quota:            Quota{MaxPerCategory: map[string]int{"J": 5}},
	}
	if r, err := gw.Publish(base); err != nil || r.Decision != DecisionPublishable {
		t.Fatalf("base publish: %v %v", r, err)
	}

	// A tighter quota that forbids category J makes the merged pairing
	// infeasible; the rejection must leave the original quota in place.
	tighter := PublishRequest{
		Batches:          []Batch{},
		Quota:            Quota{MaxPerCategory: map[string]int{"J": 0}},
		PrivacyThreshold: 1,
	}
	r, err := gw.Publish(tighter)
	if err != nil {
		t.Fatalf("tighter publish: %v", err)
	}
	if r.Decision != DecisionInfeasible {
		t.Fatalf("decision=%v, want infeasible", r.Decision)
	}
	if got := gw.Snapshot().Quota.MaxPerCategory["J"]; got != 5 {
		t.Fatalf("quota changed to %d after rejection, want 5", got)
	}
}
