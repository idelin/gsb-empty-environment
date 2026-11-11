package netsec

import (
	"testing"
	"time"
)

// TestTwoMACCompetition drives two MACs fighting for one IP. Only the MAC
// named by the highest-generation ticket may own the binding; the loser is
// quarantined with MAC-conflict evidence.
func TestTwoMACCompetition(t *testing.T) {
	sw, _ := newTestSwitch(t, testConfig())

	if err := sw.Grant(Ticket{IP: testIP, MAC: macA, Generation: 1}); err != nil {
		t.Fatalf("grant A: %v", err)
	}
	if r := sw.ObserveARP(testIP, macA, port1); r != ReasonNone {
		t.Fatalf("confirm A: %s", r)
	}
	if v, p := sw.Forward(testIP); v != VerdictForward || p != port1 {
		t.Fatalf("A binding wrong: %s/%d", v, p)
	}

	// MAC B attacks without a ticket: it must not steal the IP.
	if r := sw.ObserveARP(testIP, macB, port2); r != ReasonMACNotAllowed {
		// No ticket for B and an active ticket for A exists: not-allowed.
		if r != ReasonNoTicket {
			t.Fatalf("B attack reason = %s, want rejection", r)
		}
	}
	if got := sw.State(testIP); got != StateQuarantined {
		t.Fatalf("state after conflict = %s, want quarantined", got)
	}
	if v, _ := sw.Forward(testIP); v != VerdictDrop {
		t.Fatalf("conflict must drop frames, got %s", v)
	}

	ev := sw.Evidence(testIP)
	last := ev[len(ev)-1]
	if last.Kind != EvidenceMACConflict || last.ObservedMAC != macB || last.AllowedMAC != macA {
		t.Fatalf("conflict evidence wrong: %+v", last)
	}

	// Control plane authorizes the migration of B with a newer generation.
	if err := sw.Grant(Ticket{IP: testIP, MAC: macB, Generation: 2}); err != nil {
		t.Fatalf("grant B gen2: %v", err)
	}
	if got := sw.State(testIP); got != StateIncomplete {
		t.Fatalf("state after migration ticket = %s, want incomplete", got)
	}
	if r := sw.ObserveARP(testIP, macB, port2); r != ReasonNone {
		t.Fatalf("confirm B: %s", r)
	}
	view, _ := sw.Snapshot(testIP)
	if view.BoundMAC != macB || view.BoundPort != port2 || view.BoundGen != 2 {
		t.Fatalf("new binding wrong: %+v", view)
	}
	if v, p := sw.Forward(testIP); v != VerdictForward || p != port2 {
		t.Fatalf("frames must now follow B: %s/%d", v, p)
	}
}

// TestMigrationFramePath pins down where data frames go at every step of a
// legal migration: flood before confirmation, pinned forwarding afterwards,
// and drop only when a conflict quarantines the IP.
func TestMigrationFramePath(t *testing.T) {
	sw, _ := newTestSwitch(t, testConfig())

	sw.Grant(Ticket{IP: testIP, MAC: macA, Generation: 1})
	if v, _ := sw.Forward(testIP); v != VerdictFlood {
		t.Fatalf("pre-confirm: want flood, got %s", v)
	}
	sw.ObserveARP(testIP, macA, port1)
	if v, p := sw.Forward(testIP); v != VerdictForward || p != port1 {
		t.Fatalf("A reachable: want forward:1, got %s/%d", v, p)
	}

	// New ticket starts the migration window. The old binding keeps carrying
	// traffic to port1 until the new MAC proves itself.
	sw.Grant(Ticket{IP: testIP, MAC: macB, Generation: 2})
	if v, p := sw.Forward(testIP); v != VerdictForward || p != port1 {
		t.Fatalf("migration window must keep old path, got %s/%d", v, p)
	}

	// A late confirmation from the old holder is unauthorized now. It must be
	// rejected as a conflict, quarantine the IP, and never overwrite gen2.
	if r := sw.ObserveARP(testIP, macA, port1); r == ReasonNone {
		t.Fatal("late confirmation from old MAC must be rejected")
	}
	if got := sw.State(testIP); got != StateQuarantined {
		t.Fatalf("late confirm state = %s, want quarantined", got)
	}
	if v, _ := sw.Forward(testIP); v != VerdictDrop {
		t.Fatalf("post-conflict: want drop, got %s", v)
	}
	ev := sw.Evidence(testIP)
	if ev[len(ev)-1].Kind != EvidenceLateConfirm {
		t.Fatalf("late confirm must retain late_confirm evidence: %+v", ev[len(ev)-1])
	}

	// Only the control plane can re-open the neighbor, after which B confirms.
	sw.Grant(Ticket{IP: testIP, MAC: macB, Generation: 3})
	sw.ObserveARP(testIP, macB, port2)
	if v, p := sw.Forward(testIP); v != VerdictForward || p != port2 {
		t.Fatalf("post-migration path wrong: %s/%d", v, p)
	}
	view, _ := sw.Snapshot(testIP)
	if view.BoundGen != 3 || view.BoundMAC != macB {
		t.Fatalf("final binding wrong: %+v", view)
	}
}

func TestControlPlaneRevocation(t *testing.T) {
	sw, _ := newTestSwitch(t, testConfig())

	sw.Grant(Ticket{IP: testIP, MAC: macA, Generation: 1})
	sw.ObserveARP(testIP, macA, port1)
	if err := sw.Revoke(testIP); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	view, _ := sw.Snapshot(testIP)
	if view.HasTicket || view.State != StateStale {
		t.Fatalf("after revoke: ticket=%v state=%s, want no ticket/stale", view.HasTicket, view.State)
	}
	// Stale binding cannot be re-confirmed without a fresh ticket.
	if r := sw.ObserveARP(testIP, macA, port1); r == ReasonNone {
		t.Fatal("ARP after revocation must be rejected")
	}
	if got := sw.State(testIP); got != StateQuarantined {
		t.Fatalf("state after post-revoke ARP = %s, want quarantined", got)
	}

	// Revoking an unknown IP is an error.
	if err := sw.Revoke(netipMustAddr("10.0.0.99")); err != ErrUnknownNeighbor {
		t.Fatalf("got %v, want ErrUnknownNeighbor", err)
	}
}

func TestTicketExpiryFlipsToStaleAndFloods(t *testing.T) {
	sw, clock := newTestSwitch(t, testConfig())

	sw.Grant(Ticket{IP: testIP, MAC: macA, Generation: 1, TTL: 5 * time.Second})
	sw.ObserveARP(testIP, macA, port1)

	clock.advance(4 * time.Second)
	sw.Tick()
	if v, p := sw.Forward(testIP); v != VerdictForward || p != port1 {
		t.Fatalf("before TTL expiry path wrong: %s/%d", v, p)
	}

	clock.advance(2 * time.Second) // total 6s > 5s TTL
	sw.Tick()
	view, _ := sw.Snapshot(testIP)
	if view.HasTicket {
		t.Fatal("ticket must have expired")
	}
	if view.State != StateStale {
		t.Fatalf("state after expiry = %s, want stale", view.State)
	}
	if v, _ := sw.Forward(testIP); v != VerdictFlood {
		t.Fatalf("stale neighbor must flood, got %s", v)
	}

	// Expired ticket cannot authorize a confirmation.
	if r := sw.ObserveARP(testIP, macA, port1); r == ReasonNone {
		t.Fatal("confirmation under expired ticket must fail")
	}
	if got := sw.State(testIP); got != StateQuarantined {
		t.Fatalf("state = %s, want quarantined", got)
	}
}

func TestRevocationPreservesQuarantineAndEvidence(t *testing.T) {
	sw, _ := newTestSwitch(t, testConfig())

	sw.Grant(Ticket{IP: testIP, MAC: macA, Generation: 1})
	sw.ObserveARP(testIP, macA, port1)
	sw.ObserveARP(testIP, macB, port2) // conflict -> quarantine
	if got := sw.State(testIP); got != StateQuarantined {
		t.Fatalf("setup state = %s", got)
	}

	if err := sw.Revoke(testIP); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if got := sw.State(testIP); got != StateQuarantined {
		t.Fatalf("revoke must not clear quarantine, got %s", got)
	}
	if len(sw.Evidence(testIP)) == 0 {
		t.Fatal("evidence must survive revocation")
	}
	if v, _ := sw.Forward(testIP); v != VerdictDrop {
		t.Fatalf("quarantined frames must still drop, got %s", v)
	}
}
