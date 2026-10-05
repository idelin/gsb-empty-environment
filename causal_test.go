package causal

import (
	"reflect"
	"testing"
)

func msg(id, sender string, seq uint64, vector map[string]uint64) Message {
	return Message{ID: id, Sender: sender, Seq: seq, Vector: vector}
}

func ids(ms []Message) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}

// Same-source ordering: an out-of-order message is buffered with an exact
// gap, then delivered in order once the predecessor arrives.
func TestSameSourceOrdering(t *testing.T) {
	c := New()

	delivered, gaps, dup := c.Submit(msg("a2", "A", 2, map[string]uint64{"A": 2}))
	if dup || delivered != nil {
		t.Fatalf("a2 should pend, got delivered=%v dup=%v", ids(delivered), dup)
	}
	want := []Gap{{Sender: "A", From: 1, To: 1}}
	if !reflect.DeepEqual(gaps, want) {
		t.Fatalf("gap = %+v, want %+v", gaps, want)
	}

	delivered, gaps, dup = c.Submit(msg("a1", "A", 1, map[string]uint64{"A": 1}))
	if dup || gaps != nil {
		t.Fatalf("a1 should deliver, got gaps=%v dup=%v", gaps, dup)
	}
	if got := ids(delivered); !reflect.DeepEqual(got, []string{"a1", "a2"}) {
		t.Fatalf("delivery order = %v, want [a1 a2]", got)
	}
	if c.Pending() != 0 {
		t.Fatalf("pending = %d, want 0", c.Pending())
	}
}

// Cross-source dependency: B's message requires A#2; delivering A#1 is not
// enough, delivering A#2 releases it.
func TestCrossSourceDependency(t *testing.T) {
	c := New()

	_, gaps, _ := c.Submit(msg("b1", "B", 1, map[string]uint64{"B": 1, "A": 2}))
	want := []Gap{{Sender: "A", From: 1, To: 2}}
	if !reflect.DeepEqual(gaps, want) {
		t.Fatalf("gap = %+v, want %+v", gaps, want)
	}

	delivered, _, _ := c.Submit(msg("a1", "A", 1, map[string]uint64{"A": 1}))
	if got := ids(delivered); !reflect.DeepEqual(got, []string{"a1"}) {
		t.Fatalf("after a1 delivered = %v, want [a1] (b1 still blocked)", got)
	}
	if c.Pending() != 1 {
		t.Fatalf("pending = %d, want 1", c.Pending())
	}

	delivered, _, _ = c.Submit(msg("a2", "A", 2, map[string]uint64{"A": 2}))
	if got := ids(delivered); !reflect.DeepEqual(got, []string{"a2", "b1"}) {
		t.Fatalf("after a2 delivered = %v, want [a2 b1]", got)
	}
}

// A chain of cross-source dependencies cascades through several senders.
func TestCascadingChain(t *testing.T) {
	c := New()

	c.Submit(msg("c1", "C", 1, map[string]uint64{"C": 1, "B": 1}))
	c.Submit(msg("b1", "B", 1, map[string]uint64{"B": 1, "A": 1}))
	if c.Pending() != 2 {
		t.Fatalf("pending = %d, want 2", c.Pending())
	}

	delivered, _, _ := c.Submit(msg("a1", "A", 1, map[string]uint64{"A": 1}))
	if got := ids(delivered); !reflect.DeepEqual(got, []string{"a1", "b1", "c1"}) {
		t.Fatalf("cascade = %v, want [a1 b1 c1]", got)
	}
	if c.Pending() != 0 {
		t.Fatalf("pending = %d, want 0", c.Pending())
	}
}

// Duplicates of a delivered message change nothing and wake nobody.
func TestDuplicateDelivered(t *testing.T) {
	c := New()

	m := msg("a1", "A", 1, map[string]uint64{"A": 1})
	if delivered, _, dup := c.Submit(m); dup || len(delivered) != 1 {
		t.Fatalf("first submit: delivered=%v dup=%v", ids(delivered), dup)
	}

	// Buffer a dependent, then replay the already-delivered message.
	c.Submit(msg("b1", "B", 1, map[string]uint64{"B": 1, "A": 2}))
	delivered, gaps, dup := c.Submit(m)
	if !dup || delivered != nil || gaps != nil {
		t.Fatalf("duplicate: delivered=%v gaps=%v dup=%v, want pure duplicate", ids(delivered), gaps, dup)
	}
	if c.Pending() != 1 {
		t.Fatalf("duplicate caused wakeup: pending = %d, want 1", c.Pending())
	}

	// Same position under a different ID is also a duplicate.
	if _, _, dup = c.Submit(msg("a1-retry", "A", 1, map[string]uint64{"A": 1})); !dup {
		t.Fatal("replayed position should be a duplicate")
	}
}

// Duplicates of a pending message do not double-register it: when the
// predecessor arrives it is delivered exactly once.
func TestDuplicatePending(t *testing.T) {
	c := New()

	m := msg("a2", "A", 2, map[string]uint64{"A": 2})
	if _, gaps, _ := c.Submit(m); len(gaps) != 1 {
		t.Fatalf("a2 should pend with one gap, got %+v", gaps)
	}
	if _, _, dup := c.Submit(m); !dup {
		t.Fatal("resubmit of pending message should be a duplicate")
	}
	if c.Pending() != 1 {
		t.Fatalf("pending = %d, want 1 (no double registration)", c.Pending())
	}

	delivered, _, _ := c.Submit(msg("a1", "A", 1, map[string]uint64{"A": 1}))
	if got := ids(delivered); !reflect.DeepEqual(got, []string{"a1", "a2"}) {
		t.Fatalf("delivered = %v, want [a1 a2] exactly once", got)
	}
	if c.Pending() != 0 {
		t.Fatalf("pending = %d, want 0", c.Pending())
	}
}

// Gaps report the exact missing range per sender, and the index is fully
// cleaned up once everything is delivered.
func TestExactMultiSenderGaps(t *testing.T) {
	c := New()

	c.Submit(msg("a1", "A", 1, map[string]uint64{"A": 1}))
	_, gaps, _ := c.Submit(msg("x1", "X", 3, map[string]uint64{"X": 3, "A": 3, "B": 2}))
	want := []Gap{
		{Sender: "A", From: 2, To: 3},
		{Sender: "B", From: 1, To: 2},
		{Sender: "X", From: 1, To: 2},
	}
	if !reflect.DeepEqual(gaps, want) {
		t.Fatalf("gaps = %+v, want %+v", gaps, want)
	}

	c.Submit(msg("a2", "A", 2, map[string]uint64{"A": 2}))
	c.Submit(msg("a3", "A", 3, map[string]uint64{"A": 3}))
	c.Submit(msg("b1", "B", 1, map[string]uint64{"B": 1}))
	c.Submit(msg("b2", "B", 2, map[string]uint64{"B": 2}))
	c.Submit(msg("x1m", "X", 1, map[string]uint64{"X": 1}))
	delivered, _, _ := c.Submit(msg("x2", "X", 2, map[string]uint64{"X": 2}))
	if got := ids(delivered); !reflect.DeepEqual(got, []string{"x2", "x1"}) {
		t.Fatalf("delivered = %v, want [x2 x1]", got)
	}
	if c.Pending() != 0 || len(c.waiters) != 0 || len(c.waitingOn) != 0 {
		t.Fatalf("index not clean: pending=%d waiters=%d waitingOn=%d",
			c.Pending(), len(c.waiters), len(c.waitingOn))
	}
}
