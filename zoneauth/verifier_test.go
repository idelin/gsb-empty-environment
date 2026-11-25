package zoneauth_test

import (
	"crypto/ed25519"
	"strings"
	"testing"

	"prescreen.local/ddd-10561-v4/testsupport"
	"prescreen.local/ddd-10561-v4/zoneauth"
)

func mustZone(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func mustAnswer(t *testing.T, p *zoneauth.Proof) *zoneauth.Answer {
	t.Helper()
	answer, err := p.DeriveAnswer()
	if err != nil {
		t.Fatal(err)
	}
	return answer
}

func clonePatch(t *testing.T, p *zoneauth.Patch) *zoneauth.Patch {
	t.Helper()
	raw, err := jsonMarshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var clone zoneauth.Patch
	if err := jsonUnmarshal(raw, &clone); err != nil {
		t.Fatal(err)
	}
	return &clone
}

// TestTamperedRootRejected mutates hashes/signatures and requires rejection.
func TestTamperedRootRejected(t *testing.T) {
	fixture := loadGolden(t, "wildcard-only")
	trusted := trustedKeys(fixture)

	t.Run("forged new root signature", func(t *testing.T) {
		patch := clonePatch(t, fixture.Patch)
		patch.NewRoot.Signature[0] ^= 0xFF
		if _, _, err := zoneauth.Reevaluate(patch, fixture.OldRoot, trusted, nil); err == nil {
			t.Fatal("forged signature accepted")
		}
	})

	t.Run("tampered root hash", func(t *testing.T) {
		patch := clonePatch(t, fixture.Patch)
		patch.NewRoot.RootHash[0] ^= 0xFF
		if _, _, err := zoneauth.Reevaluate(patch, fixture.OldRoot, trusted, nil); err == nil {
			t.Fatal("tampered root hash accepted")
		}
	})

	t.Run("untrusted key", func(t *testing.T) {
		if _, _, err := zoneauth.Reevaluate(
			fixture.Patch, fixture.OldRoot, map[string]ed25519.PublicKey{}, nil); err == nil {
			t.Fatal("untrusted signing key accepted")
		}
	})

	t.Run("replay against wrong cached root", func(t *testing.T) {
		other := loadGolden(t, "reexpose")
		if _, _, err := zoneauth.Reevaluate(
			fixture.Patch, other.OldRoot, trusted, nil); err == nil {
			t.Fatal("patch replayed against a different cached root")
		}
	})

	t.Run("non-incrementing sequence", func(t *testing.T) {
		patch := clonePatch(t, fixture.Patch)
		patch.NewRoot.Sequence = patch.OldRoot.Sequence + 7
		if _, _, err := zoneauth.Reevaluate(patch, fixture.OldRoot, trusted, nil); err == nil {
			t.Fatal("invalid sequence order accepted")
		}
	})
}

// TestMissingSiblingIsPending proves an unprovided sibling is never treated
// as an empty tree: when the anchor proves the wildcard edge exists but its
// node evidence is withheld, the verdict is Pending, not NXDomain.
func TestMissingSiblingIsPending(t *testing.T) {
	keys := testsupport.LoadKeys()
	z := zoneauth.NewZone("example", keys.OldPriv)
	mustZone(t, z.PutRecords(zoneauth.Name{"parent"}, zoneauth.Record{Type: "A", Value: "1.1.1.1"}))

	b := zoneauth.NewPatchBuilder(z)
	oldProof := z.BuildProof(zoneauth.Name{"parent", "absent"})
	oldAnswer := mustAnswer(t, oldProof)
	if oldAnswer.Kind != zoneauth.AnswerNXDomain {
		t.Fatalf("precondition: want nxdomain, got %s", oldAnswer.Kind)
	}
	mustZone(t, b.SetWildcard(zoneauth.Name{"parent"},
		[]zoneauth.Record{{Type: "TXT", Value: "tmpl"}}))
	patch := b.Sign()
	b.AttachClientProofs(patch, zoneauth.Name{"parent", "absent"})

	trusted := map[string]ed25519.PublicKey{
		zoneauth.KeyIDFor(keys.OldPub): keys.OldPub,
	}
	// The complete patch must verify before evidence is withheld.
	if _, err := zoneauth.VerifyPatch(patch, patch.OldRoot, trusted); err != nil {
		t.Fatalf("patch should verify before evidence withholding: %v", err)
	}

	// Withhold the new wildcard node while the anchor retains its edge.
	for i := range patch.Ops {
		if patch.Ops[i].Kind == zoneauth.OpSetWildcard {
			patch.Ops[i].NewProof.WildcardNode = nil
			nodes := patch.Ops[i].NewProof.Nodes
			if len(nodes) > 0 {
				last := nodes[len(nodes)-1]
				if len(last.Path) > 0 && last.Path[len(last.Path)-1] == "*" {
					patch.Ops[i].NewProof.Nodes = nodes[:len(nodes)-1]
				}
			}
		}
	}
	for _, proof := range patch.ClientProofs {
		proof.WildcardNode = nil
		// A template node may also appear as the final real chain node.
		if len(proof.Nodes) > 0 {
			last := proof.Nodes[len(proof.Nodes)-1]
			if len(last.Path) > 0 && last.Path[len(last.Path)-1] == "*" {
				proof.Nodes = proof.Nodes[:len(proof.Nodes)-1]
			}
		}
	}

	entries := []zoneauth.CachedQuery{{
		Query:  zoneauth.Name{"parent", "absent"},
		Answer: oldAnswer,
		Proof:  oldProof,
	}}
	verdicts := zoneauth.ReevaluateVerified(patch, entries)
	if verdicts[0].Kind != zoneauth.VerdictPending {
		t.Fatalf("got %s, want pending for withheld wildcard evidence", verdicts[0].Kind)
	}
}

// TestFabricatedEmptyTreeRejected requires a removal whose old proof no
// longer shows the deleted node to fail verification.
func TestFabricatedEmptyTreeRejected(t *testing.T) {
	keys := testsupport.LoadKeys()
	z := zoneauth.NewZone("example", keys.OldPriv)
	mustZone(t, z.AddDelegation(zoneauth.Name{"host"},
		[]zoneauth.Record{{Type: "NS", Value: "ns.host"}}))

	b := zoneauth.NewPatchBuilder(z)
	mustZone(t, b.RemoveDelegation(zoneauth.Name{"host"}))
	patch := b.Sign()

	patch.Ops[0].OldProof.Nodes = patch.Ops[0].OldProof.Nodes[:len(patch.Ops[0].OldProof.Nodes)-1]

	trusted := map[string]ed25519.PublicKey{
		zoneauth.KeyIDFor(keys.OldPub): keys.OldPub,
	}
	_, err := zoneauth.VerifyPatch(patch, patch.OldRoot, trusted)
	if err == nil || !strings.Contains(err.Error(), "evidence") {
		t.Fatalf("got %v, want evidence error for fabricated empty tree", err)
	}
}

// TestNegativeAnswerTransitions covers all three negative classifications:
// still negative, turned into a delegation or wildcard positive, and pending.
func TestNegativeAnswerTransitions(t *testing.T) {
	keys := testsupport.LoadKeys()
	z := zoneauth.NewZone("example", keys.OldPriv)
	mustZone(t, z.PutRecords(zoneauth.Name{"a"}, zoneauth.Record{Type: "A", Value: "1.1.1.1"}))

	// Freeze the old root first; client proofs must share that exact state.
	b := zoneauth.NewPatchBuilder(z)
	mustZone(t, b.AddDelegation(zoneauth.Name{"a", "y"},
		[]zoneauth.Record{{Type: "NS", Value: "ns.y"}}))
	mustZone(t, b.SetWildcard(zoneauth.Name{"a"},
		[]zoneauth.Record{{Type: "A", Value: "9.9.9.9"}}))
	patch := b.Sign()
	b.AttachClientProofs(patch,
		zoneauth.Name{"a", "x"},
		zoneauth.Name{"a", "y"},
		zoneauth.Name{"a", "z"})

	// Old-root evidence is anchored at exactly the cached root.
	oldAdd := patch.Ops[0].OldProof
	oldWild := patch.Ops[1].OldProof
	stillNeg := &zoneauth.Proof{
		Query: zoneauth.Name{"a", "x"},
		Nodes: append([]*zoneauth.TrieNode(nil), oldWild.Nodes...),
	}
	becomesDeleg := oldAdd
	becomesPositive := oldWild

	trusted := map[string]ed25519.PublicKey{
		zoneauth.KeyIDFor(keys.OldPub): keys.OldPub,
	}
	if _, err := zoneauth.VerifyPatch(patch, patch.OldRoot, trusted); err != nil {
		t.Fatalf("patch should verify before evidence withholding: %v", err)
	}

	// Withhold the wildcard template so "a/z" cannot be resolved to the new
	// positive answer from evidence alone.
	for i := range patch.Ops {
		if patch.Ops[i].Kind != zoneauth.OpSetWildcard {
			continue
		}
		np := patch.Ops[i].NewProof
		np.WildcardNode = nil
		if len(np.Nodes) > 0 {
			last := np.Nodes[len(np.Nodes)-1]
			if len(last.Path) > 0 && last.Path[len(last.Path)-1] == "*" {
				np.Nodes = np.Nodes[:len(np.Nodes)-1]
			}
		}
	}
	for _, proof := range patch.ClientProofs {
		proof.WildcardNode = nil
		last := proof.Nodes[len(proof.Nodes)-1]
		if len(last.Path) > 0 && last.Path[len(last.Path)-1] == "*" {
			proof.Nodes = proof.Nodes[:len(proof.Nodes)-1]
		}
	}

	entries := []zoneauth.CachedQuery{
		{Query: zoneauth.Name{"a", "x"}, Answer: mustAnswer(t, stillNeg), Proof: stillNeg},
		{Query: zoneauth.Name{"a", "y"}, Answer: mustAnswer(t, becomesDeleg), Proof: becomesDeleg},
		{Query: zoneauth.Name{"a", "z"}, Answer: mustAnswer(t, becomesPositive), Proof: becomesPositive},
	}
	verdicts := zoneauth.ReevaluateVerified(patch, entries)

	want := map[string]zoneauth.VerdictKind{
		"a\x00x": zoneauth.VerdictPending,
		"a\x00y": zoneauth.VerdictChanged,
		"a\x00z": zoneauth.VerdictPending,
	}
	for _, verdict := range verdicts {
		expected, ok := want[verdict.Query.Key()]
		if !ok {
			t.Fatalf("unexpected query %v", verdict.Query)
		}
		if verdict.Kind != expected {
			t.Errorf("query %v: got %s, want %s", verdict.Query, verdict.Kind, expected)
		}
		if verdict.Kind == zoneauth.VerdictChanged {
			if verdict.Query.Key() == (zoneauth.Name{"a", "y"}).Key() {
				if verdict.NewAnswer.Kind != zoneauth.AnswerReferral {
					t.Errorf("a/y new kind = %s, want referral", verdict.NewAnswer.Kind)
				}
			}
		}
	}
}

// TestStructuralAncestryNotStringPrefix confirms label-structural ancestry:
// "kee" edits never invalidate "keep", despite the string prefix overlap.
func TestStructuralAncestryNotStringPrefix(t *testing.T) {
	fixture := loadGolden(t, "side-branch")
	trusted := trustedKeys(fixture)
	entries := make([]zoneauth.CachedQuery, 0, len(fixture.Cached))
	for _, entry := range fixture.Cached {
		entries = append(entries, zoneauth.CachedQuery{
			Query: entry.Query, Answer: entry.Answer, Proof: entry.Proof,
		})
	}
	verdicts, _, err := zoneauth.Reevaluate(fixture.Patch, fixture.OldRoot, trusted, entries)
	if err != nil {
		t.Fatal(err)
	}
	for _, verdict := range verdicts {
		want := fixture.ExpectKind[testsupport.QueryKey(verdict.Query)]
		if verdict.Kind != want {
			t.Errorf("query %s: got %s, want %s", verdict.Query, verdict.Kind, want)
		}
	}
}

// TestEmptyAndHandoffOnlyPatch checks the patch well-formedness rules.
func TestEmptyAndHandoffOnlyPatch(t *testing.T) {
	keys := testsupport.LoadKeys()
	z := zoneauth.NewZone("example", keys.OldPriv)
	oldEnv := z.Commit()

	empty := &zoneauth.Patch{Zone: "example", OldRoot: oldEnv, NewRoot: oldEnv}
	trusted := map[string]ed25519.PublicKey{
		zoneauth.KeyIDFor(keys.OldPub): keys.OldPub,
	}
	if _, err := zoneauth.VerifyPatch(empty, oldEnv, trusted); err == nil {
		t.Fatal("empty patch accepted")
	}
}
