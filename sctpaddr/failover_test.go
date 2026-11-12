package sctpaddr

import (
	"net/netip"
	"testing"
)

// Deleting the active primary must deterministically promote another confirmed
// address rather than leaving the association without a primary.
func TestPrimaryFailoverOnDelete(t *testing.T) {
	m := newTestManager(t)
	if m.Primary() != addr1 {
		t.Fatalf("initial primary = %v", m.Primary())
	}

	// Delete addr1 (the primary) sourced from addr2 so the source rule passes.
	got := m.Dispatch(asconf(t, 100, OpDeleteIP, addr1), addr2)
	if got.Kind != KindASCONFACK || !got.Accepted {
		t.Fatalf("delete primary: %+v", got)
	}
	if m.HasAddr(addr1) {
		t.Fatal("primary not removed")
	}
	if m.Primary() != addr2 || !m.Confirmed(addr2) {
		t.Fatalf("failover primary = %v", m.Primary())
	}
}

// IPv6 addresses and the HMAC-SHA1 AUTH identifier round-trip correctly.
func TestIPv6AndSHA1(t *testing.T) {
	v6a := netip.MustParseAddr("2001:db8::1")
	v6b := netip.MustParseAddr("2001:db8::2")
	m, err := New(Config{
		LocalPort:  5000,
		PeerPort:   6000,
		LocalTag:   0x11111111,
		PeerTag:    0x22222222,
		HMACKey:    testKey,
		HMACID:     HMACSHA1,
		LocalAddrs: []netip.Addr{v6a},
		Primary:    v6a,
		NextSN:     1,
	})
	if err != nil {
		t.Fatal(err)
	}

	op, err := addressParam(ParamASCONFAddIP, v6b)
	if err != nil {
		t.Fatal(err)
	}
	hdr := CommonHeader{SrcPort: 6000, DstPort: 5000, VerificationTag: m.localTag}
	pkt, err := signPacket(testKey, HMACSHA1, hdr, []Chunk{buildASCONF(1, op)})
	if err != nil {
		t.Fatal(err)
	}

	got := m.Dispatch(pkt, v6a)
	if got.Kind != KindASCONFACK || !got.Accepted || got.Address != v6b {
		t.Fatalf("IPv6 add: %+v", got)
	}
	if !m.HasAddr(v6b) || m.Confirmed(v6b) {
		t.Fatal("IPv6 address must exist but be unconfirmed")
	}

	probe, err := m.Probe(v6b)
	if err != nil {
		t.Fatal(err)
	}
	_, chunks, err := ParsePacket(probe)
	if err != nil {
		t.Fatal(err)
	}
	nonce, err := parseHeartbeatInfo(chunks[0])
	if err != nil {
		t.Fatal(err)
	}
	echo := marshalPacket(hdr, buildHeartbeat(ChunkHeartbeatACK, nonce))
	if got := m.Dispatch(echo, v6b); got.Kind != KindConfirmed {
		t.Fatalf("IPv6 confirm: %+v", got)
	}

	pri, err := signPacket(testKey, HMACSHA1, hdr,
		[]Chunk{buildASCONF(2, mustAddrParam(t, ParamASCONFSetPrimary, v6b))})
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Dispatch(pri, v6a); !got.Accepted || m.Primary() != v6b {
		t.Fatalf("IPv6 set primary: %+v primary=%v", got, m.Primary())
	}
}

func mustAddrParam(t *testing.T, opType uint16, addr netip.Addr) tlv {
	t.Helper()
	p, err := addressParam(opType, addr)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Re-adding an already present address is idempotent and never re-confirms it.
func TestReAddIdempotent(t *testing.T) {
	m := newTestManager(t)

	first := m.Dispatch(asconf(t, 100, OpAddIP, addr3), addr1)
	if !first.Accepted || m.Confirmed(addr3) {
		t.Fatalf("first add: %+v", first)
	}
	again := m.Dispatch(asconf(t, 101, OpAddIP, addr3), addr1)
	if !again.Accepted {
		t.Fatalf("re-add: %+v", again)
	}
	if m.Confirmed(addr3) {
		t.Fatal("re-add must not confirm the address")
	}
}
