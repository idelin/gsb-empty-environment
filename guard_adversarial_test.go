package quicguard_test

import (
	"errors"
	"testing"
	"time"

	"formal.local/w27-86968a8181"
)

var (
	clientDCID = []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	serverSCID = []byte{0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18}
)

// TestAdversarialInitial1199 is the deterministic 1199-byte short-packet
// case: the first Initial must be rejected without touching any counter.
func TestAdversarialInitial1199(t *testing.T) {
	g, _ := newTestGuard(t)
	src := mustAddr(t, "198.51.100.7:4444")

	for _, size := range []int{1199, 1198, 100} {
		packet := initialPacket(clientDCID, []byte{0xa0}, nil, size)
		_, err := g.OnDatagram(src, packet)
		if !errors.Is(err, quicguard.ErrInitialTooSmall) {
			t.Fatalf("size=%d: want ErrInitialTooSmall, got %v", size, err)
		}
	}

	snap := g.Snapshot(src)
	if snap.RXBytes != 0 || snap.TXBytes != 0 {
		t.Fatalf("rejected Initial must not be counted, got rx=%d tx=%d", snap.RXBytes, snap.TXBytes)
	}

	// A 1200-byte Initial is accepted and fully counted.
	good := initialPacket(clientDCID, []byte{0xa0}, nil, 1200)
	result, err := g.OnDatagram(src, good)
	if err != nil {
		t.Fatalf("1200-byte Initial rejected: %v", err)
	}
	if result.RXBytes != 1200 || result.Limit != 3600 {
		t.Fatalf("unexpected accounting rx=%d limit=%d", result.RXBytes, result.Limit)
	}
}

// TestAdversarialThreeXBoundary proves txBytes never exceeds 3*rxBytes and
// that the boundary itself is allowed while the next byte is suspended.
func TestAdversarialThreeXBoundary(t *testing.T) {
	g, _ := newTestGuard(t)
	src := mustAddr(t, "203.0.113.9:5555")

	// One 100-byte datagram creates a 300-byte budget on an unvalidated path.
	// A 0-RTT packet is used because a Handshake packet would itself validate
	// the source address and lift the multiplier.
	if _, err := g.OnDatagram(src, zeroRTTPacket(clientDCID, serverSCID, 100)); err != nil {
		t.Fatal(err)
	}

	send := func(n int, wantSent bool) {
		t.Helper()
		r, err := g.Send(src, make([]byte, n))
		if wantSent {
			if err != nil || !r.Sent {
				t.Fatalf("send %d: want sent, got sent=%v err=%v", n, r.Sent, err)
			}
			return
		}
		if !errors.Is(err, quicguard.ErrAmplificationLimited) || r == nil || !r.Queued {
			t.Fatalf("send %d: want suspension, got sent=%v queued=%v err=%v", n, r.Sent, r.Queued, err)
		}
	}

	send(200, true)  // tx=200 <= 300
	send(100, true)  // tx=300 == 3*100, exact boundary allowed
	send(1, false)   // tx would be 301 > 300: suspended, not truncated
	send(500, false) // still suspended and queued whole

	snap := g.Snapshot(src)
	if snap.TXBytes != 300 || snap.RXBytes != 100 || snap.QueuedDatagrams != 2 {
		t.Fatalf("invariant violated or queue wrong: %+v", snap)
	}

	// Validation flushes the suspended datagrams whole, in FIFO order.
	drained := g.ConfirmHandshake(src)
	if len(drained) != 2 || len(drained[0].Data) != 1 || len(drained[1].Data) != 500 {
		t.Fatalf("drained queue mismatch: %+v", drained)
	}
	snap = g.Snapshot(src)
	if !snap.Validated || snap.TXBytes != 801 || snap.QueuedDatagrams != 0 {
		t.Fatalf("post-validation state wrong: %+v", snap)
	}

	// A validated path ignores the multiplier.
	if r, err := g.Send(src, make([]byte, 4096)); err != nil || !r.Sent {
		t.Fatalf("validated send blocked: %+v err=%v", r, err)
	}
}

// TestAdversarialRetransmitAckCoalescedBilling charges ACKs, retransmits and
// coalesced datagrams by their exact wire length.
func TestAdversarialRetransmitAckCoalescedBilling(t *testing.T) {
	g, _ := newTestGuard(t)
	src := mustAddr(t, "192.0.2.50:9000")

	dgram := coalescedDatagram(clientDCID, serverSCID) // 200 + 100 = 300 bytes
	result, err := g.OnDatagram(src, dgram)
	if err != nil {
		t.Fatal(err)
	}
	if result.RXBytes != 300 {
		t.Fatalf("coalesced datagram accounting = %d, want 300", result.RXBytes)
	}

	ack := make([]byte, 10)
	retransmit := make([]byte, 800)
	if _, err := g.Send(src, ack); err != nil {
		t.Fatalf("ACK blocked: %v", err)
	}
	if _, err := g.Send(src, retransmit); err != nil {
		t.Fatalf("retransmit blocked: %v", err)
	}
	// 10 + 800 = 810 <= 900; the next 91-byte datagram would reach 901.
	if r, err := g.Send(src, make([]byte, 91)); !errors.Is(err, quicguard.ErrAmplificationLimited) {
		t.Fatalf("over-budget retransmit not suspended: sent=%v err=%v", r.Sent, err)
	}
	snap := g.Snapshot(src)
	if snap.TXBytes != 810 || snap.AmplificationLimit != 900 {
		t.Fatalf("billing wrong: tx=%d limit=%d", snap.TXBytes, snap.AmplificationLimit)
	}
}

// TestAdversarialRetryTokenCrossIPReplay covers cross-IP reuse, expiry and
// single-use replay of an AES-256-GCM Retry token.
func TestAdversarialRetryTokenCrossIPReplay(t *testing.T) {
	g, clock := newTestGuard(t)
	victim := mustAddr(t, "198.51.100.20:1111")
	attacker := mustAddr(t, "198.51.100.200:2222")

	token, err := g.MintRetryToken(&quicguard.RetryTokenClaims{
		ClientIP:     victim.Addr(),
		OriginalDCID: clientDCID,
		RetrySCID:    serverSCID,
		IssuedAt:     clock.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Cross-IP presentation from the attacker is rejected before replay marking.
	if _, err := g.VerifyRetryToken(token, attacker.Addr()); !errors.Is(err, quicguard.ErrTokenWrongIP) {
		t.Fatalf("cross-IP token accepted: %v", err)
	}

	// Expired token is rejected even from the bound IP.
	clock.Advance(11 * time.Second)
	if _, err := g.VerifyRetryToken(token, victim.Addr()); !errors.Is(err, quicguard.ErrTokenExpired) {
		t.Fatalf("expired token accepted: %v", err)
	}

	// Rewind inside TTL: first use succeeds and validates the path, second use
	// is a replay even though IP and expiry still match.
	clock.t = clock.t.Add(-2 * time.Second)
	claims, err := g.VerifyRetryToken(token, victim.Addr())
	if err != nil {
		t.Fatalf("first valid presentation failed: %v", err)
	}
	if string(claims.RetrySCID) != string(serverSCID) {
		t.Fatalf("claims corrupted: %x", claims.RetrySCID)
	}
	if _, err := g.VerifyRetryToken(token, victim.Addr()); !errors.Is(err, quicguard.ErrTokenReplay) {
		t.Fatalf("token replay accepted: %v", err)
	}

	// End-to-end: an Initial carrying a replayed/cross-IP token never validates.
	g2, clock2 := newTestGuard(t)
	boundToken, err := g2.MintRetryToken(&quicguard.RetryTokenClaims{
		ClientIP:     victim.Addr(),
		OriginalDCID: clientDCID,
		RetrySCID:    serverSCID,
		IssuedAt:     clock2.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	fromAttacker := initialPacket(serverSCID, []byte{0xb0}, boundToken, 1200)
	result, err := g2.OnDatagram(attacker, fromAttacker)
	if err != nil {
		t.Fatalf("datagram should still be accounted: %v", err)
	}
	if !errors.Is(result.TokenErr, quicguard.ErrTokenWrongIP) || result.Validated {
		t.Fatalf("cross-IP token validated path: tokenErr=%v validated=%v", result.TokenErr, result.Validated)
	}

	// Tampering with one token byte breaks AEAD authentication.
	boundToken[5] ^= 0x01
	if _, err := g2.VerifyRetryToken(boundToken, victim.Addr()); !errors.Is(err, quicguard.ErrTokenAEAD) {
		t.Fatalf("tampered token accepted: %v", err)
	}
}

// TestAdversarialStatelessResetLoop exercises the strict length rules and the
// refusal to answer another stateless reset.
func TestAdversarialStatelessResetLoop(t *testing.T) {
	g, _ := newTestGuard(t)
	cid := clientDCID

	// Triggers of 21 bytes or below cannot get a legal reset.
	for _, n := range []int{0, 10, 21} {
		if _, err := g.BuildStatelessReset(cid, n); !errors.Is(err, quicguard.ErrResetTriggerTooSmall) {
			t.Fatalf("trigger=%d: want ErrResetTriggerTooSmall, got %v", n, err)
		}
	}

	// 22-byte trigger yields the minimum, strictly smaller 21-byte reset.
	reset, err := g.BuildStatelessReset(cid, 22)
	if err != nil {
		t.Fatal(err)
	}
	if len(reset) != 21 {
		t.Fatalf("min reset length = %d, want 21", len(reset))
	}
	if !g.IsStatelessReset(reset, cid) {
		t.Fatal("produced reset not recognized for its CID")
	}

	// A large trigger produces a reset that stays below the 64-byte cap and is
	// always strictly shorter than the trigger.
	big, err := g.BuildStatelessReset(cid, 1500)
	if err != nil {
		t.Fatal(err)
	}
	if len(big) != 64 || len(big) >= 1500 {
		t.Fatalf("big reset length = %d", len(big))
	}

	// Feeding a valid reset back must never produce another reset: loop broken.
	if _, err := g.MaybeStatelessReset(reset, cid); !errors.Is(err, quicguard.ErrResetLoop) {
		t.Fatalf("reset loop not intercepted: %v", err)
	}

	// A non-reset 30-byte packet still gets a compliant reset.
	plain := shortPacket(cid, 30)
	answer, err := g.MaybeStatelessReset(plain, cid)
	if err != nil {
		t.Fatalf("normal trigger denied: %v", err)
	}
	if len(answer) >= 30 || len(answer) < 21 {
		t.Fatalf("reset length %d not within [21,30)", len(answer))
	}

	// A reset token is CID-scoped: another CID must not recognize it.
	if g.IsStatelessReset(reset, serverSCID) {
		t.Fatal("reset token leaked across connection IDs")
	}
}
