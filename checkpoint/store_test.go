package checkpoint

import (
	"errors"
	"reflect"
	"testing"
)

func put(t *testing.T, s *Store, data string) Digest {
	t.Helper()
	return s.PutBlock([]byte(data))
}

func mustCommit(t *testing.T, s *Store, id string, parents []string, ops []Operation) *Snapshot {
	t.Helper()
	snap, err := s.Commit(id, parents, ops)
	if err != nil {
		t.Fatalf("commit %s: %v", id, err)
	}
	return snap
}

func addOp(refs ...Ref) Operation { return Operation{Kind: OpAdd, Refs: refs} }

func removeOp(paths ...string) Operation {
	op := Operation{Kind: OpRemove}
	for _, p := range paths {
		op.Refs = append(op.Refs, Ref{Path: p})
	}
	return op
}

func TestSharedBaseBlocks(t *testing.T) {
	s := NewStore()
	a := put(t, s, "aaa")
	b := put(t, s, "bbb")
	c := put(t, s, "ccc")

	mustCommit(t, s, "root", nil, []Operation{addOp(Ref{"a.txt", a}, Ref{"b.txt", b})})
	mustCommit(t, s, "child", []string{"root"}, []Operation{addOp(Ref{"c.txt", c})})

	// Same content stored twice must share one block.
	again := s.PutBlock([]byte("aaa"))
	if again != a || s.BlockCount() != 3 {
		t.Fatalf("expected block sharing, count=%d", s.BlockCount())
	}

	set, err := s.Expand("child")
	if err != nil {
		t.Fatal(err)
	}
	want := RefSet{"a.txt": a, "b.txt": b, "c.txt": c}
	if !reflect.DeepEqual(set, want) {
		t.Fatalf("expand = %v, want %v", set, want)
	}
}

func TestExpandIncrementalRemove(t *testing.T) {
	s := NewStore()
	x := put(t, s, "x")
	y := put(t, s, "y")
	z := put(t, s, "z")

	mustCommit(t, s, "v1", nil, []Operation{addOp(Ref{"x", x}, Ref{"y", y})})
	mustCommit(t, s, "v2", []string{"v1"}, []Operation{removeOp("x"), addOp(Ref{"z", z})})

	set, err := s.Expand("v2")
	if err != nil {
		t.Fatal(err)
	}
	want := RefSet{"y": y, "z": z}
	if !reflect.DeepEqual(set, want) {
		t.Fatalf("expand = %v, want %v", set, want)
	}
	// Parent expansion is unaffected by the child's remove.
	parent, _ := s.Expand("v1")
	if !reflect.DeepEqual(parent, RefSet{"x": x, "y": y}) {
		t.Fatalf("parent expand = %v", parent)
	}
}

func TestDownloadPlanDedup(t *testing.T) {
	s := NewStore()
	shared := put(t, s, "shared-content")
	other := put(t, s, "other")

	// Three paths reference the same block.
	mustCommit(t, s, "v1", nil, []Operation{addOp(
		Ref{"p1", shared}, Ref{"p2", shared}, Ref{"p3", shared}, Ref{"p4", other},
	)})

	plan, err := s.DownloadPlan("v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 2 {
		t.Fatalf("plan = %v, want 2 distinct blocks", plan)
	}

	// Local blocks are excluded.
	plan, err = s.DownloadPlan("v1", []Digest{other})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan, []Digest{shared}) {
		t.Fatalf("plan = %v, want [%s]", plan, shared)
	}
}

func TestRemoveOnlyAffectsCurrentBranch(t *testing.T) {
	s := NewStore()
	x := put(t, s, "x")
	y := put(t, s, "y")

	mustCommit(t, s, "base", nil, []Operation{addOp(Ref{"x", x}, Ref{"y", y})})
	mustCommit(t, s, "branchA", []string{"base"}, []Operation{removeOp("x")})
	mustCommit(t, s, "branchB", []string{"base"}, nil)

	a, _ := s.Expand("branchA")
	if _, ok := a["x"]; ok {
		t.Fatalf("branchA should not contain x: %v", a)
	}
	b, _ := s.Expand("branchB")
	if b["x"] != x {
		t.Fatalf("branchB must keep x: %v", b)
	}
	// Block x is still referenced by branchB/base, so GC keeps it.
	if freed := s.GC(); len(freed) != 0 {
		t.Fatalf("GC freed %v, want nothing", freed)
	}
}

func TestDeleteBranchKeepsSharedBlocks(t *testing.T) {
	s := NewStore()
	shared := put(t, s, "shared")
	aOnly := put(t, s, "a-only")
	bOnly := put(t, s, "b-only")

	mustCommit(t, s, "root", nil, []Operation{addOp(Ref{"shared", shared})})
	mustCommit(t, s, "branchA", []string{"root"}, []Operation{addOp(Ref{"a", aOnly})})
	mustCommit(t, s, "branchB", []string{"root"}, []Operation{addOp(Ref{"b", bOnly})})

	freed, err := s.DeleteSnapshot("branchA")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(freed, []Digest{aOnly}) {
		t.Fatalf("freed = %v, want [%s]", freed, aOnly)
	}
	if _, ok := s.Block(shared); !ok {
		t.Fatal("shared block must survive branch deletion")
	}
	if _, ok := s.Block(bOnly); !ok {
		t.Fatal("other branch's block must survive")
	}

	// Deleting a non-tip is rejected.
	if _, err := s.DeleteSnapshot("root"); !errors.Is(err, ErrHasChildren) {
		t.Fatalf("delete root: %v, want ErrHasChildren", err)
	}
}

func TestMissingParentAndSelfParent(t *testing.T) {
	s := NewStore()
	if _, err := s.Commit("x", []string{"nope"}, nil); !errors.Is(err, ErrMissingParent) {
		t.Fatalf("got %v, want ErrMissingParent", err)
	}
	if _, err := s.Commit("x", []string{"x"}, nil); !errors.Is(err, ErrSelfParent) {
		t.Fatalf("got %v, want ErrSelfParent", err)
	}
}

func TestDuplicateRefRejected(t *testing.T) {
	s := NewStore()
	b1 := put(t, s, "1")
	b2 := put(t, s, "2")
	op := addOp(Ref{"p", b1}, Ref{"p", b2})
	if _, err := s.Commit("v1", nil, []Operation{op}); !errors.Is(err, ErrDuplicateRef) {
		t.Fatalf("got %v, want ErrDuplicateRef", err)
	}
}

func TestUnknownBlockRejected(t *testing.T) {
	s := NewStore()
	op := addOp(Ref{"p", "sha256:deadbeef"})
	if _, err := s.Commit("v1", nil, []Operation{op}); !errors.Is(err, ErrUnknownBlock) {
		t.Fatalf("got %v, want ErrUnknownBlock", err)
	}
}

func TestDigestBindsParentAndOpOrder(t *testing.T) {
	s := NewStore()
	a := put(t, s, "a")
	b := put(t, s, "b")

	r1 := mustCommit(t, s, "r1", nil, []Operation{addOp(Ref{"a", a})})
	mustCommit(t, s, "r2", nil, []Operation{addOp(Ref{"b", b})})

	// Same ops, different parents -> different digests.
	c1 := mustCommit(t, s, "c1", []string{"r1"}, []Operation{addOp(Ref{"n", a})})
	c2 := mustCommit(t, s, "c2", []string{"r2"}, []Operation{addOp(Ref{"n", a})})
	if c1.Digest == c2.Digest {
		t.Fatal("digest must bind the parent snapshot")
	}

	// Same parent, different operation order -> different digests.
	d1 := mustCommit(t, s, "d1", []string{"r1"}, []Operation{addOp(Ref{"a2", a}), removeOp("a")})
	d2 := mustCommit(t, s, "d2", []string{"r1"}, []Operation{removeOp("a"), addOp(Ref{"a2", a})})
	if d1.Digest == d2.Digest {
		t.Fatal("digest must bind operation order")
	}
	_ = r1
}

func TestForkHeads(t *testing.T) {
	s := NewStore()
	a := put(t, s, "a")
	mustCommit(t, s, "root", nil, []Operation{addOp(Ref{"a", a})})
	mustCommit(t, s, "f1", []string{"root"}, nil)
	mustCommit(t, s, "f2", []string{"root"}, nil)

	heads := s.Heads()
	if !reflect.DeepEqual(heads, []string{"f1", "f2"}) {
		t.Fatalf("heads = %v, want [f1 f2]", heads)
	}
	if issues := s.Verify(); len(issues) != 0 {
		t.Fatalf("verify issues: %v", issues)
	}
}
