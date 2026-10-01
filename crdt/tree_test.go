package crdt

import "testing"

// buildHello returns a replica containing "hello" and the ID of 'o'.
func buildHello(t *testing.T, site string) (*Tree, ID) {
	t.Helper()
	tr := New(site)
	parent := RootID()
	for _, r := range "hello" {
		n, err := tr.Insert(parent, r)
		if err != nil {
			t.Fatalf("insert %q: %v", r, err)
		}
		parent = n.ID
	}
	if got := tr.String(); got != "hello" {
		t.Fatalf("setup: got %q, want %q", got, "hello")
	}
	return tr, parent
}

// Acceptance: two clients concurrently type '!' and '?' after "hello".
// Both characters share the same parent ('o'); the causal tree must
// deterministically order them as "hello!?" on both replicas.
func TestConcurrentInsertConvergence(t *testing.T) {
	alice, oID := buildHello(t, "alice")
	bob := New("bob")
	bob.Merge(alice) // bob starts from the same "hello"

	// Concurrent inserts: neither replica has seen the other's edit.
	bang, err := alice.Insert(oID, '!')
	if err != nil {
		t.Fatal(err)
	}
	quest, err := bob.Insert(oID, '?')
	if err != nil {
		t.Fatal(err)
	}

	// Both nodes must point at the same parent character.
	if bang.Parent != quest.Parent {
		t.Fatalf("parents differ: %v vs %v", bang.Parent, quest.Parent)
	}

	// Sync in both directions.
	alice.Merge(bob)
	bob.Merge(alice)

	const want = "hello!?"
	if got := alice.String(); got != want {
		t.Errorf("alice: got %q, want %q", got, want)
	}
	if got := bob.String(); got != want {
		t.Errorf("bob: got %q, want %q", got, want)
	}
	if alice.String() != bob.String() {
		t.Errorf("replicas diverged: %q vs %q", alice.String(), bob.String())
	}
}

// The converged result must not depend on the order in which the
// concurrent operations are exchanged.
func TestMergeOrderIndependence(t *testing.T) {
	alice, oID := buildHello(t, "alice")
	bob := New("bob")
	bob.Merge(alice)

	bang, _ := alice.Insert(oID, '!')
	quest, _ := bob.Insert(oID, '?')

	// Path 1: alice learns of '?' first, bob learns of '!' first.
	a1, b1 := New("a1"), New("b1")
	a1.Merge(alice)
	b1.Merge(bob)
	a1.Apply(quest)
	b1.Apply(bang)

	// Path 2: the exact opposite delivery order.
	a2, b2 := New("a2"), New("b2")
	a2.Merge(alice)
	b2.Merge(bob)
	b2.Apply(bang) // bob-side replica gets '!' ... then '?'
	b2.Apply(quest)
	a2.Apply(quest)
	a2.Apply(bang)

	const want = "hello!?"
	for name, tr := range map[string]*Tree{"a1": a1, "b1": b1, "a2": a2, "b2": b2} {
		if got := tr.String(); got != want {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
}

// Nodes arriving before their parent must be buffered and still converge.
func TestOutOfOrderDelivery(t *testing.T) {
	alice, oID := buildHello(t, "alice")
	bang, _ := alice.Insert(oID, '!')
	world, _ := alice.Insert(bang.ID, 'w') // "hello!w" chain

	bob := New("bob")
	bob.Apply(world) // 'w' arrives before its parent '!'
	if got := bob.String(); got != "" {
		t.Fatalf("premature render: %q", got)
	}
	bob.Merge(alice) // rest of the history arrives
	if got, want := bob.String(), "hello!w"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Merging must be idempotent: re-applying known nodes changes nothing.
func TestIdempotentMerge(t *testing.T) {
	alice, _ := buildHello(t, "alice")
	bob := New("bob")
	bob.Merge(alice)
	bob.Merge(alice)
	bob.Merge(alice)
	if got := bob.String(); got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
	if bob.Len() != 5 {
		t.Errorf("len = %d, want 5", bob.Len())
	}
}

// Lamport clocks must stay unique per (clock, site) pair and the local
// clock must catch up to the highest remote clock seen.
func TestLamportClock(t *testing.T) {
	alice, _ := buildHello(t, "alice") // clocks 1..5
	bob := New("bob")
	bob.Merge(alice)
	n, err := bob.Insert(RootID(), 'X')
	if err != nil {
		t.Fatal(err)
	}
	if n.ID.Clock != 6 {
		t.Errorf("bob clock = %d, want 6 (caught up via merge)", n.ID.Clock)
	}
}
