package causalrouter

import (
	"errors"
	"testing"
)

func deliveredIDs(r *Router) []string {
	var ids []string
	for _, m := range r.Delivered() {
		ids = append(ids, m.ID)
	}
	return ids
}

func equalIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Acceptance boundary: client X sends (A:1); client Y observes it and
// sends (A:1, B:1). If the receiver gets Y's message first, it must
// buffer it and only deliver both in causal order once X's (A:1)
// arrives.
func TestCausalOrderAcceptance(t *testing.T) {
	x := NewClient("A")
	y := NewClient("B")

	m1 := x.Send("hello from A") // clock {A:1}
	y.Receive(m1)
	m2 := y.Send("reply from B") // clock {A:1, B:1}

	if got := m1.Clock["A"]; got != 1 {
		t.Fatalf("m1 clock A = %d, want 1", got)
	}
	if m2.Clock["A"] != 1 || m2.Clock["B"] != 1 {
		t.Fatalf("m2 clock = %v, want {A:1 B:1}", m2.Clock)
	}

	r := NewRouter()

	// Y's message arrives first: must be buffered, nothing delivered.
	if err := r.Submit(m2); err != nil {
		t.Fatalf("submit m2: %v", err)
	}
	if n := len(r.Delivered()); n != 0 {
		t.Fatalf("delivered %d messages before predecessor arrived, want 0", n)
	}
	if n := len(r.Pending()); n != 1 {
		t.Fatalf("pending = %d, want 1 (m2 buffered)", n)
	}

	// X's message arrives: m1 delivered first, then m2 unblocked.
	if err := r.Submit(m1); err != nil {
		t.Fatalf("submit m1: %v", err)
	}
	want := []string{"A:1", "B:1"}
	if got := deliveredIDs(r); !equalIDs(got, want) {
		t.Fatalf("delivery order = %v, want causal order %v", got, want)
	}
	if n := len(r.Pending()); n != 0 {
		t.Fatalf("pending = %d after drain, want 0", n)
	}
}

func TestInOrderDeliveryIsImmediate(t *testing.T) {
	r := NewRouter()
	a := NewClient("A")
	if err := r.Submit(a.Send("m1")); err != nil {
		t.Fatal(err)
	}
	if err := r.Submit(a.Send("m2")); err != nil {
		t.Fatal(err)
	}
	if got := deliveredIDs(r); !equalIDs(got, []string{"A:1", "A:2"}) {
		t.Fatalf("delivered = %v", got)
	}
}

func TestSameSenderGapIsBuffered(t *testing.T) {
	r := NewRouter()
	a := NewClient("A")
	m1 := a.Send("m1")
	m2 := a.Send("m2")

	if err := r.Submit(m2); err != nil {
		t.Fatal(err)
	}
	if n := len(r.Delivered()); n != 0 {
		t.Fatalf("delivered %d, want 0 while A:1 missing", n)
	}
	if err := r.Submit(m1); err != nil {
		t.Fatal(err)
	}
	if got := deliveredIDs(r); !equalIDs(got, []string{"A:1", "A:2"}) {
		t.Fatalf("delivered = %v", got)
	}
}

func TestDuplicateRejected(t *testing.T) {
	r := NewRouter()
	a := NewClient("A")
	m1 := a.Send("m1")
	if err := r.Submit(m1); err != nil {
		t.Fatal(err)
	}
	if err := r.Submit(m1); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("resubmit err = %v, want ErrDuplicate", err)
	}
	if n := len(r.Delivered()); n != 1 {
		t.Fatalf("delivered = %d, want 1", n)
	}
}

// A causal cycle: C's first message claims to depend on D:1 while D's
// first message claims to depend on C:1. Neither predecessor can ever
// be produced, so both messages must be quarantined with evidence.
func TestCausalCycleQuarantine(t *testing.T) {
	r := NewRouter()
	mC := NewMessage("C", VectorClock{"C": 1, "D": 1}, "forged by C")
	mD := NewMessage("D", VectorClock{"D": 1, "C": 1}, "forged by D")

	if err := r.Submit(mC); err != nil {
		t.Fatal(err)
	}
	if err := r.Submit(mD); err != nil {
		t.Fatal(err)
	}

	if n := len(r.Pending()); n != 0 {
		t.Fatalf("pending = %d, want 0 after quarantine", n)
	}
	if n := len(r.Delivered()); n != 0 {
		t.Fatalf("delivered = %d, want 0", n)
	}
	q := r.Quarantined()
	if len(q) != 2 {
		t.Fatalf("quarantined = %d, want 2", len(q))
	}
	ev := q[0].Evidence
	if ev.Kind != "causal-cycle" {
		t.Fatalf("evidence kind = %q, want causal-cycle", ev.Kind)
	}
	if len(ev.Cycle) != 2 {
		t.Fatalf("evidence cycle = %v, want 2 members", ev.Cycle)
	}
	if len(ev.Blocked) == 0 {
		t.Fatal("evidence must list the blocking dependencies")
	}
	if q[1].Evidence.Kind != "causal-cycle" {
		t.Fatal("both quarantined messages must carry the cycle evidence")
	}
}

// A longer chain buffered out of order must still drain in causal order.
func TestChainDrainsInCausalOrder(t *testing.T) {
	r := NewRouter()
	a := NewClient("A")
	b := NewClient("B")
	c := NewClient("C")

	m1 := a.Send("a1")
	b.Receive(m1)
	m2 := b.Send("b1")
	c.Receive(m2)
	m3 := c.Send("c1")

	for _, m := range []*Message{m3, m2} { // reversed arrival
		if err := r.Submit(m); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(r.Delivered()); n != 0 {
		t.Fatalf("delivered %d before chain head arrived", n)
	}
	if err := r.Submit(m1); err != nil {
		t.Fatal(err)
	}
	if got := deliveredIDs(r); !equalIDs(got, []string{"A:1", "B:1", "C:1"}) {
		t.Fatalf("delivered = %v", got)
	}
}

func TestVectorClockOrdering(t *testing.T) {
	a := VectorClock{"A": 1}
	b := VectorClock{"A": 1, "B": 1}
	if !a.HappenedBefore(b) {
		t.Fatal("{A:1} should happen before {A:1,B:1}")
	}
	if b.HappenedBefore(a) {
		t.Fatal("{A:1,B:1} must not happen before {A:1}")
	}
	if (VectorClock{"A": 1}).HappenedBefore(VectorClock{"B": 1}) {
		t.Fatal("concurrent clocks must not be ordered")
	}
}

func TestClientClockMerge(t *testing.T) {
	a := NewClient("A")
	b := NewClient("B")
	m := a.Send("x")
	b.Receive(m)
	reply := b.Send("y")
	if reply.Clock["A"] != 1 || reply.Clock["B"] != 1 {
		t.Fatalf("reply clock = %v, want {A:1 B:1}", reply.Clock)
	}
}
