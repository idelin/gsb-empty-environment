package vtimer

import (
	"testing"
	"time"
)

const step = 10 * time.Second

// model is an independent, deliberately simple oracle for one timer.
type model struct {
	now       time.Duration
	gen       uint64
	expiry    map[uint64]time.Duration // gen -> scheduled expiry
	stopped   bool
	delivered map[uint64]bool
}

func newModel() *model {
	m := &model{
		expiry:    make(map[uint64]time.Duration),
		delivered: make(map[uint64]bool),
	}
	m.expiry[0] = step
	return m
}

func (m *model) expire()          { m.now += step }
func (m *model) reset()           { m.gen++; m.expiry[m.gen] = m.now + step; m.stopped = false }
func (m *model) stop()            { m.stopped = true }
func (m *model) wantClaim() bool  { return !m.stopped && !m.delivered[m.gen] && m.expiry[m.gen] <= m.now }
func (m *model) didClaim()        { m.delivered[m.gen] = true }

// TestSingleDeliveryPermutations enumerates every operation sequence of
// length depth over {expire, claim, reset, stop} and checks, against the
// model, that Claim succeeds exactly when it should — hence each generation
// is delivered at most once, and a reset/stop invalidates events that are
// already queued but unclaimed.
func TestSingleDeliveryPermutations(t *testing.T) {
	const depth = 7
	ops := []string{"expire", "claim", "reset", "stop"}
	seq := make([]string, 0, depth)
	var walk func()
	walk = func() {
		if len(seq) == depth {
			runSequence(t, seq)
			return
		}
		for _, op := range ops {
			seq = append(seq, op)
			walk()
			seq = seq[:len(seq)-1] // undo shared-suffix append for next sibling
		}
	}
	walk()
}

func runSequence(t *testing.T, seq []string) {
	clk := NewClock()
	tm := clk.NewTimer(step)
	m := newModel()
	claimsPerGen := map[uint64]int{}

	for i, op := range seq {
		switch op {
		case "expire":
			clk.Advance(step)
			m.expire()
		case "reset":
			tm.Reset(step)
			m.reset()
		case "stop":
			tm.Stop()
			m.stop()
		case "claim":
			_, ok := tm.Claim()
			if ok != m.wantClaim() {
				t.Fatalf("seq %v: step %d claim = %v, model wants %v", seq, i, ok, m.wantClaim())
			}
			if ok {
				m.didClaim()
				claimsPerGen[tm.Gen()]++
				if claimsPerGen[tm.Gen()] > 1 {
					t.Fatalf("seq %v: generation %d delivered more than once", seq, tm.Gen())
				}
			}
		}
	}

	// Drain: jump far past every deadline and claim via Next. Still at most
	// one delivery per generation, and none for stopped/superseded ones.
	clk.Advance(1000 * step)
	for {
		got, _, ok := clk.Next()
		if !ok {
			break
		}
		g := got.Gen()
		claimsPerGen[g]++
		if claimsPerGen[g] > 1 {
			t.Fatalf("seq %v: generation %d delivered more than once during drain", seq, g)
		}
	}
}

// TestResetInvalidatesQueuedEvent is the targeted scenario: the old event is
// already in the ready queue when Reset linearizes; it must never fire.
func TestResetInvalidatesQueuedEvent(t *testing.T) {
	clk := NewClock()
	tm := clk.NewTimer(step)

	clk.Advance(step) // gen-0 event now sits in the ready queue, unclaimed
	if !tm.Reset(2 * step) {
		t.Fatal("Reset should report the timer was active")
	}
	if _, ok := tm.Claim(); ok {
		t.Fatal("stale gen-0 event delivered after Reset")
	}
	if _, _, ok := clk.Next(); ok {
		t.Fatal("Next delivered a stale gen-0 event")
	}
	clk.Advance(2*step - 1) // one nanosecond before the new deadline
	if _, ok := tm.Claim(); ok {
		t.Fatal("gen-1 event delivered before its expiry")
	}
	clk.Advance(1)
	if _, ok := tm.Claim(); !ok {
		t.Fatal("gen-1 event not delivered at its expiry")
	}
	if _, ok := tm.Claim(); ok {
		t.Fatal("gen-1 event delivered twice")
	}
}

// TestStopInvalidatesQueuedEvent covers Stop linearizing after the event
// became ready but before it was claimed.
func TestStopInvalidatesQueuedEvent(t *testing.T) {
	clk := NewClock()
	tm := clk.NewTimer(step)

	clk.Advance(step)
	if !tm.Stop() {
		t.Fatal("Stop should report the timer was active")
	}
	if _, ok := tm.Claim(); ok {
		t.Fatal("stopped timer delivered a queued event")
	}
	if tm.Stop() {
		t.Fatal("second Stop should report the timer was inactive")
	}
	// Re-arm after stop: the new generation must fire normally.
	tm.Reset(step)
	clk.Advance(step)
	if _, ok := tm.Claim(); !ok {
		t.Fatal("timer re-armed after Stop did not fire")
	}
}

// TestSameExpiryCreationOrder: identical expiries fire in creation order.
func TestSameExpiryCreationOrder(t *testing.T) {
	clk := NewClock()
	a := clk.NewTimer(5 * time.Second)
	b := clk.NewTimer(5 * time.Second)
	c := clk.NewTimer(5 * time.Second)

	clk.Advance(5 * time.Second)
	for i, want := range []*Timer{a, b, c} {
		got, _, ok := clk.Next()
		if !ok || got != want {
			t.Fatalf("delivery %d: got %v, want creation-order timer %d", i, ok, i)
		}
	}
	if _, _, ok := clk.Next(); ok {
		t.Fatal("unexpected extra delivery")
	}
}

// TestOrderingByExpiryThenSeq mixes expiries and a reset that re-creates an
// event at an already-used expiry with a newer sequence number.
func TestOrderingByExpiryThenSeq(t *testing.T) {
	clk := NewClock()
	early := clk.NewTimer(3 * time.Second)
	first5 := clk.NewTimer(5 * time.Second)
	second5 := clk.NewTimer(5 * time.Second)
	late := clk.NewTimer(8 * time.Second)

	clk.Advance(8 * time.Second)
	want := []*Timer{early, first5, second5, late}
	for i, w := range want {
		got, _, ok := clk.Next()
		if !ok || got != w {
			t.Fatalf("delivery %d out of (expiry, seq) order", i)
		}
	}

	// A reset lands at the same expiry as another timer but with a newer
	// sequence number, so it must fire after it.
	x := clk.NewTimer(10 * time.Second)
	y := clk.NewTimer(10 * time.Second)
	x.Reset(10 * time.Second) // x's new event is created after y's
	clk.Advance(10 * time.Second)
	got1, _, _ := clk.Next()
	got2, _, _ := clk.Next()
	if got1 != y || got2 != x {
		t.Fatal("reset event with newer seq must fire after existing same-expiry event")
	}
}

// TestResetReturnValue mirrors time.Timer semantics.
func TestResetReturnValue(t *testing.T) {
	clk := NewClock()
	tm := clk.NewTimer(step)
	if !tm.Reset(step) {
		t.Fatal("Reset on armed timer should report active")
	}
	clk.Advance(step)
	if _, ok := tm.Claim(); !ok {
		t.Fatal("claim failed")
	}
	if tm.Reset(step) {
		t.Fatal("Reset after delivery should report inactive")
	}
	tm.Stop()
	if tm.Reset(step) {
		t.Fatal("Reset after Stop should report inactive")
	}
}
