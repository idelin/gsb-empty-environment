package gossip

import (
	"math"
	"testing"
)

// The dissemination queue must prefer events with the fewest transmits.
func TestPiggybackPrefersLeastTransmitted(t *testing.T) {
	d := NewDisseminator(100)
	hot := Event{Type: Join, Node: "hot"}
	cold := Event{Type: Join, Node: "cold"}
	d.Queue(hot)
	d.Queue(cold)

	// Both events emitted once; a fresh event with 0 transmits must win,
	// and among equal transmits the tie is broken by node name.
	d.Piggyback(2)                          // cold=1, hot=1
	d.Queue(Event{Type: Join, Node: "aaa"}) // fresh, 0 transmits

	got := d.Piggyback(2)
	if got[0].Node != "aaa" {
		t.Fatalf("expected least-transmitted event first, got %v", got)
	}
	if d.Transmits(cold) != 2 || d.Transmits(hot) != 1 {
		t.Fatalf("tie should go to cold (name order), cold=%d hot=%d",
			d.Transmits(cold), d.Transmits(hot))
	}
}

// Events must be evicted after Lambda*log2(N) transmissions.
func TestMaxRoundsEviction(t *testing.T) {
	n := 100
	d := NewDisseminator(n)
	want := MaxRounds(n) // ceil(3 * log2(100)) = 20
	if want != int(math.Ceil(float64(Lambda)*math.Log2(float64(n)))) {
		t.Fatalf("MaxRounds(%d) = %d, unexpected", n, want)
	}
	e := Event{Type: Join, Node: "x"}
	d.Queue(e)
	for i := 0; i < want; i++ {
		if got := d.Piggyback(1); len(got) != 1 {
			t.Fatalf("round %d: expected event, got none", i)
		}
	}
	if d.Pending() != 0 {
		t.Fatalf("event should be evicted after %d rounds, pending=%d", want, d.Pending())
	}
	if got := d.Piggyback(1); len(got) != 0 {
		t.Fatalf("evicted event must not be piggybacked again, got %v", got)
	}
}

// Acceptance boundary: a new node's JOIN is piggybacked on Ping/Ack and
// reaches all 100 nodes within O(log N) gossip periods.
func TestJoinConvergesWithinLogN(t *testing.T) {
	const n = 100
	cluster := NewCluster(n, 42)
	newbie := cluster.AddNode("node-new")

	limit := int(math.Ceil(math.Log2(n))) + 2 // log2(100) ~ 6.6, small slack
	periods := 0
	for !cluster.AllKnow(newbie.ID) {
		cluster.RunPeriod()
		periods++
		if periods > limit {
			t.Fatalf("not converged after %d periods (limit %d): aware=%d/%d",
				periods, limit, cluster.AwareCount(newbie.ID), n+1)
		}
	}
	t.Logf("converged: all %d nodes know %s after %d periods (log2(N)=%.1f)",
		n+1, newbie.ID, periods, math.Log2(n))

	for _, node := range cluster.Nodes {
		m, ok := node.MemberView()[newbie.ID]
		if !ok || m.State != Alive {
			t.Fatalf("%s has wrong view of %s: %+v", node.ID, newbie.ID, m)
		}
	}
}

// LEAVE and SUSPECT events must also propagate and update view tables.
func TestLeaveAndSuspectPropagate(t *testing.T) {
	const n = 32
	cluster := NewCluster(n, 7)
	victim := cluster.Nodes[3]
	victim.Leave()
	cluster.Nodes[5].SuspectNode(cluster.Nodes[9].ID)

	limit := int(math.Ceil(math.Log2(n))) * 3
	for p := 0; p < limit; p++ {
		cluster.RunPeriod()
	}
	for _, node := range cluster.Nodes {
		if node == victim {
			continue
		}
		if got := node.MemberView()[victim.ID].State; got != Left {
			t.Fatalf("%s sees %s as %s, want left", node.ID, victim.ID, got)
		}
		if got := node.MemberView()[cluster.Nodes[9].ID].State; got != Suspected {
			t.Fatalf("%s sees %s as %s, want suspected", node.ID, cluster.Nodes[9].ID, got)
		}
	}
}

// Stale events (older incarnation) must not overwrite fresher state.
func TestStaleEventIgnored(t *testing.T) {
	n := NewNode("a", 4)
	n.members["b"] = Member{ID: "b", State: Alive, Incarnation: 5}
	if n.applyEvent(Event{Type: Suspect, Node: "b", Incarnation: 3}) {
		t.Fatal("stale suspect event should be ignored")
	}
	if got := n.members["b"].State; got != Alive {
		t.Fatalf("state changed by stale event: %s", got)
	}
}
