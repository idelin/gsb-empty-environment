package netsec

import "testing"

func mustTracker(t *testing.T, width uint32) *Tracker {
	t.Helper()
	tr, err := NewTracker(width)
	if err != nil {
		t.Fatalf("NewTracker(%d): %v", width, err)
	}
	return tr
}

// acceptSeq simulates a packet that passed authentication: estimate,
// then commit.
func acceptSeq(t *testing.T, tr *Tracker, seq uint16) bool {
	t.Helper()
	return tr.Accept(seq, tr.EstimateROC(seq))
}

func TestMinWindowEnforced(t *testing.T) {
	if _, err := NewTracker(63); err != ErrWindowTooSmall {
		t.Fatalf("NewTracker(63) err = %v, want ErrWindowTooSmall", err)
	}
	if _, err := NewTracker(0); err != ErrWindowTooSmall {
		t.Fatalf("NewTracker(0) err = %v, want ErrWindowTooSmall", err)
	}
	if _, err := NewTracker(64); err != nil {
		t.Fatalf("NewTracker(64): %v", err)
	}
}

// TestEstimateROC checks the RFC 3711 Appendix A decision table.
func TestEstimateROC(t *testing.T) {
	cases := []struct {
		name    string
		sl      uint16
		roc     uint32
		seq     uint16
		wantROC uint32
	}{
		{"sl small, seq ahead", 100, 3, 200, 3},
		{"sl small, seq far behind => roc-1", 100, 3, 65530, 2},
		{"sl small, seq slightly behind", 100, 3, 90, 3},
		{"sl large, seq wrapped => roc+1", 65535, 3, 5, 4},
		{"sl large, seq near", 40000, 3, 39000, 3},
		{"sl large, seq ahead", 40000, 3, 41000, 3},
		{"boundary sl=32768, seq 0", 32768, 7, 0, 7},
		{"boundary sl=32767, seq 65535", 32767, 7, 65535, 7},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr := mustTracker(t, 64)
			tr.init = true
			tr.sl = c.sl
			tr.roc = c.roc
			tr.highest = uint64(c.roc)<<16 | uint64(c.sl)
			if got := tr.EstimateROC(c.seq); got != c.wantROC {
				t.Fatalf("EstimateROC(%d) = %d, want %d", c.seq, got, c.wantROC)
			}
		})
	}
}

// TestForgedJumpPacketCannotAdvanceROC is the core attack scenario: a
// forgery with a wildly jumping sequence number is estimated but never
// authenticated, so state must not move and later legitimate traffic
// must keep flowing.
func TestForgedJumpPacketCannotAdvanceROC(t *testing.T) {
	tr := mustTracker(t, 64)

	// Legitimate stream: seq 1..100.
	for seq := uint16(1); seq <= 100; seq++ {
		if !acceptSeq(t, tr, seq) {
			t.Fatalf("legit seq %d rejected", seq)
		}
	}
	if tr.ROC() != 0 || tr.SeqL() != 100 {
		t.Fatalf("after legit stream: roc=%d s_l=%d", tr.ROC(), tr.SeqL())
	}

	// Attacker forges a packet with a huge sequence jump. The gateway
	// estimates an index for it, but the auth tag fails, so Accept is
	// never called.
	forged := []uint16{50000, 60000, 1 << 15, 65535}
	for _, seq := range forged {
		_ = tr.EstimateIndex(seq) // auth fails here in real receive path
	}

	if tr.ROC() != 0 || tr.SeqL() != 100 || tr.Highest() != 100 {
		t.Fatalf("forged packets mutated state: roc=%d s_l=%d highest=%d",
			tr.ROC(), tr.SeqL(), tr.Highest())
	}

	// Legitimate traffic resumes unaffected.
	for seq := uint16(101); seq <= 200; seq++ {
		if !acceptSeq(t, tr, seq) {
			t.Fatalf("legit seq %d rejected after attack", seq)
		}
	}
	if tr.ROC() != 0 || tr.SeqL() != 200 {
		t.Fatalf("after recovery: roc=%d s_l=%d", tr.ROC(), tr.SeqL())
	}
}

// TestForgedJumpNearWrap: attacker tries to push the ROC forward by
// forging a small sequence number while s_l is large (the estimate
// would be roc+1). Without authentication nothing may change.
func TestForgedJumpNearWrap(t *testing.T) {
	tr := mustTracker(t, 64)
	for seq := uint16(39900); seq <= 40000; seq++ {
		if !acceptSeq(t, tr, seq) {
			t.Fatalf("legit seq %d rejected", seq)
		}
	}
	// Forgery that would estimate ROC+1 if it were real.
	if got := tr.EstimateROC(10); got != 1 {
		t.Fatalf("estimate for forged seq = %d, want 1", got)
	}
	// Auth fails; no Accept. Legit stream continues at ROC 0.
	if !acceptSeq(t, tr, 40001) {
		t.Fatal("legit seq 40001 rejected after forged wrap attempt")
	}
	if tr.ROC() != 0 {
		t.Fatalf("roc advanced to %d on forged packet", tr.ROC())
	}
}

// TestWrapIncrementsROCExactlyOnce drives the sequence number past
// 65535 and checks the ROC moves 0 -> 1 exactly, and indices are
// continuous.
func TestWrapIncrementsROCExactlyOnce(t *testing.T) {
	tr := mustTracker(t, 64)
	for seq := uint32(65530); seq <= 65535; seq++ {
		if !acceptSeq(t, tr, uint16(seq)) {
			t.Fatalf("seq %d rejected", seq)
		}
	}
	if tr.ROC() != 0 {
		t.Fatalf("roc before wrap = %d", tr.ROC())
	}
	if !acceptSeq(t, tr, 0) {
		t.Fatal("wrapped seq 0 rejected")
	}
	if tr.ROC() != 1 {
		t.Fatalf("roc after wrap = %d, want exactly 1", tr.ROC())
	}
	if tr.Highest() != 1<<16 {
		t.Fatalf("highest after wrap = %d, want %d", tr.Highest(), 1<<16)
	}
	for seq := uint16(1); seq <= 10; seq++ {
		if !acceptSeq(t, tr, seq) {
			t.Fatalf("post-wrap seq %d rejected", seq)
		}
	}
	if tr.ROC() != 1 {
		t.Fatalf("roc drifted to %d after wrap", tr.ROC())
	}
}

// TestReplayWithinWindow: an authenticated packet replayed inside the
// window is dropped; out-of-order arrivals within the window pass once.
func TestReplayWithinWindow(t *testing.T) {
	tr := mustTracker(t, 64)
	for seq := uint16(1); seq <= 60; seq++ {
		if !acceptSeq(t, tr, seq) {
			t.Fatalf("seq %d rejected", seq)
		}
	}
	if acceptSeq(t, tr, 60) {
		t.Fatal("replayed seq 60 accepted")
	}
	if acceptSeq(t, tr, 30) {
		t.Fatal("replayed seq 30 accepted")
	}
	// Late out-of-order packet, not yet seen: accepted once.
	if !acceptSeq(t, tr, 61) {
		t.Fatal("new seq 61 rejected")
	}
	if acceptSeq(t, tr, 61) {
		t.Fatal("replayed seq 61 accepted")
	}
}

// TestTooOldDropped: packets older than the window are dropped even
// with a valid auth tag.
func TestTooOldDropped(t *testing.T) {
	const width = 64
	tr := mustTracker(t, width)
	for seq := uint16(1); seq <= 100; seq++ {
		if seq == 37 {
			continue // leave a gap inside the future window
		}
		if !acceptSeq(t, tr, seq) {
			t.Fatalf("seq %d rejected", seq)
		}
	}
	// highest = 100, window covers indices 37..100.
	if acceptSeq(t, tr, 36) {
		t.Fatal("seq 36 (highest-width) accepted, want dropped as too old")
	}
	if !acceptSeq(t, tr, 37) {
		t.Fatal("seq 37 (oldest in window) rejected")
	}
	if acceptSeq(t, tr, 37) {
		t.Fatal("replayed seq 37 accepted")
	}
}

// TestEstimateIsReadOnly hammers EstimateROC/EstimateIndex and checks
// no state changes.
func TestEstimateIsReadOnly(t *testing.T) {
	tr := mustTracker(t, 128)
	if !acceptSeq(t, tr, 500) {
		t.Fatal("initial accept failed")
	}
	snapROC, snapSL, snapH := tr.ROC(), tr.SeqL(), tr.Highest()
	for seq := uint16(0); ; seq++ {
		_ = tr.EstimateROC(seq)
		_ = tr.EstimateIndex(seq)
		if seq == 65535 {
			break
		}
	}
	if tr.ROC() != snapROC || tr.SeqL() != snapSL || tr.Highest() != snapH {
		t.Fatal("estimation mutated tracker state")
	}
}

// TestWideWindow exercises a caller-supplied window larger than 64.
func TestWideWindow(t *testing.T) {
	const width = 256
	tr := mustTracker(t, width)
	for seq := uint16(1); seq <= 200; seq++ {
		if !acceptSeq(t, tr, seq) {
			t.Fatalf("seq %d rejected", seq)
		}
	}
	// highest = 200, window covers 1..200 minus... indices > 200-256,
	// so seq 1 is still inside a 256-wide window.
	if acceptSeq(t, tr, 1) {
		t.Fatal("replayed seq 1 accepted")
	}
	// Advance far enough that seq 1 ages out.
	for seq := uint16(201); seq <= 300; seq++ {
		if !acceptSeq(t, tr, seq) {
			t.Fatalf("seq %d rejected", seq)
		}
	}
	// highest = 300, window covers 45..300.
	if acceptSeq(t, tr, 44) {
		t.Fatal("seq 44 accepted though older than window")
	}
}
