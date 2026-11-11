package dhcpsnoop

import (
	"testing"
	"time"
)

// TestNak verifies NAK handling: trusted, XID/MAC matched, no binding.
func TestNak(t *testing.T) {
	s, _ := newTestSnooper(t)

	s.HandleDHCP(DHCPPacket{InPort: port1, XID: 90, Type: DHCPRequest, Chaddr: mac1})

	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: uplink, XID: 91, Type: DHCPNak, Chaddr: mac1,
	}), drop(ReasonTransactionUnknown))
	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: uplink, XID: 90, Type: DHCPNak, Chaddr: mac2,
	}), drop(ReasonClientIDMismatch))
	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: uplink, XID: 90, Type: DHCPNak, Chaddr: mac1,
	}), allow(ReasonOK))
	if s.BindingCount() != 0 {
		t.Fatalf("NAK must not create bindings: %d", s.BindingCount())
	}

	// The transaction is consumed by the NAK; a later ACK cannot use it.
	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: uplink, XID: 90, Type: DHCPAck, Chaddr: mac1,
		Yiaddr: ip1, Lease: hour,
	}), drop(ReasonTransactionUnknown))
}

// TestInformAck verifies INFORM never creates a lease binding.
func TestInformAck(t *testing.T) {
	s, _ := newTestSnooper(t)

	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: port1, XID: 110, Type: DHCPInform, Chaddr: mac1, Ciaddr: ip1,
	}), allow(ReasonOK))

	// INFORM ACK carries no yiaddr/lease and must not install a binding.
	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: uplink, XID: 110, Type: DHCPAck, Chaddr: mac1,
	}), allow(ReasonOK))
	if s.BindingCount() != 0 {
		t.Fatalf("INFORM ACK created a binding: %d", s.BindingCount())
	}

	// A lease-like ACK answering an INFORM transaction is invalid.
	s.HandleDHCP(DHCPPacket{InPort: port1, XID: 111, Type: DHCPInform, Chaddr: mac1})
	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: uplink, XID: 111, Type: DHCPAck, Chaddr: mac1,
		Yiaddr: ip1, Lease: hour,
	}), drop(ReasonInvalidMessage))
}

// TestTransactionTTL ages out XID state with the virtual clock.
func TestTransactionTTL(t *testing.T) {
	s, clock := newTestSnooper(t)
	s.SetTransactionTTL(30 * time.Second)

	s.HandleDHCP(DHCPPacket{InPort: port1, XID: 120, Type: DHCPDiscover, Chaddr: mac1})
	clock.Advance(31 * time.Second)

	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: uplink, XID: 120, Type: DHCPOffer, Chaddr: mac1, Yiaddr: ip1,
	}), drop(ReasonTransactionUnknown))

	// Repeated discovery on the same XID refreshes the transaction.
	s.HandleDHCP(DHCPPacket{InPort: port1, XID: 120, Type: DHCPDiscover, Chaddr: mac1})
	checkResult(t, s.HandleDHCP(DHCPPacket{
		InPort: uplink, XID: 120, Type: DHCPOffer, Chaddr: mac1, Yiaddr: ip1,
	}), allow(ReasonOK))
}

// TestTrustedBypassAndClock covers uplink bypass and clock mechanics.
func TestTrustedBypassAndClock(t *testing.T) {
	clock := NewVirtualClock(time.Time{})
	if !clock.Now().Equal(time.Unix(0, 0).UTC()) {
		t.Fatalf("zero-value origin = %s", clock.Now())
	}
	advanced := clock.Advance(5 * time.Second)
	if !advanced.Equal(clock.Now()) || clock.Now().Unix() != 5 {
		t.Fatalf("virtual clock advance failed: %s", clock.Now())
	}
	clock.Set(time.Unix(42, 0).UTC())
	if clock.Now().Unix() != 42 {
		t.Fatalf("virtual clock set failed: %s", clock.Now())
	}

	s := New(clock, uplink)

	// Trusted-side ARP/IP traffic is never policed.
	checkResult(t, s.HandleARP(ARPPacket{InPort: uplink, SenderMAC: mac1, SenderIP: ip1}),
		allow(ReasonTrustedBypass))
	checkResult(t, s.HandleIP(IPPacket{InPort: uplink, SrcMAC: mac1, SrcIP: ip1}),
		allow(ReasonTrustedBypass))

	// Unspecified source addresses are malformed.
	checkResult(t, s.HandleIP(IPPacket{InPort: port1, SrcMAC: mac1}),
		drop(ReasonInvalidMessage))
	checkResult(t, s.HandleARP(ARPPacket{InPort: port1, SenderIP: ip1}),
		drop(ReasonInvalidMessage))

	// Bindings snapshot reflects live state.
	s.HandleDHCP(DHCPPacket{InPort: port1, XID: 130, Type: DHCPRequest, Chaddr: mac1})
	s.HandleDHCP(DHCPPacket{
		InPort: uplink, XID: 130, Type: DHCPAck, Chaddr: mac1,
		Yiaddr: ip1, Lease: hour,
	})
	if got := s.Bindings(); len(got) != 1 || got[0].IP != ip1 {
		t.Fatalf("unexpected bindings snapshot: %+v", got)
	}
}

func TestNewRequiresTrustedPort(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("New without trusted ports should panic")
		}
	}()
	New(NewVirtualClock(time.Unix(0, 0).UTC()))
}

func TestAddressParsers(t *testing.T) {
	m, err := ParseMAC("aa:bb:cc:dd:ee:ff")
	if err != nil || m.String() != "aa:bb:cc:dd:ee:ff" || m.IsZero() {
		t.Fatalf("ParseMAC failed: %v %s", err, m)
	}
	if _, err := ParseMAC("not-a-mac"); err == nil {
		t.Fatal("ParseMAC accepted garbage")
	}

	ip, err := ParseIPv4("192.168.1.2")
	if err != nil || ip.String() != "192.168.1.2" || ip.IsZero() {
		t.Fatalf("ParseIPv4 failed: %v %s", err, ip)
	}
	if _, err := ParseIPv4("10.0.0"); err == nil {
		t.Fatal("ParseIPv4 accepted a short address")
	}
	if _, err := ParseIPv4("::1"); err == nil {
		t.Fatal("ParseIPv4 accepted an IPv6 address")
	}

	if MustMAC("01:02:03:04:05:06") != (MAC{1, 2, 3, 4, 5, 6}) {
		t.Fatal("MustMAC mismatch")
	}
	if MustIPv4("1.2.3.4") != (IPv4{1, 2, 3, 4}) {
		t.Fatal("MustIPv4 mismatch")
	}
}

func TestStringers(t *testing.T) {
	cases := []struct {
		got, want string
	}{
		{DHCPDiscover.String(), "DISCOVER"},
		{DHCPOffer.String(), "OFFER"},
		{DHCPRequest.String(), "REQUEST"},
		{DHCPDecline.String(), "DECLINE"},
		{DHCPAck.String(), "ACK"},
		{DHCPNak.String(), "NAK"},
		{DHCPRelease.String(), "RELEASE"},
		{DHCPInform.String(), "INFORM"},
		{DHCPType(42).String(), "DHCPType(42)"},
		{ActionAllow.String(), "ALLOW"},
		{ActionDrop.String(), "DROP"},
		{Action(9).String(), "Action(9)"},
		{ReasonOK.String(), "ok"},
		{Reason(77).String(), "Reason(77)"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Fatalf("String() = %q, want %q", c.got, c.want)
		}
	}
}
