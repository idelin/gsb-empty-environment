package thermosim

import (
	"reflect"
	"testing"
)

func mustProcess(t *testing.T, c *Controller, clk *Clock, temp float64) Decision {
	t.Helper()
	d, err := c.Process(Sample{Time: clk.Now(), Temp: temp})
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", c.Name, err)
	}
	return d
}

func assertState(t *testing.T, c *Controller, want State) {
	t.Helper()
	if c.State() != want {
		t.Errorf("%s: state = %v, want %v", c.Name, c.State(), want)
	}
}

// Shared capacity: grants up to MaxOn, excess demand waits and is
// served in (priority, sequence) order as capacity is released.
func TestSharedCapacityAndWaitingOrder(t *testing.T) {
	clk := NewClock(0)
	arb := NewArbiter(2)
	A := NewController("A", 20, 30, 0, Off)
	B := NewController("B", 20, 30, 0, Off)
	C := NewController("C", 20, 30, 0, Off)
	D := NewController("D", 20, 30, 0, Off)
	arb.Attach(A, 0)
	arb.Attach(B, 1)
	arb.Attach(C, 1)
	arb.Attach(D, 2)

	clk.AdvanceTo(1)
	for _, c := range []*Controller{A, B, C, D} {
		mustProcess(t, c, clk, 10) // all demand On
	}
	if arb.OnCount() != 2 {
		t.Fatalf("OnCount = %d, want 2", arb.OnCount())
	}
	assertState(t, A, On)
	assertState(t, B, On)
	assertState(t, C, Off)
	assertState(t, D, Off)
	if !C.Pending() || !D.Pending() {
		t.Fatalf("C and D should be pending, got C=%v D=%v", C.Pending(), D.Pending())
	}
	if got := arb.Waiting(); !reflect.DeepEqual(got, []string{"C", "D"}) {
		t.Fatalf("waiting = %v, want [C D]", got)
	}

	// A releases: C (priority 1, earlier seq) beats D (priority 2).
	clk.AdvanceTo(2)
	if d := mustProcess(t, A, clk, 35); d != SwitchedOff {
		t.Fatalf("A decision = %v, want SwitchedOff", d)
	}
	assertState(t, C, On)
	assertState(t, D, Off)
	if ts, ok := C.LastSwitch(); !ok || ts != 2 {
		t.Errorf("C granted at %v, want t=2", ts)
	}

	// B releases: D finally gets the slot, still exactly 2 On.
	clk.AdvanceTo(3)
	mustProcess(t, B, clk, 35)
	assertState(t, D, On)
	if arb.OnCount() != 2 {
		t.Fatalf("OnCount = %d, want 2", arb.OnCount())
	}
}

// A controller's minimum dwell is never interrupted by global
// arbitration: a higher-priority requester must wait until the holder
// itself is allowed to switch Off.
func TestDwellNotInterruptedByArbitration(t *testing.T) {
	clk := NewClock(0)
	arb := NewArbiter(1)
	A := NewController("A", 20, 30, 100, Off)
	B := NewController("B", 20, 30, 0, Off)
	arb.Attach(A, 1) // lower priority
	arb.Attach(B, 0) // higher priority

	mustProcess(t, A, clk, 10) // A On at t=0
	clk.AdvanceTo(1)
	if d := mustProcess(t, B, clk, 10); d != WaitCapacity {
		t.Fatalf("B decision = %v, want WaitCapacity", d)
	}

	// A wants Off but dwell not met: stays On, B keeps waiting.
	clk.AdvanceTo(50)
	if d := mustProcess(t, A, clk, 35); d != Deferred {
		t.Fatalf("A decision = %v, want Deferred", d)
	}
	assertState(t, A, On)
	assertState(t, B, Off)
	if arb.OnCount() != 1 {
		t.Fatalf("OnCount = %d, want 1", arb.OnCount())
	}

	// Dwell met: A switches Off and B is granted at that same time.
	clk.AdvanceTo(100)
	mustProcess(t, A, clk, 35)
	assertState(t, A, Off)
	assertState(t, B, On)
	if ts, ok := B.LastSwitch(); !ok || ts != 100 {
		t.Errorf("B granted at %v, want t=100", ts)
	}
}

// Local safety: a pending request is withdrawn when the controller's
// own samples no longer demand On, so freed capacity never goes to a
// controller that stopped needing it.
func TestLocalSafetyWithdrawal(t *testing.T) {
	clk := NewClock(0)
	arb := NewArbiter(1)
	A := NewController("A", 20, 30, 0, Off)
	B := NewController("B", 20, 30, 0, Off)
	arb.Attach(A, 0)
	arb.Attach(B, 0)

	mustProcess(t, A, clk, 10) // A On at t=0
	clk.AdvanceTo(1)
	mustProcess(t, B, clk, 10) // B pending

	// B's next sample lands in the hold band: request withdrawn.
	clk.AdvanceTo(2)
	if d := mustProcess(t, B, clk, 25); d != Hold {
		t.Fatalf("B decision = %v, want Hold", d)
	}
	if B.Pending() {
		t.Fatal("B should have withdrawn its request")
	}
	if got := arb.Waiting(); len(got) != 0 {
		t.Fatalf("waiting = %v, want empty", got)
	}

	// A releases: nobody waiting, capacity just frees up.
	clk.AdvanceTo(3)
	mustProcess(t, A, clk, 35)
	assertState(t, B, Off)
	if arb.OnCount() != 0 {
		t.Fatalf("OnCount = %d, want 0", arb.OnCount())
	}

	// B demands On again: granted immediately.
	clk.AdvanceTo(4)
	if d := mustProcess(t, B, clk, 10); d != SwitchedOn {
		t.Fatalf("B decision = %v, want SwitchedOn", d)
	}
}

// After capacity recovers, waiting controllers are granted strictly in
// (priority, sequence) order, one per freed slot.
func TestRecoveryWaitingOrder(t *testing.T) {
	clk := NewClock(0)
	arb := NewArbiter(1)
	A := NewController("A", 20, 30, 0, Off)
	B := NewController("B", 20, 30, 0, Off)
	C := NewController("C", 20, 30, 0, Off)
	D := NewController("D", 20, 30, 0, Off)
	arb.Attach(A, 0)
	arb.Attach(B, 2)
	arb.Attach(C, 1)
	arb.Attach(D, 1)

	mustProcess(t, A, clk, 10) // A On at t=0
	clk.AdvanceTo(1)
	mustProcess(t, B, clk, 10) // waits, prio 2
	clk.AdvanceTo(2)
	mustProcess(t, C, clk, 10) // waits, prio 1, seq 1
	clk.AdvanceTo(3)
	mustProcess(t, D, clk, 10) // waits, prio 1, seq 2
	if got := arb.Waiting(); !reflect.DeepEqual(got, []string{"C", "D", "B"}) {
		t.Fatalf("waiting = %v, want [C D B]", got)
	}

	order := []string{}
	holder := A
	for i := range []string{"C", "D", "B"} {
		clk.AdvanceTo(int64(4 + i))
		mustProcess(t, holder, clk, 35) // holder releases one slot
		if arb.OnCount() != 1 {
			t.Fatalf("step %d: OnCount = %d, want 1", i, arb.OnCount())
		}
		for _, c := range []*Controller{A, B, C, D} {
			if c.State() == On && c.Name != holder.Name {
				order = append(order, c.Name)
				holder = c
			}
		}
	}
	if !reflect.DeepEqual(order, []string{"C", "D", "B"}) {
		t.Fatalf("grant order = %v, want %v", order, []string{"C", "D", "B"})
	}
}

// A controller that withdraws and re-requests gets a fresh sequence
// number and queues behind earlier same-priority requests.
func TestRequeueGetsNewSequence(t *testing.T) {
	clk := NewClock(0)
	arb := NewArbiter(1)
	A := NewController("A", 20, 30, 0, Off)
	B := NewController("B", 20, 30, 0, Off)
	C := NewController("C", 20, 30, 0, Off)
	arb.Attach(A, 0)
	arb.Attach(B, 1)
	arb.Attach(C, 1)

	mustProcess(t, A, clk, 10) // A On at t=0
	clk.AdvanceTo(1)
	mustProcess(t, B, clk, 10) // B waits, seq 0
	clk.AdvanceTo(2)
	mustProcess(t, B, clk, 25) // B withdraws (hold band)
	clk.AdvanceTo(3)
	mustProcess(t, C, clk, 10) // C waits, seq 1
	clk.AdvanceTo(4)
	mustProcess(t, B, clk, 10) // B re-requests, seq 2 -> behind C
	if got := arb.Waiting(); !reflect.DeepEqual(got, []string{"C", "B"}) {
		t.Fatalf("waiting = %v, want [C B]", got)
	}

	clk.AdvanceTo(5)
	mustProcess(t, A, clk, 35) // releases: C wins
	assertState(t, C, On)
	assertState(t, B, Off)
}
