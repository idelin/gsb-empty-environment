package matrixclock

import (
	"fmt"
	"testing"
)

// runRound lets every node perform one local event and broadcast to all
// peers, then delivers every queued message.
func runRound(h *Hub, nodes []*Node, round int) {
	for _, n := range nodes {
		n.LocalEvent(fmt.Sprintf("round-%d-local", round))
		for _, p := range nodes {
			if p.ID != n.ID {
				h.Send(n.Send(p.ID, fmt.Sprintf("round-%d-%s->%s", round, n.ID, p.ID)))
			}
		}
	}
	h.DeliverAll()
}

func newCluster() (*Hub, []*Node) {
	ids := []string{"A", "B", "C"}
	nodes := make([]*Node, len(ids))
	for i, id := range ids {
		peers := make([]string, 0, len(ids)-1)
		for _, other := range ids {
			if other != id {
				peers = append(peers, other)
			}
		}
		nodes[i] = NewNode(id, peers)
	}
	return NewHub(nodes...), nodes
}

// Acceptance: a 3-node cluster communicates for several rounds; node A
// uses its matrix clock to prove that every event before LSN=10 has been
// received and processed by every node, then physically truncates its log.
func TestThreeNodeClusterGarbageCollection(t *testing.T) {
	hub, nodes := newCluster()
	a := nodes[0]

	// Round 1: nothing can be safe yet, peers have not reported back.
	runRound(hub, nodes, 1)
	if safe := a.SafeLSN(); safe != 0 {
		t.Fatalf("after round 1 SafeLSN = %d, want 0 (no acknowledgements yet)", safe)
	}

	// Round 2: A's matrix only reflects round-1 acknowledgements, so the
	// watermark still lags far behind LSN 10.
	runRound(hub, nodes, 2)
	if safe := a.SafeLSN(); safe >= 10 {
		t.Fatalf("after round 2 SafeLSN = %d, LSN 10 must not be collectable yet", safe)
	}

	// Rounds 3-4: every node's matrix snapshot propagates further, and
	// A can eventually prove all events up to LSN 10 are acknowledged
	// cluster-wide.
	runRound(hub, nodes, 3)
	runRound(hub, nodes, 4)
	safe := a.SafeLSN()
	if safe < 10 {
		t.Fatalf("after round 4 SafeLSN = %d, want >= 10", safe)
	}

	// Safety proof: every entry at or below the watermark is covered by
	// the per-column minimum of A's matrix clock.
	ack := a.GCWatermark()
	for _, ev := range a.Log() {
		if ev.LSN > safe {
			break
		}
		if ev.Seq > ack[ev.Origin] {
			t.Fatalf("unsafe truncation: %v not covered by watermark %v", ev, ack)
		}
	}
	t.Logf("A's GC watermark (acknowledged by ALL nodes): %v", ack)

	truncated := a.Truncate()
	if truncated < 10 {
		t.Fatalf("truncated up to LSN %d, want >= 10", truncated)
	}
	for _, ev := range a.Log() {
		if ev.LSN < 10 {
			t.Fatalf("log entry %v survived truncation below LSN 10", ev)
		}
	}
	if a.BaseLSN() != truncated+1 {
		t.Fatalf("BaseLSN = %d, want %d", a.BaseLSN(), truncated+1)
	}
	t.Logf("A physically truncated log through LSN %d; %d entries remain (LSN %d+)",
		truncated, len(a.Log()), a.BaseLSN())
}

// The GC watermark must never claim an event that some node has not seen:
// a node that stays silent keeps the watermark at 0 for its column.
func TestSilentNodeBlocksGarbageCollection(t *testing.T) {
	hub, nodes := newCluster()
	a, b := nodes[0], nodes[1]

	// A and B talk; C never sends anything.
	for round := 1; round <= 3; round++ {
		a.LocalEvent("ping")
		hub.Send(a.Send("B", "ping"))
		b.LocalEvent("pong")
		hub.Send(b.Send("A", "pong"))
		hub.DeliverAll()
	}

	ack := a.GCWatermark()
	if ack["C"] != 0 {
		t.Fatalf("ack[C] = %d, want 0: C never reported its clock", ack["C"])
	}
	// A's log contains only A/B events, which are mutually acknowledged,
	// so those may still be collected even though C is silent.
	for _, ev := range a.Log() {
		if ev.LSN <= a.SafeLSN() && (ev.Origin != "A" && ev.Origin != "B") {
			t.Fatalf("collected event from unacknowledged origin: %v", ev)
		}
	}
}
