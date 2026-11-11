package dhcpsnoop

import (
	"testing"
	"time"
)

const (
	uplink Port = 0
	port1  Port = 1
	port2  Port = 2
	port3  Port = 3
)

var (
	mac1 = MustMAC("aa:bb:cc:00:00:01")
	mac2 = MustMAC("aa:bb:cc:00:00:02")
	ip1  = MustIPv4("10.0.0.11")
	ip2  = MustIPv4("10.0.0.12")

	hour   = time.Hour
	minute = time.Minute
)

func newTestSnooper(t *testing.T) (*Snooper, *VirtualClock) {
	t.Helper()
	clock := NewVirtualClock(time.Unix(1_700_000_000, 0).UTC())
	return New(clock, uplink), clock
}

func checkResult(t *testing.T, got, want Result) {
	t.Helper()
	if got != want {
		t.Fatalf("verdict = %s(%s), want %s(%s)",
			got.Action, got.Reason, want.Action, want.Reason)
	}
}

func mustBinding(t *testing.T, s *Snooper, ip IPv4) Binding {
	t.Helper()
	b, ok := s.Binding(ip)
	if !ok {
		t.Fatalf("expected binding for %s, table has %d entries", ip, s.BindingCount())
	}
	return b
}

func noBinding(t *testing.T, s *Snooper, ip IPv4) {
	t.Helper()
	if _, ok := s.Binding(ip); ok {
		t.Fatalf("binding for %s should not exist", ip)
	}
}

// TestFullLeaseLifecycle walks a complete DORA exchange and verifies
// every packet verdict plus the shared binding table used by DAI/IPSG.
func TestFullLeaseLifecycle(t *testing.T) {
	s, clock := newTestSnooper(t)
	start := clock.Now()

	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: port1, XID: 100, Type: DHCPDiscover, Chaddr: mac1,
	}), allow(ReasonOK))

	// An OFFER must arrive on the trusted uplink and reference the XID.
	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: uplink, XID: 100, Type: DHCPOffer, Chaddr: mac1, Yiaddr: ip1,
	}), allow(ReasonOK))

	// Forged server reply on an access port is dropped, table untouched.
	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: port1, XID: 100, Type: DHCPAck, Chaddr: mac1,
		Yiaddr: ip1, Lease: hour,
	}), drop(ReasonUntrustedServerReply))
	if s.BindingCount() != 0 {
		t.Fatalf("forged ACK created a binding: %d entries", s.BindingCount())
	}

	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: port1, XID: 101, Type: DHCPRequest, Chaddr: mac1,
	}), allow(ReasonOK))

	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: uplink, XID: 101, Type: DHCPAck, Chaddr: mac1,
		Yiaddr: ip1, Lease: hour,
	}), allow(ReasonOK))

	if s.BindingCount() != 1 {
		t.Fatalf("ACK did not create exactly one binding: %d", s.BindingCount())
	}
	b := mustBinding(t, s, ip1)
	if b.MAC != mac1 || b.Port != port1 || b.Expiry != start.Add(hour) {
		t.Fatalf("unexpected binding: %+v", b)
	}

	// ARP inspection and IP source guard consult the same table.
	checkResult(t, s.HandleARP(ARPPacket{
		InPort: port1, Op: ARPRequest, SenderMAC: mac1, SenderIP: ip1, TargetIP: ip1,
	}), allow(ReasonOK))
	checkResult(t, s.HandleIP(IPPacket{
		InPort: port1, SrcMAC: mac1, SrcIP: ip1,
	}), allow(ReasonOK))

	// Spoofed source addresses on the same or other access ports fail.
	checkResult(t, s.HandleARP(ARPPacket{
		InPort: port1, Op: ARPReply, SenderMAC: mac2, SenderIP: ip1,
	}), drop(ReasonMacMismatch))
	checkResult(t, s.HandleIP(IPPacket{
		InPort: port2, SrcMAC: mac1, SrcIP: ip1,
	}), drop(ReasonPortMismatch))
	checkResult(t, s.HandleARP(ARPPacket{
		InPort: port3, Op: ARPRequest, SenderMAC: mac2, SenderIP: ip2,
	}), drop(ReasonNoBinding))

	// ARP probe (sender IP 0.0.0.0) is legal before the client has a lease.
	checkResult(t, s.HandleARP(ARPPacket{
		InPort: port3, Op: ARPRequest, SenderMAC: mac2,
		TargetIP: ip2,
	}), allow(ReasonArpProbe))

	// RELEASE tears the binding down immediately.
	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: port1, XID: 102, Type: DHCPRelease, Chaddr: mac1, Ciaddr: ip1,
	}), allow(ReasonOK))
	noBinding(t, s, ip1)
	checkResult(t, s.HandleIP(IPPacket{
		InPort: port1, SrcMAC: mac1, SrcIP: ip1,
	}), drop(ReasonNoBinding))
}

// TestForgedServerReplies ensures OFFER/ACK/NAK only enter via uplinks.
func TestForgedServerReplies(t *testing.T) {
	s, _ := newTestSnooper(t)

	for _, typ := range []DHCPType{DHCPOffer, DHCPAck, DHCPNak} {
		checkResult(t, s.HandleDHCP(DHCPPacket{
			InPort: port1, XID: 1, Type: typ, Chaddr: mac1,
			Yiaddr: ip1, Lease: hour,
		}), drop(ReasonUntrustedServerReply))
	}
	if s.BindingCount() != 0 {
		t.Fatalf("untrusted replies modified the table: %d", s.BindingCount())
	}

	// Trusted-port traffic that is client-to-server bypasses policing.
	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: uplink, XID: 2, Type: DHCPRelease, Chaddr: mac1, Ciaddr: ip1,
	}), allow(ReasonTrustedBypass))
}

// TestAckValidation covers XID/MAC attribution and lease sanity checks.
func TestAckValidation(t *testing.T) {
	s, _ := newTestSnooper(t)

	// ACK for an XID the switch never saw from a local client.
	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: uplink, XID: 9, Type: DHCPAck, Chaddr: mac1,
		Yiaddr: ip1, Lease: hour,
	}), drop(ReasonTransactionUnknown))

	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: port1, XID: 10, Type: DHCPRequest, Chaddr: mac1,
	}), allow(ReasonOK))

	// Wrong client MAC for the tracked XID: must not create a binding.
	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: uplink, XID: 10, Type: DHCPAck, Chaddr: mac2,
		Yiaddr: ip1, Lease: hour,
	}), drop(ReasonClientIDMismatch))
	noBinding(t, s, ip1)

	// ACK without leased address or lease time is unusable.
	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: uplink, XID: 10, Type: DHCPAck, Chaddr: mac1, Lease: hour,
	}), drop(ReasonNoLease))
	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: uplink, XID: 10, Type: DHCPAck, Chaddr: mac1, Yiaddr: ip1,
	}), drop(ReasonNoLease))

	// Malformed packets are rejected outright.
	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: port1, XID: 10, Type: DHCPRequest,
	}), drop(ReasonInvalidMessage))
	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: port1, XID: 10, Type: DHCPType(99), Chaddr: mac1,
	}), drop(ReasonInvalidMessage))
}

// TestDuplicateAck pins down every repeated-ACK outcome.
func TestDuplicateAck(t *testing.T) {
	s, clock := newTestSnooper(t)

	s.HandleDHCP(DHCPPacket{InPort: port1, XID: 20, Type: DHCPDiscover, Chaddr: mac1})
	s.HandleDHCP(DHCPPacket{InPort: port1, XID: 21, Type: DHCPRequest, Chaddr: mac1})

	ack := DHCPPacket{InPort: uplink, XID: 21, Type: DHCPAck, Chaddr: mac1,
		Yiaddr: ip1, Lease: hour}
	checkResult(t, s.HandleDHCP(ack), allow(ReasonOK))
	first := mustBinding(t, s, ip1).Expiry

	// Identical duplicate ACK at the same virtual time is idempotent.
	checkResult(t, s.HandleDHCP(ack), allow(ReasonOK))
	if got := mustBinding(t, s, ip1).Expiry; !got.Equal(first) {
		t.Fatalf("duplicate ACK changed expiry: %s vs %s", got, first)
	}

	// Duplicate ACK later on refreshes the lease, still one entry.
	clock.Advance(30 * minute)
	checkResult(t, s.HandleDHCP(ack), allow(ReasonOK))
	if got := mustBinding(t, s, ip1).Expiry; !got.Equal(clock.Now().Add(hour)) {
		t.Fatalf("duplicate ACK did not refresh lease, expiry=%s", got)
	}
	if s.BindingCount() != 1 {
		t.Fatalf("duplicate ACK created an extra entry: %d", s.BindingCount())
	}

	// ACK for the same XID but a different client MAC is rejected; the IP
	// is already leased to another client, so the conflict is reported.
	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: uplink, XID: 21, Type: DHCPAck, Chaddr: mac2,
		Yiaddr: ip1, Lease: hour,
	}), drop(ReasonBindingConflict))

	// A fresh XID targeting an unleased IP still fails on the MAC check.
	s.HandleDHCP(DHCPPacket{InPort: port3, XID: 22, Type: DHCPRequest, Chaddr: mac2})
	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: uplink, XID: 22, Type: DHCPAck, Chaddr: mac1,
		Yiaddr: ip2, Lease: hour,
	}), drop(ReasonClientIDMismatch))

	// ACK with an unknown XID can only refresh an existing same-client lease.
	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: uplink, XID: 99, Type: DHCPAck, Chaddr: mac1,
		Yiaddr: ip1, Lease: hour,
	}), allow(ReasonOK))

	// It must never let another MAC hijack the leased IP.
	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: uplink, XID: 98, Type: DHCPAck, Chaddr: mac2,
		Yiaddr: ip1, Lease: hour,
	}), drop(ReasonBindingConflict))

	// An unknown XID for a never-leased IP cannot create a binding.
	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: uplink, XID: 97, Type: DHCPAck, Chaddr: mac2,
		Yiaddr: ip2, Lease: hour,
	}), drop(ReasonTransactionUnknown))
	noBinding(t, s, ip2)
}
