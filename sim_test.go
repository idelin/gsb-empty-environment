package mtusim

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func scenarioData(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + 3)
	}
	return b
}

// A datagram is sent under MTU 128; the path MTU drops to 68 mid-flight,
// oversized fragments are dropped, and the source re-plans with a new epoch
// until the receiver completes the datagram.
func TestSimDeliveryAfterMTUDrop(t *testing.T) {
	sim := &Sim{}
	link := NewLink("L1", 128, 3)
	link.PerFrag = 2
	rx := NewReceiver("R1")
	src := NewSource("S1", link, rx, 128, 20, 8, 25)
	rx.OnDone = func(s *Sim, id int) { src.Ack(id) }

	pkt1 := scenarioData(300)
	pkt2 := scenarioData(60)
	src.Send(sim, 0, 1, pkt1)
	ScheduleMTUChange(sim, 1, link, src, 68)
	src.Send(sim, 50, 2, pkt2)
	sim.Run()

	if !bytes.Equal(rx.Done[1], pkt1) {
		t.Errorf("pkt1: reassembled bytes differ from original")
	}
	if !bytes.Equal(rx.Done[2], pkt2) {
		t.Errorf("pkt2: reassembled bytes differ from original")
	}
	if link.Dropped != 2 {
		t.Errorf("link dropped %d fragments, want 2 (the two oversized epoch-1 frames)", link.Dropped)
	}
	if src.EpochOf(1) != 2 {
		t.Errorf("pkt1 final epoch %d, want 2 (one re-plan after MTU drop)", src.EpochOf(1))
	}
	if sim.Now < 0 {
		t.Errorf("negative tick %d", sim.Now)
	}
	for _, line := range sim.Log {
		if !strings.HasPrefix(line, "tick ") {
			t.Errorf("log line missing tick prefix: %q", line)
		}
	}
}

// Time must advance monotonically in integer ticks.
func TestSimTicksAreMonotoneIntegers(t *testing.T) {
	sim := &Sim{}
	link := NewLink("L1", 64, 2)
	rx := NewReceiver("R1")
	src := NewSource("S1", link, rx, 64, 20, 8, 10)
	rx.OnDone = func(s *Sim, id int) { src.Ack(id) }
	src.Send(sim, 0, 1, scenarioData(200))
	src.Send(sim, 1, 2, scenarioData(33))
	sim.Run()
	last := -1
	for _, line := range sim.Log {
		var tick int
		if _, err := fmt.Sscanf(line, "tick %d", &tick); err != nil {
			t.Fatalf("cannot parse tick from %q", line)
		}
		if tick < last {
			t.Errorf("ticks not monotone: %d after %d", tick, last)
		}
		last = tick
	}
}

// Infeasible MTUs must fail explicitly inside the simulation.
func TestSimInfeasibleMTUFails(t *testing.T) {
	sim := &Sim{}
	link := NewLink("L1", 24, 1)
	rx := NewReceiver("R1")
	src := NewSource("S1", link, rx, 24, 20, 8, 10)
	rx.OnDone = func(s *Sim, id int) { src.Ack(id) }
	src.Send(sim, 0, 1, scenarioData(100)) // aligned unit 0: infeasible
	ScheduleMTUChange(sim, 5, link, src, 10)
	src.Send(sim, 6, 2, scenarioData(40)) // mtu below header
	sim.Run()
	if _, ok := rx.Done[1]; ok {
		t.Errorf("pkt1 should not be delivered under infeasible mtu")
	}
	if _, ok := rx.Done[2]; ok {
		t.Errorf("pkt2 should not be delivered when mtu < header")
	}
	joined := strings.Join(sim.Log, "\n")
	if !strings.Contains(joined, "aligned payload unit 0") {
		t.Errorf("expected explicit infeasibility message, log:\n%s", joined)
	}
	if !strings.Contains(joined, "below minimum header") {
		t.Errorf("expected explicit mtu-below-header message, log:\n%s", joined)
	}
}
