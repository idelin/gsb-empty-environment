package netsec

import (
	"net/netip"
	"testing"
	"time"
)

const (
	macA  = MAC("02:00:00:00:00:01")
	macB  = MAC("02:00:00:00:00:02")
	macC  = MAC("02:00:00:00:00:03")
	port1 = PortID(1)
	port2 = PortID(2)
	port3 = PortID(3)
)

var testIP = netip.MustParseAddr("10.0.0.10")

type fakeClock struct {
	t time.Time
}

func (f *fakeClock) now() time.Time { return f.t }

func (f *fakeClock) advance(d time.Duration) { f.t = f.t.Add(d) }

func newTestSwitch(t *testing.T, cfg Config) (*Switch, *fakeClock) {
	t.Helper()
	clock := &fakeClock{t: time.Unix(1_000_000, 0)}
	return NewSwitchWithClock(cfg, clock.now), clock
}

func testConfig() Config {
	return Config{
		ReachableTTL:     10 * time.Second,
		ProbeInterval:    time.Second,
		MaxProbes:        3,
		MaxEvidencePerIP: 16,
		MaxEvents:        1024,
	}
}

func mustMAC(t *testing.T, s string) MAC {
	t.Helper()
	m, err := ParseMAC(s)
	if err != nil {
		t.Fatalf("ParseMAC(%q): %v", s, err)
	}
	return m
}

func TestParseMACCanonicalizes(t *testing.T) {
	m, err := ParseMAC("AA:BB:CC:DD:EE:FF")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m != MAC("aa:bb:cc:dd:ee:ff") {
		t.Fatalf("got %q, want canonical lowercase", m)
	}
	if _, err := ParseMAC("not-a-mac"); err != ErrInvalidMAC {
		t.Fatalf("got %v, want ErrInvalidMAC", err)
	}
	if MAC("01:02").Valid() {
		t.Fatal("short string must not be a valid MAC")
	}
}

func TestAuthorizedLifecycleAndForwarding(t *testing.T) {
	sw, _ := newTestSwitch(t, testConfig())

	if err := sw.Grant(Ticket{IP: testIP, MAC: macA, Generation: 1}); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if got := sw.State(testIP); got != StateIncomplete {
		t.Fatalf("state after grant = %s, want incomplete", got)
	}
	if v, _ := sw.Forward(testIP); v != VerdictFlood {
		t.Fatalf("unconfirmed neighbor must flood, got %s", v)
	}

	if r := sw.ObserveARP(testIP, macA, port1); r != ReasonNone {
		t.Fatalf("authorized ARP rejected: %s", r)
	}
	view, _ := sw.Snapshot(testIP)
	if view.State != StateReachable || view.BoundMAC != macA || view.BoundPort != port1 || view.BoundGen != 1 {
		t.Fatalf("unexpected view after confirm: %+v", view)
	}
	if v, p := sw.Forward(testIP); v != VerdictForward || p != port1 {
		t.Fatalf("reachable neighbor must forward to port1, got %s/%d", v, p)
	}

	// Re-confirmation on another port moves the binding.
	if r := sw.ObserveARP(testIP, macA, port2); r != ReasonNone {
		t.Fatalf("re-confirm rejected: %s", r)
	}
	if v, p := sw.Forward(testIP); v != VerdictForward || p != port2 {
		t.Fatalf("binding move failed, got %s/%d", v, p)
	}
}

func TestUnauthorizedARPWithoutTicketQuarantines(t *testing.T) {
	sw, _ := newTestSwitch(t, testConfig())

	r := sw.ObserveARP(testIP, macA, port1)
	if r != ReasonNoTicket {
		t.Fatalf("got reason %s, want no_ticket", r)
	}
	if got := sw.State(testIP); got != StateQuarantined {
		t.Fatalf("state = %s, want quarantined", got)
	}
	if v, _ := sw.Forward(testIP); v != VerdictDrop {
		t.Fatalf("quarantined neighbor must drop, got %s", v)
	}
	ev := sw.Evidence(testIP)
	if len(ev) != 1 || ev[0].Kind != EvidenceUnauthorizedARP || ev[0].ObservedMAC != macA {
		t.Fatalf("unexpected evidence: %+v", ev)
	}

	// Data-plane noise can never escape quarantine.
	if r := sw.ObserveARP(testIP, macB, port2); r != ReasonQuarantined {
		t.Fatalf("got %s, want quarantined", r)
	}
	if got := sw.State(testIP); got != StateQuarantined {
		t.Fatalf("quarantine must be sticky against data plane, got %s", got)
	}

	// A newer trusted ticket lifts quarantine; evidence is retained.
	if err := sw.Grant(Ticket{IP: testIP, MAC: macB, Generation: 1}); err != nil {
		t.Fatalf("re-grant: %v", err)
	}
	if got := sw.State(testIP); got != StateIncomplete {
		t.Fatalf("state after re-grant = %s, want incomplete", got)
	}
	if len(sw.Evidence(testIP)) != 2 {
		t.Fatal("forensic evidence must survive re-grant")
	}
	if r := sw.ObserveARP(testIP, macB, port2); r != ReasonNone {
		t.Fatalf("authorized recovery ARP rejected: %s", r)
	}
	if v, p := sw.Forward(testIP); v != VerdictForward || p != port2 {
		t.Fatalf("recovery forwarding failed, got %s/%d", v, p)
	}
}

func TestStaleTicketGenerationsRejected(t *testing.T) {
	sw, _ := newTestSwitch(t, testConfig())

	if err := sw.Grant(Ticket{IP: testIP, MAC: macB, Generation: 2}); err != nil {
		t.Fatalf("grant gen2: %v", err)
	}
	before, _ := sw.Snapshot(testIP)

	for _, gen := range []uint64{2, 1} {
		err := sw.Grant(Ticket{IP: testIP, MAC: macA, Generation: gen})
		if err != ErrStaleTicket {
			t.Fatalf("generation %d: got %v, want ErrStaleTicket", gen, err)
		}
	}
	after, _ := sw.Snapshot(testIP)
	if after != before {
		t.Fatalf("stale grant mutated state:\nbefore=%+v\nafter =%+v", before, after)
	}

	// Invalid inputs are rejected before touching the table.
	if err := sw.Grant(Ticket{IP: netip.Addr{}, MAC: macA, Generation: 3}); err != ErrInvalidIP {
		t.Fatalf("got %v, want ErrInvalidIP", err)
	}
	if err := sw.Grant(Ticket{IP: testIP, MAC: MAC("zz"), Generation: 3}); err != ErrInvalidMAC {
		t.Fatalf("got %v, want ErrInvalidMAC", err)
	}
}

func TestEvidenceRingIsBoundedAndDrainable(t *testing.T) {
	cfg := testConfig()
	cfg.MaxEvidencePerIP = 2
	sw, _ := newTestSwitch(t, cfg)

	for i := 0; i < 4; i++ {
		sw.ObserveARP(testIP, macA, PortID(i+1))
	}
	ev := sw.Evidence(testIP)
	if len(ev) != 2 {
		t.Fatalf("ring bound: got %d records, want 2", len(ev))
	}
	if ev[0].ObservedPort != port3 || ev[1].ObservedPort != PortID(4) {
		t.Fatalf("ring must retain newest records, got %+v", ev)
	}
	if drained := sw.DrainEvidence(testIP); len(drained) != 2 {
		t.Fatalf("drain returned %d records", len(drained))
	}
	if len(sw.Evidence(testIP)) != 0 {
		t.Fatal("drain must clear the evidence ring")
	}
}

func TestConcurrentControlAndDataPlane(t *testing.T) {
	sw, _ := newTestSwitch(t, testConfig())
	done := make(chan struct{})

	go func() {
		defer close(done)
		for g := uint64(1); g <= 200; g++ {
			mac := macA
			if g%2 == 0 {
				mac = macB
			}
			sw.Grant(Ticket{IP: testIP, MAC: mac, Generation: g})
		}
	}()

	for i := 0; i < 200; i++ {
		sw.ObserveARP(testIP, macA, port1)
		sw.ObserveARP(testIP, macB, port2)
		sw.Forward(testIP)
		sw.Snapshot(testIP)
	}
	<-done

	// Final ticket must be the highest generation with its authorized MAC.
	view, _ := sw.Snapshot(testIP)
	if view.TicketGen != 200 || view.TicketMAC != macB {
		t.Fatalf("final ticket = gen%d/%s, want gen200/%s", view.TicketGen, view.TicketMAC, macB)
	}
}
