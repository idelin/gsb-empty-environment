package sctpaddr

import (
	"net/netip"
	"testing"
)

// A wrong verification tag must be dropped before AUTH is even evaluated, and
// INIT is the only chunk exempt from the tag check.
func TestVerificationTagRequiredExceptINIT(t *testing.T) {
	m := newTestManager(t)
	pkt := asconf(t, 100, OpAddIP, addr3)

	got := m.Dispatch(retag(pkt, 0xDEADBEEF), addr2)
	if got.Kind != KindDropped || got.Err != ErrBadTag {
		t.Fatalf("wrong tag: %+v", got)
	}
	if m.HasAddr(addr3) {
		t.Fatal("dropped packet must not add an address")
	}
	if m.expected != 100 {
		t.Fatalf("expected SN moved to %d on dropped packet", m.expected)
	}

	// INIT chunks are exempt by protocol design and simply pass the tag gate;
	// here they are not address-management traffic and are ignored.
	initPkt := marshalPacket(
		CommonHeader{SrcPort: 6000, DstPort: 5000, VerificationTag: 0},
		Chunk{Type: ChunkINIT, Data: make([]byte, 16)},
	)
	if got := m.Dispatch(initPkt, addr2); got.Kind != KindIgnored {
		t.Fatalf("INIT handling: %+v", got)
	}
}

// ASCONF without AUTH, with a tampered HMAC, or signed with another key is
// rejected and never changes state.
func TestASCONFRequiresValidAUTH(t *testing.T) {
	m := newTestManager(t)
	pkt := asconf(t, 100, OpAddIP, addr3)

	t.Run("missing AUTH", func(t *testing.T) {
		hdr, chunks, err := ParsePacket(pkt)
		if err != nil {
			t.Fatal(err)
		}
		stripped := marshalPacket(hdr, chunks[0])
		got := m.Dispatch(stripped, addr2)
		if got.Kind != KindDropped || got.Err != ErrMissingAUTH {
			t.Fatalf("missing AUTH: %+v", got)
		}
	})

	t.Run("tampered HMAC", func(t *testing.T) {
		bad := tamperLastByte(bytesClone(pkt))
		got := m.Dispatch(bad, addr2)
		if got.Kind != KindDropped || got.Err != ErrBadHMAC {
			t.Fatalf("bad HMAC: %+v", got)
		}
	})

	t.Run("wrong key", func(t *testing.T) {
		other, err := New(Config{
			LocalPort:  5000,
			PeerPort:   6000,
			LocalTag:   0x11111111,
			PeerTag:    0x22222222,
			HMACKey:    []byte("a-different-secret"),
			LocalAddrs: []netip.Addr{addr1, addr2},
			Primary:    addr1,
			NextSN:     100,
		})
		if err != nil {
			t.Fatal(err)
		}
		got := other.Dispatch(pkt, addr2)
		if got.Kind != KindDropped || got.Err != ErrBadHMAC {
			t.Fatalf("foreign key: %+v", got)
		}
	})

	if m.HasAddr(addr3) {
		t.Fatal("AUTH failures must not add the address")
	}
	if m.expected != 100 {
		t.Fatalf("expected SN moved to %d after AUTH failures", m.expected)
	}
}

// Replayed (equal or older) and jumped-ahead serials are refused; only the
// exact next value is accepted.
func TestStrictSequenceNumberWindow(t *testing.T) {
	m := newTestManager(t)

	// Replay of the current expected value before anything is consumed.
	first := asconf(t, 100, OpAddIP, addr3)
	got := m.Dispatch(first, addr2)
	if got.Kind != KindASCONFACK || !got.Accepted {
		t.Fatalf("first ASCONF: %+v", got)
	}

	replay := asconf(t, 100, OpSetPrimary, addr3)
	got = m.Dispatch(replay, addr2)
	if got.Kind != KindDropped || got.Err != ErrReplayedASCONF {
		t.Fatalf("replay: %+v", got)
	}

	older := asconf(t, 50, OpAddIP, addr4)
	got = m.Dispatch(older, addr2)
	if got.Kind != KindDropped || got.Err != ErrStaleASCONF {
		t.Fatalf("stale: %+v", got)
	}

	future := asconf(t, 102, OpAddIP, addr4)
	got = m.Dispatch(future, addr2)
	if got.Kind != KindDropped || got.Err != ErrFutureASCONF {
		t.Fatalf("future: %+v", got)
	}

	// A byte-level retransmit of the original packet (identical bytes) is
	// likewise a replay and must not re-run the add.
	if got = m.Dispatch(bytesClone(first), addr2); got.Err != ErrReplayedASCONF {
		t.Fatalf("identical retransmit: %+v", got)
	}

	if m.expected != 101 {
		t.Fatalf("expected SN = %d, want 101", m.expected)
	}

	// The exact next serial is accepted again.
	next := asconf(t, 101, OpAddIP, addr4)
	got = m.Dispatch(next, addr2)
	if got.Kind != KindASCONFACK || !got.Accepted {
		t.Fatalf("next ASCONF: %+v", got)
	}
	if m.expected != 102 {
		t.Fatalf("expected SN = %d, want 102", m.expected)
	}
}

// A newly added address is unconfirmed and cannot become primary until its
// HEARTBEAT nonce is echoed back; stale or foreign nonces are ignored.
func TestUnconfirmedAddressCannotBePrimary(t *testing.T) {
	m := newTestManager(t)

	add := asconf(t, 100, OpAddIP, addr3)
	if got := m.Dispatch(add, addr2); !got.Accepted {
		t.Fatalf("add: %+v", got)
	}
	if !m.HasAddr(addr3) || m.Confirmed(addr3) {
		t.Fatal("new address must exist but be unconfirmed")
	}

	setPri := asconf(t, 101, OpSetPrimary, addr3)
	got := m.Dispatch(setPri, addr2)
	if got.Kind != KindASCONFACK || got.Accepted || got.Cause != CauseOperationRefused {
		t.Fatalf("set primary unconfirmed: %+v", got)
	}
	if m.Primary() != addr1 {
		t.Fatalf("primary changed to unconfirmed %v", m.Primary())
	}

	probe, err := m.Probe(addr3)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}

	// A HEARTBEAT ACK carrying a random foreign nonce confirms nothing.
	foreign := marshalPacket(
		CommonHeader{SrcPort: 6000, DstPort: 5000, VerificationTag: m.localTag},
		buildHeartbeat(ChunkHeartbeatACK, []byte("not-the-armed-nonce")),
	)
	if got := m.Dispatch(foreign, addr3); got.Kind != KindIgnored {
		t.Fatalf("foreign nonce: %+v", got)
	}
	if m.Confirmed(addr3) {
		t.Fatal("foreign nonce must not confirm address")
	}

	// Echoing the exact armed nonce confirms reachability.
	_, chunks, err := ParsePacket(probe)
	if err != nil {
		t.Fatal(err)
	}
	nonce, err := parseHeartbeatInfo(chunks[0])
	if err != nil {
		t.Fatal(err)
	}
	echo := marshalPacket(
		CommonHeader{SrcPort: 6000, DstPort: 5000, VerificationTag: m.localTag},
		buildHeartbeat(ChunkHeartbeatACK, nonce),
	)
	got = m.Dispatch(echo, addr3)
	if got.Kind != KindConfirmed || got.Address != addr3 {
		t.Fatalf("confirmation: %+v", got)
	}
	if !m.Confirmed(addr3) {
		t.Fatal("address not confirmed after matching HEARTBEAT ACK")
	}

	// The same nonce cannot be replayed to confirm another address.
	if got := m.Dispatch(bytesClone(echo), addr3); got.Kind != KindIgnored {
		t.Fatalf("nonce replay: %+v", got)
	}

	setPri2 := asconf(t, 102, OpSetPrimary, addr3)
	got = m.Dispatch(setPri2, addr2)
	if got.Kind != KindASCONFACK || !got.Accepted {
		t.Fatalf("set primary confirmed: %+v", got)
	}
	if m.Primary() != addr3 {
		t.Fatalf("primary = %v, want %v", m.Primary(), addr3)
	}
}
