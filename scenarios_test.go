package dhcpsnoop

import (
	"testing"
)

// TestPortMigration moves a client between access ports and checks that
// the binding follows the latest valid transaction.
func TestPortMigration(t *testing.T) {
	s, _ := newTestSnooper(t)

	leaseOn := func(p Port, xid uint32, mac MAC, ip IPv4) {
		t.Helper()
		s.HandleDHCP(DHCPPacket{InPort: p, XID: xid, Type: DHCPDiscover, Chaddr: mac})
		s.HandleDHCP(DHCPPacket{InPort: p, XID: xid + 1, Type: DHCPRequest, Chaddr: mac})
		checkResult(t, s.HandleDHCP(DHCPPacket{
			InPort: uplink, XID: xid + 1, Type: DHCPAck, Chaddr: mac,
			Yiaddr: ip, Lease: hour,
		}), allow(ReasonOK))
	}

	leaseOn(port1, 30, mac1, ip1)
	if b := mustBinding(t, s, ip1); b.Port != port1 {
		t.Fatalf("initial binding on %d, want port %d", b.Port, port1)
	}

	// The host physically moves to port2 and re-runs DHCP.
	leaseOn(port2, 40, mac1, ip1)
	b := mustBinding(t, s, ip1)
	if b.Port != port2 || b.MAC != mac1 {
		t.Fatalf("binding did not migrate: %+v", b)
	}
	if s.BindingCount() != 1 {
		t.Fatalf("migration left stale entries: %d", s.BindingCount())
	}

	// Old port is now blocked, new port passes, for both DAI and IPSG.
	checkResult(t, s.HandleIP(IPPacket{InPort: port1, SrcMAC: mac1, SrcIP: ip1}),
		drop(ReasonPortMismatch))
	checkResult(t, s.HandleARP(ARPPacket{InPort: port1, SenderMAC: mac1, SenderIP: ip1}),
		drop(ReasonPortMismatch))
	checkResult(t, s.HandleIP(IPPacket{InPort: port2, SrcMAC: mac1, SrcIP: ip1}),
		allow(ReasonOK))
	checkResult(t, s.HandleARP(ARPPacket{InPort: port2, SenderMAC: mac1, SenderIP: ip1}),
		allow(ReasonOK))

	// Another host cannot be granted the already-leased IP.
	s.HandleDHCP(DHCPPacket{InPort: port3, XID: 50, Type: DHCPDiscover, Chaddr: mac2})
	s.HandleDHCP(DHCPPacket{InPort: port3, XID: 51, Type: DHCPRequest, Chaddr: mac2})
	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: uplink, XID: 51, Type: DHCPAck, Chaddr: mac2,
		Yiaddr: ip1, Lease: hour,
	}), drop(ReasonBindingConflict))
	if b := mustBinding(t, s, ip1); b.MAC != mac1 || b.Port != port2 {
		t.Fatalf("conflicting ACK altered the binding: %+v", b)
	}
}

// TestLeaseExpiry verifies virtual-clock-driven removal, both lazily on
// the next packet and via the explicit timer Sweep.
func TestLeaseExpiry(t *testing.T) {
	s, clock := newTestSnooper(t)

	s.HandleDHCP(DHCPPacket{InPort: port1, XID: 60, Type: DHCPRequest, Chaddr: mac1})
	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: uplink, XID: 60, Type: DHCPAck, Chaddr: mac1,
		Yiaddr: ip1, Lease: hour,
	}), allow(ReasonOK))

	clock.Advance(59 * minute)
	checkResult(t, s.HandleIP(IPPacket{InPort: port1, SrcMAC: mac1, SrcIP: ip1}),
		allow(ReasonOK))

	// Exactly at expiry the lease is no longer active (ActiveAt is strict).
	clock.Advance(minute)
	if n := s.Sweep(); n != 1 {
		t.Fatalf("Sweep removed %d entries, want 1", n)
	}
	noBinding(t, s, ip1)

	// A second lease that expires between packets is removed lazily.
	s.HandleDHCP(DHCPPacket{InPort: port1, XID: 61, Type: DHCPRequest, Chaddr: mac2})
	s.HandleDHCP(DHCPPacket{
		InPort: uplink, XID: 61, Type: DHCPAck, Chaddr: mac2,
		Yiaddr: ip2, Lease: 2 * hour,
	})
	clock.Advance(3 * hour)
	checkResult(t, s.HandleARP(ARPPacket{InPort: port1, SenderMAC: mac2, SenderIP: ip2}),
		drop(ReasonNoBinding))
	noBinding(t, s, ip2)
}

// TestReleaseAndDecline covers both client-driven removal paths and
// their rejection modes.
func TestReleaseAndDecline(t *testing.T) {
	s, _ := newTestSnooper(t)

	s.HandleDHCP(DHCPPacket{InPort: port1, XID: 70, Type: DHCPRequest, Chaddr: mac1})
	s.HandleDHCP(DHCPPacket{
		InPort: uplink, XID: 70, Type: DHCPAck, Chaddr: mac1,
		Yiaddr: ip1, Lease: hour,
	})

	// RELEASE from a different port or MAC is rejected, entry retained.
	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: port2, XID: 71, Type: DHCPRelease, Chaddr: mac1, Ciaddr: ip1,
	}), drop(ReasonPortMismatch))
	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: port1, XID: 72, Type: DHCPRelease, Chaddr: mac2, Ciaddr: ip1,
	}), drop(ReasonClientIDMismatch))
	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: port1, XID: 73, Type: DHCPRelease, Chaddr: mac1, Ciaddr: ip2,
	}), drop(ReasonReleaseNoMatch))
	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: port1, XID: 74, Type: DHCPRelease, Chaddr: mac1,
	}), drop(ReasonInvalidMessage))
	mustBinding(t, s, ip1)

	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: port1, XID: 75, Type: DHCPRelease, Chaddr: mac1, Ciaddr: ip1,
	}), allow(ReasonOK))
	noBinding(t, s, ip1)

	// DECLINE on a fresh lease removes the entry just like RELEASE.
	s.HandleDHCP(DHCPPacket{InPort: port2, XID: 80, Type: DHCPRequest, Chaddr: mac2})
	s.HandleDHCP(DHCPPacket{
		InPort: uplink, XID: 80, Type: DHCPAck, Chaddr: mac2,
		Yiaddr: ip2, Lease: hour,
	})
	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: port2, XID: 81, Type: DHCPDecline, Chaddr: mac2, Ciaddr: ip2,
	}), allow(ReasonOK))
	noBinding(t, s, ip2)
	if s.BindingCount() != 0 {
		t.Fatalf("table should be empty, has %d", s.BindingCount())
	}
}
