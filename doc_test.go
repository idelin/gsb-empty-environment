package undocrdt

import (
	"strings"
	"testing"
)

// broadcast delivers ops to every replica except the sender.
func broadcast(t *testing.T, from *Doc, to []*Doc, ops ...Op) {
	t.Helper()
	for _, doc := range to {
		if doc == from {
			continue
		}
		for _, op := range ops {
			doc.Apply(op)
		}
	}
}

func converge(t *testing.T, docs ...*Doc) string {
	t.Helper()
	want := docs[0].String()
	for _, d := range docs[1:] {
		if got := d.String(); got != want {
			t.Fatalf("replicas diverged: %q vs %q", want, got)
		}
	}
	return want
}

// TestAcceptance is the acceptance boundary from the spec:
// A types "hello", B inserts "!", A undoes "hello" (B's "!" survives),
// A redoes and the document is seamlessly back to "hello!".
func TestAcceptance(t *testing.T) {
	a := NewDoc("A")
	b := NewDoc("B")
	replicas := []*Doc{a, b}

	// User A types "hello".
	hello := a.LocalInsert(RootID, "hello")
	broadcast(t, a, replicas, hello)
	if got := converge(t, replicas...); got != "hello" {
		t.Fatalf("after insert: got %q, want %q", got, "hello")
	}

	// User B appends "!" after A's last character.
	bang := b.LocalInsert(b.LastVisible(), "!")
	broadcast(t, b, replicas, bang)
	if got := converge(t, replicas...); got != "hello!" {
		t.Fatalf("after B insert: got %q, want %q", got, "hello!")
	}

	// User A undoes "hello"; B's "!" must survive.
	undo, ok := a.Undo()
	if !ok {
		t.Fatal("undo: nothing to undo")
	}
	broadcast(t, a, replicas, undo)
	if got := converge(t, replicas...); got != "!" {
		t.Fatalf("after A undo: got %q, want %q", got, "!")
	}

	// User A redoes; the document is seamlessly restored.
	redo, ok := a.Redo()
	if !ok {
		t.Fatal("redo: nothing to redo")
	}
	broadcast(t, a, replicas, redo)
	if got := converge(t, replicas...); got != "hello!" {
		t.Fatalf("after A redo: got %q, want %q", got, "hello!")
	}
}

// TestUndoOnlyAffectsOwnOps interleaves edits from two users and checks
// that one user's undo never touches the other's characters.
func TestUndoOnlyAffectsOwnOps(t *testing.T) {
	a := NewDoc("A")
	b := NewDoc("B")
	replicas := []*Doc{a, b}

	op1 := a.LocalInsert(RootID, "aaa")
	broadcast(t, a, replicas, op1)
	op2 := b.LocalInsert(b.LastVisible(), "bbb")
	broadcast(t, b, replicas, op2)
	op3 := a.LocalInsert(a.LastVisible(), "ccc")
	broadcast(t, a, replicas, op3)

	// A undoes "ccc" then "aaa"; B's "bbb" must remain intact.
	undo1, _ := a.Undo()
	broadcast(t, a, replicas, undo1)
	if got := converge(t, replicas...); got != "aaabbb" {
		t.Fatalf("after undo ccc: got %q", got)
	}
	undo2, _ := a.Undo()
	broadcast(t, a, replicas, undo2)
	if got := converge(t, replicas...); got != "bbb" {
		t.Fatalf("after undo aaa: got %q, want only B's text", got)
	}

	// Redo both, in reverse order.
	redo1, _ := a.Redo()
	broadcast(t, a, replicas, redo1)
	redo2, _ := a.Redo()
	broadcast(t, a, replicas, redo2)
	if got := converge(t, replicas...); got != "aaabbbccc" {
		t.Fatalf("after redo all: got %q", got)
	}
}

// TestConcurrentInsertsConverge delivers the same ops in different orders
// to different replicas and requires identical results.
func TestConcurrentInsertsConverge(t *testing.T) {
	a := NewDoc("A")
	b := NewDoc("B")

	// Both insert at the root concurrently (neither has seen the other).
	opA := a.LocalInsert(RootID, "hello")
	opB := b.LocalInsert(RootID, "world")

	a.Apply(opB) // A: own op first
	b.Apply(opA) // B: remote op first (reverse order)

	if a.String() != b.String() {
		t.Fatalf("diverged: A=%q B=%q", a.String(), b.String())
	}
	if !strings.Contains(a.String(), "hello") || !strings.Contains(a.String(), "world") {
		t.Fatalf("lost characters: %q", a.String())
	}
}

// TestOutOfOrderDelivery buffers a compensation op that arrives before the
// insert it reverses.
func TestOutOfOrderDelivery(t *testing.T) {
	a := NewDoc("A")
	b := NewDoc("B")

	hello := a.LocalInsert(RootID, "hello")
	undo, _ := a.Undo()

	// B receives the undo compensation before the insert itself.
	b.Apply(undo)
	if got := b.String(); got != "" {
		t.Fatalf("buffered op leaked: %q", got)
	}
	b.Apply(hello)
	if got := b.String(); got != "" {
		t.Fatalf("after dependency arrived: got %q, want empty (undone)", got)
	}

	redo, _ := a.Redo()
	b.Apply(redo)
	if got := b.String(); got != "hello" {
		t.Fatalf("after redo: got %q", got)
	}
}

// TestUndoOfDelete verifies that deleting text is itself reversible.
func TestUndoOfDelete(t *testing.T) {
	a := NewDoc("A")
	b := NewDoc("B")
	replicas := []*Doc{a, b}

	op := a.LocalInsert(RootID, "hello")
	broadcast(t, a, replicas, op)

	// A deletes "ell" (chars 2..4 of its own insert).
	del := a.LocalDelete(op.CharIDs()[1:4])
	broadcast(t, a, replicas, del)
	if got := converge(t, replicas...); got != "ho" {
		t.Fatalf("after delete: got %q, want %q", got, "ho")
	}

	undo, _ := a.Undo()
	broadcast(t, a, replicas, undo)
	if got := converge(t, replicas...); got != "hello" {
		t.Fatalf("after undo delete: got %q", got)
	}

	redo, _ := a.Redo()
	broadcast(t, a, replicas, redo)
	if got := converge(t, replicas...); got != "ho" {
		t.Fatalf("after redo delete: got %q", got)
	}
}

// TestUndoTreeShape checks that undo/redo append compensation nodes with
// proper edges instead of mutating history.
func TestUndoTreeShape(t *testing.T) {
	a := NewDoc("A")

	hello := a.LocalInsert(RootID, "hello")
	undo, ok := a.Undo()
	if !ok {
		t.Fatal("nothing to undo")
	}
	redo, ok := a.Redo()
	if !ok {
		t.Fatal("nothing to redo")
	}

	if undo.Kind != Delete || undo.Undoes != hello.ID {
		t.Fatalf("undo comp = %+v, want Delete reversing %v", undo, hello.ID)
	}
	if redo.Kind != Restore || redo.Undoes != undo.ID {
		t.Fatalf("redo comp = %+v, want Restore reversing %v", redo, undo.ID)
	}
	if len(a.ops) != 3 {
		t.Fatalf("undo tree has %d nodes, want 3 (insert, undo, redo)", len(a.ops))
	}
	if len(a.edges) != 2 {
		t.Fatalf("undo tree has %d edges, want 2", len(a.edges))
	}
	if got := a.String(); got != "hello" {
		t.Fatalf("final doc: got %q", got)
	}
}

// TestNewEditClearsRedo follows the classic editor rule: a fresh edit
// invalidates the redo history.
func TestNewEditClearsRedo(t *testing.T) {
	a := NewDoc("A")
	a.LocalInsert(RootID, "hello")
	if _, ok := a.Undo(); !ok {
		t.Fatal("nothing to undo")
	}
	a.LocalInsert(a.LastVisible(), "xy")
	if _, ok := a.Redo(); ok {
		t.Fatal("redo should be empty after a fresh edit")
	}
	if got := a.String(); got != "xy" {
		t.Fatalf("got %q, want %q", got, "xy")
	}
}
