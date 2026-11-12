package netsec

import (
	"math"
	"testing"
)

// Replayed initiation: an accepted timestamp rejects replays and older
// values, and only strictly greater timestamps are accepted afterwards.
func TestHandshakeReplayRejectsReplayedInitiation(t *testing.T) {
	var h HandshakeReplay

	ts := NewTAI64N(1000, 500)
	if !h.CheckTimestamp(ts) {
		t.Fatal("first timestamp should be acceptable")
	}
	if !h.CommitTimestamp(ts) {
		t.Fatal("first commit should succeed")
	}

	if h.CheckTimestamp(ts) {
		t.Fatal("replayed timestamp must be rejected by check")
	}
	if h.CommitTimestamp(ts) {
		t.Fatal("replayed timestamp must be rejected by commit")
	}

	older := NewTAI64N(1000, 499)
	if h.CheckTimestamp(older) || h.CommitTimestamp(older) {
		t.Fatal("older timestamp must be rejected")
	}

	newer := NewTAI64N(1000, 501)
	if !h.CommitTimestamp(newer) {
		t.Fatal("strictly greater timestamp must be accepted")
	}
	if h.CommitTimestamp(NewTAI64N(1000, 501)) {
		t.Fatal("same nanosecond timestamp must be rejected after commit")
	}
}

// A forged initiation carrying a huge timestamp passes the cheap check, but
// because MAC1/decryption fail it is never committed; the recorded maximum
// must stay put so honest handshakes keep working.
func TestHandshakeForgedLargeTimestampNotCommitted(t *testing.T) {
	var h HandshakeReplay

	honest := NewTAI64N(1000, 0)
	if !h.CommitTimestamp(honest) {
		t.Fatal("honest handshake should commit")
	}

	forged := NewTAI64N(math.MaxUint64, math.MaxUint32)
	if !h.CheckTimestamp(forged) {
		t.Fatal("forged huge timestamp passes the stateless pre-check")
	}
	// MAC1 verification / static key / timestamp decryption fail, so the
	// loop never calls CommitTimestamp for the forged message.

	next := NewTAI64N(1001, 0)
	if !h.CheckTimestamp(next) {
		t.Fatal("forged timestamp must not advance the recorded maximum")
	}
	if !h.CommitTimestamp(next) {
		t.Fatal("honest handshake after forged attempt must commit")
	}
	if h.CheckTimestamp(NewTAI64N(1001, 0)) {
		t.Fatal("state must track the honest maximum, not the forged one")
	}
}

// Window boundary: counters exactly ReplayWindowSize behind the highest are
// rejected, counters one step newer are still accepted, and a failed
// authentication does not consume a counter.
func TestSlidingWindowBoundary(t *testing.T) {
	var w SlidingWindow

	if !w.Mark(0) {
		t.Fatal("first counter should be accepted")
	}
	if w.Mark(0) {
		t.Fatal("replayed counter must be rejected")
	}

	// Failed AEAD authentication: checked but never marked.
	if !w.Check(7) {
		t.Fatal("counter 7 should pass the pre-auth check")
	}
	if !w.Mark(7) {
		t.Fatal("counter 7 must still be usable after a failed authentication")
	}

	// Advance the window so the highest counter is exactly ReplayWindowSize.
	if !w.Mark(ReplayWindowSize) {
		t.Fatal("counter at window size should be accepted")
	}
	if w.Check(0) || w.Mark(0) {
		t.Fatal("counter exactly ReplayWindowSize behind must be rejected")
	}
	if !w.Check(1) || !w.Mark(1) {
		t.Fatal("counter one step inside the window must be accepted")
	}
	if w.Mark(1) {
		t.Fatal("replayed in-window counter must be rejected")
	}

	// Out-of-order delivery within the window.
	if !w.Mark(ReplayWindowSize - 1) {
		t.Fatal("out-of-order in-window counter should be accepted")
	}
	if w.Mark(ReplayWindowSize - 1) {
		t.Fatal("replayed out-of-order counter must be rejected")
	}

	// A jump larger than the window evicts everything older.
	if !w.Mark(3*ReplayWindowSize + 10) {
		t.Fatal("large forward jump should be accepted")
	}
	if w.Check(ReplayWindowSize) || w.Mark(ReplayWindowSize) {
		t.Fatal("counter evicted by a large jump must be rejected")
	}
}

// Rotation overlap: the old keypair keeps authenticating packets until the
// first valid data packet arrives under the new keypair.
func TestKeyRotationOverlap(t *testing.T) {
	var kr KeyRotation

	oldKp := NewKeypair(1)
	kr.Rotate(oldKp)
	if !kr.ConfirmValid(oldKp, 0) {
		t.Fatal("packet on initial keypair should be accepted")
	}

	newKp := NewKeypair(2)
	kr.Rotate(newKp)
	if !kr.HasPrevious() {
		t.Fatal("old keypair must be retained after rotation")
	}

	// In-flight packets under the old keypair are still valid.
	if got := kr.Lookup(1); got != oldKp {
		t.Fatal("old keypair must still be found before new key confirms")
	}
	if !kr.ReceiveCheck(oldKp, 1) || !kr.ConfirmValid(oldKp, 1) {
		t.Fatal("old keypair packet during overlap must be accepted")
	}

	// First valid data packet under the new keypair retires the old one.
	if got := kr.Lookup(2); got != newKp {
		t.Fatal("new keypair must be found")
	}
	if !kr.ReceiveCheck(newKp, 0) || !kr.ConfirmValid(newKp, 0) {
		t.Fatal("first packet on new keypair should be accepted")
	}
	if kr.HasPrevious() {
		t.Fatal("old keypair must be dropped after new key confirms")
	}
	if got := kr.Lookup(1); got != nil {
		t.Fatal("old keypair must no longer be found after retirement")
	}
	if kr.ReceiveCheck(oldKp, 2) {
		t.Fatal("packets on retired keypair must be rejected")
	}

	// The new keypair keeps its own replay protection.
	if kr.ConfirmValid(newKp, 0) {
		t.Fatal("replayed counter on new keypair must be rejected")
	}
	if !kr.ConfirmValid(newKp, 1) {
		t.Fatal("fresh counter on new keypair must be accepted")
	}
}
