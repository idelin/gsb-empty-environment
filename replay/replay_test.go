package replay

import (
	"sync"
	"testing"
	"time"
)

func TestNewWindowRejectsBadWidth(t *testing.T) {
	for _, width := range []uint64{0, 32, 100, 63} {
		if _, err := NewWindow(width); err == nil {
			t.Fatalf("width %d: expected construction error", width)
		}
	}
	if _, err := NewWindow(2048); err != nil {
		t.Fatalf("width 2048 must construct successfully: %v", err)
	}
}

// TestWindowBoundaryWMinus1 covers a jump of exactly W-1: the vacated
// range touches every word but clears no bit of the surviving counter 0.
func TestWindowBoundaryWMinus1(t *testing.T) {
	const W uint64 = 128
	win, _ := NewWindow(W)

	if !win.Check(0) {
		t.Fatal("first counter 0 must be accepted")
	}
	if !win.Check(W - 1) {
		t.Fatal("jump of W-1 must be accepted")
	}

	if !win.Seen(0) || !win.Seen(W-1) {
		t.Fatalf("both edge counters must remain set: words=%#x", win.Words())
	}
	if win.Max() != W-1 {
		t.Fatalf("max = %d, want %d", win.Max(), W-1)
	}
	// Counter 0 is exactly on the window floor, so still inside the
	// window; its bit is set, hence a replay.
	if win.Check(0) {
		t.Fatal("counter on window floor must be reported as duplicate, not silently re-accepted")
	}
}

// TestWindowBoundaryW covers a jump of exactly W: the whole table must be
// cleared before the new bit is set, so the oldest counter ages out.
func TestWindowBoundaryW(t *testing.T) {
	const W uint64 = 128
	win, _ := NewWindow(W)

	win.Check(0)
	if !win.Check(W) {
		t.Fatal("jump of W must be accepted")
	}
	if win.Seen(0) {
		t.Fatalf("counter 0 must have aged out after a W jump: words=%#x", win.Words())
	}
	if !win.Seen(W) {
		t.Fatal("new counter must be the only set bit")
	}
	if countSetBits(win.Words()) != 1 {
		t.Fatalf("expected exactly one set bit, got %d (%#x)", countSetBits(win.Words()), win.Words())
	}
	if win.Check(0) {
		t.Fatal("aged-out counter 0 must be rejected as replay")
	}
}

// TestWindowBoundaryWPlus65 covers a jump larger than W spanning an
// additional word; the table must again be fully reset.
func TestWindowBoundaryWPlus65(t *testing.T) {
	const W uint64 = 128
	win, _ := NewWindow(W)

	win.Check(0)
	win.Check(5)
	if !win.Check(W + 65) {
		t.Fatal("jump of W+65 must be accepted")
	}
	if countSetBits(win.Words()) != 1 {
		t.Fatalf("table must be reset to a single bit, got %#x", win.Words())
	}
	for _, stale := range []uint64{0, 5} {
		if win.Seen(stale) {
			t.Fatalf("stale counter %d must be gone after W+65 jump", stale)
		}
		if win.Check(stale) {
			t.Fatalf("stale counter %d must be rejected", stale)
		}
	}
	// Counters inside the new W-wide window but never seen must accept.
	for _, fresh := range []uint64{W - 1, W} {
		if win.Seen(fresh) {
			t.Fatalf("counter %d must not be marked seen yet", fresh)
		}
		if !win.Check(fresh) {
			t.Fatalf("in-window unseen counter %d must be accepted", fresh)
		}
	}
	if !win.Seen(W + 65) {
		t.Fatal("new counter must be set")
	}
}

// TestWindowPreciseWordClearing checks the RFC 6479 rule that only the
// bitmap words actually crossed by a jump are touched, including partial
// boundary words and ring wrap-around.
func TestWindowPreciseWordClearing(t *testing.T) {
	const W uint64 = 128
	win, _ := NewWindow(W)

	// Keep bits in both words, then advance to counter 66: the first
	// word is fully cleared while surviving bits in the second word
	// remain.
	win.Check(0)
	win.Check(64)
	win.Check(65)
	if !win.Check(66) {
		t.Fatal("new counter 66 must be accepted")
	}
	words := win.Words()
	if words[0] != 1 {
		t.Fatalf("word 0 must retain only the bit of counter 0, got %#x", words[0])
	}
	if !win.Seen(64) || !win.Seen(65) || !win.Seen(66) {
		t.Fatalf("second-word bits must survive, words=%#x", words)
	}

	// Wrap around the ring: the vacated range crosses the ring end and
	// continues at word 0.
	if !win.Check(W - 2) {
		t.Fatal("counter near ring end must be accepted")
	}
	if !win.Check(W + 3) {
		t.Fatal("wrapping jump must be accepted")
	}
	words = win.Words()
	if !win.Seen(W+3) || !win.Seen(W-2) {
		t.Fatalf("recent counters must survive wrap, words=%#x", words)
	}
	// The new window floor is (W+3)-W+1 = 4; counter 3 sits below it
	// even though ring slot 3 now belongs to the new highest counter.
	if win.Seen(3) {
		t.Fatalf("counter 3 below the floor must be reported unseen, words=%#x", words)
	}
	if win.Check(3) {
		t.Fatal("counter below the window floor must keep being rejected as replay")
	}
	if win.Check(3) {
		t.Fatal("repeated floor-violating check must stay rejected")
	}
}

// TestWindowDuplicateAndFloor exercises plain duplicates and the window
// floor boundary.
func TestWindowDuplicateAndFloor(t *testing.T) {
	const W uint64 = 256
	win, _ := NewWindow(W)

	win.Check(1000)
	if win.Check(1000) {
		t.Fatal("duplicate counter must be rejected")
	}
	if !win.Check(1000 + W - 1) {
		t.Fatal("counter W-1 above max must be accepted")
	}
	if win.Check(1000) {
		t.Fatal("oldest counter at floor minus one must be rejected after advance")
	}
	if !win.Check(1001) {
		t.Fatal("in-window unseen counter must be accepted")
	}
	if win.Check(1001) {
		t.Fatal("just-accepted counter must now be a duplicate")
	}
}

func newTestRing(t *testing.T) (*KeyRing, *manualClock) {
	t.Helper()
	clock := &manualClock{}
	return NewKeyRing(clock.now), clock
}

func testConfig(index uint32, now func() time.Time) SessionConfig {
	return SessionConfig{
		Index:           index,
		RejectAfterTime: 10 * time.Millisecond,
		WindowWidth:     128,
		Now:             now,
	}
}

// TestAtomicThreeGenerationPromotion verifies that Current and Previous
// stay alive while Next is pending, and that only the first fully valid
// Next packet rotates all three slots atomically.
func TestAtomicThreeGenerationPromotion(t *testing.T) {
	ring, clock := newTestRing(t)

	first, err := ring.Bootstrap(testConfig(10, clock.now))
	if err != nil {
		t.Fatal(err)
	}
	second, err := ring.CreateNext(testConfig(20, clock.now))
	if err != nil {
		t.Fatal(err)
	}

	if ring.Current().index != 10 || ring.Next().index != 20 || ring.Previous() != nil {
		t.Fatal("unexpected slots before promotion")
	}

	// A valid Current packet must not promote anything.
	res := ring.Receive(Packet{Index: 10, Counter: 0, Authentic: true})
	if !res.Accepted || res.Promoted {
		t.Fatalf("current-generation packet must not promote, got %+v", res)
	}
	if ring.Next() == nil || ring.Current().index != 10 {
		t.Fatal("Next must remain pending until its own first valid packet")
	}

	// First authenticated, replay-free Next packet triggers the switch.
	res = ring.Receive(Packet{Index: 20, Counter: 0, Authentic: true})
	if !res.Accepted || !res.Promoted {
		t.Fatalf("first valid Next packet must promote, got %+v", res)
	}
	if ring.Current().index != 20 || ring.Previous().index != 10 || ring.Next() != nil {
		t.Fatalf("atomic rotation failed: prev=%v cur=%v next=%v",
			ring.Previous(), ring.Current(), ring.Next())
	}

	// A second rekey drops the oldest generation on confirmation.
	third, err := ring.CreateNext(testConfig(30, clock.now))
	if err != nil {
		t.Fatal(err)
	}
	if ring.Previous().index != 10 || ring.Current().index != 20 || ring.Next().index != 30 {
		t.Fatal("new Next must preserve the older generations")
	}
	res = ring.Receive(Packet{Index: 30, Counter: 0, Authentic: true})
	if !res.Accepted || !res.Promoted {
		t.Fatalf("second promotion failed: %+v", res)
	}
	if ring.Current().index != 30 || ring.Previous().index != 20 || ring.Next() != nil {
		t.Fatal("old Current must become Previous and the oldest generation must be dropped")
	}
	if got := ring.Receive(Packet{Index: 10, Counter: 1, Authentic: true}); got.Reason != RejectNoSession {
		t.Fatalf("dropped generation must be unknown, got %+v", got)
	}

	if first.Window() == second.Window() || second.Window() == third.Window() {
		t.Fatal("each session must own an independent window")
	}
}

// TestForgedNextNeverPromotes ensures authentication failures leave the
// key ring, including the pending Next slot, completely untouched.
func TestForgedNextNeverPromotes(t *testing.T) {
	ring, clock := newTestRing(t)
	ring.Bootstrap(testConfig(10, clock.now))
	ring.CreateNext(testConfig(20, clock.now))

	for counter := uint64(0); counter < 3; counter++ {
		res := ring.Receive(Packet{Index: 20, Counter: counter, Authentic: false})
		if res.Accepted || res.Reason != RejectForge || res.Promoted {
			t.Fatalf("forged packet %d: %+v", counter, res)
		}
	}
	if ring.Next() == nil || ring.Next().index != 20 || ring.Current().index != 10 {
		t.Fatal("forged Next packets must never rotate the generations")
	}
	if ring.Next().Accepted() != 0 {
		t.Fatal("forged packets must not mutate session state")
	}

	// Forgery consumes no counter: the real first valid packet still
	// uses counter 0 and still promotes.
	res := ring.Receive(Packet{Index: 20, Counter: 0, Authentic: true})
	if !res.Accepted || !res.Promoted {
		t.Fatalf("valid Next packet after forgery attempts must promote: %+v", res)
	}

	before := ring.Current().index
	if got := ring.Receive(Packet{Index: 999, Counter: 0, Authentic: false}); got.Reason != RejectForge {
		t.Fatalf("forged unknown packet: %+v", got)
	}
	if ring.Current().index != before {
		t.Fatal("forged unknown packet must not change the ring")
	}
}

// TestPreviousLatePacketsAroundExpiry verifies late Previous packets are
// delivered while the generation is still live, then rejected once the
// session age reaches RejectAfterTime.
func TestPreviousLatePacketsAroundExpiry(t *testing.T) {
	ring, clock := newTestRing(t)
	ring.Bootstrap(testConfig(10, clock.now))
	ring.CreateNext(testConfig(20, clock.now))

	ring.Receive(Packet{Index: 10, Counter: 0, Authentic: true})
	ring.Receive(Packet{Index: 20, Counter: 0, Authentic: true}) // promotes at t=0
	if ring.Previous().index != 10 {
		t.Fatal("generation 10 must be the Previous slot")
	}

	// At t=4ms both generations are inside their 10ms lifetime, so an
	// out-of-order late Previous packet still passes.
	clock.advance(4)
	res := ring.Receive(Packet{Index: 10, Counter: 5, Authentic: true})
	if !res.Accepted || res.SessionIndex != 10 {
		t.Fatalf("late previous packet before expiry must pass: %+v", res)
	}

	if got := ring.Receive(Packet{Index: 10, Counter: 5, Authentic: true}); got.Reason != RejectReplay {
		t.Fatalf("expected replay rejection, got %+v", got)
	}

	// Confirm the new generation is healthy while the old one is live.
	if got := ring.Receive(Packet{Index: 20, Counter: 1, Authentic: true}); !got.Accepted {
		t.Fatalf("current generation must keep working: %+v", got)
	}

	// At t=11ms every generation created at t=0 has aged out.
	clock.advance(7)
	if got := ring.Receive(Packet{Index: 10, Counter: 6, Authentic: true}); got.Reason != RejectExpired {
		t.Fatalf("expected expiry rejection, got %+v", got)
	}
	if got := ring.Receive(Packet{Index: 10, Counter: 0, Authentic: true}); got.Reason != RejectExpired {
		t.Fatalf("expired session must reject even known counters, got %+v", got)
	}
	if got := ring.Receive(Packet{Index: 20, Counter: 2, Authentic: true}); got.Reason != RejectExpired {
		t.Fatalf("current generation must expire on the same clock, got %+v", got)
	}
}

// TestRejectAfterMessages covers the accepted-message cap and the 2^64
// sequence-number ceiling.
func TestRejectAfterMessages(t *testing.T) {
	ring, clock := newTestRing(t)
	cfg := testConfig(10, clock.now)
	cfg.RejectAfterMessages = 3
	ring.Bootstrap(cfg)

	for i := uint64(0); i < 3; i++ {
		if got := ring.Receive(Packet{Index: 10, Counter: i, Authentic: true}); !got.Accepted {
			t.Fatalf("message %d must be accepted: %+v", i, got)
		}
	}
	if got := ring.Receive(Packet{Index: 10, Counter: 3, Authentic: true}); got.Reason != RejectMessageCap {
		t.Fatalf("fourth message must hit the cap, got %+v", got)
	}

	ceilingRing, ceilingClock := newTestRing(t)
	ceilingCfg := testConfig(11, ceilingClock.now)
	ceilingCfg.RejectAfterMessages = 100
	ceilingRing.Bootstrap(ceilingCfg)
	ceiling := ^uint64(0) - 100 + 1
	if got := ceilingRing.Receive(Packet{Index: 11, Counter: ceiling, Authentic: true}); got.Reason != RejectMessageCap {
		t.Fatalf("counter at the 2^64 ceiling must be refused, got %+v", got)
	}
	if got := ceilingRing.Receive(Packet{Index: 11, Counter: ceiling - 1, Authentic: true}); !got.Accepted {
		t.Fatalf("counter just below the ceiling must be accepted, got %+v", got)
	}
}

// TestReceiveConcurrent exercises the mutex under parallel receivers so
// the race detector can observe any unsynchronized state; every accepted
// counter must remain unique within the window.
func TestReceiveConcurrent(t *testing.T) {
	ring, clock := newTestRing(t)
	cfg := testConfig(10, clock.now)
	cfg.RejectAfterTime = 0
	cfg.RejectAfterMessages = 0
	cfg.WindowWidth = 2048
	ring.Bootstrap(cfg)

	const workers = 8
	const perWorker = 200
	var wg sync.WaitGroup
	accepted := make(chan uint64, workers*perWorker)
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(base uint64) {
			defer wg.Done()
			for i := uint64(0); i < perWorker; i++ {
				counter := base + i
				res := ring.Receive(Packet{Index: 10, Counter: counter, Authentic: true})
				if res.Accepted {
					accepted <- counter
				}
			}
		}(uint64(worker) * perWorker)
	}
	wg.Wait()
	close(accepted)

	seen := make(map[uint64]struct{}, workers*perWorker)
	for counter := range accepted {
		if _, dup := seen[counter]; dup {
			t.Fatalf("counter %d accepted twice", counter)
		}
		seen[counter] = struct{}{}
	}
	if len(seen) != workers*perWorker {
		t.Fatalf("accepted %d unique counters, want %d", len(seen), workers*perWorker)
	}
	if ring.Current().Accepted() != workers*perWorker {
		t.Fatalf("session accepted count = %d, want %d", ring.Current().Accepted(), workers*perWorker)
	}
}
