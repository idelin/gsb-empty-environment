package rbcast

import "testing"

func TestNodeDedupDuplicateAndReorder(t *testing.T) {
	n := NewNode("n1", 8)
	msgs := []Message{
		{Source: "s", Seq: 1}, {Source: "s", Seq: 3}, {Source: "s", Seq: 3},
		{Source: "s", Seq: 2}, {Source: "s", Seq: 1}, {Source: "s", Seq: 4},
	}
	wantDelivered := []bool{true, true, false, true, false, true}
	for i, m := range msgs {
		if got := n.Receive(m); got != wantDelivered[i] {
			t.Fatalf("msg %d: delivered=%v want %v", i, got, wantDelivered[i])
		}
	}
	if got := n.Watermark("s"); got != 4 {
		t.Fatalf("watermark=%d want 4", got)
	}
	if got := n.GapSize("s"); got != 0 {
		t.Fatalf("gap size=%d want 0", got)
	}
	if n.Duplicates != 2 {
		t.Fatalf("duplicates=%d want 2", n.Duplicates)
	}
	if len(n.Delivered) != 4 {
		t.Fatalf("delivered=%d want 4", len(n.Delivered))
	}
}

func TestNodeGapTableSparse(t *testing.T) {
	n := NewNode("n1", 8)
	n.Receive(Message{Source: "s", Seq: 1})
	n.Receive(Message{Source: "s", Seq: 5})
	n.Receive(Message{Source: "s", Seq: 7})
	if got := n.Watermark("s"); got != 1 {
		t.Fatalf("watermark=%d want 1", got)
	}
	if got := n.GapSize("s"); got != 2 {
		t.Fatalf("gap size=%d want 2", got)
	}
	// Retransmission of an old sparse entry must be recognised as duplicate.
	if n.Receive(Message{Source: "s", Seq: 5}) {
		t.Fatal("seq 5 delivered twice")
	}
	// Fill the gap; watermark folds over sparse entries.
	n.Receive(Message{Source: "s", Seq: 2})
	n.Receive(Message{Source: "s", Seq: 3})
	n.Receive(Message{Source: "s", Seq: 4})
	if got := n.Watermark("s"); got != 5 {
		t.Fatalf("watermark=%d want 5", got)
	}
	n.Receive(Message{Source: "s", Seq: 6})
	if got := n.Watermark("s"); got != 7 {
		t.Fatalf("watermark=%d want 7 after fold", got)
	}
	if got := n.GapSize("s"); got != 0 {
		t.Fatalf("gap size=%d want 0 after fold", got)
	}
}

func TestNodeBoundedGapTable(t *testing.T) {
	n := NewNode("n1", 2)
	n.Receive(Message{Source: "s", Seq: 1})
	n.Receive(Message{Source: "s", Seq: 3})
	n.Receive(Message{Source: "s", Seq: 4})
	// Gap table full: a further out-of-order message is refused.
	if n.Receive(Message{Source: "s", Seq: 9}) {
		t.Fatal("expected refusal beyond bounded gap table")
	}
	// But the contiguous next seq is always acceptable.
	if !n.Receive(Message{Source: "s", Seq: 2}) {
		t.Fatal("contiguous seq must be accepted even when gap table full")
	}
	if got := n.Watermark("s"); got != 4 {
		t.Fatalf("watermark=%d want 4", got)
	}
}

func TestOfflineCatchUpRecognisesOldMessages(t *testing.T) {
	sim := NewSimulator(NewNetwork(1, 0, 0, 0), 16)
	sim.AddSource("s")
	sim.Join("a")
	sim.Join("b")

	sim.Broadcast("s", "m1")
	sim.Broadcast("s", "m2")
	sim.GoOffline("b")
	sim.Broadcast("s", "m3")
	sim.Broadcast("s", "m4")
	sim.Broadcast("s", "m5")

	if got := sim.Nodes["b"].Watermark("s"); got != 2 {
		t.Fatalf("offline node watermark=%d want 2", got)
	}
	sim.GoOnline("b") // triggers catch-up retransmission
	if got := sim.Nodes["b"].Watermark("s"); got != 5 {
		t.Fatalf("after catch-up watermark=%d want 5", got)
	}
	if got := len(sim.Nodes["b"].Delivered); got != 5 {
		t.Fatalf("delivered=%d want 5 (no duplicate delivery)", got)
	}

	// A second catch-up pass (e.g. repeated retransmission) must be all dups.
	before := len(sim.Nodes["b"].Delivered)
	sim.CatchUp("b")
	if got := len(sim.Nodes["b"].Delivered); got != before {
		t.Fatalf("re-catch-up delivered %d new messages, want 0", got-before)
	}
}

func TestCompactionRequiresAllValidSubscribers(t *testing.T) {
	sim := NewSimulator(NewNetwork(1, 0, 0, 0), 16)
	src := sim.AddSource("s")
	sim.Join("a")
	sim.Join("b")

	for i := 0; i < 5; i++ {
		sim.Broadcast("s", "x")
	}
	sim.GoOffline("b") // b stays valid but cannot ack
	sim.CollectAcks()
	sim.CompactAll()
	if got := src.Retained(); got != 5 {
		t.Fatalf("retained=%d want 5: offline valid member must pin history", got)
	}

	sim.GoOnline("b") // catch-up, then ack
	sim.CollectAcks()
	sim.CompactAll()
	if got := src.Retained(); got != 0 {
		t.Fatalf("retained=%d want 0 after all valid members acked", got)
	}
	if got := src.Compacted(); got != 5 {
		t.Fatalf("compacted=%d want 5", got)
	}
}

func TestLeaveUnblocksCompactionAndRejoinResetsEpoch(t *testing.T) {
	sim := NewSimulator(NewNetwork(1, 0, 0, 0), 16)
	src := sim.AddSource("s")
	sim.Join("a")
	sim.Join("b")

	for i := 0; i < 4; i++ {
		sim.Broadcast("s", "x")
	}
	sim.CollectAcks()

	// b leaves: no longer a valid subscriber, must not block compaction.
	sim.Leave("b")
	sim.CompactAll()
	if got := src.Retained(); got != 0 {
		t.Fatalf("retained=%d want 0 after b left", got)
	}

	// b rejoins with a bumped generation and must re-ack from scratch.
	gen := sim.Members.Generation("b")
	sim.Join("b")
	if got := sim.Members.Generation("b"); got != gen+1 {
		t.Fatalf("generation=%d want %d after rejoin", got, gen+1)
	}
	for i := 0; i < 3; i++ {
		sim.Broadcast("s", "y")
	}
	sim.CollectAcks()
	sim.CompactAll()
	// b rejoined at watermark 4 and has only acked up to 7; a acked 7 too.
	if got := src.Compacted(); got != 7 {
		t.Fatalf("compacted=%d want 7", got)
	}
}

func TestStaleGenerationAckRejected(t *testing.T) {
	sim := NewSimulator(NewNetwork(1, 0, 0, 0), 16)
	src := sim.AddSource("s")
	sim.Join("b")

	sim.Broadcast("s", "m1")
	sim.CollectAcks()
	staleGen := sim.Members.Generation("b")

	sim.Leave("b")
	sim.Join("b") // generation bumped

	// An ack replayed from the pre-leave epoch must be rejected.
	stale := Ack{MemberID: "b", Generation: staleGen, Source: "s", Watermark: 99}
	if sim.SubmitAck(stale) {
		t.Fatal("stale-generation ack accepted")
	}
	if got := src.CompactableSeq(sim.Members.ValidMembers()); got != 0 {
		t.Fatalf("compactable=%d want 0: stale ack must not count", got)
	}

	// Current-generation ack is accepted.
	fresh := Ack{MemberID: "b", Generation: sim.Members.Generation("b"), Source: "s", Watermark: 1}
	if !sim.SubmitAck(fresh) {
		t.Fatal("current-generation ack rejected")
	}
	if got := src.CompactableSeq(sim.Members.ValidMembers()); got != 1 {
		t.Fatalf("compactable=%d want 1", got)
	}
}

func TestUnreliableNetworkEventuallyConsistent(t *testing.T) {
	// Loss + duplication + reorder; bounded gap table; catch-up loop must
	// still converge with no duplicate deliveries.
	sim := NewSimulator(NewNetwork(42, 0.3, 0.3, 7), 4)
	sim.AddSource("s")
	sim.Join("a")
	sim.Join("b")

	const total = 30
	for i := 0; i < total; i++ {
		sim.Broadcast("s", "x")
	}
	for round := 0; round < 50; round++ {
		done := true
		for _, id := range []string{"a", "b"} {
			if sim.Nodes[id].Watermark("s") < total {
				sim.CatchUp(id)
				done = false
			}
		}
		if done {
			break
		}
	}
	for _, id := range []string{"a", "b"} {
		n := sim.Nodes[id]
		if got := n.Watermark("s"); got != total {
			t.Fatalf("%s watermark=%d want %d", id, got, total)
		}
		if len(n.Delivered) != total {
			t.Fatalf("%s delivered=%d want %d (duplicate delivery)", id, len(n.Delivered), total)
		}
		seen := make(map[MessageKey]bool)
		for _, m := range n.Delivered {
			if seen[m.Key()] {
				t.Fatalf("%s delivered %+v twice", id, m.Key())
			}
			seen[m.Key()] = true
		}
	}
	sim.CollectAcks()
	sim.CompactAll()
	if got := sim.Sources["s"].Retained(); got != 0 {
		t.Fatalf("retained=%d want 0 after full ack", got)
	}
}
