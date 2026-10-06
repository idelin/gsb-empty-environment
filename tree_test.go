package objtree_test

import (
	"testing"

	"objtree"
)

func TestSetCopiesOnlyAncestors(t *testing.T) {
	root := sampleTree()
	snapshot := objtree.Clone(root)

	got := objtree.Set(root, []string{"a", "b", "c"}, 99)

	// Snapshot isolation: the old root is untouched.
	if !objtree.Equal(root, snapshot) {
		t.Fatal("Set mutated the original tree")
	}
	if got == root {
		t.Fatal("Set returned the old root")
	}

	// The new value is visible at the path.
	if v := objtree.Get(got, []string{"a", "b", "c"}); v == nil || v.Value != 99 {
		t.Fatalf("Get(a/b/c) = %v, want leaf 99", v)
	}

	// Ancestors a/b/c are copied; everything else keeps its identity.
	if got.Children["a"] == root.Children["a"] {
		t.Error("ancestor a was not copied")
	}
	if got.Children["a"].Children["b"] == root.Children["a"].Children["b"] {
		t.Error("ancestor a/b was not copied")
	}
	if got.Children["x"] != root.Children["x"] {
		t.Error("sibling subtree x must be shared")
	}
	if got.Children["a"].Children["e"] != root.Children["a"].Children["e"] {
		t.Error("sibling subtree a/e must be shared")
	}
	if got.Children["a"].Children["b"].Children["d"] != root.Children["a"].Children["b"].Children["d"] {
		t.Error("sibling subtree a/b/d must be shared")
	}
}

func TestSetCreatesMissingPath(t *testing.T) {
	root := sampleTree()
	got := objtree.Set(root, []string{"a", "new", "deep"}, 7)
	if v := objtree.Get(got, []string{"a", "new", "deep"}); v == nil || v.Value != 7 {
		t.Fatalf("Get(a/new/deep) = %v, want leaf 7", v)
	}
	if got.Children["x"] != root.Children["x"] {
		t.Error("untouched subtree x must be shared")
	}
}

func TestDeleteSharesUntouchedSubtrees(t *testing.T) {
	root := sampleTree()
	snapshot := objtree.Clone(root)

	got := objtree.Delete(root, []string{"a", "b", "c"})

	if !objtree.Equal(root, snapshot) {
		t.Fatal("Delete mutated the original tree")
	}
	if objtree.Get(got, []string{"a", "b", "c"}) != nil {
		t.Fatal("deleted node still reachable")
	}
	if got.Children["x"] != root.Children["x"] {
		t.Error("untouched subtree x must be shared")
	}
	if got.Children["a"].Children["b"].Children["d"] != root.Children["a"].Children["b"].Children["d"] {
		t.Error("sibling a/b/d must be shared")
	}
}

func TestDeleteMissingPathIsIdentity(t *testing.T) {
	root := sampleTree()
	if got := objtree.Delete(root, []string{"a", "nope"}); got != root {
		t.Error("deleting a missing path must return the same root")
	}
}

func TestSnapshotOfOldRootSurvivesUpdates(t *testing.T) {
	root := sampleTree()
	snapshot := objtree.Clone(root)

	// A sequence of updates; the original root must remain a valid snapshot.
	r1 := objtree.Set(root, []string{"a", "b", "c"}, 10)
	r2 := objtree.Delete(r1, []string{"x"})
	r3 := objtree.Set(r2, []string{"a", "e"}, 30)

	if !objtree.Equal(root, snapshot) {
		t.Fatal("original root changed after updates")
	}
	if !objtree.Equal(r3, refApplyAll(root, []objtree.Op{
		objtree.SetOp([]string{"a", "b", "c"}, 10),
		objtree.DeleteOp([]string{"x"}),
		objtree.SetOp([]string{"a", "e"}, 30),
	})) {
		t.Fatal("chained updates diverge from the reference model")
	}
	// Intermediate snapshots still see their own versions.
	if v := objtree.Get(r1, []string{"a", "b", "c"}); v == nil || v.Value != 10 {
		t.Fatal("snapshot r1 lost its value")
	}
	if objtree.Get(r2, []string{"x"}) != nil {
		t.Fatal("snapshot r2 should not contain x")
	}
	if objtree.Get(r1, []string{"x"}) == nil {
		t.Fatal("snapshot r1 must still contain x")
	}
}
