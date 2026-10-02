package timingwheel

import (
	"fmt"
	"testing"
)

// TestExactWakeup schedules 100 one-shot timers with distinct deadlines
// spread across multiple wheel levels, advances the clock 1000 ticks, and
// verifies every timer fires exactly on its deadline tick.
func TestExactWakeup(t *testing.T) {
	tw := New(20) // levels: 20, 400, 8000 ticks
	const n = 100
	firedAt := make(map[int]int64, n) // timer id -> tick it fired on
	deadlines := make(map[int]int64, n)

	for i := 0; i < n; i++ {
		delay := int64((i*37)%1000) + 1 // 1..1000, well spread
		id := i
		deadlines[id] = delay
		tw.After(delay, func() { firedAt[id] = tw.Now() })
	}
	if got := tw.Pending(); got != n {
		t.Fatalf("Pending() = %d, want %d", got, n)
	}

	tw.Advance(1000)

	if len(firedAt) != n {
		t.Fatalf("fired %d timers, want %d", len(firedAt), n)
	}
	for id, want := range deadlines {
		got, ok := firedAt[id]
		if !ok {
			t.Fatalf("timer %d never fired", id)
		}
		if got != want {
			t.Errorf("timer %d fired at tick %d, want exactly %d", id, got, want)
		}
	}
	if got := tw.Pending(); got != 0 {
		t.Errorf("Pending() = %d after all timers fired, want 0", got)
	}
	if got := tw.wheel.countTimers(); got != 0 {
		t.Errorf("%d timers still stored in wheels, want 0 (memory released)", got)
	}
}

// TestCancelZeroFires verifies cancelled timers never fire and are fully
// removed from the wheel.
func TestCancelZeroFires(t *testing.T) {
	tw := New(20)
	const n = 100
	timers := make([]*Timer, n)
	fired := make([]int, n)

	for i := 0; i < n; i++ {
		id := i
		timers[i] = tw.After(int64((i*13)%900)+1, func() { fired[id]++ })
	}
	// Cancel every even-indexed timer up front.
	for i := 0; i < n; i += 2 {
		if !timers[i].Cancel() {
			t.Fatalf("Cancel() on timer %d returned false", i)
		}
	}
	// Double cancel must be a no-op.
	if timers[0].Cancel() {
		t.Fatal("second Cancel() returned true")
	}
	if got := tw.Pending(); got != n/2 {
		t.Fatalf("Pending() = %d, want %d", got, n/2)
	}

	tw.Advance(1000)

	for i := 0; i < n; i++ {
		want := 1
		if i%2 == 0 {
			want = 0
		}
		if fired[i] != want {
			t.Errorf("timer %d fired %d times, want %d", i, fired[i], want)
		}
	}
	if got := tw.Pending(); got != 0 {
		t.Errorf("Pending() = %d, want 0", got)
	}
	if got := tw.wheel.countTimers(); got != 0 {
		t.Errorf("%d timers still stored in wheels, want 0 (memory released)", got)
	}
}

// TestCancelMidFlight cancels timers while the clock is advancing and
// verifies they never fire afterwards.
func TestCancelMidFlight(t *testing.T) {
	tw := New(8)
	var fires int
	t1 := tw.After(50, func() { fires++ })
	t2 := tw.Ticker(10, func() { fires++ })

	tw.Advance(30) // t2 fires at 10, 20, 30
	if fires != 3 {
		t.Fatalf("fires = %d after 30 ticks, want 3", fires)
	}
	t1.Cancel()
	t2.Cancel()
	if got := tw.Pending(); got != 0 {
		t.Fatalf("Pending() = %d, want 0", got)
	}

	tw.Advance(1000)
	if fires != 3 {
		t.Errorf("fires = %d after cancelled timers, want 3 (zero new fires)", fires)
	}
	if got := tw.wheel.countTimers(); got != 0 {
		t.Errorf("%d timers still stored in wheels, want 0", got)
	}
}

// TestTicker verifies a periodic ticker fires exactly on every multiple of
// its period and stops after cancellation.
func TestTicker(t *testing.T) {
	tw := New(16)
	var ticks []int64
	tk := tw.Ticker(10, func() { ticks = append(ticks, tw.Now()) })

	tw.Advance(105)
	if len(ticks) != 10 {
		t.Fatalf("ticker fired %d times in 105 ticks, want 10", len(ticks))
	}
	for i, got := range ticks {
		want := int64(i+1) * 10
		if got != want {
			t.Errorf("fire %d at tick %d, want exactly %d", i, got, want)
		}
	}

	tk.Cancel()
	tw.Advance(100)
	if len(ticks) != 10 {
		t.Errorf("ticker fired after Cancel: %d fires, want 10", len(ticks))
	}
	if got := tw.Pending(); got != 0 {
		t.Errorf("Pending() = %d, want 0", got)
	}
}

// TestTickerCancelInsideTask verifies a ticker can cancel itself from
// within its own task without being rescheduled.
func TestTickerCancelInsideTask(t *testing.T) {
	tw := New(8)
	var fires int
	var tk *Timer
	tk = tw.Ticker(5, func() {
		fires++
		if fires == 3 {
			tk.Cancel()
		}
	})
	tw.Advance(1000)
	if fires != 3 {
		t.Errorf("fires = %d, want 3", fires)
	}
	if got := tw.Pending(); got != 0 {
		t.Errorf("Pending() = %d, want 0", got)
	}
}

// TestHierarchicalCascade forces timers through multiple overflow wheels
// with a tiny base wheel and verifies exact wake-up ticks.
func TestHierarchicalCascade(t *testing.T) {
	tw := New(4) // levels: 4, 16, 64, 256, 1024, 4096 ticks
	cases := []int64{1, 3, 4, 5, 15, 16, 17, 63, 64, 65, 255, 256, 257, 1000, 3000}
	firedAt := make(map[int64]int64, len(cases))
	for _, d := range cases {
		d := d
		tw.After(d, func() { firedAt[d] = tw.Now() })
	}
	tw.Advance(3000)
	if len(firedAt) != len(cases) {
		t.Fatalf("fired %d timers, want %d", len(firedAt), len(cases))
	}
	for _, d := range cases {
		if got := firedAt[d]; got != d {
			t.Errorf("timer with deadline %d fired at tick %d", d, got)
		}
	}
	if got := tw.wheel.countTimers(); got != 0 {
		t.Errorf("%d timers still stored in wheels, want 0", got)
	}
}

// TestFiringOrder verifies timers due on the same or different ticks fire
// in non-decreasing deadline order.
func TestFiringOrder(t *testing.T) {
	tw := New(10)
	var order []int64
	for _, d := range []int64{9, 1, 5, 5, 3, 100} {
		d := d
		tw.After(d, func() { order = append(order, d) })
	}
	tw.Advance(100)
	for i := 1; i < len(order); i++ {
		if order[i] < order[i-1] {
			t.Errorf("fire order %v is not sorted by deadline", order)
			break
		}
	}
}

// TestImmediateAfter verifies a non-positive delay runs the task inline.
func TestImmediateAfter(t *testing.T) {
	tw := New(8)
	fires := 0
	tm := tw.After(0, func() { fires++ })
	if fires != 1 {
		t.Errorf("fires = %d, want 1 (immediate)", fires)
	}
	if tm.Cancel() {
		t.Error("Cancel() on an already-fired timer returned true")
	}
	if got := tw.Pending(); got != 0 {
		t.Errorf("Pending() = %d, want 0", got)
	}
}

// TestManyTimersStress is the acceptance scenario: 100 timers with varying
// deadlines, 1000 ticks of advancement, exact wake-up, cancelled timers
// silent, and all memory released.
func TestManyTimersStress(t *testing.T) {
	tw := New(10)
	const n = 100
	fireCount := make(map[int]int, n)
	fireTick := make(map[int]int64, n)
	var timers []*Timer
	cancelled := make(map[int]bool)

	for i := 0; i < n; i++ {
		id := i
		delay := int64((i*97)%1000) + 1
		timers = append(timers, tw.After(delay, func() {
			fireCount[id]++
			fireTick[id] = tw.Now()
		}))
		if i%3 == 0 { // cancel a third of the timers
			cancelled[id] = true
		}
	}
	for id := range cancelled {
		timers[id].Cancel()
	}

	tw.Advance(1000)

	for i := 0; i < n; i++ {
		if cancelled[i] {
			if fireCount[i] != 0 {
				t.Errorf("cancelled timer %d fired %d times, want 0", i, fireCount[i])
			}
			continue
		}
		if fireCount[i] != 1 {
			t.Errorf("timer %d fired %d times, want 1", i, fireCount[i])
		}
		want := int64((i*97)%1000) + 1
		if fireTick[i] != want {
			t.Errorf("timer %d fired at tick %d, want exactly %d", i, fireTick[i], want)
		}
	}
	if got := tw.Pending(); got != 0 {
		t.Errorf("Pending() = %d, want 0 (all memory released)", got)
	}
	if got := tw.wheel.countTimers(); got != 0 {
		t.Errorf("%d timers still stored in wheels, want 0", got)
	}
}

func ExampleTimingWheel() {
	tw := New(8)
	tw.After(3, func() { fmt.Println("one-shot at tick", tw.Now()) })
	tk := tw.Ticker(2, func() { fmt.Println("ticker at tick", tw.Now()) })
	tw.Advance(4)
	tk.Cancel()
	tw.Advance(4)
	fmt.Println("done, pending:", tw.Pending())
	// Output:
	// ticker at tick 2
	// one-shot at tick 3
	// ticker at tick 4
	// done, pending: 0
}
