package llhls

import (
	"testing"
	"time"
)

func TestEncoderPublishesPartsOnClockTicks(t *testing.T) {
	clock := NewVirtualClock()
	pl := newTestPlaylist(t, clock)
	enc := NewEncoder(pl, clock, time.Second)

	stop := make(chan struct{})
	go enc.Run(stop)

	// Nothing happens until simulated time advances.
	waitTimerArmed(t, clock)
	clock.Advance(time.Second)
	waitForState(t, func() bool {
		count, ok := pl.PartCount(0)
		return ok && count == 1
	}, "first part")

	// Advance one second at a time and synchronize on each re-arm so the
	// encoder goroutine cannot miss the single Advance call.
	for i := 0; i < 5; i++ {
		waitTimerArmed(t, clock)
		clock.Advance(time.Second)
	}
	waitForState(t, func() bool {
		count, ok := pl.PartCount(0)
		return ok && count == 6
	}, "six parts of MSN 0")

	// The sixth part rolls MSN 0 over; MSN 1 is now open.
	waitForState(t, func() bool { return pl.FirstMSN() == 0 && pl.NextMSN() == 2 },
		"segment rollover")

	close(stop)

	// After stopping, advancing time must not publish anything new.
	count, _ := pl.PartCount(1)
	clock.Advance(10 * time.Second)
	if got, _ := pl.PartCount(1); got != count {
		t.Fatalf("encoder kept publishing after stop: %d -> %d parts", count, got)
	}
}

func waitTimerArmed(t *testing.T, clock *VirtualClock) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if clock.PendingCount() > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for the encoder to arm its timer")
}

func waitForState(t *testing.T, cond func() bool, label string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", label)
}
