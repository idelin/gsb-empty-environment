package causal

import "testing"

var members = []NodeID{"A", "B", "C"}

// Safety: a message sent by B after B received A's message carries A's
// message in its causal past, so C must not deliver it before A's.
func TestCausalDependencyOrder(t *testing.T) {
	a, b, c := NewNode("A", members, 8), NewNode("B", members, 8), NewNode("C", members, 8)

	m1 := a.Broadcast("from A")
	if err := b.Receive(m1); err != nil {
		t.Fatal(err)
	}
	m2 := b.Broadcast("B after seeing A") // causally depends on m1

	// Network reorders: m2 reaches C first.
	if err := c.Receive(m2); err != nil {
		t.Fatal(err)
	}
	if c.Delivered() != 0 || c.Buffered() != 1 {
		t.Fatalf("m2 must be buffered, delivered=%d buffered=%d", c.Delivered(), c.Buffered())
	}

	// Once the predecessor arrives, both are delivered in causal order.
	if err := c.Receive(m1); err != nil {
		t.Fatal(err)
	}
	log := c.Log()
	if len(log) != 2 || log[0].ID != m1.ID || log[1].ID != m2.ID {
		t.Fatalf("bad delivery order: %v", log)
	}
	if err := VerifyLog(log); err != nil {
		t.Fatal(err)
	}
}

// Safety: duplicates are delivered at most once, whether the extra copy
// arrives after delivery or while the original is still buffered.
func TestDeduplication(t *testing.T) {
	a, c := NewNode("A", members, 8), NewNode("C", members, 8)
	m1 := a.Broadcast("one")
	m2 := a.Broadcast("two")

	// Duplicate of a buffered (not yet deliverable) message.
	c.Receive(m2)
	c.Receive(m2)
	if c.Buffered() != 1 || c.Duplicates != 1 {
		t.Fatalf("buffered dup: buffered=%d dups=%d", c.Buffered(), c.Duplicates)
	}

	// Duplicate of an already delivered message.
	c.Receive(m1)
	c.Receive(m1)
	if c.Delivered() != 2 || c.Duplicates != 2 {
		t.Fatalf("delivered dup: delivered=%d dups=%d", c.Delivered(), c.Duplicates)
	}
	if err := VerifyLog(c.Log()); err != nil {
		t.Fatal(err)
	}
}

// Backpressure: a full hold-back buffer rejects further unready
// messages with ErrBackpressure instead of growing silently.
func TestBackpressureSignal(t *testing.T) {
	a, c := NewNode("A", members, 8), NewNode("C", members, 1)
	a.Broadcast("a1")
	m2 := a.Broadcast("a2")
	m3 := a.Broadcast("a3")

	if err := c.Receive(m2); err != nil { // buffered, waits for a1
		t.Fatal(err)
	}
	if err := c.Receive(m3); err != ErrBackpressure {
		t.Fatalf("want ErrBackpressure, got %v", err)
	}
	if c.Buffered() != 1 {
		t.Fatalf("buffer must stay bounded, got %d", c.Buffered())
	}
}

// Safety: a message whose predecessor never arrives is never delivered,
// no matter how much time passes or how much later traffic arrives.
// There is no timeout path that could mark it satisfied.
func TestMissingPredecessorNeverDelivered(t *testing.T) {
	dropA1toC := func(to NodeID, m Message) bool {
		return to == "C" && m.ID.Sender == "A" && m.ID.Seq == 1
	}
	s := NewSim(Config{
		Members:   members,
		MaxBuffer: 8,
		MinDelay:  1, MaxDelay: 5,
		DropPred:   dropA1toC,
		RetryAfter: 3,
		MaxTime:    10000,
		Seed:       7,
	})

	s.Broadcast("A", "a1") // dropped on the way to C
	s.Run()                // B receives a1
	s.Broadcast("B", "b2 depends on a1")
	s.Broadcast("A", "a2") // more traffic, keeps time advancing
	s.Broadcast("B", "b3")
	s.Run()

	c := s.Node("C")
	for _, m := range c.Log() {
		if m.ID.Sender == "B" {
			t.Fatalf("C delivered %v without its causal predecessor A#1", m.ID)
		}
	}
	if c.Buffered() == 0 {
		t.Fatal("dependent messages must remain buffered forever")
	}
	if err := VerifyLog(c.Log()); err != nil {
		t.Fatal(err)
	}
	// B itself did deliver everything: the block is at C only.
	if s.Node("B").Delivered() != 4 {
		t.Fatalf("B delivered %d, want 4", s.Node("B").Delivered())
	}
}
