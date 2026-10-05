package tree

import (
	"reflect"
	"testing"
)

func create(id, parent, name string, t int64) Op {
	return Op{ID: "c-" + id, Time: t, Kind: OpCreate, Node: id, Parent: parent, Name: name}
}

func move(opID string, t int64, node, parent string) Op {
	return Op{ID: opID, Time: t, Kind: OpMove, Node: node, Parent: parent}
}

func run(batches ...[]Op) *Engine {
	e := NewEngine()
	for _, b := range batches {
		e.Merge(b...)
	}
	return e
}

func mustReject(t *testing.T, e *Engine, opID string, reason RejectReason) {
	t.Helper()
	for _, r := range e.Rejections() {
		if r.Op.ID == opID && r.Reason == reason {
			return
		}
	}
	t.Fatalf("expected rejection %s for op %s, got %v", reason, opID, e.Rejections())
}

func mustParent(t *testing.T, e *Engine, node, want string) {
	t.Helper()
	got, ok := e.Parent(node)
	if !ok || got != want {
		t.Fatalf("parent(%s) = %q, %v; want %q", node, got, ok, want)
	}
}

func mustValid(t *testing.T, e *Engine) {
	t.Helper()
	if err := Validate(e.Snapshot()); err != nil {
		t.Fatalf("validator rejected merged state: %v\ntree:\n%s", err, e.Tree())
	}
}

// A moves x under y while B moves y under x. The later move in total order
// targets its own descendant and must be rejected; the tree stays acyclic.
func TestMutualMove(t *testing.T) {
	base := []Op{
		create("x", RootID, "x", 1),
		create("y", RootID, "y", 2),
	}
	opA := move("m-a", 3, "x", "y") // x -> y
	opB := move("m-b", 4, "y", "x") // y -> x, arrives concurrently

	want := run(append(append([]Op{}, base...), opA, opB))
	mustParent(t, want, "x", "y")
	mustParent(t, want, "y", RootID)
	mustReject(t, want, "m-b", ReasonTargetIsDesc)
	mustValid(t, want)

	// Same set, different arrival order and batching: identical result.
	orders := [][]Op{
		{opB, opA},
		{opA, opB},
	}
	for _, ops := range orders {
		got := run(append(append([]Op{}, base...), ops...))
		if !reflect.DeepEqual(want.Snapshot(), got.Snapshot()) {
			t.Fatalf("arrival order changed result:\nfirst:  %+v\nsecond: %+v", want.Snapshot(), got.Snapshot())
		}
	}
	// Split into batches, late op second.
	got := run(append([]Op{}, base...), []Op{opB}, []Op{opA})
	if !reflect.DeepEqual(want.Snapshot(), got.Snapshot()) {
		t.Fatal("batching changed result")
	}
}

// Moving into a deleted (hidden) directory is rejected; descendants of a
// deleted node are hidden by ancestor visibility.
func TestMoveToHiddenParent(t *testing.T) {
	e := run([]Op{
		create("x", RootID, "x", 1),
		create("y", RootID, "y", 2),
		create("c", "y", "c", 3),
		{ID: "del-y", Time: 4, Kind: OpDelete, Node: "y"},
		move("m-into-hidden", 5, "x", "y"),
	})
	mustReject(t, e, "m-into-hidden", ReasonParentHidden)
	mustParent(t, e, "x", RootID)
	if e.Visible("y") || e.Visible("c") {
		t.Fatal("deleted node and its descendant must be hidden")
	}
	if !e.Visible("x") {
		t.Fatal("x must stay visible under root")
	}
	mustValid(t, e)
}

// A late-arriving operation with an early logical time re-decides later
// operations on resume from a checkpoint (explicit total-order replay).
func TestLateEarlyOpRollsBack(t *testing.T) {
	creates := []Op{
		create("x", RootID, "x", 1),
		create("y", RootID, "y", 2),
		create("z", RootID, "z", 3),
	}
	early := move("m-early", 4, "y", "z") // arrives late
	batch1 := []Op{move("m-x-y", 5, "x", "y"), move("m-z-x", 6, "z", "x")}

	// Full replay in one shot is the ground truth.
	all := append(append(append([]Op{}, creates...), batch1...), early)
	want := run(all)

	// Checkpoint after batch1 (without the late op), then resume with it.
	e1 := run(creates, batch1)
	mustParent(t, e1, "z", "x") // provisionally applied
	cp := e1.Checkpoint()
	got := Resume(cp, early)

	if !reflect.DeepEqual(want.Snapshot(), got.Snapshot()) {
		t.Fatalf("resume mismatch:\nfull:   %+v\nresumed: %+v", want.Snapshot(), got.Snapshot())
	}
	// After the late op lands: y moves under z (t=4), so z->x at t=6 would
	// cycle (x is a descendant of z) and is rejected; z rolls back to root.
	mustParent(t, got, "y", "z")
	mustParent(t, got, "x", "y")
	mustParent(t, got, "z", RootID)
	mustReject(t, got, "m-z-x", ReasonTargetIsDesc)
	mustValid(t, got)
}

// Conflicting payloads sharing an op ID resolve deterministically to the
// canonically smaller op; the loser is kept as a rejection. Identical
// duplicates dedupe silently.
func TestDuplicateOpIDConflict(t *testing.T) {
	base := []Op{
		create("x", RootID, "x", 1),
		create("y", RootID, "y", 2),
		create("z", RootID, "z", 3),
	}
	a := move("dup", 4, "x", "y") // canonically smaller (t=4)
	b := move("dup", 5, "x", "z") // conflicts on same op ID

	for _, ops := range [][]Op{{a, b}, {b, a}} {
		e := run(append(append([]Op{}, base...), ops...))
		mustParent(t, e, "x", "y") // t=4 payload wins regardless of order
		mustReject(t, e, "dup", ReasonDuplicateOpID)
		found := false
		for _, r := range e.Rejections() {
			if r.Op.ID == "dup" && r.Op == b {
				found = true
			}
		}
		if !found {
			t.Fatal("losing duplicate payload must be preserved in rejections")
		}
		mustValid(t, e)
	}

	// Identical duplicate: applied once, no rejection.
	e := run(base, []Op{a}, []Op{a})
	mustParent(t, e, "x", "y")
	if len(e.Rejections()) != 0 {
		t.Fatalf("identical duplicates must dedupe silently, got %v", e.Rejections())
	}
}

// Creating a node ID that already exists is rejected; the first create wins.
func TestDuplicateNodeIDOnCreate(t *testing.T) {
	e := run([]Op{
		create("x", RootID, "first", 1),
		{ID: "c-x-again", Time: 2, Kind: OpCreate, Node: "x", Parent: RootID, Name: "second"},
	})
	mustReject(t, e, "c-x-again", ReasonNodeExists)
	p, _ := e.Path("x")
	if p != "/root(root)/first(x)" {
		t.Fatalf("first create must win, got path %q", p)
	}
	mustValid(t, e)
}

// Rename keeps the node ID; same-name siblings are allowed because paths
// carry IDs.
func TestRenameAndSameNameSiblings(t *testing.T) {
	e := run([]Op{
		{ID: "c1", Time: 1, Kind: OpCreate, Node: "a", Parent: RootID, Name: "docs"},
		{ID: "c2", Time: 2, Kind: OpCreate, Node: "b", Parent: RootID, Name: "docs"},
		{ID: "r1", Time: 3, Kind: OpRename, Node: "a", Name: "renamed"},
	})
	pa, _ := e.Path("a")
	pb, _ := e.Path("b")
	if pa != "/root(root)/renamed(a)" {
		t.Fatalf("rename must keep ID, got %q", pa)
	}
	if pb != "/root(root)/docs(b)" {
		t.Fatalf("sibling path must carry ID, got %q", pb)
	}
	if pa == pb {
		t.Fatal("same-name siblings must have distinct paths")
	}
	mustValid(t, e)
}

// The root can neither be moved nor deleted.
func TestRootProtected(t *testing.T) {
	e := run([]Op{
		create("x", RootID, "x", 1),
		move("m-root", 2, RootID, "x"),
		{ID: "d-root", Time: 3, Kind: OpDelete, Node: RootID},
	})
	mustReject(t, e, "m-root", ReasonRootImmovable)
	mustReject(t, e, "d-root", ReasonRootUndeletable)
	mustValid(t, e)
}

// The same operation set must converge to the same parent mapping and
// rejection list under every batching and arrival order, with or without a
// checkpoint in the middle.
func TestConvergenceAcrossBatching(t *testing.T) {
	ops := []Op{
		create("a", RootID, "a", 1),
		create("b", RootID, "b", 2),
		create("c", "a", "c", 3),
		move("m1", 4, "b", "c"),
		{ID: "del-a", Time: 5, Kind: OpDelete, Node: "a"},
		move("m2", 6, "c", "b"), // b is hidden under deleted a? no: b moved under c which is under a -> hidden
		move("m3", 7, "a", "b"), // a is ancestor of b -> descendant target
		{ID: "r", Time: 8, Kind: OpRename, Node: "b", Name: "bee"},
		move("m4", 9, "ghost", "b"),  // missing node
		move("m5", 10, "b", "ghost"), // missing parent
	}
	ground := run(ops)
	mustValid(t, ground)

	batchings := [][][]Op{
		{ops},
		{ops[:3], ops[3:]},
		{ops[:5], ops[5:8], ops[8:]},
		{reverse(ops)},
		{ops[5:], ops[:5]},
	}
	for i, batches := range batchings {
		got := run(batches...)
		if !reflect.DeepEqual(ground.Snapshot(), got.Snapshot()) {
			t.Fatalf("batching %d diverged:\nground: %+v\ngot:    %+v", i, ground.Snapshot(), got.Snapshot())
		}
	}

	// Checkpoint at every split point, resume with the remainder.
	for i := 0; i <= len(ops); i++ {
		e := run(ops[:i])
		got := Resume(e.Checkpoint(), ops[i:]...)
		if !reflect.DeepEqual(ground.Snapshot(), got.Snapshot()) {
			t.Fatalf("checkpoint resume at %d diverged", i)
		}
	}
}

func reverse(ops []Op) []Op {
	out := make([]Op, len(ops))
	for i, o := range ops {
		out[len(ops)-1-i] = o
	}
	return out
}

// The independent validator must accept replayed states and reject
// hand-corrupted ones.
func TestValidator(t *testing.T) {
	e := run([]Op{
		create("x", RootID, "x", 1),
		create("y", "x", "y", 2),
		move("m", 3, "y", RootID),
	})
	if err := Validate(e.Snapshot()); err != nil {
		t.Fatalf("valid state rejected: %v", err)
	}

	good := e.Snapshot()

	cycle := Snapshot{
		Names:   map[string]string{RootID: "root", "a": "a", "b": "b"},
		Parents: map[string]string{"a": "b", "b": "a"},
	}
	if err := Validate(cycle); err == nil {
		t.Fatal("cycle must be rejected")
	}

	orphan := good
	orphan.Parents["ghost-child"] = "ghost"
	orphan.Names["ghost-child"] = "gc"
	if err := Validate(orphan); err == nil {
		t.Fatal("missing parent must be rejected")
	}

	noRoot := Snapshot{Names: map[string]string{"a": "a"}, Parents: map[string]string{"a": RootID}}
	if err := Validate(noRoot); err == nil {
		t.Fatal("missing root must be rejected")
	}

	rootParent := e.Snapshot()
	rootParent.Parents[RootID] = "x"
	if err := Validate(rootParent); err == nil {
		t.Fatal("root with parent must be rejected")
	}

	badDelete := e.Snapshot()
	badDelete.Deleted["nope"] = true
	if err := Validate(badDelete); err == nil {
		t.Fatal("delete marker for unknown node must be rejected")
	}
}
