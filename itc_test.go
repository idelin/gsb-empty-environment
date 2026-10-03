package itc

import (
	"math/rand"
	"testing"
)

// The initial node must own the whole interval [0, 1].
func TestSeedHoldsFullInterval(t *testing.T) {
	s := Seed()
	if got := s.ID().String(); got != "1" {
		t.Fatalf("seed id = %s, want 1 (full interval)", got)
	}
	if got := s.EventTree().String(); got != "0" {
		t.Fatalf("seed event = %s, want 0", got)
	}
}

// Fork must split the interval into two disjoint halves that sum back to
// the original, and both children must inherit the causal history.
func TestForkSplitsInterval(t *testing.T) {
	root := Seed().Event() // {1; 1}
	a, b := root.Fork()
	if got := a.ID().String(); got != "(1, 0)" {
		t.Fatalf("left fork id = %s, want (1, 0)", got)
	}
	if got := b.ID().String(); got != "(0, 1)" {
		t.Fatalf("right fork id = %s, want (0, 1)", got)
	}
	if !a.Equal(root) || !b.Equal(root) {
		t.Fatalf("fork children must inherit causal history: a=%s b=%s root=%s", a, b, root)
	}
	// Joining the untouched halves restores the full interval.
	j := Join(a, b)
	if got := j.ID().String(); got != "1" {
		t.Fatalf("join of halves id = %s, want 1", got)
	}
}

// Acceptance scenario: one node forks into two, both evolve concurrently
// in different places, then Join must preserve the causal history of both.
func TestForkConcurrentJoin(t *testing.T) {
	root := Seed().Event() // initial node, one event: {1; 1}
	a, b := root.Fork()    // split [0,1] into two halves

	// Concurrent, independent updates on both sides.
	a1 := a.Event()
	b1 := b.Event()
	b2 := b1.Event()

	if !a1.Concurrent(b2) {
		t.Fatalf("a1=%s and b2=%s must be concurrent", a1, b2)
	}
	if !root.Leq(a1) || !root.Leq(b2) {
		t.Fatalf("both forks must descend from root: root=%s a1=%s b2=%s", root, a1, b2)
	}

	j := Join(a1, b2)
	if got := j.ID().String(); got != "1" {
		t.Fatalf("joined id = %s, want 1 (full interval restored)", got)
	}
	if !a1.Leq(j) || !b2.Leq(j) {
		t.Fatalf("join must dominate both inputs: a1=%s b2=%s j=%s", a1, b2, j)
	}
	if j.Leq(a1) || j.Leq(b2) {
		t.Fatalf("join must be strictly after both inputs: j=%s", j)
	}

	// An event recorded after the join causally follows everything before.
	j1 := j.Event()
	if !a1.Leq(j1) || !b2.Leq(j1) || !j.Leq(j1) {
		t.Fatalf("event after join must dominate all prior history: j1=%s", j1)
	}
	if j1.Concurrent(a1) || j1.Concurrent(b2) {
		t.Fatalf("j1=%s must not be concurrent with its ancestors", j1)
	}
}

// Exact event-tree arithmetic for the acceptance scenario.
func TestJoinPreservesBothHistories(t *testing.T) {
	a, b := Seed().Fork()
	a = a.Event()         // {(1, 0); (0, 1, 0)}
	b = b.Event().Event() // {(0, 1); (0, 0, 2)}
	j := Join(a, b)
	if got, want := j.EventTree().String(), "(0, 1, 2)"; got != want {
		t.Fatalf("joined event = %s, want %s", got, want)
	}
	// One more event on the left before joining must not be lost.
	c := a.Event() // {(1, 0); (0, 2, 0)}
	j2 := Join(c, b)
	// (0, 2, 2) normalizes to the leaf 2: every counter equals 2.
	if got, want := j2.EventTree().String(), "2"; got != want {
		t.Fatalf("joined event = %s, want %s", got, want)
	}
	if !a.Leq(j2) || !c.Leq(j2) || !b.Leq(j2) {
		t.Fatalf("join lost causal history: a=%s c=%s b=%s j2=%s", a, c, b, j2)
	}
}

// Nodes may join at any time by forking any existing node, and leave by
// simply dropping their stamp; rejoining later still preserves causality.
func TestDynamicJoinAndLeave(t *testing.T) {
	root := Seed()
	a, b := root.Fork() // two nodes

	// A third node joins at runtime: a forks and hands over one half.
	a1, c := a.Fork()
	if got, want := c.ID().String(), "((0, 1), 0)"; got != want {
		t.Fatalf("newcomer id = %s, want %s", got, want)
	}

	// All three evolve independently.
	a1 = a1.Event()
	b = b.Event()
	c = c.Event().Event()

	if !a1.Concurrent(b) || !a1.Concurrent(c) || !b.Concurrent(c) {
		t.Fatalf("independent nodes must be concurrent: a1=%s b=%s c=%s", a1, b, c)
	}

	// c leaves: nothing to do, its stamp is simply dropped.
	// Later c comes back and merges with a1.
	m := Join(a1, c)
	if got, want := m.ID().String(), "(1, 0)"; got != want {
		t.Fatalf("rejoined id = %s, want %s (a's original half)", got, want)
	}
	if !a1.Leq(m) || !c.Leq(m) {
		t.Fatalf("rejoin must preserve both histories: a1=%s c=%s m=%s", a1, c, m)
	}
	// b never saw any of this: still concurrent.
	if !m.Concurrent(b) {
		t.Fatalf("m=%s and b=%s must be concurrent", m, b)
	}
	// Full merge of all three restores the whole interval.
	full := Join(m, b)
	if got := full.ID().String(); got != "1" {
		t.Fatalf("full join id = %s, want 1", got)
	}
	if !a1.Leq(full) || !b.Leq(full) || !c.Leq(full) {
		t.Fatalf("full join must dominate every node: full=%s", full)
	}
}

// Events must be strictly monotonic on every node.
func TestEventMonotonic(t *testing.T) {
	s := Seed()
	prev := s
	for i := 0; i < 20; i++ {
		s = s.Event()
		if !prev.Leq(s) || s.Leq(prev) {
			t.Fatalf("event %d not strictly increasing: prev=%s s=%s", i, prev, s)
		}
		prev = s
	}
	if got, want := s.EventTree().String(), "20"; got != want {
		t.Fatalf("event tree = %s, want %s", got, want)
	}
}

// Recursive forking must produce disjoint identities that recombine to the
// full interval, no matter how deep the split goes.
func TestRecursiveFork(t *testing.T) {
	a, b := Seed().Fork()
	a1, a2 := a.Fork()
	a1a, a1b := a1.Fork()

	parts := []*Stamp{a1a, a1b, a2, b}
	for i := 0; i < 5; i++ {
		for _, p := range parts {
			*p = *p.Event()
		}
	}
	// Every pair of distinct nodes must be concurrent after independent
	// events, and merging all of them restores the full interval.
	for i := 0; i < len(parts); i++ {
		for j := i + 1; j < len(parts); j++ {
			if !parts[i].Concurrent(parts[j]) {
				t.Fatalf("parts %d and %d must be concurrent: %s vs %s",
					i, j, parts[i], parts[j])
			}
		}
	}
	full := parts[0]
	for _, p := range parts[1:] {
		full = Join(full, p)
	}
	if got := full.ID().String(); got != "1" {
		t.Fatalf("merged id = %s, want 1", got)
	}
	for i, p := range parts {
		if !p.Leq(full) {
			t.Fatalf("part %d (%s) not <= full (%s)", i, p, full)
		}
	}
}

// Joining stamps with overlapping identities is a programming error and
// must panic rather than silently corrupt causality.
func TestJoinOverlappingPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic when joining overlapping identities")
		}
	}()
	a, _ := Seed().Fork()
	Join(a, a) // same half joined with itself: overlap
}

// Stamps are immutable: operations must never mutate their inputs.
func TestImmutability(t *testing.T) {
	root := Seed().Event()
	a, b := root.Fork()
	before := a.String()
	a1 := a.Event()
	Join(a1, b)
	if a.String() != before {
		t.Fatalf("receiver mutated: was %s, now %s", before, a)
	}
	if got := root.String(); got != "{1; 1}" {
		t.Fatalf("root mutated: %s", got)
	}
}

// Randomized consistency check: the causal order must agree with the
// join-based definition (a <= b  iff  join(a, b) == b), joins must
// dominate their inputs, and fork/join on identities must round-trip.
func TestRandomizedConsistency(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	stamps := []*Stamp{Seed()}
	for step := 0; step < 2000; step++ {
		switch r.Intn(3) {
		case 0: // a random node records an event
			i := r.Intn(len(stamps))
			stamps[i] = stamps[i].Event()
		case 1: // a random node forks; a new node joins the system
			i := r.Intn(len(stamps))
			a, b := stamps[i].Fork()
			stamps[i] = a
			stamps = append(stamps, b)
		case 2: // two nodes with disjoint ids merge; one leaves
			if len(stamps) < 2 {
				continue
			}
			i, j := r.Intn(len(stamps)), r.Intn(len(stamps))
			if i == j {
				continue
			}
			m := Join(stamps[i], stamps[j])
			if !stamps[i].Leq(m) || !stamps[j].Leq(m) {
				t.Fatalf("step %d: join does not dominate inputs: %s %s -> %s",
					step, stamps[i], stamps[j], m)
			}
			// keep the pool small so deep trees get exercised
			stamps[i] = m
			stamps = append(stamps[:j], stamps[j+1:]...)
		}
		// Invariant: a <= b iff join(a, b) == b, for a random pair.
		if len(stamps) >= 2 {
			i, j := r.Intn(len(stamps)), r.Intn(len(stamps))
			if i == j {
				continue
			}
			a, b := stamps[i], stamps[j]
			m := Join(a, b)
			if got := leqEvent(a.event, b.event); got != equalEvent(joinEvent(a.event, b.event), b.event) {
				t.Fatalf("step %d: leq/join mismatch (%v) for a=%s b=%s m=%s",
					step, got, a, b, m)
			}
			if !a.Leq(m) || !b.Leq(m) {
				t.Fatalf("step %d: join lost history: a=%s b=%s m=%s", step, a, b, m)
			}
		}
	}
	// Merging every live node must restore the full interval [0, 1].
	full := stamps[0]
	for _, s := range stamps[1:] {
		full = Join(full, s)
	}
	if got := full.ID().String(); got != "1" {
		t.Fatalf("full merge id = %s, want 1", got)
	}
	for i, s := range stamps {
		if !s.Leq(full) {
			t.Fatalf("node %d (%s) not <= full merge (%s)", i, s, full)
		}
	}
}
