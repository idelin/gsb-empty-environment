package streamjoin

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func at(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }

func left(id string, sec int) Event   { return Event{Kind: LeftEvent, ID: id, At: at(sec)} }
func right(id string, sec int) Event  { return Event{Kind: RightEvent, ID: id, At: at(sec)} }
func cancel(id string, sec int) Event { return Event{Kind: CancelEvent, ID: id, At: at(sec)} }

type harness struct {
	clock   *ManualClock
	joiner  *Joiner
	results []Result
}

func newHarness() *harness {
	h := &harness{clock: NewManualClock(t0)}
	h.joiner = NewJoiner(5*time.Second, 10*time.Second, h.clock, func(r Result) {
		h.results = append(h.results, r)
	})
	return h
}

func (h *harness) assertZeroLeak(t *testing.T) {
	t.Helper()
	s := h.joiner.Stats()
	if s.PendingLefts != 0 || s.BufferedRights != 0 || s.EmittedLefts != 0 {
		t.Fatalf("state leak: %+v", s)
	}
	if n := h.clock.PendingTimers(); n != 0 {
		t.Fatalf("timer leak: %d pending timers", n)
	}
}

// Acceptance: L1 at t=0, R1 at t=3 -> immediate paired output.
func TestImmediatePair(t *testing.T) {
	h := newHarness()
	h.joiner.Handle(left("L1", 0))
	h.clock.Advance(at(3))
	h.joiner.Handle(right("L1", 3))

	if len(h.results) != 1 {
		t.Fatalf("want 1 result, got %d: %+v", len(h.results), h.results)
	}
	r := h.results[0]
	if r.Kind != Paired || r.ID != "L1" || r.Late {
		t.Fatalf("want immediate PAIRED for L1, got %+v", r)
	}
	if !r.Left.At.Equal(at(0)) || !r.Right.At.Equal(at(3)) {
		t.Fatalf("wrong event times in %+v", r)
	}
	// Window passes with nothing more emitted.
	h.clock.Advance(at(60))
	if len(h.results) != 1 {
		t.Fatalf("extra output after window: %+v", h.results)
	}
	h.assertZeroLeak(t)
}

// Acceptance: L2 at t=10, cancel at t=12, timeout at t=15 must not fire.
func TestCancelSuppressesOrphanOutput(t *testing.T) {
	h := newHarness()
	h.clock.Advance(at(10))
	h.joiner.Handle(left("L2", 10))
	h.clock.Advance(at(12))
	h.joiner.Handle(cancel("L2", 12))
	h.clock.Advance(at(15)) // original deadline
	h.clock.Advance(at(60))

	if len(h.results) != 0 {
		t.Fatalf("cancelled left must produce no output, got %+v", h.results)
	}
	h.assertZeroLeak(t)
}

// Timeout with no match and no cancel -> L-Only result.
func TestTimeoutEmitsLeftOnly(t *testing.T) {
	h := newHarness()
	h.joiner.Handle(left("L3", 0))
	h.clock.Advance(at(4))
	if len(h.results) != 0 {
		t.Fatalf("output before deadline: %+v", h.results)
	}
	h.clock.Advance(at(5))
	if len(h.results) != 1 || h.results[0].Kind != LeftOnly || h.results[0].ID != "L3" {
		t.Fatalf("want LEFT-ONLY for L3, got %+v", h.results)
	}
	h.clock.Advance(at(120))
	h.assertZeroLeak(t)
}

// Late R (event time inside the window) after a LeftOnly -> compensating pair.
func TestLateRightCompensates(t *testing.T) {
	h := newHarness()
	h.joiner.Handle(left("L4", 0))
	h.clock.Advance(at(6)) // past the t=5 deadline: LEFT-ONLY emitted
	if len(h.results) != 1 || h.results[0].Kind != LeftOnly {
		t.Fatalf("want LEFT-ONLY first, got %+v", h.results)
	}
	// R4's event time is t=3 (in-window) but it arrives at t=7.
	h.clock.Advance(at(7))
	h.joiner.Handle(right("L4", 3))
	if len(h.results) != 2 {
		t.Fatalf("want compensating pair, got %+v", h.results)
	}
	r := h.results[1]
	if r.Kind != Paired || !r.Late || r.ID != "L4" {
		t.Fatalf("want PAIRED late=true, got %+v", r)
	}
	h.clock.Advance(at(120))
	h.assertZeroLeak(t)
}

// Late R beyond the lateness tolerance is dropped silently.
func TestLateRightBeyondToleranceDropped(t *testing.T) {
	h := newHarness()
	h.joiner.Handle(left("L5", 0))
	h.clock.Advance(at(6)) // LEFT-ONLY
	h.clock.Advance(at(20))
	h.joiner.Handle(right("L5", 3)) // in-window event time, but past tolerance
	if len(h.results) != 1 {
		t.Fatalf("late right beyond tolerance must not pair, got %+v", h.results)
	}
	h.clock.Advance(at(120))
	h.assertZeroLeak(t)
}

// Out-of-order: R arrives before L and is buffered; L matches it on arrival.
func TestOutOfOrderRightBeforeLeft(t *testing.T) {
	h := newHarness()
	h.joiner.Handle(right("L6", 3)) // arrives first, no left yet -> buffered
	h.clock.Advance(at(4))
	h.joiner.Handle(left("L6", 0)) // event time t=0, window covers t=3
	if len(h.results) != 1 || h.results[0].Kind != Paired || h.results[0].Late {
		t.Fatalf("want PAIRED from buffered right, got %+v", h.results)
	}
	h.clock.Advance(at(120))
	h.assertZeroLeak(t)
}

// Out-of-order storm: mixed orders, cancels, and unmatched events must
// leave zero internal state once time advances past all retention.
func TestOutOfOrderZeroLeak(t *testing.T) {
	h := newHarness()

	// A: normal pair.
	h.joiner.Handle(left("A", 0))
	h.clock.Advance(at(2))
	h.joiner.Handle(right("A", 2))

	// B: right buffered out of order, left arrives later and pairs.
	h.clock.Advance(at(3))
	h.joiner.Handle(right("B", 4))
	h.clock.Advance(at(5))
	h.joiner.Handle(left("B", 1))

	// C: left cancelled before deadline.
	h.clock.Advance(at(6))
	h.joiner.Handle(left("C", 6))
	h.clock.Advance(at(8))
	h.joiner.Handle(cancel("C", 8))

	// D: left times out -> LEFT-ONLY, then late right compensates.
	h.clock.Advance(at(9))
	h.joiner.Handle(left("D", 9))
	h.clock.Advance(at(15)) // deadline t=14 passed
	h.joiner.Handle(right("D", 12))

	// E: right never matched, buffer must expire on its own.
	h.joiner.Handle(right("E", 15))

	// F: left never matched, no late right; emitted record must expire.
	h.clock.Advance(at(16))
	h.joiner.Handle(left("F", 16))

	// G: buffered right cancelled before its left ever shows up.
	h.joiner.Handle(right("G", 16))
	h.clock.Advance(at(17))
	h.joiner.Handle(cancel("G", 17))

	h.clock.Advance(at(15)) // D deadline already passed at t=14
	h.clock.Advance(at(200))

	want := []struct {
		kind ResultKind
		id   string
		late bool
	}{
		{Paired, "A", false},
		{Paired, "B", false},
		{LeftOnly, "D", false},
		{Paired, "D", true},
		{LeftOnly, "F", false},
	}
	if len(h.results) != len(want) {
		t.Fatalf("want %d results, got %d: %+v", len(want), len(h.results), h.results)
	}
	for i, w := range want {
		got := h.results[i]
		if got.Kind != w.kind || got.ID != w.id || got.Late != w.late {
			t.Fatalf("result %d: want {%v %v late=%v}, got %+v", i, w.kind, w.id, w.late, got)
		}
	}
	h.assertZeroLeak(t)
}

// Cancel also discards a buffered right for the same key.
func TestCancelRemovesBufferedRight(t *testing.T) {
	h := newHarness()
	h.joiner.Handle(right("L7", 2))
	h.clock.Advance(at(3))
	h.joiner.Handle(cancel("L7", 3))
	h.clock.Advance(at(4))
	h.joiner.Handle(left("L7", 0)) // would have matched the buffered right
	h.clock.Advance(at(6))         // left now times out -> LEFT-ONLY
	if len(h.results) != 1 || h.results[0].Kind != LeftOnly {
		t.Fatalf("want only LEFT-ONLY after cancel, got %+v", h.results)
	}
	h.clock.Advance(at(120))
	h.assertZeroLeak(t)
}

// Right outside the window of a waiting left does not pair.
func TestRightOutsideWindowNoPair(t *testing.T) {
	h := newHarness()
	h.joiner.Handle(left("L8", 0))
	h.clock.Advance(at(6))
	h.joiner.Handle(right("L8", 6)) // event time t=6 > t=0+5s
	if len(h.results) != 1 || h.results[0].Kind != LeftOnly {
		t.Fatalf("want only LEFT-ONLY, got %+v", h.results)
	}
	h.clock.Advance(at(120))
	h.assertZeroLeak(t)
}
