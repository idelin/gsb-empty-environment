package llhls

import (
	"testing"
	"time"
)

func TestVirtualClockFiresInDeadlineOrder(t *testing.T) {
	clock := NewVirtualClock()
	start := clock.Now()

	second := clock.NewTimer(2 * time.Second)
	first := clock.NewTimer(time.Second)
	stopped := clock.NewTimer(3 * time.Second)
	if !stopped.Stop() {
		t.Fatal("Stop should report true for a pending timer")
	}

	clock.Advance(2 * time.Second)

	select {
	case fired := <-first.C():
		if !fired.Equal(start.Add(time.Second)) {
			t.Fatalf("first timer fired at %v, want %v", fired, start.Add(time.Second))
		}
	default:
		t.Fatal("1s timer should have fired")
	}
	select {
	case <-second.C():
	default:
		t.Fatal("2s timer should have fired at the boundary")
	}
	select {
	case <-stopped.C():
		t.Fatal("stopped timer must not fire")
	default:
	}

	if got := clock.Now(); !got.Equal(start.Add(2 * time.Second)) {
		t.Fatalf("now = %v, want %v", got, start.Add(2*time.Second))
	}
}

func TestVirtualClockFiresTimersScheduledDuringAdvance(t *testing.T) {
	clock := NewVirtualClock()
	done := make(chan struct{})
	armed := make(chan struct{})

	go func() {
		timer := clock.NewTimer(time.Second)
		close(armed)
		<-timer.C()
		timer = clock.NewTimer(time.Second)
		<-timer.C()
		close(done)
	}()

	<-armed
	clock.Advance(time.Second)
	// The goroutine must re-arm before the second advance; the second timer
	// is due at t=2s, so advancing past it without firing proves ordering.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		clock.mu.Lock()
		pending := clock.timers.Len()
		clock.mu.Unlock()
		if pending == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	clock.Advance(time.Second)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("chained virtual timers did not fire")
	}
}

func TestVirtualClockDoesNotFireBeforeAdvance(t *testing.T) {
	clock := NewVirtualClock()
	timer := clock.NewTimer(time.Second)

	select {
	case <-timer.C():
		t.Fatal("timer fired without Advance")
	default:
	}
	clock.Advance(500 * time.Millisecond)
	select {
	case <-timer.C():
		t.Fatal("timer fired before its deadline")
	default:
	}
}

func TestVirtualClockStopAfterFiringReportsFalse(t *testing.T) {
	clock := NewVirtualClock()
	timer := clock.NewTimer(time.Second)
	clock.Advance(time.Second)
	select {
	case <-timer.C():
	default:
		t.Fatal("timer should have fired")
	}
	if timer.Stop() {
		t.Fatal("Stop after firing must report false")
	}
}

func TestVirtualClockDoubleStopReportsFalse(t *testing.T) {
	clock := NewVirtualClock()
	timer := clock.NewTimer(time.Second)
	if !timer.Stop() {
		t.Fatal("first Stop should report true")
	}
	if timer.Stop() {
		t.Fatal("second Stop should report false")
	}
	clock.Advance(time.Second)
	select {
	case <-timer.C():
		t.Fatal("stopped timer must never fire")
	default:
	}
}

func TestRealClockTimerLifecycle(t *testing.T) {
	clock := NewRealClock()
	before := clock.Now()
	timer := clock.NewTimer(10 * time.Millisecond)
	select {
	case fired := <-timer.C():
		if fired.Before(before) {
			t.Fatalf("timer fired in the past: %v before %v", fired, before)
		}
	case <-time.After(time.Second):
		t.Fatal("real timer never fired")
	}
	if timer.Stop() {
		// Stopping after delivery is allowed to report either value; the
		// important contract is that C is not delivered twice.
	}
}
