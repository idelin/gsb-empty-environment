package gossip

import (
	"math"
	"testing"
)

func fixedSize(n int) func() int { return func() int { return n } }

func TestDisseminatorPrefersLeastPropagated(t *testing.T) {
	d := NewDisseminator(3, fixedSize(16))
	d.Queue(Event{NodeID: "a", State: Alive})
	d.Queue(Event{NodeID: "b", State: Alive})
	d.Queue(Event{NodeID: "c", State: Alive})

	first := d.Piggyback(1)
	if len(first) != 1 || first[0].NodeID != "a" {
		t.Fatalf("expected [a] (tie broken by ID), got %v", first)
	}
	// "a" has been transmitted once; next pick must prefer b and c.
	next := d.Piggyback(2)
	if len(next) != 2 || next[0].NodeID != "b" || next[1].NodeID != "c" {
		t.Fatalf("expected [b c] (least propagated first), got %v", next)
	}
}

func TestDisseminatorMaxRounds(t *testing.T) {
	d := NewDisseminator(3, fixedSize(4))
	want := int(math.Ceil(3 * math.Log2(4+1))) // 7 rounds
	d.Queue(Event{NodeID: "x", State: Suspect, Incarnation: 1})

	for round := 1; round <= want; round++ {
		if got := d.Piggyback(6); len(got) != 1 {
			t.Fatalf("round %d: event dropped too early", round)
		}
	}
	if got := d.Piggyback(6); len(got) != 0 {
		t.Fatalf("event exceeded its %d-round budget", want)
	}
	if d.Pending() != 0 {
		t.Fatalf("event should be evicted after %d rounds", want)
	}
}

func TestDisseminatorOverrideAndStale(t *testing.T) {
	d := NewDisseminator(3, fixedSize(8))
	d.Queue(Event{NodeID: "n1", State: Suspect, Incarnation: 2})
	d.Piggyback(6) // burn one transmission round

	// Newer event replaces the queued one and resets the budget.
	d.Queue(Event{NodeID: "n1", State: Dead, Incarnation: 2})
	got := d.Piggyback(6)
	if len(got) != 1 || got[0].State != Dead {
		t.Fatalf("expected dead to supersede suspect, got %v", got)
	}

	// Stale events are ignored.
	d.Queue(Event{NodeID: "n1", State: Alive, Incarnation: 1})
	d.Queue(Event{NodeID: "n1", State: Suspect, Incarnation: 2})
	got = d.Piggyback(6)
	if len(got) != 1 || got[0].State != Dead {
		t.Fatalf("stale event must not override, got %v", got)
	}
}

func TestNodeRefutesSuspicion(t *testing.T) {
	n := NewNode("self", "127.0.0.1:1", NewDisseminator(3, fixedSize(4)))
	changed := n.Apply(Event{NodeID: "self", State: Suspect, Incarnation: 0})
	if !changed {
		t.Fatal("suspicion about self should trigger a refutation")
	}
	if n.Incarnation() != 1 {
		t.Fatalf("incarnation should bump to 1, got %d", n.Incarnation())
	}
	m, _ := n.Member("self")
	if m.State != Alive || m.Incarnation != 1 {
		t.Fatalf("self should be alive@1, got %v@%d", m.State, m.Incarnation)
	}
	// The refutation itself must be queued for gossip.
	if got := n.GossipEvents(6); len(got) != 1 || got[0].State != Alive || got[0].Incarnation != 1 {
		t.Fatalf("expected alive@1 refutation queued, got %v", got)
	}
}

func TestNodeIgnoresStaleEvents(t *testing.T) {
	n := NewNode("self", "127.0.0.1:1", NewDisseminator(3, fixedSize(4)))
	n.Apply(Event{NodeID: "peer", Addr: "127.0.0.1:2", State: Suspect, Incarnation: 3})
	if n.Apply(Event{NodeID: "peer", State: Alive, Incarnation: 2}) {
		t.Fatal("older incarnation must be ignored")
	}
	if n.Apply(Event{NodeID: "peer", State: Alive, Incarnation: 3}) {
		t.Fatal("weaker state at equal incarnation must be ignored")
	}
	m, _ := n.Member("peer")
	if m.State != Suspect || m.Incarnation != 3 {
		t.Fatalf("view corrupted by stale event: %v@%d", m.State, m.Incarnation)
	}
}

// Acceptance boundary: after a new node joins, its change event is
// piggybacked on subsequent Ping headers and every one of the 100 cluster
// nodes learns it within ceil(log2(N)) protocol periods.
func TestJoinConvergesWithinLogN(t *testing.T) {
	const total = 100
	c := NewCluster(total-1, 3, 6, 42)
	joined := c.Join(0) // node-099 joins via seed node-000

	maxPeriods := int(math.Ceil(math.Log2(total))) // 7
	periods := 0
	for c.InformedCount(joined.ID) < total {
		if periods >= maxPeriods {
			t.Fatalf("not converged after %d periods: only %d/%d nodes informed",
				periods, c.InformedCount(joined.ID), total)
		}
		c.RunPeriod()
		periods++
	}
	t.Logf("all %d nodes learned the join of %s after %d periods (budget %d)",
		total, joined.ID, periods, maxPeriods)

	// Every node's view table must record the joiner as alive.
	for _, n := range c.Nodes {
		m, ok := n.Member(joined.ID)
		if !ok || m.State != Alive {
			t.Fatalf("%s missing joiner in view table", n.ID)
		}
	}
}

// A suspect event about a peer must also spread cluster-wide via piggyback.
func TestSuspectEventConverges(t *testing.T) {
	const total = 100
	c := NewCluster(total, 3, 6, 7)
	victim := c.Nodes[10]
	c.Nodes[0].Apply(Event{NodeID: victim.ID, Addr: victim.Addr, State: Suspect, Incarnation: 0})

	// The victim refutes; everyone must end up with a consistent entry for it.
	for period := 0; period < 12; period++ {
		c.RunPeriod()
	}
	consistent := 0
	for _, n := range c.Nodes {
		if _, ok := n.Member(victim.ID); ok {
			consistent++
		}
	}
	if consistent != total {
		t.Fatalf("only %d/%d nodes have an entry for the suspected node", consistent, total)
	}
}
