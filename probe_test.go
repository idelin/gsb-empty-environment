package netsec

import (
	"net/netip"
	"testing"
	"time"
)

func netipMustAddr(s string) netip.Addr { return netip.MustParseAddr(s) }

func TestProbeTimeoutResolutionFailure(t *testing.T) {
	cfg := testConfig() // 1s interval, 3 probes
	sw, clock := newTestSwitch(t, cfg)

	sw.Grant(Ticket{IP: testIP, MAC: macA, Generation: 1})
	if err := sw.StartResolution(testIP); err != nil {
		t.Fatalf("start resolution: %v", err)
	}
	if got := sw.State(testIP); got != StateIncomplete {
		t.Fatalf("state = %s, want incomplete", got)
	}

	// Two probe timeouts are tolerated; frames flood while unresolved.
	for i := 0; i < 2; i++ {
		clock.advance(cfg.ProbeInterval)
		sw.Tick()
	}
	if got := sw.State(testIP); got != StateIncomplete {
		t.Fatalf("after 2 probes state = %s, want incomplete", got)
	}
	if v, _ := sw.Forward(testIP); v != VerdictFlood {
		t.Fatalf("unresolved must flood, got %s", v)
	}

	// Third unanswered probe exhausts resolution.
	clock.advance(cfg.ProbeInterval)
	sw.Tick()
	view, _ := sw.Snapshot(testIP)
	if view.State != StateIncomplete || view.Probes != 0 {
		t.Fatalf("after exhaustion: state=%s probes=%d", view.State, view.Probes)
	}
	found := false
	for _, e := range sw.Events() {
		if e.Type == EventResolutionFailed {
			found = true
		}
	}
	if !found {
		t.Fatal("expected a resolution_failed event")
	}

	// An answer arriving afterwards (ticket still valid) succeeds.
	if r := sw.ObserveARP(testIP, macA, port1); r != ReasonNone {
		t.Fatalf("late answer rejected: %s", r)
	}
	if v, p := sw.Forward(testIP); v != VerdictForward || p != port1 {
		t.Fatalf("post-resolution forwarding wrong: %s/%d", v, p)
	}
}

func TestReachableAgingAndRevalidationFailure(t *testing.T) {
	cfg := testConfig()
	sw, clock := newTestSwitch(t, cfg)

	sw.Grant(Ticket{IP: testIP, MAC: macA, Generation: 1})
	sw.ObserveARP(testIP, macA, port1)

	clock.advance(cfg.ReachableTTL)
	sw.Tick()
	if got := sw.State(testIP); got != StateStale {
		t.Fatalf("after TTL state = %s, want stale", got)
	}
	// Stale bindings keep their last port but are flooded, not forwarded.
	if v, _ := sw.Forward(testIP); v != VerdictFlood {
		t.Fatalf("stale must flood, got %s", v)
	}

	if err := sw.Revalidate(testIP); err != nil {
		t.Fatalf("revalidate: %v", err)
	}
	if got := sw.State(testIP); got != StateProbe {
		t.Fatalf("state = %s, want probe", got)
	}

	// A confirmation during probing restores reachability on the observed port.
	clock.advance(500 * time.Millisecond)
	sw.ObserveARP(testIP, macA, port3)
	if got := sw.State(testIP); got != StateReachable {
		t.Fatalf("state after probe answer = %s", got)
	}
	if v, p := sw.Forward(testIP); v != VerdictForward || p != port3 {
		t.Fatalf("revalidated path wrong: %s/%d", v, p)
	}

	// Now exhaust revalidation: aging -> stale -> probe -> no answer.
	clock.advance(cfg.ReachableTTL)
	sw.Tick()
	if err := sw.Revalidate(testIP); err != nil {
		t.Fatalf("second revalidate: %v", err)
	}
	for i := uint8(0); i < cfg.MaxProbes; i++ {
		clock.advance(cfg.ProbeInterval)
		sw.Tick()
	}
	view, _ := sw.Snapshot(testIP)
	if view.State != StateIncomplete || view.HasBinding {
		t.Fatalf("failed revalidation must drop binding, got %+v", view)
	}
	if v, _ := sw.Forward(testIP); v != VerdictFlood {
		t.Fatalf("must flood after binding dropped, got %s", v)
	}
}

func TestRevalidationGuards(t *testing.T) {
	sw, _ := newTestSwitch(t, testConfig())

	if err := sw.Revalidate(netipMustAddr("10.9.9.9")); err != ErrUnknownNeighbor {
		t.Fatalf("unknown: got %v", err)
	}
	sw.Grant(Ticket{IP: testIP, MAC: macA, Generation: 1})
	if err := sw.Revalidate(testIP); err != ErrNotStale {
		t.Fatalf("non-stale: got %v, want ErrNotStale", err)
	}
	sw.ObserveARP(testIP, macB, port2) // conflict -> quarantine
	if err := sw.Revalidate(testIP); err != ErrQuarantined {
		t.Fatalf("quarantined: got %v, want ErrQuarantined", err)
	}

	sw2, _ := newTestSwitch(t, testConfig())
	sw2.Grant(Ticket{IP: testIP, MAC: macA, Generation: 1})
	if err := sw2.StartResolution(testIP); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := sw2.StartResolution(testIP); err != ErrAlreadyBound {
		t.Fatalf("bound start: got %v, want ErrAlreadyBound", err)
	}
	sw2.Revoke(testIP)
	if err := sw2.StartResolution(testIP); err != ErrNoTicket {
		t.Fatalf("no ticket: got %v, want ErrNoTicket", err)
	}
}
