package netsec

import (
	"encoding/binary"
	"net/netip"
	"testing"
)

const (
	testLocalSSRC       = uint32(0x11111111)
	testRemoteSSRC      = uint32(0x22222222)
	testReplacementSSRC = uint32(0x33333333)
)

var (
	trustedSource = netip.MustParseAddrPort("192.0.2.10:41000")
	forgedSource  = netip.MustParseAddrPort("198.51.100.20:42000")
	otherPort     = netip.MustParseAddrPort("192.0.2.10:41001")
)

func startSession(t *testing.T, b *SourceBinder) {
	t.Helper()
	decision := b.HandleRTP(testRemoteSSRC, 100, trustedSource)
	if !decision.Accepted {
		t.Fatalf("first packet rejected: %s", decision.Reason)
	}
	if !decision.SourceBound {
		t.Fatal("first accepted packet did not bind the source")
	}
	locked, ok := b.LockedSource()
	if !ok || locked != trustedSource {
		t.Fatalf("locked source = %v, %v; want %v, true", locked, ok, trustedSource)
	}
}

func TestForgedSourceIsDropped(t *testing.T) {
	b := NewSourceBinder(testLocalSSRC)
	startSession(t, b)

	decision := b.HandleRTP(testRemoteSSRC, 101, forgedSource)
	if decision.Accepted {
		t.Fatal("packet from forged endpoint was accepted")
	}
	if decision.Reason != DropSourceMismatch {
		t.Fatalf("reason = %s, want %s", decision.Reason, DropSourceMismatch)
	}
	if _, ok := b.ActiveSSRC(); !ok {
		t.Fatal("forged packet disturbed the active stream")
	}
	if stats := b.Stats(); stats.SourceMismatchDrops != 1 {
		t.Fatalf("source mismatch drops = %d, want 1", stats.SourceMismatchDrops)
	}
}

func TestSequenceJumpNeedsTwoAdjacentPackets(t *testing.T) {
	b := NewSourceBinder(testLocalSSRC)
	startSession(t, b)

	// Exactly MAX_DROPOUT is already outside the permissible gap (A.1 uses
	// delta < MAX_DROPOUT for the in-order case).
	if decision := b.HandleRTP(testRemoteSSRC, 3100, trustedSource); decision.Accepted ||
		decision.Reason != DropSequenceJump {
		t.Fatalf("large jump decision = %+v, want sequence-jump drop", decision)
	}
	if stats := b.Stats(); stats.SequenceJumpDrops != 1 {
		t.Fatalf("sequence jump drops = %d, want 1", stats.SequenceJumpDrops)
	}

	// The immediately following sequence number is the second packet required
	// by bad_seq and resynchronizes the stream.
	decision := b.HandleRTP(testRemoteSSRC, 3101, trustedSource)
	if !decision.Accepted || !decision.Resynchronized {
		t.Fatalf("second adjacent packet decision = %+v, want accepted resync", decision)
	}
	if stats := b.Stats(); stats.Resynchronizations != 1 {
		t.Fatalf("resynchronizations = %d, want 1", stats.Resynchronizations)
	}

	// The new epoch must remain usable after the resynchronizing packet.
	if decision := b.HandleRTP(testRemoteSSRC, 3102, trustedSource); !decision.Accepted {
		t.Fatalf("packet after resync rejected: %s", decision.Reason)
	}
	if got := b.sequence.MaxSequence(); got != 3102 {
		t.Fatalf("max sequence = %d, want 3102", got)
	}
}

func TestNonAdjacentPacketsDoNotResynchronize(t *testing.T) {
	b := NewSourceBinder(testLocalSSRC)
	startSession(t, b)

	for _, seq := range []uint16{5000, 6000, 7000} {
		decision := b.HandleRTP(testRemoteSSRC, seq, trustedSource)
		if decision.Accepted || decision.Reason != DropSequenceJump {
			t.Fatalf("seq %d decision = %+v, want sequence-jump drop", seq, decision)
		}
	}
	if stats := b.Stats(); stats.SequenceJumpDrops != 3 {
		t.Fatalf("sequence jump drops = %d, want 3", stats.SequenceJumpDrops)
	}

	if decision := b.HandleRTP(testRemoteSSRC, 7001, trustedSource); !decision.Accepted || !decision.Resynchronized {
		t.Fatalf("two adjacent candidates were not accepted: %+v", decision)
	}
}

func TestNewSSRCRequiresSignalingDeclaration(t *testing.T) {
	b := NewSourceBinder(testLocalSSRC)
	startSession(t, b)

	decision := b.HandleRTP(testReplacementSSRC, 101, trustedSource)
	if decision.Accepted || decision.Reason != DropUnexpectedSSRC {
		t.Fatalf("undeclared SSRC=0x%x decision = %+v, want unexpected-SSRC drop", testReplacementSSRC, decision)
	}
	if active, _ := b.ActiveSSRC(); active != testRemoteSSRC {
		t.Fatalf("active SSRC = 0x%x, want old SSRC 0x%x", active, testRemoteSSRC)
	}
	if stats := b.Stats(); stats.UnexpectedSSRCDrops != 1 {
		t.Fatalf("unexpected SSRC drops = %d, want 1", stats.UnexpectedSSRCDrops)
	}

	if !b.DeclareSSRCChange(testReplacementSSRC) {
		t.Fatal("signaling declaration was rejected")
	}
	decision = b.HandleRTP(testReplacementSSRC, 500, trustedSource)
	if !decision.Accepted || !decision.SSRCChanged {
		t.Fatalf("declared SSRC change decision = %+v, want accepted change", decision)
	}
	if active, _ := b.ActiveSSRC(); active != testReplacementSSRC {
		t.Fatalf("active SSRC = 0x%x, want new SSRC 0x%x", active, testReplacementSSRC)
	}

	if decision := b.HandleRTP(testRemoteSSRC, 102, trustedSource); decision.Accepted {
		t.Fatal("old SSRC was accepted after a declared replacement")
	}
}

func TestBYERequiresLockedEndpoint(t *testing.T) {
	b := NewSourceBinder(testLocalSSRC)
	startSession(t, b)

	for _, source := range []netip.AddrPort{forgedSource, otherPort} {
		decision := b.HandleRTCPBYE(testRemoteSSRC, source)
		if decision.Accepted {
			t.Fatalf("forged BYE from %v was accepted", source)
		}
		if decision.Reason != DropBYESourceMismatch {
			t.Fatalf("BYE from %v reason = %s, want source mismatch", source, decision.Reason)
		}
	}
	if b.Terminated() {
		t.Fatal("forged BYE terminated the session")
	}
	if stats := b.Stats(); stats.ForgedBYEDrops != 2 {
		t.Fatalf("forged BYE drops = %d, want 2", stats.ForgedBYEDrops)
	}

	if decision := b.HandleRTCPBYE(testReplacementSSRC, trustedSource); decision.Accepted {
		t.Fatal("BYE for an inactive SSRC was accepted")
	}

	decision := b.HandleRTCPBYE(testRemoteSSRC, trustedSource)
	if !decision.Accepted || !decision.RTCPBYE || !decision.SessionTerminated {
		t.Fatalf("authentic BYE decision = %+v, want accepted termination", decision)
	}
	if !b.Terminated() {
		t.Fatal("authentic BYE did not terminate the session")
	}
	if decision := b.HandleRTP(testRemoteSSRC, 102, trustedSource); decision.Accepted ||
		decision.Reason != DropSessionEnded {
		t.Fatalf("RTP after BYE decision = %+v, want session-ended drop", decision)
	}
}

func TestLocalSSRCCollisionReannouncesNewSSRC(t *testing.T) {
	const replacement = uint32(0xaabbccdd)
	var announcedOld, announcedNew uint32
	announcements := 0

	b := NewSourceBinder(
		testLocalSSRC,
		WithSSRCGenerator(func(_, _ uint32) uint32 { return replacement }),
		WithSSRCChangeHandler(func(oldSSRC, newSSRC uint32) {
			announcements++
			announcedOld, announcedNew = oldSSRC, newSSRC
		}),
	)

	decision := b.HandleRTP(testLocalSSRC, 1, trustedSource)
	if decision.Accepted {
		t.Fatal("local-SSRC collision packet was accepted")
	}
	if !decision.SSRCCollision || !decision.LocalSSRCChanged {
		t.Fatalf("collision decision = %+v, want collision and local change", decision)
	}
	if got := b.LocalSSRC(); got != replacement {
		t.Fatalf("local SSRC = 0x%x, want replacement 0x%x", got, replacement)
	}
	if announcements != 1 || announcedOld != testLocalSSRC || announcedNew != replacement {
		t.Fatalf("announcement = (%d, 0x%x, 0x%x), want one (%x, 0x%x)",
			announcements, announcedOld, announcedNew, testLocalSSRC, replacement)
	}

	// The old SSRC was involved in the collision and must not become the
	// remote stream on the next packet merely because the local SSRC changed.
	decision = b.HandleRTP(testLocalSSRC, 2, trustedSource)
	if decision.Accepted || decision.Reason != DropSSRCCollision {
		t.Fatalf("repeated collision SSRC decision = %+v, want collision drop", decision)
	}
	if got := b.LocalSSRC(); got != replacement {
		t.Fatalf("repeated collision changed local SSRC to 0x%x", got)
	}
}

func TestForgedSourceCannotTriggerCollision(t *testing.T) {
	b := NewSourceBinder(testLocalSSRC)
	startSession(t, b)
	before := b.LocalSSRC()

	decision := b.HandleRTP(testLocalSSRC, 101, forgedSource)
	if decision.Accepted || decision.Reason != DropSourceMismatch {
		t.Fatalf("forged collision decision = %+v, want source-mismatch drop", decision)
	}
	if after := b.LocalSSRC(); after != before {
		t.Fatalf("forged source changed local SSRC from 0x%x to 0x%x", before, after)
	}
}

func TestRawRTPAndRTCPParsing(t *testing.T) {
	b := NewSourceBinder(testLocalSSRC)

	rtp := make([]byte, 12)
	rtp[0] = 0x80
	binary.BigEndian.PutUint16(rtp[2:4], 100)
	binary.BigEndian.PutUint32(rtp[8:12], testRemoteSSRC)
	decision, err := b.HandleRTPPacket(rtp, trustedSource)
	if err != nil {
		t.Fatalf("HandleRTPPacket: %v", err)
	}
	if !decision.Accepted {
		t.Fatalf("raw RTP packet rejected: %s", decision.Reason)
	}

	bye := make([]byte, 12)
	bye[0] = 0x81
	bye[1] = 203
	binary.BigEndian.PutUint16(bye[2:4], 2)
	binary.BigEndian.PutUint32(bye[8:12], testRemoteSSRC)

	decisions, err := b.HandleRTCPPacket(bye, forgedSource)
	if err != nil {
		t.Fatalf("HandleRTCPPacket: %v", err)
	}
	if len(decisions) != 1 || decisions[0].Accepted {
		t.Fatalf("forged raw BYE decisions = %+v, want one rejected decision", decisions)
	}
	if b.Terminated() {
		t.Fatal("forged raw BYE terminated the session")
	}
}

func TestSequenceStateWrapsAndRejectsOldReordering(t *testing.T) {
	s := NewSequenceState(65534)
	if update := s.Update(65535); !update.Accepted {
		t.Fatal("65535 should be in sequence")
	}
	if update := s.Update(0); !update.Accepted {
		t.Fatal("sequence rollover should be accepted")
	}
	if got := s.ExtendedSequence(); got != 65536 {
		t.Fatalf("extended sequence = %d, want 65536", got)
	}

	// A delta in the duplicate/reordered range is not a restart candidate and
	// does not modify the maximum sequence.
	if update := s.Update(65500); !update.Accepted || update.LargeJump {
		t.Fatalf("old reordered packet update = %+v, want accepted duplicate/reorder", update)
	}
	if got := s.MaxSequence(); got != 0 {
		t.Fatalf("max sequence after old packet = %d, want 0", got)
	}
}
