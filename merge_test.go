package treemerge

import (
	"reflect"
	"testing"
)

func createOps() []Op {
	return []Op{
		{ID: "c-x", Kind: OpCreate, Node: "x", Parent: RootID, Name: "x", Time: 0},
		{ID: "c-y", Kind: OpCreate, Node: "y", Parent: RootID, Name: "y", Time: 0},
	}
}

func rejectReasons(r *Result) map[string]RejectReason {
	out := map[string]RejectReason{}
	for _, rej := range r.Rejected() {
		out[rej.Op.ID] = rej.Reason
	}
	return out
}

func assertSameResult(t *testing.T, a, b *Result) {
	t.Helper()
	if !reflect.DeepEqual(a.Parents(), b.Parents()) {
		t.Fatalf("parent maps differ:\n%v\n%v", a.Parents(), b.Parents())
	}
	if !reflect.DeepEqual(a.Rejected(), b.Rejected()) {
		t.Fatalf("rejection lists differ:\n%v\n%v", a.Rejected(), b.Rejected())
	}
	if err := ValidateTree(a.Parents()); err != nil {
		t.Fatalf("merged tree invalid: %v", err)
	}
}

// A moves x under y while B moves y under x at the same logical time.
// The total order picks one winner; the loser is rejected and the tree
// stays acyclic.
func TestMutualMoves(t *testing.T) {
	ops := append(createOps(),
		Op{ID: "a1", Kind: OpMove, Node: "x", Parent: "y", Time: 1},
		Op{ID: "b1", Kind: OpMove, Node: "y", Parent: "x", Time: 1},
	)
	r := New()
	r.Apply(ops...)
	res := r.Result()

	parents := res.Parents()
	if parents["x"] != "y" || parents["y"] != RootID {
		t.Fatalf("expected x under y, y under root; got %v", parents)
	}
	rej := rejectReasons(res)
	if rej["b1"] != ReasonSelfOrDescendant {
		t.Fatalf("expected b1 rejected as self_or_descendant, got %v", rej)
	}
	if err := ValidateTree(parents); err != nil {
		t.Fatalf("tree invalid: %v", err)
	}

	// Swap which replica gets the lexicographically smaller op ID: the
	// other move now wins. The outcome is fully determined by the order.
	ops2 := append(createOps(),
		Op{ID: "a1", Kind: OpMove, Node: "y", Parent: "x", Time: 1},
		Op{ID: "b1", Kind: OpMove, Node: "x", Parent: "y", Time: 1},
	)
	r2 := New()
	r2.Apply(ops2...)
	res2 := r2.Result()
	parents2 := res2.Parents()
	if parents2["y"] != "x" || parents2["x"] != RootID {
		t.Fatalf("expected y under x, x under root; got %v", parents2)
	}
	if rejectReasons(res2)["b1"] != ReasonSelfOrDescendant {
		t.Fatalf("expected b1 rejected, got %v", res2.Rejected())
	}

	// Arrival order and batching must not matter.
	r3 := New()
	r3.Apply(ops[3], ops[1], ops[0])
	r3.Apply(ops[2])
	assertSameResult(t, res, r3.Result())
}

// Moving into a deleted (hidden) parent is legal: the parent still exists,
// so the move applies and the moved node becomes hidden with its subtree.
func TestMoveToHiddenParent(t *testing.T) {
	ops := append(createOps(),
		Op{ID: "c-z", Kind: OpCreate, Node: "z", Parent: "x", Name: "z", Time: 0},
		Op{ID: "d1", Kind: OpDelete, Node: "y", Time: 1},
		Op{ID: "m1", Kind: OpMove, Node: "x", Parent: "y", Time: 2},
	)
	r := New()
	r.Apply(ops...)
	res := r.Result()

	if res.Parents()["x"] != "y" {
		t.Fatalf("move into hidden parent should apply, got %v", res.Parents())
	}
	if len(res.Rejected()) != 0 {
		t.Fatalf("expected no rejections, got %v", res.Rejected())
	}
	if res.Visible("y") || res.Visible("x") || res.Visible("z") {
		t.Fatalf("deleted ancestor must hide descendants")
	}
	if !res.Visible(RootID) {
		t.Fatalf("root must stay visible")
	}
	if got := res.Tree(); got != "root#root\n" {
		t.Fatalf("visible tree should only contain root, got %q", got)
	}
	if err := ValidateTree(res.Parents()); err != nil {
		t.Fatalf("tree invalid: %v", err)
	}
}

// An early operation arriving late (after a checkpoint) must still take
// effect, rolling back the outcome of later operations that it displaces.
func TestLateEarlyOp(t *testing.T) {
	base := createOps()
	late := Op{ID: "m-early", Kind: OpMove, Node: "x", Parent: "y", Time: 1}
	later := Op{ID: "m-late", Kind: OpMove, Node: "y", Parent: "x", Time: 2}

	// Replica sees only the later move first, then checkpoints.
	m1 := New()
	m1.Apply(append(base, later)...)
	cp := m1.Checkpoint()
	before := m1.Result()
	if before.Parents()["y"] != "x" {
		t.Fatalf("before late arrival, y should be under x: %v", before.Parents())
	}

	// The early move arrives after the checkpoint.
	m2 := FromCheckpoint(cp)
	m2.Apply(late)
	after := m2.Result()

	// Full replay of the union must agree.
	full := New()
	full.Apply(append(base, late, later)...)
	assertSameResult(t, full.Result(), after)

	// The early move wins; the later one is now rejected.
	if after.Parents()["x"] != "y" || after.Parents()["y"] != RootID {
		t.Fatalf("late early op not honored: %v", after.Parents())
	}
	if rejectReasons(after)["m-late"] != ReasonSelfOrDescendant {
		t.Fatalf("m-late should be rejected after rollback, got %v", after.Rejected())
	}
}

// Duplicate operation IDs: exact retransmissions are deduplicated;
// conflicting operations sharing an ID resolve deterministically.
func TestDuplicateOpID(t *testing.T) {
	base := []Op{
		{ID: "c-a", Kind: OpCreate, Node: "a", Parent: RootID, Name: "a", Time: 0},
		{ID: "c-b", Kind: OpCreate, Node: "b", Parent: RootID, Name: "b", Time: 0},
		{ID: "c-c", Kind: OpCreate, Node: "c", Parent: RootID, Name: "c", Time: 0},
	}

	// Exact duplicate applied twice: single effect, no rejection.
	m := New()
	dup := Op{ID: "m1", Kind: OpMove, Node: "a", Parent: "b", Time: 1}
	m.Apply(append(base, dup, dup)...)
	res := m.Result()
	if len(res.Rejected()) != 0 || res.Parents()["a"] != "b" {
		t.Fatalf("exact duplicate mishandled: %v %v", res.Parents(), res.Rejected())
	}

	// Conflicting operations with the same ID: "b" < "c" canonically, so
	// the move to b wins regardless of arrival order.
	o1 := Op{ID: "m2", Kind: OpMove, Node: "a", Parent: "b", Time: 2}
	o2 := Op{ID: "m2", Kind: OpMove, Node: "a", Parent: "c", Time: 2}
	m1 := New()
	m1.Apply(append(base, o1, o2)...)
	m2 := New()
	m2.Apply(append(base, o2, o1)...)
	assertSameResult(t, m1.Result(), m2.Result())

	res = m1.Result()
	if res.Parents()["a"] != "b" {
		t.Fatalf("canonical winner should be move to b, got %v", res.Parents())
	}
	rej := res.Rejected()
	if len(rej) != 1 || rej[0].Reason != ReasonDuplicateOpID || rej[0].Op.Parent != "c" {
		t.Fatalf("loser should be rejected as duplicate_op_id, got %v", rej)
	}
}

// Recovery from a checkpoint plus subsequent operations equals a full
// replay, for any batching and arrival order.
func TestCheckpointRecovery(t *testing.T) {
	all := append(createOps(),
		Op{ID: "c-z", Kind: OpCreate, Node: "z", Parent: "y", Name: "z", Time: 1},
		Op{ID: "m1", Kind: OpMove, Node: "x", Parent: "z", Time: 2},
		Op{ID: "m2", Kind: OpMove, Node: "y", Parent: "x", Time: 3}, // rejected: x is under y's subtree
		Op{ID: "r1", Kind: OpRename, Node: "x", Name: "x-renamed", Time: 4},
		Op{ID: "d1", Kind: OpDelete, Node: "z", Time: 5},
		Op{ID: "m3", Kind: OpMove, Node: "x", Parent: RootID, Time: 6},
	)
	full := New()
	full.Apply(all...)
	want := full.Result()

	// Checkpoint after a prefix, then feed the rest one by one.
	m := New()
	m.Apply(all[:4]...)
	cp := m.Checkpoint()
	m2 := FromCheckpoint(cp)
	for _, op := range all[4:] {
		m2.Apply(op)
	}
	assertSameResult(t, want, m2.Result())

	// Same total set, shuffled arrival across three batches.
	m3 := New()
	m3.Apply(all[5], all[0], all[3])
	m3.Apply(all[1], all[6], all[7])
	cp3 := m3.Checkpoint()
	m4 := FromCheckpoint(cp3)
	m4.Apply(all[4], all[2])
	assertSameResult(t, want, m4.Result())
}

// Renames keep the node ID; same-name siblings are allowed and stay
// distinguishable because paths carry IDs.
func TestRenameAndPaths(t *testing.T) {
	ops := []Op{
		{ID: "c-d", Kind: OpCreate, Node: "d1", Parent: RootID, Name: "docs", Time: 0},
		{ID: "c-f1", Kind: OpCreate, Node: "f1", Parent: "d1", Name: "file", Time: 1},
		{ID: "c-f2", Kind: OpCreate, Node: "f2", Parent: "d1", Name: "file", Time: 1},
		{ID: "r1", Kind: OpRename, Node: "d1", Name: "documents", Time: 2},
	}
	m := New()
	m.Apply(ops...)
	res := m.Result()

	p1, ok1 := res.Path("f1")
	p2, ok2 := res.Path("f2")
	if !ok1 || !ok2 {
		t.Fatalf("paths must resolve")
	}
	if p1 != "/root#root/documents#d1/file#f1" {
		t.Fatalf("unexpected path %q", p1)
	}
	if p2 != "/root#root/documents#d1/file#f2" {
		t.Fatalf("unexpected path %q", p2)
	}
	if p1 == p2 {
		t.Fatalf("same-name siblings must have distinct paths")
	}
	if res.Names()["d1"] != "documents" {
		t.Fatalf("rename not applied: %v", res.Names())
	}
	if _, ok := res.Parents()["d1"]; !ok {
		t.Fatalf("rename must not change the node ID")
	}
}

// Each rejection reason is recorded with the offending operation.
func TestRejections(t *testing.T) {
	ops := append(createOps(),
		Op{ID: "m-root", Kind: OpMove, Node: RootID, Parent: "x", Time: 1},
		Op{ID: "m-ghost", Kind: OpMove, Node: "ghost", Parent: "x", Time: 1},
		Op{ID: "m-noparent", Kind: OpMove, Node: "x", Parent: "ghost", Time: 1},
		Op{ID: "m-self", Kind: OpMove, Node: "x", Parent: "x", Time: 1},
		Op{ID: "c-z", Kind: OpCreate, Node: "z", Parent: "x", Name: "z", Time: 1},
		Op{ID: "m-desc", Kind: OpMove, Node: "x", Parent: "z", Time: 2},
		Op{ID: "c-x2", Kind: OpCreate, Node: "x", Parent: RootID, Name: "x2", Time: 2},
		Op{ID: "c-orphan", Kind: OpCreate, Node: "w", Parent: "ghost", Name: "w", Time: 2},
		Op{ID: "c-root", Kind: OpCreate, Node: RootID, Parent: RootID, Name: "r", Time: 2},
		Op{ID: "d-root", Kind: OpDelete, Node: RootID, Time: 2},
		Op{ID: "m-early", Kind: OpMove, Node: "y", Parent: "late", Time: 2},
		Op{ID: "c-late", Kind: OpCreate, Node: "late", Parent: RootID, Name: "late", Time: 3},
	)
	m := New()
	m.Apply(ops...)
	res := m.Result()

	want := map[string]RejectReason{
		"m-root":     ReasonImmovableRoot,
		"m-ghost":    ReasonUnknownNode,
		"m-noparent": ReasonMissingParent,
		"m-self":     ReasonSelfOrDescendant,
		"m-desc":     ReasonSelfOrDescendant,
		"c-x2":       ReasonNodeExists,
		"c-orphan":   ReasonMissingParent,
		"c-root":     ReasonReservedRoot,
		"d-root":     ReasonImmovableRoot,
		// The parent is created at a later logical time, so at replay
		// time of the move it does not exist yet.
		"m-early": ReasonMissingParent,
	}
	got := rejectReasons(res)
	for id, reason := range want {
		if got[id] != reason {
			t.Fatalf("op %s: want %s, got %s", id, reason, got[id])
		}
	}
	if len(got) != len(want) {
		t.Fatalf("unexpected rejections: %v", got)
	}
	if err := ValidateTree(res.Parents()); err != nil {
		t.Fatalf("tree invalid: %v", err)
	}
}

// The independent validator accepts merged trees and rejects broken ones.
func TestValidateTree(t *testing.T) {
	m := New()
	m.Apply(append(createOps(),
		Op{ID: "m1", Kind: OpMove, Node: "x", Parent: "y", Time: 1},
	)...)
	if err := ValidateTree(m.Result().Parents()); err != nil {
		t.Fatalf("valid tree rejected: %v", err)
	}

	cases := map[string]map[string]string{
		"missing root":    {"a": RootID},
		"root has parent": {RootID: "a", "a": RootID},
		"self parent":     {RootID: "", "a": "a"},
		"cycle":           {RootID: "", "a": "b", "b": "a"},
		"unknown parent":  {RootID: "", "a": "ghost"},
		"orphan":          {RootID: "", "a": "b", "b": ""},
	}
	for name, parents := range cases {
		if err := ValidateTree(parents); err == nil {
			t.Fatalf("%s: expected validation error", name)
		}
	}
}

// VisibleSet agrees with Result.Visible on a tree with nested deletes.
func TestVisibleSetConsistency(t *testing.T) {
	ops := append(createOps(),
		Op{ID: "c-z", Kind: OpCreate, Node: "z", Parent: "x", Name: "z", Time: 1},
		Op{ID: "d1", Kind: OpDelete, Node: "x", Time: 2},
	)
	m := New()
	m.Apply(ops...)
	res := m.Result()
	independent := VisibleSet(res.Parents(), map[string]bool{"x": true})
	for id := range res.Parents() {
		if res.Visible(id) != independent[id] {
			t.Fatalf("visibility mismatch for %s", id)
		}
	}
	if independent["z"] || independent["x"] || !independent["y"] {
		t.Fatalf("bad visible set: %v", independent)
	}
}
