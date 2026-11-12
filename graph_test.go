package netsec

import "testing"

func TestSelectRevokedIntermediateNode(t *testing.T) {
	h := newTestHarness(t)
	manifest := &Manifest{
		Nodes: []VersionNode{
			h.node(t, "v1", LevelLow, false, nil, nil),
			h.node(t, "v2", LevelStable, true, nil, nil),
			h.node(t, "v3", LevelStable, false, nil, nil),
			h.node(t, "v4", LevelHardened, false, nil, nil),
		},
		Edges: []Edge{
			{From: "v1", To: "v2", Cost: 1},
			{From: "v2", To: "v4", Cost: 1},
			{From: "v1", To: "v3", Cost: 5},
			{From: "v3", To: "v4", Cost: 1},
		},
	}

	path, reason := Select(manifest, "v1", h.verifier)
	if reason != nil {
		t.Fatalf("unexpected rejection: %v", reason)
	}
	if !equalStrings(path.Versions, []string{"v1", "v3"}) {
		t.Fatalf("path versions = %v, want [v1 v3], the cheaper eligible destination",
			path.Versions)
	}
	if path.TotalCost != 5 {
		t.Fatalf("total cost = %d, want 5", path.TotalCost)
	}
	if !equalInts(path.Costs, []int{5}) {
		t.Fatalf("edge costs = %v, want [5]", path.Costs)
	}
	if path.Level != LevelStable {
		t.Fatalf("destination level = %d, want %d", path.Level, LevelStable)
	}

	if r := NodeEligibility(manifest, "v2", h.verifier); r == nil || r.Code != RejectRevoked {
		t.Fatalf("v2 eligibility = %+v, want revoked", r)
	}

	// With v3 revoked as well, v4 becomes unreachable because every route to
	// it passes through a revoked intermediate, so the client stays on v1.
	manifest.Nodes[2].Revoked = true
	path, reason = Select(manifest, "v1", h.verifier)
	if reason != nil {
		t.Fatalf("unexpected rejection after revoking v3: %v", reason)
	}
	if !equalStrings(path.Versions, []string{"v1"}) || path.TotalCost != 0 {
		t.Fatalf("path = %v cost %d, want zero-length fallback [v1]",
			path.Versions, path.TotalCost)
	}
}

func TestSelectZeroCostCycle(t *testing.T) {
	h := newTestHarness(t)
	manifest := &Manifest{
		Nodes: []VersionNode{
			h.node(t, "v1", LevelLow, false, nil, nil),
			h.node(t, "v2", LevelLow, false, nil, nil),
			h.node(t, "v3", LevelStable, false, nil, nil),
		},
		Edges: []Edge{
			{From: "v1", To: "v2", Cost: 0},
			{From: "v2", To: "v1", Cost: 0},
			{From: "v2", To: "v3", Cost: 0},
			{From: "v3", To: "v2", Cost: 0},
		},
	}

	path, reason := Select(manifest, "v1", h.verifier)
	if reason != nil {
		t.Fatalf("unexpected rejection: %v", reason)
	}
	if !equalStrings(path.Versions, []string{"v1", "v2"}) {
		t.Fatalf("zero-cost migration must win over staying, got %v", path.Versions)
	}
	if path.TotalCost != 0 || !equalInts(path.Costs, []int{0}) {
		t.Fatalf("path must be zero cost via the cycle graph, got cost %d edges %v",
			path.TotalCost, path.Costs)
	}
}

func TestSelectForcedMigrationAcrossZeroCostCycle(t *testing.T) {
	h := newTestHarness(t)
	manifest := &Manifest{
		Nodes: []VersionNode{
			h.node(t, "v1", LevelLow, true, nil, nil),
			h.node(t, "v2", LevelLow, false, nil, nil),
			h.node(t, "v3", LevelStable, false, nil, nil),
		},
		Edges: []Edge{
			{From: "v1", To: "v2", Cost: 0},
			{From: "v2", To: "v1", Cost: 0},
			{From: "v2", To: "v3", Cost: 3},
			{From: "v3", To: "v2", Cost: 0},
		},
	}

	path, reason := Select(manifest, "v1", h.verifier)
	if reason != nil {
		t.Fatalf("unexpected rejection: %v", reason)
	}
	if !equalStrings(path.Versions, []string{"v1", "v2"}) {
		t.Fatalf("path = %v, want shortest [v1 v2] without cycling", path.Versions)
	}
	if path.TotalCost != 0 {
		t.Fatalf("total cost = %d, want 0", path.TotalCost)
	}
}
