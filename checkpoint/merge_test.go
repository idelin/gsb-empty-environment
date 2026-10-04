package checkpoint

import (
	"errors"
	"reflect"
	"testing"
)

func TestMergeClean(t *testing.T) {
	s := NewStore()
	a := put(t, s, "a")
	b := put(t, s, "b")
	c := put(t, s, "c")

	mustCommit(t, s, "base", nil, []Operation{addOp(Ref{"a", a})})
	mustCommit(t, s, "left", []string{"base"}, []Operation{addOp(Ref{"b", b})})
	mustCommit(t, s, "right", []string{"base"}, []Operation{addOp(Ref{"c", c})})

	m, err := s.Merge("m", "left", "right", nil)
	if err != nil {
		t.Fatal(err)
	}
	set, err := s.Expand(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := RefSet{"a": a, "b": b, "c": c}
	if !reflect.DeepEqual(set, want) {
		t.Fatalf("merged set = %v, want %v", set, want)
	}
	if issues := s.Verify(); len(issues) != 0 {
		t.Fatalf("verify issues: %v", issues)
	}
}

func TestMergeConflictRequiresResolution(t *testing.T) {
	s := NewStore()
	v0 := put(t, s, "v0")
	v1 := put(t, s, "v1")
	v2 := put(t, s, "v2")
	v3 := put(t, s, "v3")
	k := put(t, s, "keep")

	mustCommit(t, s, "base", nil, []Operation{addOp(Ref{"f", v0}, Ref{"keep", k})})
	mustCommit(t, s, "left", []string{"base"}, []Operation{addOp(Ref{"f", v1})})
	mustCommit(t, s, "right", []string{"base"}, []Operation{addOp(Ref{"f", v2})})

	// Unresolved conflict must fail and name the path.
	_, err := s.Merge("m1", "left", "right", nil)
	var conflict *MergeConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("got %v, want MergeConflictError", err)
	}
	if !reflect.DeepEqual(conflict.Paths, []string{"f"}) {
		t.Fatalf("conflict paths = %v, want [f]", conflict.Paths)
	}
	if !errors.Is(err, ErrMergeConflict) {
		t.Fatalf("errors.Is mismatch: %v", err)
	}

	// Explicit resolution succeeds.
	m, err := s.Merge("m2", "left", "right", []Operation{addOp(Ref{"f", v3})})
	if err != nil {
		t.Fatal(err)
	}
	set, _ := s.Expand(m.ID)
	want := RefSet{"f": v3, "keep": k}
	if !reflect.DeepEqual(set, want) {
		t.Fatalf("merged set = %v, want %v", set, want)
	}
	if issues := s.Verify(); len(issues) != 0 {
		t.Fatalf("verify issues: %v", issues)
	}
}

func TestMergeDeleteVsModifyConflict(t *testing.T) {
	s := NewStore()
	v0 := put(t, s, "v0")
	v2 := put(t, s, "v2")

	mustCommit(t, s, "base", nil, []Operation{addOp(Ref{"f", v0})})
	mustCommit(t, s, "left", []string{"base"}, []Operation{removeOp("f")})
	mustCommit(t, s, "right", []string{"base"}, []Operation{addOp(Ref{"f", v2})})

	if _, err := s.Merge("m1", "left", "right", nil); !errors.Is(err, ErrMergeConflict) {
		t.Fatalf("got %v, want ErrMergeConflict", err)
	}
	// Resolve by accepting the deletion.
	m, err := s.Merge("m2", "left", "right", []Operation{removeOp("f")})
	if err != nil {
		t.Fatal(err)
	}
	set, _ := s.Expand(m.ID)
	if len(set) != 0 {
		t.Fatalf("merged set = %v, want empty", set)
	}
	if issues := s.Verify(); len(issues) != 0 {
		t.Fatalf("verify issues: %v", issues)
	}
}

func TestMergeIdenticalChangeNoConflict(t *testing.T) {
	s := NewStore()
	v0 := put(t, s, "v0")
	v1 := put(t, s, "v1")

	mustCommit(t, s, "base", nil, []Operation{addOp(Ref{"f", v0})})
	// Both branches make the same change: not a conflict.
	mustCommit(t, s, "left", []string{"base"}, []Operation{addOp(Ref{"f", v1})})
	mustCommit(t, s, "right", []string{"base"}, []Operation{addOp(Ref{"f", v1})})

	m, err := s.Merge("m", "left", "right", nil)
	if err != nil {
		t.Fatal(err)
	}
	set, _ := s.Expand(m.ID)
	if !reflect.DeepEqual(set, RefSet{"f": v1}) {
		t.Fatalf("merged set = %v", set)
	}
}

func TestMergeNoCommonAncestor(t *testing.T) {
	s := NewStore()
	a := put(t, s, "a")
	b := put(t, s, "b")
	mustCommit(t, s, "r1", nil, []Operation{addOp(Ref{"a", a})})
	mustCommit(t, s, "r2", nil, []Operation{addOp(Ref{"b", b})})

	if _, err := s.Merge("m", "r1", "r2", nil); !errors.Is(err, ErrNoCommonAncestor) {
		t.Fatalf("got %v, want ErrNoCommonAncestor", err)
	}
}

func TestMergeChainAfterMerge(t *testing.T) {
	s := NewStore()
	a := put(t, s, "a")
	b := put(t, s, "b")
	c := put(t, s, "c")
	d := put(t, s, "d")

	mustCommit(t, s, "base", nil, []Operation{addOp(Ref{"a", a})})
	mustCommit(t, s, "left", []string{"base"}, []Operation{addOp(Ref{"b", b})})
	mustCommit(t, s, "right", []string{"base"}, []Operation{addOp(Ref{"c", c})})
	if _, err := s.Merge("m", "left", "right", nil); err != nil {
		t.Fatal(err)
	}
	// Continue committing on top of a merge snapshot.
	mustCommit(t, s, "next", []string{"m"}, []Operation{addOp(Ref{"d", d})})
	set, _ := s.Expand("next")
	want := RefSet{"a": a, "b": b, "c": c, "d": d}
	if !reflect.DeepEqual(set, want) {
		t.Fatalf("set = %v, want %v", set, want)
	}
	if issues := s.Verify(); len(issues) != 0 {
		t.Fatalf("verify issues: %v", issues)
	}
}

func TestCommitRejectsTwoParents(t *testing.T) {
	s := NewStore()
	a := put(t, s, "a")
	mustCommit(t, s, "r", nil, []Operation{addOp(Ref{"a", a})})
	if _, err := s.Commit("bad", []string{"r", "r"}, nil); !errors.Is(err, ErrTooManyParents) {
		t.Fatalf("got %v, want ErrTooManyParents", err)
	}
}
