package rga

import (
	"math/rand"
	"strings"
	"testing"
)

// broadcast delivers ops to every replica except the sender.
func broadcast(replicas []*RGA, sender int, ops ...Op) {
	for i, r := range replicas {
		if i != sender {
			for _, op := range ops {
				r.Apply(op)
			}
		}
	}
}

// Acceptance scenario: two users co-edit "AB"; user 1 inserts 'X' after 'A'
// while user 2 concurrently inserts 'Y' after 'A'. After merging, both
// replicas must show the same stable document: "AXYB" or "AYXB".
func TestConcurrentInsertAtSamePosition(t *testing.T) {
	u1, u2 := New("user1"), New("user2")

	// Shared initial document "AB": user 1 types it, user 2 receives it.
	opA := u1.InsertAfter(ID{}, 'A')
	opB := u1.InsertAfter(opA.ID, 'B')
	u2.Apply(opA)
	u2.Apply(opB)
	if u1.String() != "AB" || u2.String() != "AB" {
		t.Fatalf("setup: got %q and %q, want \"AB\"", u1.String(), u2.String())
	}

	// Concurrent inserts after 'A' (no communication in between).
	opX := u1.InsertAfter(opA.ID, 'X')
	opY := u2.InsertAfter(opA.ID, 'Y')

	// Merge in both directions.
	u2.Apply(opX)
	u1.Apply(opY)

	s1, s2 := u1.String(), u2.String()
	if s1 != s2 {
		t.Fatalf("replicas diverged: %q vs %q", s1, s2)
	}
	if s1 != "AXYB" && s1 != "AYXB" {
		t.Fatalf("unexpected merge result %q, want AXYB or AYXB", s1)
	}
	t.Logf("merged document: %s", s1)
}

// Concurrent inserts at the same position must resolve by descending ID on
// every replica, regardless of the order in which operations arrive.
func TestConcurrentInsertDeterministicOrder(t *testing.T) {
	const sites = 5
	replicas := make([]*RGA, sites)
	names := []string{"a", "b", "c", "d", "e"}
	for i, n := range names {
		replicas[i] = New(n)
	}

	// Everyone shares "AB".
	opA := replicas[0].InsertAfter(ID{}, 'A')
	opB := replicas[0].InsertAfter(opA.ID, 'B')
	for _, r := range replicas[1:] {
		r.Apply(opA)
		r.Apply(opB)
	}

	// Each site concurrently inserts its own character after 'A'.
	ops := make([]Op, sites)
	for i, r := range replicas {
		ops[i] = r.InsertAfter(opA.ID, rune('0'+i))
	}

	// Deliver the ops to every replica in a different random order.
	for i, r := range replicas {
		perm := rand.Perm(sites)
		for _, j := range perm {
			r.Apply(ops[j])
		}
		_ = i
	}

	want := replicas[0].String()
	for i, r := range replicas {
		if got := r.String(); got != want {
			t.Fatalf("replica %d diverged: %q vs %q", i, got, want)
		}
	}
	// 'A' followed by all five characters in descending ID order, then 'B'.
	if len(want) != 7 || want[0] != 'A' || want[6] != 'B' {
		t.Fatalf("malformed result %q", want)
	}
	t.Logf("deterministic order: %s", want)
}

// Runs of characters typed sequentially by different users at the same
// position must never interleave after merging.
func TestNoInterleavingOfConcurrentRuns(t *testing.T) {
	u1, u2 := New("u1"), New("u2")

	opA := u1.InsertAfter(ID{}, 'A')
	opB := u1.InsertAfter(opA.ID, 'B')
	u2.Apply(opA)
	u2.Apply(opB)

	// User 1 types "XYZ" after 'A'; user 2 concurrently types "123" after 'A'.
	var ops1, ops2 []Op
	prev1, prev2 := opA.ID, opA.ID
	for _, c := range "XYZ" {
		op := u1.InsertAfter(prev1, c)
		prev1 = op.ID
		ops1 = append(ops1, op)
	}
	for _, c := range "123" {
		op := u2.InsertAfter(prev2, c)
		prev2 = op.ID
		ops2 = append(ops2, op)
	}

	// Merge with adversarial interleaved delivery.
	for i := 0; i < 3; i++ {
		u1.Apply(ops2[i])
		u2.Apply(ops1[i])
	}

	s1, s2 := u1.String(), u2.String()
	if s1 != s2 {
		t.Fatalf("replicas diverged: %q vs %q", s1, s2)
	}
	if !strings.Contains(s1, "XYZ") || !strings.Contains(s1, "123") {
		t.Fatalf("interleaved runs in %q: want contiguous \"XYZ\" and \"123\"", s1)
	}
	if s1 != "AXYZ123B" && s1 != "A123XYZB" {
		t.Fatalf("unexpected result %q", s1)
	}
	t.Logf("merged runs: %s", s1)
}

// Deletes commute with concurrent inserts and leave tombstones behind.
func TestDelete(t *testing.T) {
	u1, u2 := New("u1"), New("u2")

	opA := u1.InsertAfter(ID{}, 'A')
	opB := u1.InsertAfter(opA.ID, 'B')
	opC := u1.InsertAfter(opB.ID, 'C')
	u2.Apply(opA)
	u2.Apply(opB)
	u2.Apply(opC)

	// u1 deletes 'B' while u2 concurrently inserts 'X' after 'B'.
	del := u1.Delete(opB.ID)
	ins := u2.InsertAfter(opB.ID, 'X')

	u1.Apply(ins)
	u2.Apply(del)

	if got := u1.String(); got != "AXC" {
		t.Fatalf("u1 = %q, want \"AXC\"", got)
	}
	if got := u2.String(); got != "AXC" {
		t.Fatalf("u2 = %q, want \"AXC\"", got)
	}
}

// Operations may arrive before their causal predecessors; the replica
// buffers them and converges once the dependencies land.
func TestOutOfOrderDelivery(t *testing.T) {
	u1, u2 := New("u1"), New("u2")

	op1 := u1.InsertAfter(ID{}, 'h')
	op2 := u1.InsertAfter(op1.ID, 'e')
	op3 := u1.InsertAfter(op2.ID, 'l')
	op4 := u1.InsertAfter(op3.ID, 'l')
	op5 := u1.InsertAfter(op4.ID, 'o')
	del := u1.Delete(op1.ID)

	// Deliver in reverse order, delete first.
	for _, op := range []Op{del, op5, op4, op3, op2, op1} {
		u2.Apply(op)
	}

	if got := u2.String(); got != "ello" {
		t.Fatalf("u2 = %q, want \"ello\"", got)
	}
}

// Re-delivering the same operation must be a no-op.
func TestIdempotentDelivery(t *testing.T) {
	u1, u2 := New("u1"), New("u2")
	op := u1.InsertAfter(ID{}, 'A')
	for i := 0; i < 3; i++ {
		u2.Apply(op)
	}
	if got := u2.String(); got != "A" {
		t.Fatalf("u2 = %q, want \"A\"", got)
	}
}

// Sequential editing through the index-based API.
func TestInsertAtAndLen(t *testing.T) {
	r := New("solo")
	r.InsertAt(0, 'B')
	r.InsertAt(0, 'A')
	r.InsertAt(2, 'C')
	if got := r.String(); got != "ABC" {
		t.Fatalf("got %q, want \"ABC\"", got)
	}
	if r.Len() != 3 {
		t.Fatalf("Len = %d, want 3", r.Len())
	}
}
