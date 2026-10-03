package swim

import (
	"testing"

	"swimdsim/sim"
)

// TestArbitrationOrdering exercises the arbitration state machine
// directly: updates are totally ordered by (incarnation, state) with
// Dead > Suspect > Alive at equal incarnation.
func TestArbitrationOrdering(t *testing.T) {
	s := sim.New()
	sw := sim.NewSwitch(s, 1, 0, 1, 1)
	n := NewNode(s, sw, DefaultConfig(), "A", []string{"A", "B"})

	step := func(u Update, wantState State, wantInc uint64) {
		t.Helper()
		n.applyUpdate(u)
		got := n.View("B")
		if got.State != wantState || got.Incarnation != wantInc {
			t.Fatalf("after %+v: view=%+v, want %s inc=%d", u, got, wantState, wantInc)
		}
	}

	step(Update{ID: "B", State: Suspect, Incarnation: 0}, Suspect, 0)
	step(Update{ID: "B", State: Alive, Incarnation: 0}, Suspect, 0)   // stale: same inc, weaker state
	step(Update{ID: "B", State: Alive, Incarnation: 1}, Alive, 1)     // newer incarnation wins
	step(Update{ID: "B", State: Suspect, Incarnation: 1}, Suspect, 1) // same inc, stronger state
	step(Update{ID: "B", State: Dead, Incarnation: 1}, Dead, 1)       // dead beats suspect
	step(Update{ID: "B", State: Alive, Incarnation: 1}, Dead, 1)      // cannot override dead at same inc
	step(Update{ID: "B", State: Alive, Incarnation: 2}, Alive, 2)     // only a newer incarnation revives
}

// TestSelfRefutation verifies that a rumor about ourselves is always
// refuted by bumping our incarnation above the rumor.
func TestSelfRefutation(t *testing.T) {
	s := sim.New()
	sw := sim.NewSwitch(s, 1, 0, 1, 1)
	n := NewNode(s, sw, DefaultConfig(), "A", []string{"A", "B"})

	n.applyUpdate(Update{ID: "A", State: Suspect, Incarnation: 5})
	if got := n.View("A"); got.State != Alive || got.Incarnation != 6 {
		t.Fatalf("self view = %+v, want ALIVE inc=6", got)
	}

	// A stale rumor below our incarnation is ignored.
	n.applyUpdate(Update{ID: "A", State: Dead, Incarnation: 3})
	if got := n.View("A"); got.State != Alive || got.Incarnation != 6 {
		t.Fatalf("self view = %+v, want ALIVE inc=6", got)
	}

	// The refutation must be queued for dissemination.
	if len(n.gossip) == 0 || n.gossip[0].u != (Update{ID: "A", State: Alive, Incarnation: 6}) {
		t.Fatalf("gossip buffer = %+v, want Alive refutation inc=6", n.gossip)
	}
}
