package seqcrdt

import (
	"reflect"
	"testing"
)

// The shared small op set used by the permutation test:
//
//	a1: A inserts 'H' after root
//	b1: B inserts 'i' after a1
//	a2: A deletes b1
//	b2: B inserts '!' after b1        (child of a future tombstone)
//	a3: A inserts '?' after a1        (concurrent with b1)
func testOps() []Op {
	return []Op{
		{ID: ID{"A", 1}, Kind: OpInsert, Parent: RootID, Value: 'H'},
		{ID: ID{"B", 1}, Kind: OpInsert, Parent: ID{"A", 1}, Value: 'i'},
		{ID: ID{"A", 2}, Kind: OpDelete, Target: ID{"B", 1}},
		{ID: ID{"B", 2}, Kind: OpInsert, Parent: ID{"B", 1}, Value: '!'},
		{ID: ID{"A", 3}, Kind: OpInsert, Parent: ID{"A", 1}, Value: '?'},
	}
}

const wantText = "H?!" // a1, a3('?'), b1(deleted anchor), b2('!')

func permutations(ops []Op) [][]Op {
	var out [][]Op
	var rec func(k int)
	rec = func(k int) {
		if k == len(ops) {
			cp := make([]Op, len(ops))
			copy(cp, ops)
			out = append(out, cp)
			return
		}
		for i := k; i < len(ops); i++ {
			ops[k], ops[i] = ops[i], ops[k]
			rec(k + 1)
			ops[k], ops[i] = ops[i], ops[k]
		}
	}
	rec(0)
	return out
}

// Every delivery permutation of the op set — including ones that violate
// causal generation order — must converge to the same visible text with
// an empty pending set.
func TestPermutationsConverge(t *testing.T) {
	perms := permutations(testOps())
	if len(perms) != 120 {
		t.Fatalf("expected 120 permutations, got %d", len(perms))
	}
	for _, perm := range perms {
		d := New("M")
		for _, op := range perm {
			if err := d.Apply(op); err != nil {
				t.Fatalf("apply %v: %v", op, err)
			}
		}
		if got := d.Text(); got != wantText {
			t.Fatalf("perm %v: text = %q, want %q", perm, got, wantText)
		}
		if n := len(d.Pending()); n != 0 {
			t.Fatalf("perm %v: %d ops still pending", perm, n)
		}
	}
}

// Duplicates on top of every permutation stay idempotent.
func TestDuplicateDeliveryIdempotent(t *testing.T) {
	d := New("M")
	ops := testOps()
	for round := 0; round < 3; round++ {
		for _, op := range ops {
			if err := d.Apply(op); err != nil {
				t.Fatalf("round %d apply %v: %v", round, op, err)
			}
		}
	}
	if got := d.Text(); got != wantText {
		t.Fatalf("text = %q, want %q", got, wantText)
	}
	if d.Len() != 4 {
		t.Fatalf("anchors = %d, want 4 (a1, b1 tombstone, b2, a3)", d.Len())
	}
}

func TestDeleteBeforeInsertIsPending(t *testing.T) {
	d := New("M")
	del := Op{ID: ID{"A", 2}, Kind: OpDelete, Target: ID{"B", 1}}
	if err := d.Apply(del); err != nil {
		t.Fatal(err)
	}
	if len(d.Pending()) != 1 {
		t.Fatalf("delete with missing target should be pending, got %v", d.Pending())
	}
	mustApply(t, d, Op{ID: ID{"A", 1}, Kind: OpInsert, Parent: RootID, Value: 'H'})
	mustApply(t, d, Op{ID: ID{"B", 1}, Kind: OpInsert, Parent: ID{"A", 1}, Value: 'i'})
	if got := d.Text(); got != "H" {
		t.Fatalf("text = %q, want %q (pending delete applied once target arrived)", got, "H")
	}
	if len(d.Pending()) != 0 {
		t.Fatalf("pending = %v, want empty", d.Pending())
	}
}

// Insert depending on a pending insert, plus a delete of the pending
// insert: the whole chain resolves once the root dependency arrives.
func TestChainedPendingResolution(t *testing.T) {
	d := New("M")
	mustApply(t, d, Op{ID: ID{"A", 2}, Kind: OpDelete, Target: ID{"B", 1}})
	mustApply(t, d, Op{ID: ID{"B", 2}, Kind: OpInsert, Parent: ID{"B", 1}, Value: '!'})
	mustApply(t, d, Op{ID: ID{"B", 1}, Kind: OpInsert, Parent: ID{"A", 1}, Value: 'i'})
	if len(d.Pending()) != 3 {
		t.Fatalf("pending = %v, want 3 buffered ops", d.Pending())
	}
	mustApply(t, d, Op{ID: ID{"A", 1}, Kind: OpInsert, Parent: RootID, Value: 'H'})
	if got := d.Text(); got != "H!" {
		t.Fatalf("text = %q, want %q", got, "H!")
	}
	if len(d.Pending()) != 0 {
		t.Fatalf("pending = %v, want empty", d.Pending())
	}
}

// Concurrent inserts after the same predecessor are ordered by op ID
// (newest first, replica ID tie-break) on every replica.
func TestConcurrentInsertsSameParent(t *testing.T) {
	setup := func() *Doc {
		d := New("M")
		mustApply(t, d, Op{ID: ID{"A", 1}, Kind: OpInsert, Parent: RootID, Value: 'H'})
		return d
	}
	b1 := Op{ID: ID{"B", 1}, Kind: OpInsert, Parent: ID{"A", 1}, Value: 'x'}
	c1 := Op{ID: ID{"C", 1}, Kind: OpInsert, Parent: ID{"A", 1}, Value: 'y'}

	d1, d2 := setup(), setup()
	mustApply(t, d1, b1)
	mustApply(t, d1, c1)
	mustApply(t, d2, c1)
	mustApply(t, d2, b1)

	if d1.Text() != d2.Text() {
		t.Fatalf("replicas diverged: %q vs %q", d1.Text(), d2.Text())
	}
	if got := d1.Text(); got != "Hyx" {
		t.Fatalf("text = %q, want %q (C:1 sorts before B:1)", got, "Hyx")
	}
}

// A deleted element stays as an anchor: inserts referencing it still
// land correctly and remain visible.
func TestAnchorDeletion(t *testing.T) {
	d := New("M")
	mustApply(t, d, Op{ID: ID{"A", 1}, Kind: OpInsert, Parent: RootID, Value: 'H'})
	mustApply(t, d, Op{ID: ID{"B", 1}, Kind: OpInsert, Parent: ID{"A", 1}, Value: 'i'})
	mustApply(t, d, Op{ID: ID{"A", 2}, Kind: OpDelete, Target: ID{"B", 1}})
	// Concurrent insert after the (now deleted) anchor.
	mustApply(t, d, Op{ID: ID{"C", 1}, Kind: OpInsert, Parent: ID{"B", 1}, Value: '!'})
	if got := d.Text(); got != "H!" {
		t.Fatalf("text = %q, want %q", got, "H!")
	}
	if d.Len() != 3 {
		t.Fatalf("anchors = %d, want 3 (tombstone retained)", d.Len())
	}
	if n := len(d.Visible()); n != 2 {
		t.Fatalf("visible = %d, want 2", n)
	}
}

func TestConflictSameIDDifferentContent(t *testing.T) {
	d := New("M")
	op := Op{ID: ID{"A", 1}, Kind: OpInsert, Parent: RootID, Value: 'H'}
	mustApply(t, d, op)
	evil := op
	evil.Value = 'X'
	if err := d.Apply(evil); err == nil {
		t.Fatal("expected conflict error for same ID with different content")
	}
	if got := d.Text(); got != "H" {
		t.Fatalf("text = %q, want %q (conflicting op rejected)", got, "H")
	}
}

// Visible positions are only used to generate ops; the ops themselves
// reference element IDs.
func TestVisiblePositionOps(t *testing.T) {
	d := New("A")
	for _, r := range "abc" {
		if _, err := d.InsertAt(len(d.Visible()), r); err != nil {
			t.Fatal(err)
		}
	}
	if got := d.Text(); got != "abc" {
		t.Fatalf("text = %q, want %q", got, "abc")
	}
	op, err := d.InsertAt(1, 'X')
	if err != nil {
		t.Fatal(err)
	}
	if op.Parent != (ID{"A", 1}) {
		t.Fatalf("insert parent = %v, want A:1", op.Parent)
	}
	if got := d.Text(); got != "aXbc" {
		t.Fatalf("text = %q, want %q", got, "aXbc")
	}
	del, err := d.DeleteAt(0)
	if err != nil {
		t.Fatal(err)
	}
	if del.Target != (ID{"A", 1}) {
		t.Fatalf("delete target = %v, want A:1", del.Target)
	}
	if got := d.Text(); got != "Xbc" {
		t.Fatalf("text = %q, want %q", got, "Xbc")
	}
	if _, err := d.DeleteAt(3); err == nil {
		t.Fatal("expected range error")
	}
	if _, err := d.InsertAt(5, 'z'); err == nil {
		t.Fatal("expected range error")
	}
}

// InsertAt must anchor correctly even when tombstones sit at the
// insertion point.
func TestInsertAtAcrossTombstones(t *testing.T) {
	d := New("A")
	mustApply(t, d, Op{ID: ID{"R", 1}, Kind: OpInsert, Parent: RootID, Value: 'a'})
	mustApply(t, d, Op{ID: ID{"R", 2}, Kind: OpInsert, Parent: ID{"R", 1}, Value: 'b'})
	mustApply(t, d, Op{ID: ID{"R", 3}, Kind: OpDelete, Target: ID{"R", 2}})
	if _, err := d.InsertAt(1, 'c'); err != nil { // visible: "a" -> insert at end
		t.Fatal(err)
	}
	if got := d.Text(); got != "ac" {
		t.Fatalf("text = %q, want %q", got, "ac")
	}
}

func TestCheckpointRestoreContinuesMerge(t *testing.T) {
	ops := testOps()

	// Reference replica: everything in order.
	ref := New("M")
	for _, op := range ops {
		mustApply(t, ref, op)
	}

	// Replica that checkpoints while ops are still pending.
	d := New("M")
	mustApply(t, d, ops[3]) // b2: parent b1 missing -> pending
	mustApply(t, d, ops[2]) // a2: target b1 missing -> pending
	cp := d.Snapshot()

	r := Restore(cp)
	if got := r.Pending(); !reflect.DeepEqual(got, d.Pending()) {
		t.Fatalf("restored pending = %v, want %v", got, d.Pending())
	}
	for _, op := range []Op{ops[0], ops[1], ops[4]} {
		mustApply(t, r, op)
	}
	// Re-deliver everything: duplicates must stay idempotent after restore.
	for _, op := range ops {
		mustApply(t, r, op)
	}
	if got := r.Text(); got != ref.Text() {
		t.Fatalf("restored text = %q, want %q", got, ref.Text())
	}
	if len(r.Pending()) != 0 {
		t.Fatalf("pending = %v, want empty", r.Pending())
	}
	if r.Len() != ref.Len() {
		t.Fatalf("anchors = %d, want %d", r.Len(), ref.Len())
	}
}

func TestCollectTombstones(t *testing.T) {
	d := New("M")
	for _, op := range testOps() {
		mustApply(t, d, op)
	}
	before := d.Text()
	// b1 is a tombstone with a live child b2; reclaiming it re-parents b2.
	n := d.CollectTombstones(func(ID) bool { return true })
	if n != 1 {
		t.Fatalf("collected = %d, want 1", n)
	}
	if got := d.Text(); got != before {
		t.Fatalf("text changed after GC: %q -> %q", before, got)
	}
	if d.Len() != 3 {
		t.Fatalf("anchors = %d, want 3", d.Len())
	}
}

// Tombstones are only reclaimed when the safe predicate (all replicas
// confirmed) covers both the insert and the delete; otherwise they stay.
func TestCollectTombstonesBlockedUnlessConfirmed(t *testing.T) {
	build := func() *Doc {
		d := New("M")
		for _, op := range testOps() {
			mustApply(t, d, op)
		}
		return d
	}
	// Delete of b1 (op A:2) not yet confirmed everywhere: keep the anchor.
	d := build()
	if n := d.CollectTombstones(func(id ID) bool { return id != (ID{"A", 2}) }); n != 0 {
		t.Fatalf("collected = %d, want 0 (delete not confirmed by all replicas)", n)
	}
	// Insert of b1 not confirmed: keep the anchor.
	d = build()
	if n := d.CollectTombstones(func(id ID) bool { return id != (ID{"B", 1}) }); n != 0 {
		t.Fatalf("collected = %d, want 0 (insert not confirmed by all replicas)", n)
	}
	// Fully confirmed: collect, and merging of unrelated ops continues.
	d = build()
	if n := d.CollectTombstones(func(ID) bool { return true }); n != 1 {
		t.Fatalf("collected = %d, want 1", n)
	}
	mustApply(t, d, Op{ID: ID{"C", 1}, Kind: OpInsert, Parent: ID{"A", 3}, Value: 'Z'})
	if got := d.Text(); got != "H?Z!" {
		t.Fatalf("text = %q, want %q", got, "H?Z!")
	}
}

func mustApply(t *testing.T, d *Doc, op Op) {
	t.Helper()
	if err := d.Apply(op); err != nil {
		t.Fatalf("apply %v: %v", op, err)
	}
}
