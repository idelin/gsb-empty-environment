package objtree_test

import (
	"errors"
	"math/rand"
	"testing"

	"objtree"
)

func TestEmptyTransactionReturnsSameRoot(t *testing.T) {
	root := sampleTree()
	got, err := objtree.Apply(root)
	if err != nil {
		t.Fatalf("empty transaction failed: %v", err)
	}
	if got != root {
		t.Error("empty transaction must return the identical root")
	}
}

func TestEmptyTransactionOnNilRoot(t *testing.T) {
	got, err := objtree.Apply(nil)
	if err != nil || got != nil {
		t.Errorf("Apply(nil) = %v, %v; want nil, nil", got, err)
	}
}

func TestTransactionMergesCommonAncestors(t *testing.T) {
	root := sampleTree()
	snapshot := objtree.Clone(root)

	ops := []objtree.Op{
		objtree.SetOp([]string{"a", "b", "c"}, 10), // shares ancestors a, a/b ...
		objtree.SetOp([]string{"a", "b", "d"}, 20), // ... with this op
		objtree.SetOp([]string{"a", "e"}, 30),
		objtree.DeleteOp([]string{"x"}),
	}
	got, err := objtree.Apply(root, ops...)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	// Structural equality with the deep-copy reference model.
	want := refApplyAll(root, ops)
	if !objtree.Equal(got, want) {
		t.Fatal("transaction result diverges from the reference model")
	}

	// Snapshot isolation.
	if !objtree.Equal(root, snapshot) {
		t.Fatal("transaction mutated the original tree")
	}

	// The two updates under a/b must not overwrite each other.
	if v := objtree.Get(got, []string{"a", "b", "c"}); v == nil || v.Value != 10 {
		t.Error("update a/b/c lost")
	}
	if v := objtree.Get(got, []string{"a", "b", "d"}); v == nil || v.Value != 20 {
		t.Error("update a/b/d lost")
	}

	// The shared ancestor a/b is copied exactly once: both new children
	// hang off the same copied node.
	ab := got.Children["a"].Children["b"]
	if ab.Children["c"].Value != 10 || ab.Children["d"].Value != 20 {
		t.Error("merged ancestor a/b does not carry both updates")
	}

	// Identity: no node outside the operation paths was copied.
	assertShared(t, root, got, ops)
}

func TestTransactionRejectsDeleteVsSubpathSet(t *testing.T) {
	root := sampleTree()
	cases := []struct {
		name string
		ops  []objtree.Op
	}{
		{"delete ancestor of set", []objtree.Op{
			objtree.DeleteOp([]string{"a", "b"}),
			objtree.SetOp([]string{"a", "b", "c"}, 1),
		}},
		{"delete same path as set", []objtree.Op{
			objtree.DeleteOp([]string{"a", "b"}),
			objtree.SetOp([]string{"a", "b"}, 1),
		}},
		{"delete root with deep set", []objtree.Op{
			objtree.DeleteOp(nil),
			objtree.SetOp([]string{"a"}, 1),
		}},
		{"nested sets", []objtree.Op{
			objtree.SetOp([]string{"a", "b"}, 1),
			objtree.SetOp([]string{"a", "b", "c"}, 2),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := objtree.Apply(root, tc.ops...)
			if !errors.Is(err, objtree.ErrConflict) {
				t.Fatalf("err = %v, want ErrConflict", err)
			}
			if got != nil {
				t.Error("rejected transaction must not return a tree")
			}
		})
	}
}

func TestTransactionAllowsNonConflictingNesting(t *testing.T) {
	root := sampleTree()

	// Delete subsumes deeper deletes; sibling delete+set is fine.
	ops := []objtree.Op{
		objtree.DeleteOp([]string{"a"}),
		objtree.DeleteOp([]string{"a", "b"}), // subsumed by delete a
		objtree.SetOp([]string{"x"}, 40),
	}
	got, err := objtree.Apply(root, ops...)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	want := refApplyAll(root, ops)
	if !objtree.Equal(got, want) {
		t.Fatal("result diverges from the reference model")
	}
	if v := objtree.Get(got, []string{"x"}); v == nil || v.Value != 40 {
		t.Error("set on x lost")
	}
}

func TestTransactionLastWriteWinsOnSamePath(t *testing.T) {
	root := sampleTree()
	got, err := objtree.Apply(root,
		objtree.SetOp([]string{"a", "e"}, 31),
		objtree.SetOp([]string{"a", "e"}, 32),
	)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if v := objtree.Get(got, []string{"a", "e"}); v == nil || v.Value != 32 {
		t.Fatalf("Get(a/e) = %v, want 32 (last write wins)", v)
	}
}

func TestTransactionNoOpDeleteKeepsIdentity(t *testing.T) {
	root := sampleTree()
	got, err := objtree.Apply(root, objtree.DeleteOp([]string{"missing", "path"}))
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if got != root {
		t.Error("deleting a missing path must return the identical root")
	}
}

// Property test: random trees and random transactions must match the
// deep-copy reference model, preserve the old snapshot, and share every
// untouched subtree.
func TestApplyMatchesReferenceModel(t *testing.T) {
	rng := rand.New(rand.NewSource(20261006))
	keys := []string{"a", "b", "c", "d"}

	randomTree := func(depth int) *objtree.Node {
		var build func(d int) *objtree.Node
		build = func(d int) *objtree.Node {
			if d == 0 || rng.Intn(2) == 0 {
				return objtree.Leaf(rng.Intn(100))
			}
			n := 1 + rng.Intn(3)
			children := make(map[string]*objtree.Node, n)
			perm := rng.Perm(len(keys))
			for i := 0; i < n; i++ {
				children[keys[perm[i]]] = build(d - 1)
			}
			return objtree.Internal(children)
		}
		return build(depth)
	}
	randomPath := func() []string {
		n := rng.Intn(4) // 0..3 segments
		p := make([]string, n)
		for i := range p {
			p[i] = keys[rng.Intn(len(keys))]
		}
		return p
	}

	success := 0
	for trial := 0; trial < 2000 && success < 300; trial++ {
		root := randomTree(3)
		snapshot := objtree.Clone(root)

		n := 1 + rng.Intn(4)
		ops := make([]objtree.Op, n)
		for i := range ops {
			if rng.Intn(3) == 0 {
				ops[i] = objtree.DeleteOp(randomPath())
			} else {
				ops[i] = objtree.SetOp(randomPath(), rng.Intn(100))
			}
		}

		got, err := objtree.Apply(root, ops...)
		if err != nil {
			if !errors.Is(err, objtree.ErrConflict) {
				t.Fatalf("trial %d: unexpected error %v", trial, err)
			}
			continue // genuinely conflicting transaction
		}
		success++

		want := refApplyAll(root, ops)
		if !objtree.Equal(got, want) {
			t.Fatalf("trial %d: result diverges from reference model\nops: %v", trial, ops)
		}
		if !objtree.Equal(root, snapshot) {
			t.Fatalf("trial %d: transaction mutated the original tree", trial)
		}
		assertShared(t, root, got, ops)
	}
	if success < 100 {
		t.Fatalf("only %d non-conflicting transactions; generator too conflict-prone", success)
	}
}
