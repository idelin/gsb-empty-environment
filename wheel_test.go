package timingwheel

import (
	"math/rand"
	"testing"
)

// Acceptance: 100 one-shot timers with distinct deadlines, clock advanced
// 1000 ticks one tick at a time. Every timer must fire exactly once, at
// exactly its deadline tick, and the wheel must be empty afterwards.
func TestOneShotAlignment(t *testing.T) {
	tw := New(0)
	const n = 100
	rng := rand.New(rand.NewSource(42))
	deadlines := make([]int64, n)
	firedAt := make([]int64, n)
	fires := make([]int, n)
	for i := 0; i < n; i++ {
		deadlines[i] = rng.Int63n(1000) + 1 // spread across 1..1000 ticks
		firedAt[i] = -1
	}
	for i := 0; i < n; i++ {
		i := i
		tw.AfterFunc(deadlines[i], func(now int64) {
			fires[i]++
			firedAt[i] = now
		})
	}
	if got := tw.Active(); got != n {
		t.Fatalf("Active() = %d, want %d", got, n)
	}
	for tick := int64(1); tick <= 1000; tick++ {
		tw.Advance()
	}
	for i := 0; i < n; i++ {
		if fires[i] != 1 {
			t.Errorf("timer %d (deadline %d) fired %d times, want 1", i, deadlines[i], fires[i])
			continue
		}
		if firedAt[i] != deadlines[i] {
			t.Errorf("timer %d fired at tick %d, want exactly %d", i, firedAt[i], deadlines[i])
		}
	}
	assertEmpty(t, tw)
}

// Acceptance: cancelled timers must never fire and must be fully released.
func TestCancel(t *testing.T) {
	tw := New(0)
	const n = 100
	timers := make([]*Timer, n)
	fired := make([]bool, n)
	cancelled := make([]bool, n)
	deadline := func(i int) int64 { return int64(i*10 + 1) } // 1..991
	for i := 0; i < n; i++ {
		i := i
		timers[i] = tw.AfterFunc(deadline(i), func(now int64) { fired[i] = true })
	}
	// Cancel half of them before the clock starts.
	for i := 0; i < n; i += 2 {
		if !timers[i].Cancel() {
			t.Fatalf("Cancel(%d) returned false", i)
		}
		cancelled[i] = true
	}
	// Double cancel must be a no-op.
	if timers[0].Cancel() {
		t.Fatalf("second Cancel returned true")
	}
	// Cancel every i%4==3 timer one tick before its deadline, mid-flight.
	for tick := int64(1); tick <= 1000; tick++ {
		for i := 3; i < n; i += 4 {
			if !cancelled[i] && deadline(i)-1 == tick {
				timers[i].Cancel()
				cancelled[i] = true
			}
		}
		tw.Advance()
	}
	for i := 0; i < n; i++ {
		if cancelled[i] && fired[i] {
			t.Errorf("cancelled timer %d fired", i)
		}
		if !cancelled[i] && !fired[i] {
			t.Errorf("live timer %d (deadline %d) never fired", i, deadline(i))
		}
	}
	assertEmpty(t, tw)
}

// Periodic tickers must fire at exact multiples of their period and stop
// cleanly on Cancel.
func TestTicker(t *testing.T) {
	tw := New(0)
	var got []int64
	tk := tw.TickFunc(7, func(now int64) { got = append(got, now) })
	for tick := int64(1); tick <= 1000; tick++ {
		tw.Advance()
	}
	want := 1000 / 7 // 142 fires
	if len(got) != want {
		t.Fatalf("ticker fired %d times, want %d", len(got), want)
	}
	for i, at := range got {
		if exp := int64((i + 1) * 7); at != exp {
			t.Fatalf("fire %d at tick %d, want %d", i, at, exp)
		}
	}
	if !tk.Cancel() {
		t.Fatalf("ticker Cancel returned false")
	}
	for tick := int64(1); tick <= 100; tick++ {
		tw.Advance()
	}
	if len(got) != want {
		t.Fatalf("ticker fired after Cancel: %d fires, want %d", len(got), want)
	}
	assertEmpty(t, tw)
}

// A single multi-tick jump must still fire every timer at its exact
// deadline, in order, and tickers must fire once per elapsed period.
func TestJumpAdvance(t *testing.T) {
	tw := New(0)
	var oneShots []int64
	for _, d := range []int64{100, 500, 1000} {
		d := d
		tw.AfterFunc(d, func(now int64) { oneShots = append(oneShots, now) })
	}
	var ticks []int64
	tw.TickFunc(3, func(now int64) { ticks = append(ticks, now) })
	tw.AdvanceTo(1000)
	want := []int64{100, 500, 1000}
	if len(oneShots) != len(want) {
		t.Fatalf("one-shots fired %v, want %v", oneShots, want)
	}
	for i := range want {
		if oneShots[i] != want[i] {
			t.Fatalf("one-shots fired %v, want %v", oneShots, want)
		}
	}
	if len(ticks) != 333 {
		t.Fatalf("ticker fired %d times, want 333", len(ticks))
	}
	for i, at := range ticks {
		if exp := int64((i + 1) * 3); at != exp {
			t.Fatalf("ticker fire %d at tick %d, want %d", i, at, exp)
		}
	}
}

// Deep deadlines must cascade through the wheel hierarchy and still fire at
// the exact tick.
func TestCascading(t *testing.T) {
	tw := New(0)
	deadlines := []int64{1, 63, 64, 65, 4095, 4096, 262144, 1000000}
	fired := make(map[int64]int64)
	for _, d := range deadlines {
		d := d
		tw.AfterFunc(d, func(now int64) { fired[d] = now })
	}
	tw.AdvanceTo(1000000)
	for _, d := range deadlines {
		if at, ok := fired[d]; !ok || at != d {
			t.Errorf("timer with deadline %d fired at %v (ok=%v)", d, at, ok)
		}
	}
	assertEmpty(t, tw)
}

// A zero/negative delay runs the task immediately and synchronously.
func TestImmediate(t *testing.T) {
	tw := New(10)
	ran := false
	tw.AfterFunc(0, func(now int64) {
		ran = true
		if now != 10 {
			t.Errorf("immediate task got now=%d, want 10", now)
		}
	})
	if !ran {
		t.Fatalf("immediate task did not run synchronously")
	}
	assertEmpty(t, tw)
}

func assertEmpty(t *testing.T, tw *TimingWheel) {
	t.Helper()
	if got := tw.Active(); got != 0 {
		t.Errorf("Active() = %d, want 0 (timers not released)", got)
	}
	if got := tw.bucketed(); got != 0 {
		t.Errorf("bucketed() = %d, want 0 (buckets not released)", got)
	}
}
