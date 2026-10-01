package main

import (
	"net/netip"
	"strings"
	"testing"
)

func allowAllClassifier() Classifier {
	return NewClassifier([]Rule{
		rule(1, 10, "0.0.0.0/0", ProtoAny, 0, 65535, Allow),
	})
}

func pkt(id int, dst string, port int) Packet {
	return Packet{ID: id, Dst: netip.MustParseAddr(dst), Proto: 6, DstPort: port}
}

func TestDeliverOrderAndTiming(t *testing.T) {
	sim := NewSim(allowAllClassifier(), &Link{Interval: 2, Delay: 3})
	sim.Inject(0, pkt(1, "10.0.0.1", 80))
	sim.Inject(0, pkt(2, "10.0.0.2", 80))
	log := sim.Run(100)
	if sim.Delivered != 2 || sim.Dropped != 0 {
		t.Fatalf("delivered=%d dropped=%d, want 2/0", sim.Delivered, sim.Dropped)
	}
	// pkt#1 transmits at tick 0 -> delivered at 3;
	// pkt#2 transmits at tick 2 -> delivered at 5.
	var d1, d2 string
	for _, line := range log {
		if strings.Contains(line, "pkt#1") && strings.Contains(line, "delivered") {
			d1 = line
		}
		if strings.Contains(line, "pkt#2") && strings.Contains(line, "delivered") {
			d2 = line
		}
	}
	if !strings.HasPrefix(d1, "tick=3") {
		t.Errorf("pkt#1 delivery line = %q, want tick=3", d1)
	}
	if !strings.HasPrefix(d2, "tick=5") {
		t.Errorf("pkt#2 delivery line = %q, want tick=5", d2)
	}
}

func TestDenyAndDefaultDeny(t *testing.T) {
	c := NewClassifier([]Rule{
		rule(1, 10, "10.0.0.0/8", 6, 80, 80, Allow),
		rule(2, 20, "10.0.0.0/8", 6, 0, 65535, Deny),
	})
	sim := NewSim(c, &Link{Interval: 1, Delay: 1})
	sim.Inject(0, pkt(1, "10.0.0.1", 80))  // allowed
	sim.Inject(1, pkt(2, "10.0.0.1", 443)) // denied by rule #2
	sim.Inject(2, pkt(3, "192.0.2.1", 25)) // no rule -> default deny
	sim.Run(100)
	if sim.Delivered != 1 || sim.Dropped != 2 {
		t.Fatalf("delivered=%d dropped=%d, want 1/2", sim.Delivered, sim.Dropped)
	}
}

func TestQueueFullDrop(t *testing.T) {
	sim := NewSim(allowAllClassifier(), &Link{Interval: 5, Delay: 1, Capacity: 1})
	sim.Inject(0, pkt(1, "10.0.0.1", 80))
	sim.Inject(0, pkt(2, "10.0.0.2", 80)) // queue holds pkt#1 -> dropped
	sim.Run(100)
	if sim.Delivered != 1 || sim.Dropped != 1 {
		t.Fatalf("delivered=%d dropped=%d, want 1/1", sim.Delivered, sim.Dropped)
	}
}

func TestDeterministic(t *testing.T) {
	run := func() []string {
		sim := NewSim(allowAllClassifier(), &Link{Interval: 2, Delay: 3, Capacity: 4})
		for i := 0; i < 6; i++ {
			sim.Inject(i%3, pkt(100+i, "10.0.0.1", 80+i))
		}
		return sim.Run(100)
	}
	a, b := run(), run()
	if len(a) != len(b) {
		t.Fatalf("log length differs: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("log line %d differs:\n%s\n%s", i, a[i], b[i])
		}
	}
}

func TestIPv6PacketThroughSim(t *testing.T) {
	c := NewClassifier([]Rule{
		rule(1, 10, "2001:db8::/32", 6, 443, 443, Allow),
	})
	sim := NewSim(c, &Link{Interval: 1, Delay: 2})
	sim.Inject(0, Packet{ID: 1, Dst: netip.MustParseAddr("2001:db8::5"), Proto: 6, DstPort: 443})
	sim.Inject(0, Packet{ID: 2, Dst: netip.MustParseAddr("10.0.0.1"), Proto: 6, DstPort: 443})
	sim.Run(100)
	if sim.Delivered != 1 || sim.Dropped != 1 {
		t.Fatalf("delivered=%d dropped=%d, want 1/1 (v6 allowed, v4 default-denied)", sim.Delivered, sim.Dropped)
	}
}
