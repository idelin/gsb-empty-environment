package netsec

import (
	"crypto/ed25519"
	"testing"
)

func TestSelectSecurityLevelMustNotDecrease(t *testing.T) {
	h := newTestHarness(t)
	manifest := &Manifest{
		Nodes: []VersionNode{
			h.node(t, "v1", LevelStable, false, nil, nil),
			h.node(t, "v2", LevelLow, false, nil, nil),
			h.node(t, "v3", LevelStable, false, nil, nil),
		},
		Edges: []Edge{
			{From: "v1", To: "v2", Cost: 0},
			{From: "v1", To: "v3", Cost: 9},
		},
	}

	path, reason := Select(manifest, "v1", h.verifier)
	if reason != nil {
		t.Fatalf("unexpected rejection: %v", reason)
	}
	if !equalStrings(path.Versions, []string{"v1", "v3"}) {
		t.Fatalf("path = %v, downgrade edge must be ignored and level-equal v3 chosen", path.Versions)
	}

	manifest.Nodes[0].Revoked = true
	manifest.Nodes[2].Revoked = true
	path, reason = Select(manifest, "v1", h.verifier)
	if path != nil {
		t.Fatalf("unexpected path through a level decrease: %v", path.Versions)
	}
	if reason == nil || reason.Code != RejectNoEligibleDestination {
		t.Fatalf("reason = %+v, want no_eligible_destination", reason)
	}
}

func TestSelectCompatibilityAndBadSignature(t *testing.T) {
	h := newTestHarness(t)
	manifest := &Manifest{
		Nodes: []VersionNode{
			h.node(t, "v1", LevelLow, false, nil, nil),
			h.node(t, "v2", LevelStable, false,
				Features{"os": "linux"}, Features{"os": "darwin"}),
			h.node(t, "v3", LevelStable, false,
				Features{"os": "darwin"}, Features{"os": "darwin"}),
		},
		Edges: []Edge{
			{From: "v1", To: "v2", Cost: 1},
			{From: "v1", To: "v3", Cost: 4},
		},
	}

	path, reason := Select(manifest, "v1", h.verifier)
	if reason != nil {
		t.Fatalf("unexpected rejection: %v", reason)
	}
	if !equalStrings(path.Versions, []string{"v1", "v3"}) {
		t.Fatalf("path = %v, incompatible v2 must be skipped in favor of v3", path.Versions)
	}
	if r := NodeEligibility(manifest, "v2", h.verifier); r == nil || r.Code != RejectIncompatible {
		t.Fatalf("v2 eligibility = %+v, want incompatible", r)
	}

	manifest.Nodes[2].Security.Level = LevelHardened
	manifest.Nodes[0].Revoked = true
	path, reason = Select(manifest, "v1", h.verifier)
	if path != nil {
		t.Fatalf("unexpected path trusting a tampered level: %v", path.Versions)
	}
	if reason == nil || reason.Code != RejectNoEligibleDestination {
		t.Fatalf("reason = %+v, want no_eligible_destination", reason)
	}
	if r := NodeEligibility(manifest, "v3", h.verifier); r == nil || r.Code != RejectBadSignature {
		t.Fatalf("v3 eligibility = %+v, want bad_signature", r)
	}
}

func TestSelectTieBreakLexicographic(t *testing.T) {
	h := newTestHarness(t)
	manifest := &Manifest{
		Nodes: []VersionNode{
			h.node(t, "v1", LevelLow, true, nil, nil),
			h.node(t, "v2", LevelLow, false, nil, nil),
			h.node(t, "v3", LevelLow, false, nil, nil),
		},
		Edges: []Edge{
			{From: "v1", To: "v2", Cost: 5},
			{From: "v1", To: "v3", Cost: 5},
		},
	}

	path, reason := Select(manifest, "v1", h.verifier)
	if reason != nil {
		t.Fatalf("unexpected rejection: %v", reason)
	}
	if !equalStrings(path.Versions, []string{"v1", "v2"}) {
		t.Fatalf("path = %v, want lexicographically smallest tie [v1 v2]", path.Versions)
	}
}

func TestSelectManifestValidation(t *testing.T) {
	h := newTestHarness(t)

	t.Run("too many nodes", func(t *testing.T) {
		nodes := make([]VersionNode, MaxNodes+1)
		for i := range nodes {
			nodes[i] = h.node(t, nodeID(i), LevelLow, false, nil, nil)
		}
		manifest := &Manifest{Nodes: nodes}
		if err := manifest.Validate(); err == nil {
			t.Fatal("expected size validation error")
		}
		if _, reason := Select(manifest, nodeID(0), h.verifier); reason == nil ||
			reason.Code != RejectInvalidManifest {
			t.Fatalf("reason = %+v, want invalid_manifest", reason)
		}
	})

	t.Run("negative cost", func(t *testing.T) {
		manifest := &Manifest{
			Nodes: []VersionNode{
				h.node(t, "v1", LevelLow, false, nil, nil),
				h.node(t, "v2", LevelLow, false, nil, nil),
			},
			Edges: []Edge{{From: "v1", To: "v2", Cost: -1}},
		}
		if err := manifest.Validate(); err == nil {
			t.Fatal("expected negative cost validation error")
		}
	})

	t.Run("dangling edge", func(t *testing.T) {
		manifest := &Manifest{
			Nodes: []VersionNode{h.node(t, "v1", LevelLow, false, nil, nil)},
			Edges: []Edge{{From: "v1", To: "ghost", Cost: 1}},
		}
		if err := manifest.Validate(); err == nil {
			t.Fatal("expected dangling edge validation error")
		}
	})

	t.Run("duplicate edge", func(t *testing.T) {
		manifest := &Manifest{
			Nodes: []VersionNode{
				h.node(t, "v1", LevelLow, false, nil, nil),
				h.node(t, "v2", LevelLow, false, nil, nil),
			},
			Edges: []Edge{
				{From: "v1", To: "v2", Cost: 1},
				{From: "v1", To: "v2", Cost: 2},
			},
		}
		if err := manifest.Validate(); err == nil {
			t.Fatal("expected duplicate edge validation error")
		}
	})

	t.Run("current missing", func(t *testing.T) {
		manifest := &Manifest{
			Nodes: []VersionNode{h.node(t, "v1", LevelLow, false, nil, nil)},
		}
		_, reason := Select(manifest, "missing", h.verifier)
		if reason == nil || reason.Code != RejectCurrentNotFound {
			t.Fatalf("reason = %+v, want current_not_found", reason)
		}
	})

	t.Run("nil verifier", func(t *testing.T) {
		manifest := &Manifest{
			Nodes: []VersionNode{h.node(t, "v1", LevelLow, false, nil, nil)},
		}
		_, reason := Select(manifest, "v1", nil)
		if reason == nil || reason.Code != RejectVerifierMissing {
			t.Fatalf("reason = %+v, want verifier_missing", reason)
		}
	})
}

func TestThirtyNodeBoundary(t *testing.T) {
	h := newTestHarness(t)
	manifest := &Manifest{}
	for i := 0; i < MaxNodes; i++ {
		manifest.Nodes = append(manifest.Nodes,
			h.node(t, nodeID(i), SecurityLevel(i), false, nil, nil))
	}
	for i := 0; i+1 < MaxNodes; i++ {
		manifest.Edges = append(manifest.Edges,
			Edge{From: nodeID(i), To: nodeID(i + 1), Cost: 1})
	}

	path, reason := Select(manifest, nodeID(0), h.verifier)
	if reason != nil {
		t.Fatalf("unexpected rejection: %v", reason)
	}
	if !equalStrings(path.Versions, []string{nodeID(0), nodeID(1)}) {
		t.Fatalf("eligible migration must win over the zero-length fallback, got %v", path.Versions)
	}

	manifest.Nodes[0].Revoked = true
	path, reason = Select(manifest, nodeID(0), h.verifier)
	if reason != nil {
		t.Fatalf("unexpected rejection after revoking start: %v", reason)
	}
	if !equalStrings(path.Versions, []string{nodeID(0), nodeID(1)}) {
		t.Fatalf("path = %v, want forced migration to next node", path.Versions)
	}
	if path.TotalCost != 1 {
		t.Fatalf("total cost = %d, want 1", path.TotalCost)
	}
}

func TestEd25519RoundTripAndRejection(t *testing.T) {
	pub, priv, err := GenerateLevelKey()
	if err != nil {
		t.Fatalf("GenerateLevelKey: %v", err)
	}
	signer, err := NewEd25519Signer(priv)
	if err != nil {
		t.Fatalf("NewEd25519Signer: %v", err)
	}
	verifier, err := NewEd25519Verifier(pub)
	if err != nil {
		t.Fatalf("NewEd25519Verifier: %v", err)
	}

	signature, err := signer.SignLevel("v1", LevelStable)
	if err != nil {
		t.Fatalf("SignLevel: %v", err)
	}
	if err := verifier.VerifyLevel("v1", LevelStable, signature); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if err := verifier.VerifyLevel("v2", LevelStable, signature); err == nil {
		t.Fatal("signature for v1 must not verify for v2")
	}
	if err := verifier.VerifyLevel("v1", LevelHardened, signature); err == nil {
		t.Fatal("signature must not verify a different level")
	}

	otherPub, _, err := GenerateLevelKey()
	if err != nil {
		t.Fatalf("GenerateLevelKey: %v", err)
	}
	otherVerifier, err := NewEd25519Verifier(otherPub)
	if err != nil {
		t.Fatalf("NewEd25519Verifier: %v", err)
	}
	if err := otherVerifier.VerifyLevel("v1", LevelStable, signature); err == nil {
		t.Fatal("signature must not verify under another public key")
	}

	if _, err := NewEd25519Verifier(ed25519.PublicKey{}); err == nil {
		t.Fatal("expected error for malformed public key")
	}
	if _, err := NewEd25519Signer(ed25519.PrivateKey{}); err == nil {
		t.Fatal("expected error for malformed private key")
	}
}
