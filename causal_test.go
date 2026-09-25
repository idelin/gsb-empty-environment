package causal

import (
	"fmt"
	"testing"
)

// --- helpers ---------------------------------------------------------------

func deliveredIDs(log []DeliveryRecord, node int) map[MsgID]bool {
	out := make(map[MsgID]bool)
	for _, r := range log {
		if r.Node == node {
			out[r.Msg.ID] = true
		}
	}
	return out
}

func deliveredMsgs(log []DeliveryRecord, node int) []Message {
	var out []Message
	for _, r := range log {
		if r.Node == node {
			out = append(out, r.Msg)
		}
	}
	return out
}

func isLinearExtension(order []Message) error {
	pos := make(map[MsgID]int)
	for i, m := range order {
		pos[m.ID] = i
	}
	for _, a := range order {
		for _, b := range order {
			if HappensBefore(a.VC, b.VC) && pos[a.ID] >= pos[b.ID] {
				return fmt.Errorf("%s ordered before causal predecessor %s", b.ID, a.ID)
			}
		}
	}
	return nil
}

// --- requirement: B's message sent after receiving A's message
// must not be delivered before A's message ----------------------------------

func TestCausalPredecessorDeliveredFirst(t *testing.T) {
	a, b, c := NewNode(0, 3, 8), NewNode(1, 3, 8), NewNode(2, 3, 8)

	m1 := a.Send("from A")
	if _, err := b.Receive(m1); err != nil {
		t.Fatal(err)
	}
	m2 := b.Send("from B, after seeing A") // causally depends on m1

	// C sees m2 first: it must be buffered, not delivered.
	got, err := c.Receive(m2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("m2 delivered before its causal predecessor m1: %v", got)
	}
	if len(c.Pending()) != 1 {
		t.Fatalf("m2 should be pending, pending=%v", c.Pending())
	}

	// When m1 finally arrives, m1 must be delivered before m2.
	got, err = c.Receive(m1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != m1.ID || got[1].ID != m2.ID {
		t.Fatalf("expected [m1 m2], got %v", got)
	}
}

// --- requirement: duplicate dedup ------------------------------------------

func TestDuplicatesDropped(t *testing.T) {
	a, b := NewNode(0, 2, 8), NewNode(1, 2, 8)
	m := a.Send("hello")
	for i := 0; i < 3; i++ {
		got, err := b.Receive(m)
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		if i == 0 {
			want = 1
		}
		if len(got) != want {
			t.Fatalf("copy %d: delivered %d messages, want %d", i, len(got), want)
		}
	}
	if b.DuplicateCount != 2 || b.DeliveredCount != 1 {
		t.Fatalf("dups=%d delivered=%d, want 2 and 1", b.DuplicateCount, b.DeliveredCount)
	}
}

// --- requirement: explicit backpressure when buffer overflows --------------

func TestBackpressureExplicit(t *testing.T) {
	a, b := NewNode(0, 2, 1), NewNode(1, 2, 1) // buffer cap = 1
	m1 := a.Send("m1")                         // never delivered to b (lost)
	m2 := a.Send("m2")
	m3 := a.Send("m3")

	if _, err := b.Receive(m2); err != nil {
		t.Fatalf("m2 should fit in buffer: %v", err)
	}
	if _, err := b.Receive(m3); err != ErrBackpressure {
		t.Fatalf("m3 should hit explicit backpressure, got %v", err)
	}
	// Nothing was silently dropped or force-delivered.
	if b.DeliveredCount != 0 || len(b.Pending()) != 1 {
		t.Fatalf("delivered=%d pending=%d, want 0 and 1", b.DeliveredCount, len(b.Pending()))
	}
	// After the predecessor arrives, the buffer drains and a retry of m3
	// succeeds.
	if got, _ := b.Receive(m1); len(got) != 2 {
		t.Fatalf("expected m1+m2 to deliver, got %v", got)
	}
	if got, err := b.Receive(m3); err != nil || len(got) != 1 {
		t.Fatalf("retry of m3 should succeed, got=%v err=%v", got, err)
	}
}

// --- requirement: a missing predecessor is never "timed out" into
// being satisfied ------------------------------------------------------------

func TestMissingPredecessorNeverFaked(t *testing.T) {
	// n0#1 to n2 is permanently lost; n0#2 depends on it.
	sim := NewSimulator(3, 8, Drop{
		Inner: Reliable{Delay: 1},
		If:    func(m Message, to int) bool { return m.ID == (MsgID{0, 1}) && to == 2 },
	}, 1)
	sim.Send(0, 0, "lost-to-n2")
	sim.Send(0, 0, "dependent")
	sim.Run()

	// Virtual time has advanced past all events; n2 still must not have
	// the dependent message, and it must still be reported as waiting.
	got := deliveredIDs(sim.Log, 2)
	if got[MsgID{0, 2}] {
		t.Fatal("dependent message delivered despite permanently missing predecessor")
	}
	pending := sim.Nodes[2].Pending()
	if len(pending) != 1 || pending[0].ID != (MsgID{0, 2}) {
		t.Fatalf("pending=%v, want only n0#2", pending)
	}
	report := sim.Nodes[2].PendingReport()
	if len(report) != 1 {
		t.Fatalf("pending report: %v", report)
	}
	t.Logf("pending report: %s", report[0])
	if err := VerifySafety(sim.Log); err != nil {
		t.Fatal(err)
	}
}

// --- requirement: fixed membership set --------------------------------------

func TestFixedMembershipEnforced(t *testing.T) {
	n := NewNode(0, 3, 8)
	ghost := Message{ID: MsgID{Sender: 7, Seq: 1}, From: 7, VC: VectorClock{0, 0, 0, 0, 0, 0, 0, 1}}
	if _, err := n.Receive(ghost); err != ErrUnknownMember {
		t.Fatalf("sender outside membership: got %v", err)
	}
	bad := Message{ID: MsgID{Sender: 1, Seq: 1}, From: 1, VC: VectorClock{0, 1}} // wrong length
	if _, err := n.Receive(bad); err != ErrUnknownMember {
		t.Fatalf("wrong clock length: got %v", err)
	}
}

// --- requirement: concurrent messages have a stable total order -------------

func TestConcurrentMessagesStableOrder(t *testing.T) {
	const nodes = 4
	sim := NewSimulator(nodes, 64, Chaos{MinDelay: 1, MaxDelay: 30, DupRate: 0.3}, 7)
	for i := 0; i < nodes; i++ {
		sim.Send(0, i, fmt.Sprintf("first-from-%d", i)) // mutually concurrent
		sim.Send(0, i, fmt.Sprintf("second-from-%d", i))
	}
	sim.Run()

	// Every node computes the SAME stable order for the same message set,
	// regardless of its local arrival order, and that order extends causality.
	var reference []MsgID
	for node := 0; node < nodes; node++ {
		ordered := StableOrder(deliveredMsgs(sim.Log, node))
		if err := isLinearExtension(ordered); err != nil {
			t.Fatalf("node %d: %v", node, err)
		}
		ids := make([]MsgID, len(ordered))
		for i, m := range ordered {
			ids[i] = m.ID
		}
		if node == 0 {
			reference = ids
			continue
		}
		if fmt.Sprintf("%v", ids) != fmt.Sprintf("%v", reference) {
			t.Fatalf("node %d stable order %v differs from node 0: %v", node, ids, reference)
		}
	}
	t.Logf("stable order: %v", reference)
}

// --- safety evidence: fuzz over seeds with chaos faults ---------------------

func TestSafetyUnderChaos(t *testing.T) {
	for seed := int64(0); seed < 200; seed++ {
		sim := NewSimulator(4, 64, Chaos{MinDelay: 1, MaxDelay: 20, DupRate: 0.4}, seed)
		sim.RetryOnBackpressure = true
		for i := 0; i < 12; i++ {
			from := int((seed + int64(i)) % 4)
			sim.Send(int64(i*3), from, fmt.Sprintf("msg-%d", i))
		}
		sim.Run()
		if err := VerifySafety(sim.Log); err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if err := VerifyExactlyOnce(sim.Log); err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
	}
}

// --- liveness evidence: with no permanent loss, everything is delivered -----

func TestLivenessUnderChaos(t *testing.T) {
	const nodes, perNode = 5, 6
	sim := NewSimulator(nodes, 128, Chaos{MinDelay: 1, MaxDelay: 25, DupRate: 0.5}, 42)
	sim.RetryOnBackpressure = true
	for i := 0; i < nodes; i++ {
		for j := 0; j < perNode; j++ {
			sim.Send(int64(j*2), i, fmt.Sprintf("n%d-msg-%d", i, j))
		}
	}
	sim.Run()

	for node := 0; node < nodes; node++ {
		got := deliveredIDs(sim.Log, node)
		if len(got) != nodes*perNode {
			t.Fatalf("node %d delivered %d/%d messages", node, len(got), nodes*perNode)
		}
		for sender := 0; sender < nodes; sender++ {
			for seq := 1; seq <= perNode; seq++ {
				if !got[MsgID{sender, seq}] {
					t.Fatalf("node %d missing %v", node, MsgID{sender, seq})
				}
			}
		}
	}
	if err := VerifySafety(sim.Log); err != nil {
		t.Fatal(err)
	}
	if err := VerifyExactlyOnce(sim.Log); err != nil {
		t.Fatal(err)
	}
	dups := 0
	for _, n := range sim.Nodes {
		dups += n.DuplicateCount
	}
	if dups == 0 {
		t.Fatal("expected injected duplicates to exercise dedup")
	}
	t.Logf("all %d messages delivered exactly once at %d nodes; %d duplicates dropped",
		nodes*perNode, nodes, dups)
}
