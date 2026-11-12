package sctpaddr

import (
	"net/netip"
	"testing"
)

// Deleting the last remaining address, or the source address the peer is
// sending from, is refused with the RFC 6525 error causes.
func TestDeleteProtections(t *testing.T) {
	t.Run("last address", func(t *testing.T) {
		m, err := New(Config{
			LocalPort:  5000,
			PeerPort:   6000,
			LocalTag:   0x11111111,
			PeerTag:    0x22222222,
			HMACKey:    testKey,
			LocalAddrs: []netip.Addr{addr1},
			Primary:    addr1,
			NextSN:     7,
		})
		if err != nil {
			t.Fatal(err)
		}
		got := m.Dispatch(asconf(t, 7, OpDeleteIP, addr1), addr1)
		if got.Kind != KindASCONFACK || got.Accepted || got.Cause != CauseDeleteLastAddr {
			t.Fatalf("delete last: %+v", got)
		}
		if !m.HasAddr(addr1) {
			t.Fatal("last address was removed")
		}
		// Refused operations still consume the serial.
		if m.expected != 8 {
			t.Fatalf("expected SN = %d, want 8", m.expected)
		}
	})

	t.Run("source address", func(t *testing.T) {
		m := newTestManager(t)
		got := m.Dispatch(asconf(t, 100, OpDeleteIP, addr2), addr2)
		if got.Kind != KindASCONFACK || got.Accepted || got.Cause != CauseDeleteSourceAddr {
			t.Fatalf("delete source: %+v", got)
		}
		if !m.HasAddr(addr2) {
			t.Fatal("source address was removed")
		}

		// The same delete arriving from a different source is allowed; the
		// last-address check has priority and passes with two addresses.
		got = m.Dispatch(asconf(t, 101, OpDeleteIP, addr2), addr1)
		if got.Kind != KindASCONFACK || !got.Accepted {
			t.Fatalf("delete from other source: %+v", got)
		}
		if m.HasAddr(addr2) {
			t.Fatal("address not deleted")
		}
	})

	t.Run("unknown address", func(t *testing.T) {
		m := newTestManager(t)
		got := m.Dispatch(asconf(t, 100, OpDeleteIP, addr4), addr1)
		if got.Kind != KindASCONFACK || got.Accepted || got.Cause != CauseUnrecognizedParam {
			t.Fatalf("delete unknown: %+v", got)
		}
	})
}

// A full legitimate migration adds an address, proves reachability through
// HEARTBEAT, promotes it to primary, and retires the old primary.
func TestLegitimateMigration(t *testing.T) {
	m := newTestManager(t)
	if got := m.Primary(); got != addr1 {
		t.Fatalf("initial primary = %v", got)
	}

	// 1. Authenticated add with the exact next serial.
	got := m.Dispatch(asconf(t, 100, OpAddIP, addr3), addr1)
	if got.Kind != KindASCONFACK || !got.Accepted {
		t.Fatalf("add: %+v", got)
	}
	if serial, ok, _ := parseACK(t, m, got.Response); serial != 100 || !ok {
		t.Fatalf("ACK serial/ok = %d/%v", serial, ok)
	}

	// 2. Reachability confirmation via HEARTBEAT nonce.
	probe, err := m.Probe(addr3)
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
	echo := marshalPacket(
		CommonHeader{SrcPort: 6000, DstPort: 5000, VerificationTag: m.localTag},
		buildHeartbeat(ChunkHeartbeatACK, nonce),
	)
	if got := m.Dispatch(echo, addr3); got.Kind != KindConfirmed {
		t.Fatalf("confirm: %+v", got)
	}

	// 3. Promote the confirmed address to primary.
	got = m.Dispatch(asconf(t, 101, OpSetPrimary, addr3), addr1)
	if got.Kind != KindASCONFACK || !got.Accepted {
		t.Fatalf("set primary: %+v", got)
	}
	if m.Primary() != addr3 {
		t.Fatalf("primary = %v", m.Primary())
	}

	// 4. Retire the old primary from a source that is not being deleted.
	got = m.Dispatch(asconf(t, 102, OpDeleteIP, addr1), addr2)
	if got.Kind != KindASCONFACK || !got.Accepted {
		t.Fatalf("retire old primary: %+v", got)
	}
	if m.HasAddr(addr1) || !m.HasAddr(addr3) {
		t.Fatalf("address set after retire = %v", m.Addrs())
	}
	if m.Primary() != addr3 {
		t.Fatalf("primary after retire = %v", m.Primary())
	}
}

// Outbound ASCONF packets built by BuildASCONF are self-consistent: signed,
// serial-numbered, and accepted by a peer manager using the shared key.
func TestBuildASCONFRoundTrip(t *testing.T) {
	m := newTestManager(t)
	peer, err := New(Config{
		LocalPort:  6000,
		PeerPort:   5000,
		LocalTag:   0x22222222,
		PeerTag:    0x11111111,
		HMACKey:    testKey,
		LocalAddrs: []netip.Addr{addr1, addr2},
		Primary:    addr1,
		NextSN:     100,
	})
	if err != nil {
		t.Fatal(err)
	}

	pkt, err := m.BuildASCONF(OpAddIP, addr3)
	if err != nil {
		t.Fatal(err)
	}
	got := peer.Dispatch(pkt, addr1)
	if got.Kind != KindASCONFACK || !got.Accepted || got.Address != addr3 {
		t.Fatalf("round trip: %+v", got)
	}

	// Each outbound build advances the local serial and thus replays fail.
	if got := peer.Dispatch(bytesClone(pkt), addr1); got.Err != ErrReplayedASCONF {
		t.Fatalf("outbound replay: %+v", got)
	}

	second, err := m.BuildASCONF(OpSetPrimary, addr3)
	if err != nil {
		t.Fatal(err)
	}
	// addr3 is still unconfirmed on the peer until a HEARTBEAT cycle, so the
	// promotion is refused rather than silently accepted.
	got = peer.Dispatch(second, addr1)
	if got.Accepted || got.Cause != CauseOperationRefused {
		t.Fatalf("unconfirmed promotion over wire: %+v", got)
	}
}

// An inbound HEARTBEAT is answered with a HEARTBEAT ACK echoing the nonce, and
// HEARTBEAT traffic still has to carry the correct verification tag.
func TestHeartbeatRequestAndTag(t *testing.T) {
	m := newTestManager(t)
	req := marshalPacket(
		CommonHeader{SrcPort: 6000, DstPort: 5000, VerificationTag: m.localTag},
		buildHeartbeat(ChunkHeartbeat, []byte("probe-nonce")),
	)
	got := m.Dispatch(req, addr1)
	if got.Kind != KindHeartbeatACK {
		t.Fatalf("heartbeat: %+v", got)
	}
	hdr, chunks, err := ParsePacket(got.Response)
	if err != nil {
		t.Fatal(err)
	}
	if chunks[0].Type != ChunkHeartbeatACK || hdr.VerificationTag != m.peerTag {
		t.Fatalf("bad HEARTBEAT ACK: %+v", hdr)
	}
	echo, err := parseHeartbeatInfo(chunks[0])
	if err != nil || string(echo) != "probe-nonce" {
		t.Fatalf("nonce echo = %q", echo)
	}

	if got := m.Dispatch(retag(req, 99), addr1); got.Err != ErrBadTag {
		t.Fatalf("heartbeat tag check: %+v", got)
	}
}
