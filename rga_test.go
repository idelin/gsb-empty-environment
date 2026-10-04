package rga

import (
	"encoding/json"
	"errors"
	"testing"
)

func ins(rep string, n uint64, parent ID, v rune) Operation {
	return Operation{Kind: OpInsert, ID: ID{rep, n}, Parent: parent, Value: v}
}

func del(rep string, n uint64, target ID) Operation {
	return Operation{Kind: OpDelete, ID: ID{rep, n}, Target: target}
}

func mustApply(t *testing.T, r *RGA, ops ...Operation) {
	t.Helper()
	for _, op := range ops {
		if err := r.Apply(op); err != nil {
			t.Fatalf("Apply(%+v): %v", op, err)
		}
	}
}

// scenarioOps builds a small operation set with a non-trivial causal graph:
//
//	a1: ins 'a' after root        a2: ins 'b' after a1     a3: ins 'c' after a2
//	b1: ins 'x' after a1          b2: del a2               b3: ins 'q' after a1
//	a4: del a1 (anchor with children)
//
// Children of a1 sort by ID descending: b3(3) > a2(2) > b1(1).
// a1 and a2 are deleted, so the final visible text is "qcx".
func scenarioOps() (ops []Operation, deps map[int][]int) {
	a1 := ID{"a", 1}
	a2 := ID{"a", 2}
	ops = []Operation{
		ins("a", 1, rootID, 'a'), // 0: a1
		ins("a", 2, a1, 'b'),     // 1: a2
		ins("a", 3, a2, 'c'),     // 2: a3
		ins("b", 1, a1, 'x'),     // 3: b1
		del("b", 2, a2),          // 4: b2
		del("a", 4, a1),          // 5: a4
		ins("b", 3, a1, 'q'),     // 6: b3
	}
	deps = map[int][]int{
		1: {0}, // a2 needs a1
		2: {1}, // a3 needs a2
		3: {0}, // b1 needs a1
		4: {1}, // b2 needs a2
		5: {0}, // a4 needs a1
		6: {0}, // b3 needs a1
	}
	return ops, deps
}

// topoOrders enumerates every delivery permutation that respects the causal
// dependencies (each op appears only after all ops it references).
func topoOrders(n int, deps map[int][]int) [][]int {
	var orders [][]int
	done := make([]bool, n)
	var cur []int
	var rec func()
	rec = func() {
		if len(cur) == n {
			orders = append(orders, append([]int(nil), cur...))
			return
		}
		for i := 0; i < n; i++ {
			if done[i] {
				continue
			}
			ready := true
			for _, p := range deps[i] {
				if !done[p] {
					ready = false
					break
				}
			}
			if !ready {
				continue
			}
			done[i] = true
			cur = append(cur, i)
			rec()
			cur = cur[:len(cur)-1]
			done[i] = false
		}
	}
	rec()
	return orders
}

// TestCausalPermutationsConverge delivers the scenario ops in every
// causally valid order and requires identical final text and an empty
// pending set for every permutation.
func TestCausalPermutationsConverge(t *testing.T) {
	ops, deps := scenarioOps()
	orders := topoOrders(len(ops), deps)
	if len(orders) < 2 {
		t.Fatalf("expected multiple causal permutations, got %d", len(orders))
	}
	t.Logf("checking %d causal permutations", len(orders))
	for _, perm := range orders {
		r := New("merge")
		for _, i := range perm {
			if err := r.Apply(ops[i]); err != nil {
				t.Fatalf("perm %v: Apply(%+v): %v", perm, ops[i], err)
			}
		}
		if got := r.String(); got != "qcx" {
			t.Errorf("perm %v: final text = %q, want %q", perm, got, "qcx")
		}
		if p := r.Pending(); len(p) != 0 {
			t.Errorf("perm %v: pending = %v, want empty", perm, p)
		}
	}
}

// TestDeleteBeforeInsertArrival buffers a delete whose target has not
// arrived yet, then applies it once the insert shows up.
func TestDeleteBeforeInsertArrival(t *testing.T) {
	r := New("b")
	target := ID{"a", 1}
	mustApply(t, r, del("a", 2, target))
	if got := len(r.Pending()); got != 1 {
		t.Fatalf("pending = %d, want 1 (delete waiting for its target)", got)
	}
	mustApply(t, r, ins("a", 1, rootID, 'x'))
	if got := len(r.Pending()); got != 0 {
		t.Fatalf("pending = %d, want 0 after target arrived", got)
	}
	if got := r.String(); got != "" {
		t.Fatalf("text = %q, want empty (insert arrived already deleted)", got)
	}
}

// TestConcurrentInsertsSameParent checks that two replicas inserting after
// the same predecessor converge to the ID-sorted order in both delivery
// orders, and that each insert's subtree stays contiguous.
func TestConcurrentInsertsSameParent(t *testing.T) {
	a1 := ins("a", 1, rootID, 'a')
	x := ins("b", 1, ID{"a", 1}, 'x') // concurrent with a2
	b2 := ins("a", 2, ID{"a", 1}, 'b')
	c1 := ins("c", 1, ID{"b", 1}, 'y') // child of x: subtree of x

	r1 := New("r1")
	mustApply(t, r1, a1, x, b2, c1)
	r2 := New("r2")
	mustApply(t, r2, c1, b2, x, a1) // reverse delivery order

	// Children of a1 sorted by ID desc: a:2 ('b') then b:1 ('x' + subtree).
	want := "abxy"
	if got := r1.String(); got != want {
		t.Errorf("r1 text = %q, want %q", got, want)
	}
	if got := r2.String(); got != want {
		t.Errorf("r2 text = %q, want %q", got, want)
	}
}

// TestAnchorDeletion deletes an element that has children: the tombstone
// stays as anchor, the children remain visible in place, and later inserts
// can still reference the tombstone as predecessor.
func TestAnchorDeletion(t *testing.T) {
	a1, a2 := ID{"a", 1}, ID{"a", 2}
	r := New("b")
	mustApply(t, r,
		ins("a", 1, rootID, 'a'),
		ins("a", 2, a1, 'b'),
		ins("a", 3, a2, 'c'),
		del("a", 4, a1), // delete the anchor of the whole chain
	)
	if got := r.String(); got != "bc" {
		t.Fatalf("after anchor delete: text = %q, want %q", got, "bc")
	}
	// The tombstone is still a valid predecessor for new inserts. The new
	// element sorts among the tombstone's children by ID: (b,1) after (a,2).
	mustApply(t, r, ins("b", 1, a1, 'x'))
	if got := r.String(); got != "bcx" {
		t.Fatalf("insert after tombstone: text = %q, want %q", got, "bcx")
	}
}

// TestIdempotentDuplicates delivers operations twice, including while they
// are pending.
func TestIdempotentDuplicates(t *testing.T) {
	r := New("b")
	d := del("a", 2, ID{"a", 1})
	mustApply(t, r, d, d) // duplicate while pending
	if got := len(r.Pending()); got != 1 {
		t.Fatalf("pending = %d, want 1 (duplicate dropped)", got)
	}
	i := ins("a", 1, rootID, 'x')
	mustApply(t, r, i, i, d) // duplicates after application
	if got := r.String(); got != "" {
		t.Fatalf("text = %q, want empty", got)
	}
	if got := len(r.Pending()); got != 0 {
		t.Fatalf("pending = %d, want 0", got)
	}
}

// TestConflictSameIDDifferentContent rejects operations reusing an ID with
// different content, both for applied and for pending operations.
func TestConflictSameIDDifferentContent(t *testing.T) {
	r := New("b")
	mustApply(t, r, ins("a", 1, rootID, 'x'))
	err := r.Apply(ins("a", 1, rootID, 'y'))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("insert same ID different value: err = %v, want ErrConflict", err)
	}
	err = r.Apply(ins("a", 1, ID{"a", 1}, 'x'))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("insert same ID different parent: err = %v, want ErrConflict", err)
	}
	mustApply(t, r, del("a", 2, ID{"a", 1}))
	err = r.Apply(del("a", 2, ID{"a", 9}))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("delete same ID different target: err = %v, want ErrConflict", err)
	}
	// Conflict against a pending op.
	mustApply(t, r, ins("c", 1, ID{"z", 9}, 'p')) // parent missing -> pending
	err = r.Apply(ins("c", 1, ID{"z", 9}, 'q'))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("pending insert same ID different value: err = %v, want ErrConflict", err)
	}
}

// TestVisiblePositionOps generates operations from visible positions and
// verifies the generated ops reference element IDs, not indices.
func TestVisiblePositionOps(t *testing.T) {
	r := New("a")
	if _, err := r.InsertAt(0, 'a'); err != nil {
		t.Fatal(err)
	}
	if _, err := r.InsertAt(1, 'b'); err != nil {
		t.Fatal(err)
	}
	if _, err := r.InsertAt(2, 'c'); err != nil {
		t.Fatal(err)
	}
	if got := r.String(); got != "abc" {
		t.Fatalf("text = %q, want %q", got, "abc")
	}
	d, err := r.DeleteAt(1)
	if err != nil {
		t.Fatal(err)
	}
	if d.Target != (ID{"a", 2}) {
		t.Fatalf("delete target = %v, want permanent ID a:2", d.Target)
	}
	i, err := r.InsertAt(1, 'z')
	if err != nil {
		t.Fatal(err)
	}
	if i.Parent != (ID{"a", 1}) {
		t.Fatalf("insert parent = %v, want permanent ID a:1", i.Parent)
	}
	if got := r.String(); got != "azc" {
		t.Fatalf("text = %q, want %q", got, "azc")
	}
	if _, err := r.InsertAt(5, '!'); err == nil {
		t.Fatal("InsertAt out of range: want error")
	}
	if _, err := r.DeleteAt(3); err == nil {
		t.Fatal("DeleteAt out of range: want error")
	}
}

// TestCheckpointRestore checkpoints mid-merge (with a pending delete and a
// tombstone anchor), restores through JSON, and verifies merging continues
// to the same result as the uninterrupted replica.
func TestCheckpointRestore(t *testing.T) {
	ops, _ := scenarioOps()
	first, second := ops[:4], ops[4:]

	direct := New("m1")
	mustApply(t, direct, first...)

	// Rearrange so a delete arrives before its target: pending at checkpoint.
	early := []Operation{ops[4], ops[0], ops[3]}           // b2(del a2), a1, b1
	delayed := []Operation{ops[1], ops[2], ops[5], ops[6]} // a2, a3, a4, b3
	r := New("m2")
	mustApply(t, r, early...)
	if got := len(r.Pending()); got != 1 {
		t.Fatalf("pending before checkpoint = %d, want 1", got)
	}

	buf, err := json.Marshal(r.Checkpoint())
	if err != nil {
		t.Fatalf("marshal checkpoint: %v", err)
	}
	var cp Checkpoint
	if err := json.Unmarshal(buf, &cp); err != nil {
		t.Fatalf("unmarshal checkpoint: %v", err)
	}
	restored, err := Restore(cp)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}

	mustApply(t, direct, second...)
	mustApply(t, restored, delayed...)

	if got, want := restored.String(), direct.String(); got != want {
		t.Errorf("restored text = %q, want %q", got, want)
	}
	if got := restored.String(); got != "qcx" {
		t.Errorf("restored text = %q, want %q", got, "qcx")
	}
	if p := restored.Pending(); len(p) != 0 {
		t.Errorf("restored pending = %v, want empty", p)
	}
	// The restored replica keeps generating non-colliding IDs.
	if _, err := restored.InsertAt(0, '!'); err != nil {
		t.Fatal(err)
	}
	if got := restored.String(); got != "!qcx" {
		t.Errorf("after local insert: text = %q, want %q", got, "!qcx")
	}
}
