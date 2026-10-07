package thermosim

import (
	"testing"
	"time"
)

// newController builds a sample-driven controller attached to arb.
func newController(name string, prio int, dwell time.Duration, arb *Arbiter, log *[]Event) *Controller {
	c := &Controller{
		Name: name, Upper: 30, Lower: 20,
		MinDwell: dwell, Priority: prio, Arbiter: arb,
	}
	c.OnEvent = func(e Event) { *log = append(*log, e) }
	return c
}

func mustProcess(t *testing.T, c *Controller, min int, temp float64) {
	t.Helper()
	if err := c.Process(Sample{Time: at(min), Temp: temp}); err != nil {
		t.Fatalf("%s min %d: %v", c.Name, min, err)
	}
}

func waitingNames(a *Arbiter) []string {
	var out []string
	for _, c := range a.Waiting() {
		out = append(out, c.Name)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestArbiterCapacityAndPending: at most MaxOn controllers run; excess
// demand stays pending (not violated), and freed capacity grants the waiter
// at the releasing sample's timestamp.
func TestArbiterCapacityAndPending(t *testing.T) {
	arb := NewArbiter(2)
	var log []Event
	a := newController("A", 1, 0, arb, &log)
	b := newController("B", 2, 0, arb, &log)
	c := newController("C", 3, 0, arb, &log)

	mustProcess(t, a, 0, 10) // granted
	mustProcess(t, b, 1, 10) // granted
	mustProcess(t, c, 2, 10) // capacity full: pending

	if arb.OnCount() != 2 {
		t.Errorf("OnCount = %d, want 2", arb.OnCount())
	}
	if c.State() != Off || !c.Pending() {
		t.Errorf("C state = %v pending = %v, want Off and pending", c.State(), c.Pending())
	}

	mustProcess(t, a, 3, 40) // A switches off; C is granted at t=3
	if c.State() != On || c.Pending() {
		t.Errorf("after release: C state = %v pending = %v, want On and not pending", c.State(), c.Pending())
	}
	last := log[len(log)-1]
	if last.Controller != "C" || !last.Time.Equal(at(3)) || last.Reason != ReasonGrantWait {
		t.Errorf("grant event = %+v, want C granted at t=3 after wait", last)
	}
}

// TestArbiterWaitOrderPriorityThenSeq: when capacity recovers, waiters are
// granted by priority first, then by wait sequence number.
func TestArbiterWaitOrderPriorityThenSeq(t *testing.T) {
	arb := NewArbiter(1)
	var log []Event
	a := newController("A", 0, 0, arb, &log)
	b := newController("B", 2, 0, arb, &log)
	c := newController("C", 1, 0, arb, &log)
	d := newController("D", 2, 0, arb, &log)

	mustProcess(t, a, 0, 10) // A on
	mustProcess(t, b, 1, 10) // pending, prio 2, seq 1
	mustProcess(t, c, 2, 10) // pending, prio 1, seq 2
	mustProcess(t, d, 3, 10) // pending, prio 2, seq 3

	if got, want := waitingNames(arb), []string{"C", "B", "D"}; !equalStrings(got, want) {
		t.Fatalf("waiting order = %v, want %v", got, want)
	}

	mustProcess(t, a, 4, 40) // A off -> C wins on priority despite later seq
	if c.State() != On || b.Pending() != true || d.Pending() != true {
		t.Fatalf("after A off: C=%v B.pending=%v D.pending=%v", c.State(), b.Pending(), d.Pending())
	}
	mustProcess(t, c, 5, 40) // C off -> B and D tie on priority, B has earlier seq
	if b.State() != On || d.State() != Off {
		t.Fatalf("after C off: B=%v D=%v, want B On, D Off", b.State(), d.State())
	}
	mustProcess(t, b, 6, 40) // B off -> D finally granted
	if d.State() != On {
		t.Fatalf("after B off: D=%v, want On", d.State())
	}
	if arb.OnCount() != 1 {
		t.Errorf("OnCount = %d, want 1", arb.OnCount())
	}
}

// TestArbiterCannotInterruptDwell: global arbitration cannot free capacity
// by interrupting a controller's minimum dwell; the requester stays pending
// until the running controller's own thermostat releases the slot.
func TestArbiterCannotInterruptDwell(t *testing.T) {
	arb := NewArbiter(1)
	var log []Event
	a := newController("A", 1, 10*time.Minute, arb, &log)
	b := newController("B", 0, 0, arb, &log) // higher priority, still cannot preempt

	mustProcess(t, a, 0, 10) // A on at t=0
	mustProcess(t, b, 1, 10) // B pending
	mustProcess(t, a, 5, 40) // A wants off but dwell 5m < 10m: stays On

	if a.State() != On {
		t.Errorf("A state = %v, want On (dwell protected)", a.State())
	}
	if b.State() != Off || !b.Pending() {
		t.Errorf("B state = %v pending = %v, want Off and pending", b.State(), b.Pending())
	}
	if arb.OnCount() != 1 {
		t.Errorf("OnCount = %d, want 1", arb.OnCount())
	}

	mustProcess(t, a, 11, 40) // dwell met: A off, B granted at t=11
	if a.State() != Off || b.State() != On {
		t.Errorf("after dwell: A=%v B=%v, want A Off, B On", a.State(), b.State())
	}
	last := log[len(log)-1]
	if last.Controller != "B" || !last.Time.Equal(at(11)) {
		t.Errorf("B grant event = %+v, want granted at t=11", last)
	}
}

// TestPendingCancelledWhenDemandClears: a pending request is cancelled when
// the thermostat stops demanding On; the freed wait position goes to the
// next waiter when capacity recovers.
func TestPendingCancelledWhenDemandClears(t *testing.T) {
	arb := NewArbiter(1)
	var log []Event
	a := newController("A", 0, 0, arb, &log)
	b := newController("B", 1, 0, arb, &log)
	c := newController("C", 2, 0, arb, &log)

	mustProcess(t, a, 0, 10) // A on
	mustProcess(t, b, 1, 10) // B pending (higher priority)
	mustProcess(t, c, 2, 10) // C pending
	mustProcess(t, b, 3, 40) // B overheats while waiting: cancel request

	if b.Pending() || b.State() != Off {
		t.Errorf("B pending = %v state = %v, want not pending and Off", b.Pending(), b.State())
	}
	if got, want := waitingNames(arb), []string{"C"}; !equalStrings(got, want) {
		t.Fatalf("waiting = %v, want %v", got, want)
	}

	mustProcess(t, a, 4, 40) // A off: slot goes to C, not the cancelled B
	if c.State() != On || b.State() != Off {
		t.Errorf("after recovery: C=%v B=%v, want C On, B Off", c.State(), b.State())
	}
}

// TestWaitOrderAfterRecovery: a full recovery cycle — capacity saturates,
// waiters queue, and as running controllers cycle off the queue drains in
// (priority, sequence) order until empty.
func TestWaitOrderAfterRecovery(t *testing.T) {
	arb := NewArbiter(2)
	var log []Event
	a := newController("A", 0, 0, arb, &log)
	b := newController("B", 0, 0, arb, &log)
	c := newController("C", 3, 0, arb, &log)
	d := newController("D", 1, 0, arb, &log)
	e := newController("E", 2, 0, arb, &log)

	mustProcess(t, a, 0, 10) // on
	mustProcess(t, b, 0, 10) // on — note: same virtual instant, distinct controllers
	mustProcess(t, c, 1, 10) // pending prio 3 seq 1
	mustProcess(t, d, 2, 10) // pending prio 1 seq 2
	mustProcess(t, e, 3, 10) // pending prio 2 seq 3

	mustProcess(t, a, 4, 40) // frees one slot -> D (prio 1)
	if d.State() != On {
		t.Fatalf("D = %v, want On after first recovery", d.State())
	}
	mustProcess(t, b, 5, 40) // frees one slot -> E (prio 2 beats C prio 3)
	if e.State() != On || c.State() != Off {
		t.Fatalf("E = %v C = %v, want E On, C still Off", e.State(), c.State())
	}
	mustProcess(t, d, 6, 40) // frees one slot -> C finally
	if c.State() != On {
		t.Fatalf("C = %v, want On after queue drains", c.State())
	}
	if got := waitingNames(arb); len(got) != 0 {
		t.Errorf("waiting = %v, want empty", got)
	}
	if arb.OnCount() != 2 {
		t.Errorf("OnCount = %d, want 2", arb.OnCount())
	}
}
