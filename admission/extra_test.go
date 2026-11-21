package admission

import (
	"math/rand"
	"testing"
)

func permissivePolicy() *Policy {
	p := &Policy{
		Grants: map[string][]Path{
			"ops": {{}},
		},
	}
	return p
}

// Reference cycles are rejected, including indirect cycles across shared
// intermediate paths, while diamond-shaped sharing stays legal.
func TestCycleRejectedDiamondAllowed(t *testing.T) {
	policy := permissivePolicy()
	tree := NewTree()

	cycle := []Op{
		{Kind: OpLink, Path: Path{"a"}, Principal: "ops",
			Source: src("ops", "sa"), Target: Path{"b"}},
		{Kind: OpLink, Path: Path{"b"}, Principal: "ops",
			Source: src("ops", "sb"), Target: Path{"c"}},
		{Kind: OpLink, Path: Path{"c"}, Principal: "ops",
			Source: src("ops", "sc"), Target: Path{"a"}},
	}
	expectErrorKind(t, applyOnly(tree, policy, cycle), ErrCycle)

	diamond := []Op{
		{Kind: OpSet, Path: Path{"root", "v"}, Principal: "ops",
			Source: src("ops", "sv"), Literal: litString("v")},
		{Kind: OpLink, Path: Path{"x"}, Principal: "ops",
			Source: src("ops", "sx"), Target: Path{"root"}},
		{Kind: OpLink, Path: Path{"y"}, Principal: "ops",
			Source: src("ops", "sy"), Target: Path{"root"}},
	}
	_, leaves, _, err := CrossCheckApply(tree, policy, diamond)
	if err != nil {
		t.Fatalf("shared diamond should be legal: %v", err)
	}
	assertLeaf(t, leaves, Path{"x", "v"}, litString("v"), src("ops", "sv"), 1)
	assertLeaf(t, leaves, Path{"y", "v"}, litString("v"), src("ops", "sv"), 1)
}

// Components are opaque strings: dots and empty components are preserved.
func TestPathComponentsAreOpaque(t *testing.T) {
	policy := permissivePolicy()
	tree := NewTree()
	ops := []Op{
		{Kind: OpSet, Path: Path{"a.b", "", "c"}, Principal: "ops",
			Source: src("ops", "s1"), Literal: litString("first")},
		{Kind: OpSet, Path: Path{"a", "b", "c"}, Principal: "ops",
			Source: src("ops", "s2"), Literal: litString("second")},
	}
	_, leaves, _, err := CrossCheckApply(tree, policy, ops)
	if err != nil {
		t.Fatal(err)
	}
	if len(leaves) != 2 {
		t.Fatalf("dotted/empty components must not alias, got %d leaves", len(leaves))
	}
	assertLeaf(t, leaves, Path{"a.b", "", "c"}, litString("first"), src("ops", "s1"), 0)
	assertLeaf(t, leaves, Path{"a", "b", "c"}, litString("second"), src("ops", "s2"), 0)
}

// Sub-path denials override broader grants.
func TestDenialTightensGrantAtSubPath(t *testing.T) {
	policy := &Policy{
		Grants:  map[string][]Path{"ops": {{"cfg"}}},
		Denials: map[string][]Path{"ops": {{"cfg", "locked"}}},
	}
	tree := NewTree()
	okBatch := []Op{{Kind: OpSet, Path: Path{"cfg", "open"}, Principal: "ops",
		Source: src("ops", "s1"), Literal: litString("x")}}
	var err error
	tree, _, _, err = CrossCheckApply(tree, policy, okBatch)
	if err != nil {
		t.Fatalf("granted subtree write failed: %v", err)
	}
	locked := []Op{{Kind: OpSet, Path: Path{"cfg", "locked", "k"}, Principal: "ops",
		Source: src("ops", "s2"), Literal: litString("y")}}
	expectErrorKind(t, applyOnly(tree, policy, locked), ErrDenied)
}

// Deleting an ancestor requires permission for every leaf beneath it.
func TestAncestorDeleteChecksAllLeaves(t *testing.T) {
	policy := &Policy{
		Grants: map[string][]Path{
			"tenant":   {{"tenant", "public"}},
			"platform": {{"tenant"}},
		},
	}
	tree := NewTree()
	seed := []Op{
		{Kind: OpSet, Path: Path{"tenant", "public", "a"}, Principal: "tenant",
			Source: src("tenant", "sa"), Literal: litString("1")},
		{Kind: OpSet, Path: Path{"tenant", "private", "b"}, Principal: "platform",
			Source: src("platform", "sb"), Literal: litString("2")},
	}
	var err error
	tree, _, _, err = CrossCheckApply(tree, policy, seed)
	if err != nil {
		t.Fatal(err)
	}
	rogue := []Op{{Kind: OpDelete, Path: Path{"tenant"}, Principal: "tenant"}}
	admErr := expectErrorKind(t, applyOnly(tree, policy, rogue), ErrDenied)
	if !pathEqual(admErr.Path, Path{"tenant", "private", "b"}) {
		t.Fatalf("denial must name the private descendant, got %v", admErr.Path)
	}
}

// Input limits are enforced and a rejected batch keeps the old snapshot.
func TestLimitsAndAtomicity(t *testing.T) {
	policy := permissivePolicy()
	tree := NewTree()

	overOps := make([]Op, MaxOpsPerBatch+1)
	for i := range overOps {
		overOps[i] = Op{Kind: OpSet, Path: Path{"n", string(rune('a' + i%26)),
			string(rune('a' + (i/26)%26))}, Principal: "ops",
			Source: src("ops", "s"), Literal: litString("v")}
	}
	expectErrorKind(t, applyOnly(tree, policy, overOps), ErrLimitExceeded)

	pathOps := make([]Op, 0, MaxPathsPerBatch+1)
	for i := 0; i <= MaxPathsPerBatch; i++ {
		pathOps = append(pathOps, Op{Kind: OpSet,
			Path:      Path{"p", string(rune('A' + i%26)), string(rune('A' + (i/26)%26)), string(rune('a' + i%26))},
			Principal: "ops", Source: src("ops", "s"), Literal: litString("v")})
	}
	expectErrorKind(t, applyOnly(tree, policy, pathOps), ErrLimitExceeded)

	good := []Op{{Kind: OpSet, Path: Path{"keep"}, Principal: "ops",
		Source: src("ops", "keep"), Literal: litString("1")}}
	var err error
	tree, _, _, err = CrossCheckApply(tree, policy, good)
	if err != nil {
		t.Fatal(err)
	}
	bad := []Op{
		{Kind: OpSet, Path: Path{"new"}, Principal: "ops",
			Source: src("ops", "new"), Literal: litString("2")},
		{Kind: OpLink, Path: Path{"ghost"}, Principal: "ops",
			Source: src("ops", "ghost"), Target: Path{"missing"}},
	}
	expectErrorKind(t, applyOnly(tree, policy, bad), ErrDangling)
	leaves, err := tree.EffectiveLeaves(policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(leaves) != 1 {
		t.Fatalf("failed batch must not mutate old tree, got %d leaves", len(leaves))
	}
}

// Typed literals distinguish kinds and number spellings.
func TestLiteralEquality(t *testing.T) {
	n1, err := NumberLiteral("1")
	if err != nil {
		t.Fatal(err)
	}
	n10, err := NumberLiteral("1.0")
	if err != nil {
		t.Fatal(err)
	}
	if n1.Equal(n10) {
		t.Fatal("number text 1 and 1.0 must differ")
	}
	if n1.Equal(StringLiteral("1")) {
		t.Fatal("number and string must differ")
	}
	if !BoolLiteral(true).Equal(BoolLiteral(true)) {
		t.Fatal("equal bools compared unequal")
	}
	if _, err := NumberLiteral("not-a-number"); err == nil {
		t.Fatal("invalid number text accepted")
	}

	nilLit := NilLiteral()
	if nilLit.Kind() != LitNil || !nilLit.Equal(NilLiteral()) {
		t.Fatal("nil literal mismatch")
	}
	if BoolLiteral(false).Bool() {
		t.Fatal("bool accessor returned wrong value")
	}
	if StringLiteral("s").String() != "s" {
		t.Fatal("string accessor returned wrong value")
	}
	n, err := NumberLiteral("42")
	if err != nil || n.NumberText() != "42" {
		t.Fatalf("number accessor mismatch: %v %s", err, n.NumberText())
	}
}

// Merge accepts genuinely commutative batches and rejects conflicting
// provenance even when both orderings are individually legal.
func TestMergeCommutativeBatches(t *testing.T) {
	policy := permissivePolicy()
	a := []Op{{Kind: OpSet, Path: Path{"a"}, Principal: "ops",
		Source: src("ops", "sa"), Literal: litString("A")}}
	b := []Op{{Kind: OpSet, Path: Path{"b"}, Principal: "ops",
		Source: src("ops", "sb"), Literal: litString("B")}}
	_, leaves, err := MergeBatches(NewTree(), policy, a, b)
	if err != nil {
		t.Fatalf("commutative batches should merge: %v", err)
	}
	if len(leaves) != 2 {
		t.Fatalf("expected 2 leaves, got %d", len(leaves))
	}

	conflict := []Op{{Kind: OpSet, Path: Path{"a"}, Principal: "ops",
		Source: src("ops", "other"), Literal: litString("A")}}
	expectErrorKind(t, func() error {
		_, _, mErr := MergeBatches(NewTree(), policy, a, conflict)
		return mErr
	}(), ErrMergeConflict)
}

// Differential fuzz: generate many random valid batches, run both engines,
// and require identical acceptance and identical leaf-by-leaf provenance.
func TestDifferentialFuzz(t *testing.T) {
	policy := permissivePolicy()
	rng := rand.New(rand.NewSource(20261121))
	components := []string{"a", "b", "c", "a.b", ""}

	for iteration := 0; iteration < 400; iteration++ {
		tree := NewTree()
		for step := 0; step < 6; step++ {
			ops, rerr := randomBatch(rng, components)
			if rerr != nil {
				t.Fatal(rerr)
			}
			next, engineLeaves, report, xerr := CrossCheckApply(tree, policy, ops)
			if xerr != nil {
				if report != nil {
					t.Fatalf("iteration %d step %d engine divergence: %+v",
						iteration, step, report)
				}
				continue // both engines rejected identically
			}
			standalone, err := next.EffectiveLeaves(policy)
			if err != nil {
				t.Fatalf("iteration %d step %d materialize: %v",
					iteration, step, err)
			}
			if !leavesEqual(standalone, engineLeaves) {
				t.Fatalf("iteration %d step %d leaf sets drifted", iteration, step)
			}
			tree = next
		}
	}
}

func randomBatch(rng *rand.Rand, components []string) ([]Op, error) {
	n := 1 + rng.Intn(6)
	ops := make([]Op, n)
	known := []Path{}
	for i := range ops {
		path := randomPath(rng, components)
		known = append(known, path)
		switch rng.Intn(4) {
		case 0:
			ops[i] = Op{Kind: OpSet, Path: path, Principal: "ops",
				Source:  src("ops", "set-sig"),
				Literal: StringLiteral(string(rune('a' + rng.Intn(26))))}
		case 1:
			if len(known) > 1 && rng.Intn(2) == 0 {
				ops[i] = Op{Kind: OpLink, Path: path, Principal: "ops",
					Source: src("ops", "link-sig"),
					Target: known[rng.Intn(len(known)-1)]}
			} else {
				ops[i] = Op{Kind: OpSet, Path: path, Principal: "ops",
					Source: src("ops", "set-sig"), Literal: litString("z")}
			}
		case 2:
			sub, err := NewSubtree(SubtreeEntry{
				Path:    Path{"leaf"},
				Literal: litString("t"),
				Source:  src("ops", "table-sig"),
			})
			if err != nil {
				return nil, err
			}
			ops[i] = Op{Kind: OpReplace, Path: path, Principal: "ops", Table: sub}
		default:
			ops[i] = Op{Kind: OpDelete, Path: path, Principal: "ops"}
		}
	}
	return ops, nil
}

func randomPath(rng *rand.Rand, components []string) Path {
	depth := 1 + rng.Intn(3)
	path := make(Path, depth)
	for i := range path {
		path[i] = components[rng.Intn(len(components))]
	}
	return path
}

// An empty grant path authorizes the whole tree, while a denial still
// tightens it at a sub-path.
func TestRootGrantAndSubPathDenial(t *testing.T) {
	policy := &Policy{
		Grants:  map[string][]Path{"rootops": {{}}},
		Denials: map[string][]Path{"rootops": {{"forbidden"}}},
	}
	tree := NewTree()
	ok := []Op{{Kind: OpSet, Path: Path{"any", "deep", "leaf"}, Principal: "rootops",
		Source: src("rootops", "s1"), Literal: litString("v")}}
	var err error
	tree, _, _, err = CrossCheckApply(tree, policy, ok)
	if err != nil {
		t.Fatalf("root grant should cover any path: %v", err)
	}
	denied := []Op{{Kind: OpSet, Path: Path{"forbidden", "x"}, Principal: "rootops",
		Source: src("rootops", "s2"), Literal: litString("v")}}
	expectErrorKind(t, applyOnly(tree, policy, denied), ErrDenied)
}

// A reference to an empty table resolves to zero leaves rather than
// dangling, in both engines.
func TestReferenceToEmptyTable(t *testing.T) {
	policy := permissivePolicy()
	empty, err := NewSubtree()
	if err != nil {
		t.Fatal(err)
	}
	ops := []Op{
		{Kind: OpReplace, Path: Path{"empty"}, Principal: "ops", Table: empty},
		{Kind: OpLink, Path: Path{"alias"}, Principal: "ops",
			Source: src("ops", "link"), Target: Path{"empty"}},
	}
	tree, leaves, _, err := CrossCheckApply(NewTree(), policy, ops)
	if err != nil {
		t.Fatalf("empty table reference should be legal: %v", err)
	}
	if len(leaves) != 0 {
		t.Fatalf("expected no leaves, got %d", len(leaves))
	}
	tree.Clone()
	again, err := tree.EffectiveLeaves(policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("expected stable empty resolution, got %d", len(again))
	}
}

// A reference to the root table expands every top-level leaf.
// Referencing the root from a node inside the root is a self-containing
// expansion and must be reported as a cycle.
func TestRootSelfReferenceIsCycle(t *testing.T) {
	policy := permissivePolicy()
	ops := []Op{
		{Kind: OpSet, Path: Path{"top", "x"}, Principal: "ops",
			Source: src("ops", "sx"), Literal: litString("X")},
		{Kind: OpLink, Path: Path{"loop"}, Principal: "ops",
			Source: src("ops", "sl"), Target: Path{}},
	}
	expectErrorKind(t, applyOnly(NewTree(), policy, ops), ErrCycle)
}

func TestReferenceToWholeTable(t *testing.T) {
	policy := permissivePolicy()
	ops := []Op{
		{Kind: OpSet, Path: Path{"src", "x"}, Principal: "ops",
			Source: src("ops", "sx"), Literal: litString("X")},
		{Kind: OpSet, Path: Path{"src", "y"}, Principal: "ops",
			Source: src("ops", "sy"), Literal: litString("Y")},
		{Kind: OpLink, Path: Path{"mirror"}, Principal: "ops",
			Source: src("ops", "sm"), Target: Path{"src"}},
	}
	_, leaves, _, err := CrossCheckApply(NewTree(), policy, ops)
	if err != nil {
		t.Fatalf("whole-table reference should resolve: %v", err)
	}
	assertLeaf(t, leaves, Path{"mirror", "x"},
		litString("X"), src("ops", "sx"), 1)
	assertLeaf(t, leaves, Path{"mirror", "y"},
		litString("Y"), src("ops", "sy"), 1)
	assertLeaf(t, leaves, Path{"src", "x"},
		litString("X"), src("ops", "sx"), 0)
}

// Retargeting a reference re-checks every leaf that appears under it.
func TestRetargetReferenceRechecksLeaves(t *testing.T) {
	policy := &Policy{
		Grants: map[string][]Path{
			"tenant": {{"tenant"}},
			"shared": {{"shared-a"}, {"shared-b"}},
		},
	}
	tree := NewTree()
	seed := []Op{
		{Kind: OpSet, Path: Path{"shared-a", "v"}, Principal: "shared",
			Source: src("shared", "a"), Literal: litString("A")},
		{Kind: OpSet, Path: Path{"shared-b", "v"}, Principal: "shared",
			Source: src("shared", "b"), Literal: litString("B")},
		{Kind: OpLink, Path: Path{"tenant", "view"}, Principal: "tenant",
			Source: src("tenant", "s"), Target: Path{"shared-a"}},
	}
	var err error
	tree, _, _, err = CrossCheckApply(tree, policy, seed)
	if err != nil {
		t.Fatal(err)
	}
	// Tenant owns the link path and may retarget within its own subtree;
	// the affected-leaf check still covers the link location itself.
	retarget := []Op{{Kind: OpLink, Path: Path{"tenant", "view"}, Principal: "tenant",
		Source: src("tenant", "s2"), Target: Path{"shared-b"}}}
	_, leaves, _, err := CrossCheckApply(tree, policy, retarget)
	if err != nil {
		t.Fatalf("retarget should pass for the link owner: %v", err)
	}
	assertLeaf(t, leaves, Path{"tenant", "view", "v"},
		litString("B"), src("shared", "b"), 1)
}
