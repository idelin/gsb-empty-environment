package vtimer

import (
	"fmt"
	"testing"
	"time"
)

var epoch = time.Unix(0, 0)

const step = time.Second

func mustClaim(t *testing.T, c *Clock, tm *Timer, wantDeadline time.Time) Event {
	t.Helper()
	ev, ok := c.Next()
	if !ok {
		t.Fatalf("expected a ready event, got none")
	}
	if ev.Timer != tm {
		t.Fatalf("claimed event for wrong timer")
	}
	if !ev.Deadline.Equal(wantDeadline) {
		t.Fatalf("deadline = %v, want %v", ev.Deadline, wantDeadline)
	}
	return ev
}

func mustDrain(t *testing.T, c *Clock) {
	t.Helper()
	if ev, ok := c.Next(); ok {
		t.Fatalf("expected empty ready queue, got event %+v", ev)
	}
}

func TestSingleDelivery(t *testing.T) {
	c := NewClock(epoch)
	tm := c.NewTimer(step)
	c.Advance(step)
	mustClaim(t, c, tm, epoch.Add(step))
	mustDrain(t, c) // one-shot: nothing delivered twice
}

func TestResetInvalidatesQueuedEvent(t *testing.T) {
	c := NewClock(epoch)
	tm := c.NewTimer(step)
	c.Advance(step) // event of gen 0 enters the ready queue, unclaimed
	if !tm.Reset(2 * step) {
		t.Fatalf("Reset should report the timer was active")
	}
	mustDrain(t, c) // stale ready event must be discarded, not delivered
	c.Advance(2 * step)
	ev := mustClaim(t, c, tm, epoch.Add(3*step))
	if ev.Gen != 1 {
		t.Fatalf("delivered gen = %d, want 1", ev.Gen)
	}
	mustDrain(t, c)
}

func TestStopInvalidatesQueuedEvent(t *testing.T) {
	c := NewClock(epoch)
	tm := c.NewTimer(step)
	c.Advance(step)
	if !tm.Stop() {
		t.Fatalf("Stop should report it prevented a delivery")
	}
	mustDrain(t, c)
	if tm.Stop() {
		t.Fatalf("second Stop should report false")
	}
}

func TestSameDeadlineFiresInCreationOrder(t *testing.T) {
	c := NewClock(epoch)
	t1 := c.NewTimer(step)
	t2 := c.NewTimer(step)
	t3 := c.NewTimer(step)
	c.Advance(step)
	for i, tm := range []*Timer{t1, t2, t3} {
		ev, ok := c.Next()
		if !ok || ev.Timer != tm {
			t.Fatalf("claim %d: got %+v (ok=%v), want timer %d", i, ev, ok, i)
		}
	}
	mustDrain(t, c)
}

func TestMixedDeadlinesOrderedByDeadlineThenCreation(t *testing.T) {
	c := NewClock(epoch)
	t1 := c.NewTimer(2 * step)
	t2 := c.NewTimer(step)
	t3 := c.NewTimer(2 * step)
	c.Advance(2 * step)
	for i, tm := range []*Timer{t2, t1, t3} {
		ev, ok := c.Next()
		if !ok || ev.Timer != tm {
			t.Fatalf("claim %d: got %+v (ok=%v), want timer %d", i, ev, ok, i)
		}
	}
}

func TestResetKeepsCreationOrderForEqualDeadlines(t *testing.T) {
	c := NewClock(epoch)
	t1 := c.NewTimer(10 * step) // created first
	t2 := c.NewTimer(step)      // created second
	t1.Reset(step)              // t1 now ties with t2's deadline
	c.Advance(step)
	for i, tm := range []*Timer{t1, t2} {
		ev, ok := c.Next()
		if !ok || ev.Timer != tm {
			t.Fatalf("claim %d: got %+v (ok=%v), want timer %d", i, ev, ok, i)
		}
	}
}

func TestResetRearmsStoppedTimer(t *testing.T) {
	c := NewClock(epoch)
	tm := c.NewTimer(step)
	tm.Stop()
	tm.Reset(step)
	c.Advance(step)
	mustClaim(t, c, tm, epoch.Add(step))
	mustDrain(t, c)
}

// --- Permutation enumeration ----------------------------------------------

type op int

const (
	opAdvance op = iota // advance the clock by one step (enough to expire)
	opClaim             // claim one ready event
	opReset             // reset the timer to fire one step from now
	opStop              // stop the timer
)

var opNames = map[op]string{
	opAdvance: "advance",
	opClaim:   "claim",
	opReset:   "reset",
	opStop:    "stop",
}

func describe(ops []op) string {
	s := ""
	for i, o := range ops {
		if i > 0 {
			s += ","
		}
		s += opNames[o]
	}
	return s
}

// spec is an independent reference model of a single one-shot timer whose
// generations are delivered at most once. Time is counted in steps.
type spec struct {
	now       int
	gen       uint64
	armed     bool // mirrors Timer.active
	expired   bool // current generation already moved to the ready queue
	deadline  int
	ready     []uint64 // generations awaiting claim; stale if != gen or !armed
	delivered []uint64
}

func (s *spec) run(o op) {
	switch o {
	case opAdvance:
		s.now++
		if s.armed && !s.expired && s.deadline <= s.now {
			s.ready = append(s.ready, s.gen)
			s.expired = true
		}
	case opClaim:
		for len(s.ready) > 0 {
			g := s.ready[0]
			s.ready = s.ready[1:]
			if g == s.gen && s.armed {
				s.delivered = append(s.delivered, g)
				s.armed = false
				break
			}
		}
	case opReset:
		s.gen++
		s.armed = true
		s.expired = false
		s.deadline = s.now + 1
	case opStop:
		if s.armed {
			s.armed = false
			s.gen++
		}
	}
}

// runReal replays the same operations against the real implementation and
// returns the delivered generations.
func runReal(ops []op) []uint64 {
	c := NewClock(epoch)
	tm := c.NewTimer(step)
	var delivered []uint64
	for _, o := range ops {
		switch o {
		case opAdvance:
			c.Advance(step)
		case opClaim:
			if ev, ok := c.Next(); ok {
				delivered = append(delivered, ev.Gen)
			}
		case opReset:
			tm.Reset(step)
		case opStop:
			tm.Stop()
		}
	}
	return delivered
}

// TestPermutations enumerates every operation sequence over
// {advance, claim, reset, stop} up to length 6 and checks, for each one:
//   - the real implementation delivers exactly the generations the reference
//     model delivers, in the same order (which covers invalidation of stale
//     ready events by reset/stop), and
//   - no generation is ever delivered twice (single delivery).
func TestPermutations(t *testing.T) {
	const maxLen = 6
	for length := 1; length <= maxLen; length++ {
		total := 1
		for i := 0; i < length; i++ {
			total *= 4
		}
		for mask := 0; mask < total; mask++ {
			ops := make([]op, length)
			for i, m := 0, mask; i < length; i, m = i+1, m/4 {
				ops[i] = op(m % 4)
			}

			s := &spec{armed: true, deadline: 1} // NewTimer(step) at time 0
			for _, o := range ops {
				s.run(o)
			}
			got := runReal(ops)

			if fmt.Sprint(got) != fmt.Sprint(s.delivered) {
				t.Fatalf("ops [%s]: delivered gens %v, model says %v",
					describe(ops), got, s.delivered)
			}
			seen := map[uint64]bool{}
			for _, g := range got {
				if seen[g] {
					t.Fatalf("ops [%s]: generation %d delivered twice", describe(ops), g)
				}
				seen[g] = true
			}
		}
	}
}
