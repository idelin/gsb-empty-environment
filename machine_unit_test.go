package netsec

import (
	"net/netip"
	"strings"
	"testing"
)

func TestParseMACAndString(t *testing.T) {
	mac, err := ParseMAC("de-ad-be-ef-00-01")
	if err != nil {
		t.Fatalf("parse hyphen form: %v", err)
	}
	if got := mac.String(); got != "de:ad:be:ef:00:01" {
		t.Fatalf("String() = %q, want de:ad:be:ef:00:01", got)
	}
	if mac.IsZero() {
		t.Fatal("non-zero MAC reported as zero")
	}

	var zero MAC
	if !zero.IsZero() || zero.String() != "00:00:00:00:00:00" {
		t.Fatalf("zero MAC renders as %q zero=%v", zero.String(), zero.IsZero())
	}

	if _, err := ParseMAC("not-a-mac"); err == nil {
		t.Fatal("invalid MAC accepted")
	}
	// EUI-64 length must be rejected: this state machine is Ethernet only.
	if _, err := ParseMAC("00:00:00:00:00:00:00:00"); err == nil {
		t.Fatal("8-byte hardware address accepted as MAC-48")
	}
}

func TestStringersAndUnknownStates(t *testing.T) {
	if got := State(99).String(); got != "unknown(99)" {
		t.Fatalf("unknown state renders as %q", got)
	}
	if got := EventKind(99).String(); got != "unknown(99)" {
		t.Fatalf("unknown event renders as %q", got)
	}
	if got := ForwardAction(99).String(); got != "unknown(99)" {
		t.Fatalf("unknown action renders as %q", got)
	}

	names := []string{
		StateEmpty.String(), StateBound.String(), StateMigrating.String(), StateQuarantined.String(),
		EventTicketGranted.String(), EventMigrationConfirmed.String(), EventArpRefresh.String(),
		EventArpDenied.String(), EventArpConflict.String(), EventProbeTimeout.String(),
		EventTicketRevoked.String(), EventTicketReleased.String(), EventTicketRejected.String(),
		EventQuarantined.String(),
		ForwardDeliver.String(), ForwardDrop.String(),
	}
	for _, name := range names {
		if name == "" || strings.HasPrefix(name, "unknown") {
			t.Fatalf("known enum rendered as %q", name)
		}
	}
}

func TestInvalidArgumentsAndUnknownIPs(t *testing.T) {
	m, _ := newTestMachine()
	badIP := netip.Addr{}

	assertErrIs(t, m.Grant(Ticket{IP: badIP, NewMAC: macA, Generation: 1}), ErrInvalidArgument)
	assertErrIs(t, m.Grant(Ticket{IP: ip1, NewMAC: MAC{}, Generation: 1}), ErrInvalidArgument)
	assertErrIs(t, m.HandleArpClaim(badIP, macA), ErrInvalidArgument)
	assertErrIs(t, m.HandleArpClaim(ip1, MAC{}), ErrInvalidArgument)
	assertErrIs(t, m.ProbeTimeout(badIP), ErrInvalidArgument)
	assertErrIs(t, m.Revoke(badIP, 1), ErrInvalidArgument)
	assertErrIs(t, m.Revoke(ip1, 0), ErrInvalidArgument)
	assertErrIs(t, m.Release(badIP), ErrInvalidArgument)

	// An IP that has never been observed has no snapshot or evidence and
	// drops frames.
	if snap := m.Snapshot(ip2); snap != nil {
		t.Fatalf("snapshot for unknown IP = %+v, want nil", snap)
	}
	if ev := m.Evidence(ip2); ev != nil {
		t.Fatalf("evidence for unknown IP = %+v, want nil", ev)
	}
	assertDrop(t, m, ip2)

	// Release requires an actual quarantine, even when the entry exists.
	m.Grant(Ticket{IP: ip2, NewMAC: macA, Generation: 1})
	m.HandleArpClaim(ip2, macA)
	assertErrIs(t, m.Release(ip2), ErrInvalidState)
}
