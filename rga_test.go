package rga

import (
	"fmt"
	"math/rand"
	"testing"
)

// newSyncedPair builds two replicas that both contain the document "AB"
// (typed by site 1 and replicated to site 2).
func newSyncedPair(t *testing.T) (r1, r2 *RGA, idA ID) {
	t.Helper()
	r1, r2 = New(1), New(2)
	if _, err := r1.InsertAfter(RootID, 'A'); err != nil {
		t.Fatal(err)
	}
	a, err := r1.IDAt(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r1.InsertAfter(a, 'B'); err != nil {
		t.Fatal(err)
	}
	for _, op := range r1.Ops() {
		if err := r2.Apply(op); err != nil {
			t.Fatal(err)
		}
	}
	if got := r2.String(); got != "AB" {
		t.Fatalf("replica 2 = %q, want %q", got, "AB")
	}
	return r1, r2, a
}

// Acceptance test: two users edit the document "AB". User 1 inserts 'X'
// after 'A' while, concurrently, user 2 inserts 'Y' after 'A'. After
// merging, both replicas must show the same stable text — either "AXYB" or
// "AYXB" — with no interleaving or reordering surprises.
func TestConcurrentInsertAtSamePosition(t *testing.T) {
	r1, r2, idA := newSyncedPair(t)

	// Concurrent edits: neither replica has seen the other's op yet.
	opX, err := r1.InsertAfter(idA, 'X') // user 1: X after A
	if err != nil {
		t.Fatal(err)
	}
	opY, err := r2.InsertAfter(idA, 'Y') // user 2: Y after A
	if err != nil {
		t.Fatal(err)
	}

	// Exchange the operations.
	if err := r2.Apply(opX); err != nil {
		t.Fatal(err)
	}
	if err := r1.Apply(opY); err != nil {
		t.Fatal(err)
	}

	got1, got2 := r1.String(), r2.String()
	if got1 != got2 {
		t.Fatalf("replicas diverged: user1 sees %q, user2 sees %q", got1, got2)
	}
	if got1 != "AXYB" && got1 != "AYXB" {
		t.Fatalf("merged text %q is neither %q nor %q (interleaving/reordering)", got1, "AXYB", "AYXB")
	}
	t.Logf("converged to %q on both replicas", got1)
}

// The sibling tie-break is deterministic: among concurrent inserts at the
// same parent, nodes are ordered by ID descending. Here X={Site:1,Seq:3}
// and Y={Site:2,Seq:3}; Y has the greater ID (site breaks the tie), so the
// result must be "AYXB" on every replica, regardless of delivery order.
func TestConcurrentInsertDeterministicOrder(t *testing.T) {
	for _, swapDelivery := range []bool{false, true} {
		t.Run(fmt.Sprintf("swapDelivery=%v", swapDelivery), func(t *testing.T) {
			r1, r2, idA := newSyncedPair(t)
			opX, _ := r1.InsertAfter(idA, 'X')
			opY, _ := r2.InsertAfter(idA, 'Y')

			ops := []Op{opX, opY}
			if swapDelivery {
				ops[0], ops[1] = ops[1], ops[0]
			}
			for _, op := range ops {
				if err := r1.Apply(op); err != nil {
					t.Fatal(err)
				}
				if err := r2.Apply(op); err != nil {
					t.Fatal(err)
				}
			}
			if got := r1.String(); got != "AYXB" {
				t.Fatalf("replica 1 = %q, want %q", got, "AYXB")
			}
			if got := r2.String(); got != "AYXB" {
				t.Fatalf("replica 2 = %q, want %q", got, "AYXB")
			}
		})
	}
}

// Merge must converge even when the op log arrives out of causal order.
func TestMergeUnordered(t *testing.T) {
	r1, r2, idA := newSyncedPair(t)
	if _, err := r1.InsertAfter(idA, 'X'); err != nil {
		t.Fatal(err)
	}
	if _, err := r2.InsertAfter(idA, 'Y'); err != nil {
		t.Fatal(err)
	}

	// Full-state merge in both directions.
	r1.Merge(r2)
	r2.Merge(r1)
	if r1.String() != r2.String() {
		t.Fatalf("diverged after merge: %q vs %q", r1.String(), r2.String())
	}

	// A fresh replica receiving the shuffled op logs must converge too.
	rng := rand.New(rand.NewSource(42))
	ops := r1.Ops()
	rng.Shuffle(len(ops), func(i, j int) { ops[i], ops[j] = ops[j], ops[i] })
	fresh := New(3)
	fresh.log = ops // pretend this replica saw the ops in shuffled order
	r4 := New(4)
	r4.Merge(fresh)
	if got := r4.String(); got != r1.String() {
		t.Fatalf("shuffled replay = %q, want %q", got, r1.String())
	}
}

// Apply is idempotent: re-delivering an op does not duplicate characters.
func TestApplyIdempotent(t *testing.T) {
	r1, r2, idA := newSyncedPair(t)
	opX, _ := r1.InsertAfter(idA, 'X')
	for i := 0; i < 3; i++ {
		if err := r2.Apply(opX); err != nil {
			t.Fatal(err)
		}
	}
	if got := r2.String(); got != "AXB" {
		t.Fatalf("replica 2 = %q, want %q", got, "AXB")
	}
}

// Apply rejects an op whose causal predecessor is missing.
func TestApplyMissingParent(t *testing.T) {
	r := New(1)
	err := r.Apply(Op{ID: ID{Site: 2, Seq: 1}, Parent: ID{Site: 9, Seq: 9}, Ch: 'Z'})
	if err == nil {
		t.Fatal("expected error for missing parent, got nil")
	}
}

// Randomized convergence: many sites insert at random positions
// concurrently; after exchanging every op, all replicas must agree.
func TestRandomizedConvergence(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	const sites = 5
	replicas := make([]*RGA, sites)
	for i := range replicas {
		replicas[i] = New(i + 1)
	}
	// Seed a shared initial document.
	seed := New(99)
	for _, ch := range "hello" {
		id, err := seed.IDAt(seed.Len() - 1)
		if err != nil {
			id = RootID
		}
		if _, err := seed.InsertAfter(id, ch); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range replicas {
		r.Merge(seed)
	}

	// Each site performs concurrent inserts at random visible positions.
	for round := 0; round < 20; round++ {
		for _, r := range replicas {
			var parent ID
			if r.Len() > 0 {
				id, err := r.IDAt(rng.Intn(r.Len()))
				if err != nil {
					t.Fatal(err)
				}
				parent = id
			}
			ch := rune('a' + rng.Intn(26))
			if _, err := r.InsertAfter(parent, ch); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Exchange everything (full merge both ways, repeatedly).
	for _, r := range replicas {
		for _, other := range replicas {
			r.Merge(other)
		}
	}
	want := replicas[0].String()
	for i, r := range replicas {
		if got := r.String(); got != want {
			t.Fatalf("replica %d diverged:\n got %q\nwant %q", i+1, got, want)
		}
	}
	t.Logf("all %d replicas converged to %q", sites, want)
}
