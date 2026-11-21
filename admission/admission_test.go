package admission

import (
	"errors"
	"testing"
)

func src(principal, sig string) Source {
	return Source{Principal: principal, Signature: sig}
}

func litString(v string) Literal { return StringLiteral(v) }

func expectErrorKind(t *testing.T, err error, kind ErrorKind) *Error {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s error, got success", kind)
	}
	var admErr *Error
	if !errors.As(err, &admErr) {
		t.Fatalf("expected *admission.Error, got %T: %v", err, err)
	}
	if admErr.Kind != kind {
		t.Fatalf("expected kind %q, got %q (%v)", kind, admErr.Kind, err)
	}
	return admErr
}

func applyOnly(tree *Tree, policy *Policy, ops []Op) error {
	_, err := tree.Apply(policy, ops)
	return err
}

func admKind(t *testing.T, err error) ErrorKind {
	t.Helper()
	var admErr *Error
	if !errors.As(err, &admErr) {
		t.Fatalf("expected *admission.Error, got %T: %v", err, err)
	}
	return admErr.Kind
}

func assertLeaf(t *testing.T, leaves []EffectiveLeaf, vantage Path, want Literal,
	terminal Source, chainLength int) EffectiveLeaf {
	t.Helper()
	for _, leaf := range leaves {
		if pathEqual(leaf.Path, vantage) {
			if !leaf.Value.Equal(want) {
				t.Fatalf("leaf %v: value %v != %v", vantage, leaf.Value, want)
			}
			if leaf.Terminal != terminal {
				t.Fatalf("leaf %v: terminal %v != %v", vantage, leaf.Terminal, terminal)
			}
			if len(leaf.Chain) != chainLength {
				t.Fatalf("leaf %v: chain length %d != %d", vantage, len(leaf.Chain), chainLength)
			}
			if len(leaf.Sources) == 0 {
				t.Fatalf("leaf %v: empty source closure", vantage)
			}
			return leaf
		}
	}
	t.Fatalf("leaf %v not found among %d leaves", vantage, len(leaves))
	return EffectiveLeaf{}
}

// basePolicy: tenant may write its whole subtree, but a protected child is
// tightened via a denial; platform owns platform/defaults and trust rules
// pin protected vantages to platform-signed sources.
func basePolicy() *Policy {
	p := &Policy{
		Grants: map[string][]Path{
			"tenant":   {{"tenant"}},
			"platform": {{"platform"}, {"tenant", "secrets"}, {"tenant", "shared"}},
			"plugin":   {{"plugin"}},
		},
		Denials: map[string][]Path{
			"tenant": {{"tenant", "secrets"}},
		},
	}
	p.AddTrusted(Path{"tenant", "secrets"}, "platform")
	p.AddTrusted(Path{"tenant", "policy"}, "platform")
	return p
}

// Scenario 1: an ancestor whole-table replacement that would wipe a
// protected descendant must be authorized on every affected leaf, not just
// on the command path.
func TestAncestorReplaceWipesProtectedItem(t *testing.T) {
	policy := basePolicy()
	policy.Grants["platform"] = append(policy.Grants["platform"], Path{"tenant"})
	tree := NewTree()

	seed := []Op{
		{Kind: OpSet, Path: Path{"tenant", "name"}, Principal: "tenant",
			Source: src("tenant", "t-name"), Literal: litString("acme")},
		{Kind: OpSet, Path: Path{"tenant", "secrets", "key"}, Principal: "platform",
			Source: src("platform", "p-key"), Literal: litString("secret-value")},
	}
	var err error
	tree, _, _, err = CrossCheckApply(tree, policy, seed)
	if err != nil {
		t.Fatalf("seed batch failed: %v", err)
	}

	badTable, err := NewSubtree(SubtreeEntry{
		Path:    Path{"name"},
		Literal: litString("other"),
		Source:  src("tenant", "t-other"),
	})
	if err != nil {
		t.Fatal(err)
	}

	before := tree.Clone()
	badBatch := []Op{{
		Kind: OpReplace, Path: Path{"tenant"}, Principal: "tenant", Table: badTable,
	}}
	admErr := expectErrorKind(t, applyOnly(before, policy, badBatch), ErrDenied)
	if admErr.OpIndex != 0 || !pathEqual(admErr.Path, Path{"tenant", "secrets", "key"}) {
		t.Fatalf("denial must name the protected leaf, got %+v", admErr)
	}

	// Failed batch must leave the old tree untouched.
	oldLeaves, err := before.EffectiveLeaves(policy)
	if err != nil {
		t.Fatal(err)
	}
	assertLeaf(t, oldLeaves, Path{"tenant", "secrets", "key"},
		litString("secret-value"), src("platform", "p-key"), 0)

	// The interpreter independently reaches the same denial.
	interp := newInterpreter(policy)
	interpErr, _ := interp.replay(append(seed, badBatch...))
	if interpErr == nil || admKind(t, interpErr) != ErrDenied {
		t.Fatalf("interpreter did not independently deny: %+v", interpErr)
	}

	// Platform, which holds the grant on the protected subtree, may replace.
	okTable, err := NewSubtree(
		SubtreeEntry{Path: Path{"name"}, Literal: litString("acme-v2"),
			Source: src("tenant", "t-name2")},
		SubtreeEntry{Path: Path{"secrets", "key"}, Literal: litString("rotated"),
			Source: src("platform", "p-key2")},
	)
	if err != nil {
		t.Fatal(err)
	}
	tree, leaves, _, err := CrossCheckApply(before, policy, []Op{{
		Kind: OpReplace, Path: Path{"tenant"}, Principal: "platform", Table: okTable,
	}})
	if err != nil {
		t.Fatalf("platform replacement should pass: %v", err)
	}
	assertLeaf(t, leaves, Path{"tenant", "secrets", "key"},
		litString("rotated"), src("platform", "p-key2"), 0)
}

// Scenario 2: an indirect reference pulls a plugin default into a protected
// vantage; the closure contains the untrusted plugin source even though the
// literal itself is benign, so admission fails.
func TestIndirectReferenceIntroducesUntrustedDefault(t *testing.T) {
	policy := basePolicy()
	tree := NewTree()

	seed := []Op{
		{Kind: OpSet, Path: Path{"plugin", "defaults", "timeout"}, Principal: "plugin",
			Source: src("plugin", "plug-sig"), Literal: litString("30s")},
		{Kind: OpLink, Path: Path{"platform", "mirror"}, Principal: "platform",
			Source: src("platform", "mirror-sig"), Target: Path{"plugin", "defaults"}},
	}
	var err error
	tree, _, _, err = CrossCheckApply(tree, policy, seed)
	if err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	// Direct link into an unprotected vantage is fine and exposes the chain.
	direct := []Op{{Kind: OpLink, Path: Path{"tenant", "cache"}, Principal: "tenant",
		Source: src("tenant", "cache-sig"), Target: Path{"platform", "mirror"}}}
	var leaves []EffectiveLeaf
	tree, leaves, _, err = CrossCheckApply(tree, policy, direct)
	if err != nil {
		t.Fatalf("unprotected indirect reference should pass: %v", err)
	}
	leaf := assertLeaf(t, leaves, Path{"tenant", "cache", "timeout"},
		litString("30s"), src("plugin", "plug-sig"), 2)
	if leaf.Chain[0].Source != src("tenant", "cache-sig") ||
		leaf.Chain[1].Source != src("platform", "mirror-sig") {
		t.Fatalf("unexpected reference chain: %+v", leaf.Chain)
	}

	// The same value landing under a protected vantage is rejected because
	// the closure includes the plugin principal.
	policy.Grants["platform"] = append(policy.Grants["platform"], Path{"tenant", "policy"})
	protected := []Op{{Kind: OpLink, Path: Path{"tenant", "policy", "timeout"},
		Principal: "platform", Source: src("platform", "pol-sig"),
		Target: Path{"platform", "mirror"}}}
	expectErrorKind(t, applyOnly(tree, policy, protected), ErrUntrusted)

	interp := newInterpreter(policy)
	interp.seedFromTree(tree.root)
	interpErr, _ := interp.replay(protected)
	if interpErr == nil || admKind(t, interpErr) != ErrUntrusted {
		t.Fatalf("interpreter did not independently flag untrusted closure: %+v", interpErr)
	}
}

// Scenario 3: two vantages share one target subgraph; changing the shared
// target changes every vantage and must re-validate trust at each one.
func TestSharedTargetChange(t *testing.T) {
	policy := basePolicy()
	tree := NewTree()

	seed := []Op{
		{Kind: OpSet, Path: Path{"platform", "shared", "mode"}, Principal: "platform",
			Source: src("platform", "shared-v1"), Literal: litString("strict")},
		{Kind: OpLink, Path: Path{"tenant", "a", "mode"}, Principal: "tenant",
			Source: src("tenant", "la"), Target: Path{"platform", "shared", "mode"}},
		{Kind: OpLink, Path: Path{"tenant", "b", "mode"}, Principal: "tenant",
			Source: src("tenant", "lb"), Target: Path{"platform", "shared", "mode"}},
	}
	var err error
	tree, leaves, _, err := CrossCheckApply(tree, policy, seed)
	if err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	assertLeaf(t, leaves, Path{"tenant", "a", "mode"},
		litString("strict"), src("platform", "shared-v1"), 1)
	assertLeaf(t, leaves, Path{"tenant", "b", "mode"},
		litString("strict"), src("platform", "shared-v1"), 1)

	// A protected vantage accepts only platform/tenant closure sources.
	protect := &Policy{
		Grants: map[string][]Path{
			"tenant":   {{"tenant"}},
			"platform": {{"platform"}, {"tenant"}},
		},
	}
	protect.AddTrusted(Path{"tenant", "a"}, "platform", "tenant")

	// The write is authorized, but the shared vantage now resolves with a
	// plugin-signed terminal outside the trusted closure.
	badChange := []Op{
		{Kind: OpSet, Path: Path{"platform", "shared", "mode"}, Principal: "platform",
			Source: src("plugin", "plug-injected"), Literal: litString("lenient")},
	}
	expectErrorKind(t, applyOnly(tree, protect, badChange), ErrUntrusted)

	goodChange := []Op{
		{Kind: OpSet, Path: Path{"platform", "shared", "mode"}, Principal: "platform",
			Source: src("platform", "shared-v2"), Literal: litString("lenient")},
	}
	_, leaves, _, err = CrossCheckApply(tree, protect, goodChange)
	if err != nil {
		t.Fatalf("trusted shared change failed: %v", err)
	}
	assertLeaf(t, leaves, Path{"tenant", "a", "mode"},
		litString("lenient"), src("platform", "shared-v2"), 1)
	assertLeaf(t, leaves, Path{"tenant", "b", "mode"},
		litString("lenient"), src("platform", "shared-v2"), 1)
}

// Scenario 4: a write followed by a delete removes the source that a
// reference in the same final tree points at, producing a dangling
// reference. The whole batch must be rejected atomically.
func TestWriteThenDeleteSourceDisappears(t *testing.T) {
	policy := basePolicy()

	// Order A: link first, delete its exact target later in the batch.
	batchA := []Op{
		{Kind: OpSet, Path: Path{"plugin", "cfg", "x"}, Principal: "plugin",
			Source: src("plugin", "x-sig"), Literal: litString("1")},
		{Kind: OpLink, Path: Path{"tenant", "xref"}, Principal: "tenant",
			Source: src("tenant", "xref-sig"), Target: Path{"plugin", "cfg", "x"}},
		{Kind: OpDelete, Path: Path{"plugin", "cfg", "x"}, Principal: "plugin"},
	}
	tree := NewTree()
	expectErrorKind(t, applyOnly(tree, policy, batchA), ErrDangling)

	interp := newInterpreter(policy)
	interpErr, _ := interp.replay(batchA)
	if interpErr == nil || admKind(t, interpErr) != ErrDangling {
		t.Fatalf("interpreter missed dangling reference: %+v", interpErr)
	}

	// The receiver stays valid and empty after the failed batch.
	leaves, err := tree.EffectiveLeaves(policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(leaves) != 0 {
		t.Fatalf("failed batch mutated the old tree: %d leaves", len(leaves))
	}

	// Order B: delete a table ancestor that the reference targets.
	batchB := []Op{
		{Kind: OpSet, Path: Path{"plugin", "cfg", "x"}, Principal: "plugin",
			Source: src("plugin", "x-sig"), Literal: litString("1")},
		{Kind: OpLink, Path: Path{"tenant", "xref"}, Principal: "tenant",
			Source: src("tenant", "xref-sig"), Target: Path{"plugin", "cfg"}},
		{Kind: OpDelete, Path: Path{"plugin", "cfg"}, Principal: "plugin"},
	}
	expectErrorKind(t, applyOnly(NewTree(), policy, batchB), ErrDangling)
}

// Scenario 5: two batches converge on the same literal but carry different
// authorization evidence. Merging is only legal when values, terminal
// sources and closures all match.
func TestSameValueDifferentEvidenceRejectsMerge(t *testing.T) {
	policy := &Policy{
		Grants: map[string][]Path{
			"tenant-a": {{"tenant", "alpha"}},
			"tenant-b": {{"tenant", "beta"}},
		},
	}

	batchA := []Op{{
		Kind: OpSet, Path: Path{"tenant", "alpha", "flag"}, Principal: "tenant-a",
		Source: src("tenant-a", "sig-a"), Literal: litString("on"),
	}}
	batchB := []Op{{
		Kind: OpSet, Path: Path{"tenant", "beta", "flag"}, Principal: "tenant-b",
		Source: src("tenant-b", "sig-b"), Literal: litString("on"),
	}}

	// Each batch is independently legal and cross-checks cleanly.
	tree := NewTree()
	var err error
	tree, _, _, err = CrossCheckApply(tree, policy, batchA)
	if err != nil {
		t.Fatalf("batch A failed: %v", err)
	}
	tree, _, _, err = CrossCheckApply(tree, policy, batchB)
	if err != nil {
		t.Fatalf("batch B failed: %v", err)
	}

	// Merging at the empty base is legal: disjoint paths, both orders
	// produce the same values with the same evidence.
	merged, leaves, err := MergeBatches(NewTree(), policy, batchA, batchB)
	if err != nil {
		t.Fatalf("disjoint batches should merge: %v", err)
	}
	if len(leaves) != 2 {
		t.Fatalf("expected 2 merged leaves, got %d", len(leaves))
	}

	// Now make the batches write the same path with identical literals but
	// different signers. Values match, evidence does not: merge rejected.
	batchA2 := []Op{{
		Kind: OpSet, Path: Path{"tenant", "alpha", "flag"}, Principal: "tenant-a",
		Source: src("tenant-a", "sig-a"), Literal: litString("on"),
	}}
	batchB2 := []Op{{
		Kind: OpSet, Path: Path{"tenant", "alpha", "flag"}, Principal: "tenant-b",
		Source: src("tenant-b", "forged-sig"), Literal: litString("on"),
	}}
	policy2 := &Policy{
		Grants: map[string][]Path{
			"tenant-a": {{"tenant", "alpha"}},
			"tenant-b": {{"tenant", "alpha"}},
		},
	}
	expectErrorKind(t, func() error {
		_, _, mErr := MergeBatches(NewTree(), policy2, batchA2, batchB2)
		return mErr
	}(), ErrMergeConflict)

	// The same literal from an unauthorized principal is rejected outright,
	// even though the literal equals an already-accepted value.
	rogue := []Op{{
		Kind: OpSet, Path: Path{"tenant", "alpha", "flag"}, Principal: "tenant-b",
		Source: src("tenant-b", "forged-sig"), Literal: litString("on"),
	}}
	expectErrorKind(t, applyOnly(merged, policy, rogue), ErrDenied)
}
