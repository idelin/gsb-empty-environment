package dvv

import "testing"

func TestClockDomination(t *testing.T) {
	base := NewContext(map[string]uint64{"a": 2, "b": 1})
	child := base.withDot(Dot{Node: "a", Counter: 3})

	if !child.Descends(base) {
		t.Fatal("child clock must descend from its context")
	}
	if base.Descends(child) {
		t.Fatal("context must not descend from the dotted child")
	}
	if !child.Descends(child) || !child.Equal(child) {
		t.Fatal("a clock must descend from and equal itself")
	}
	if child.Concurrent(base) {
		t.Fatal("child and its context are causally ordered, not concurrent")
	}
}

func TestClockConcurrency(t *testing.T) {
	x := EmptyClock().withDot(Dot{Node: "a", Counter: 1})
	y := EmptyClock().withDot(Dot{Node: "b", Counter: 2})

	if !x.Concurrent(y) {
		t.Fatal("dots from different nodes with empty context are concurrent")
	}
	// z saw a:1 before its own dot b:1: it descends from x, and stays
	// concurrent with y because it never saw event b:2.
	z := NewContext(map[string]uint64{"a": 1}).withDot(Dot{Node: "b", Counter: 1})
	if !z.Descends(x) || z.Concurrent(x) {
		t.Fatal("z's context includes a:1, so z descends from x")
	}
	if !z.Concurrent(y) {
		t.Fatal("z never saw b:2, so z and y are concurrent")
	}
	// w saw everything and dominates all three.
	w := z.Merge(x).Merge(y).withDot(Dot{Node: "a", Counter: 2})
	for _, c := range []Clock{x, y, z} {
		if !w.Descends(c) || w.Concurrent(c) {
			t.Fatalf("w=%s must dominate %s", w, c)
		}
	}
}

// TestDotDistinguishesEvents is the core DVV property: two writes by
// the same node where the second did NOT see the first are concurrent,
// even though a classic version vector ({a:3} vs {a:2}) would call one
// an ancestor of the other and silently discard a live value.
func TestDotDistinguishesEvents(t *testing.T) {
	seen := NewContext(map[string]uint64{"a": 1}).withDot(Dot{Node: "a", Counter: 2})
	unseen := NewContext(map[string]uint64{"a": 1}).withDot(Dot{Node: "a", Counter: 3})

	if !seen.Descends(NewContext(map[string]uint64{"a": 1})) {
		t.Fatal("sanity: seen descends from its context")
	}
	if !seen.Concurrent(unseen) {
		t.Fatal("event a:3 did not see a:2: the clocks must be concurrent")
	}
	if unseen.Descends(seen) {
		t.Fatal("ghost branch: a:3 must NOT falsely dominate a:2")
	}
}

func TestNormalizationKeepsClockMinimal(t *testing.T) {
	c := EmptyClock()
	for i := uint64(1); i <= 100; i++ {
		c = c.withDot(Dot{Node: "a", Counter: i})
	}
	// 99 dots fold into the vector; the latest dot stays explicit as
	// the identity of the most recent event.
	if c.Size() != 2 {
		t.Fatalf("100 sequential dots must compact to vector+dot, got %s (size %d)", c, c.Size())
	}
	if got := c.maxCounter("a"); got != 100 {
		t.Fatalf("maxCounter = %d, want 100", got)
	}
}

func TestMergeIsJoin(t *testing.T) {
	a := EmptyClock().withDot(Dot{Node: "a", Counter: 1})
	b := EmptyClock().withDot(Dot{Node: "b", Counter: 1})
	m := a.Merge(b)
	if !m.Descends(a) || !m.Descends(b) {
		t.Fatal("merge must dominate both operands")
	}
	if !a.Merge(b).Equal(b.Merge(a)) {
		t.Fatal("merge must be commutative")
	}
	if !m.Merge(m).Equal(m) {
		t.Fatal("merge must be idempotent")
	}
}
