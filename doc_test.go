package undotree

import "testing"

// Acceptance boundary:
// A types "hello", B inserts "!", A undoes (retracting only "hello"),
// the document keeps B's "!", and A's redo restores "hello!" seamlessly.
func TestAcceptance_UndoKeepsCollaboratorInput(t *testing.T) {
	a := NewDoc("A")
	b := NewDoc("B")

	hello := a.Insert(CharID{}, "hello")
	b.Apply(hello)
	if got := a.Text(); got != "hello" {
		t.Fatalf("after A insert: A sees %q, want %q", got, "hello")
	}

	bang := b.Insert(LastChar(hello), "!")
	a.Apply(bang)
	for name, d := range map[string]*Doc{"A": a, "B": b} {
		if got := d.Text(); got != "hello!" {
			t.Fatalf("after B insert: %s sees %q, want %q", name, got, "hello!")
		}
	}

	undo, ok := a.Undo()
	if !ok {
		t.Fatal("A.Undo returned ok=false, want true")
	}
	b.Apply(undo)
	for name, d := range map[string]*Doc{"A": a, "B": b} {
		if got := d.Text(); got != "!" {
			t.Fatalf("after A undo: %s sees %q, want %q (B's input must survive)", name, got, "!")
		}
	}

	redo, ok := a.Redo()
	if !ok {
		t.Fatal("A.Redo returned ok=false, want true")
	}
	b.Apply(redo)
	for name, d := range map[string]*Doc{"A": a, "B": b} {
		if got := d.Text(); got != "hello!" {
			t.Fatalf("after A redo: %s sees %q, want %q", name, got, "hello!")
		}
	}
}

// Undo only retracts the author's own operations, never a collaborator's.
func TestUndoIsPerUser(t *testing.T) {
	a := NewDoc("A")
	b := NewDoc("B")

	opA := a.Insert(CharID{}, "aaa")
	b.Apply(opA)
	opB := b.Insert(LastChar(opA), "bbb")
	a.Apply(opB)

	if _, ok := b.Undo(); !ok {
		t.Fatal("B.Undo failed")
	}
	if got := a.Ops(); len(got) != 2 {
		t.Fatalf("A has %d ops, want 2 (undo not yet propagated)", len(got))
	}
	a.Merge(b)
	if got := a.Text(); got != "aaa" {
		t.Fatalf("after B undo: A sees %q, want %q", got, "aaa")
	}
	if _, ok := a.Undo(); !ok {
		t.Fatal("A.Undo failed")
	}
	b.Merge(a)
	if got := b.Text(); got != "" {
		t.Fatalf("after A undo: B sees %q, want empty", got)
	}
}

// Concurrent inserts at the same position converge to the same text on every
// replica regardless of merge order.
func TestConcurrentInsertsConverge(t *testing.T) {
	a := NewDoc("A")
	b := NewDoc("B")

	opA := a.Insert(CharID{}, "AAA")
	opB := b.Insert(CharID{}, "BBB")

	a.Apply(opB)
	b.Apply(opA)

	if a.Text() != b.Text() {
		t.Fatalf("diverged: A=%q B=%q", a.Text(), b.Text())
	}
	if got := a.Text(); got != "BBBAAA" && got != "AAABBB" {
		t.Fatalf("unexpected interleaving %q", got)
	}

	// Merge is idempotent and commutative.
	c := NewDoc("C")
	c.Merge(b)
	c.Merge(a)
	c.Merge(a)
	if c.Text() != a.Text() {
		t.Fatalf("C=%q, want %q", c.Text(), a.Text())
	}
}

// Undo then redo round-trips; undoing twice walks back the author's own
// operation stack.
func TestUndoRedoStack(t *testing.T) {
	a := NewDoc("A")

	first := a.Insert(CharID{}, "one ")
	a.Insert(LastChar(first), "two")

	if _, ok := a.Undo(); !ok {
		t.Fatal("undo #1 failed")
	}
	if got := a.Text(); got != "one " {
		t.Fatalf("after undo #1: %q, want %q", got, "one ")
	}
	if _, ok := a.Undo(); !ok {
		t.Fatal("undo #2 failed")
	}
	if got := a.Text(); got != "" {
		t.Fatalf("after undo #2: %q, want empty", got)
	}
	if _, ok := a.Undo(); ok {
		t.Fatal("undo #3 should report nothing to undo")
	}
	if _, ok := a.Redo(); !ok {
		t.Fatal("redo #1 failed")
	}
	if _, ok := a.Redo(); !ok {
		t.Fatal("redo #2 failed")
	}
	if got := a.Text(); got != "one two" {
		t.Fatalf("after redo #2: %q, want %q", got, "one two")
	}
	if _, ok := a.Redo(); ok {
		t.Fatal("redo #3 should report nothing to redo")
	}
}

// The graph keeps every node: undo/redo append compensation nodes instead of
// deleting history.
func TestGraphIsAppendOnly(t *testing.T) {
	a := NewDoc("A")
	a.Insert(CharID{}, "x")
	a.Undo()
	a.Redo()

	ops := a.Ops()
	if len(ops) != 3 {
		t.Fatalf("graph has %d nodes, want 3", len(ops))
	}
	want := []OpKind{OpInsert, OpUndo, OpRedo}
	for i, op := range ops {
		if op.Kind != want[i] {
			t.Fatalf("node %d is %s, want %s", i, op.Kind, want[i])
		}
		if i > 0 && ops[i].Target != ops[0].ID {
			t.Fatalf("node %d targets %+v, want %+v", i, ops[i].Target, ops[0].ID)
		}
	}
}
