package bbrv2

import (
	"testing"
	"time"
)

func testConfig() Config {
	return Config{
		CruiseMaxDuration: 200 * time.Millisecond,
		// Disable the 10-second PROBE_RTT timer for behavioural tests that
		// are unrelated to minRTT expiry; the dedicated test enables it.
		MinRTTExpiry: time.Hour,
	}
}

// healthySample injects a non-app-limited delivery sample at the given rate.
func healthySample(e *simEnv, rateBps uint64) {
	interval := 25 * time.Millisecond
	delivered := rateBps * uint64(interval.Nanoseconds()) / 1_000_000_000
	e.now = e.now.Add(interval)
	// Keep a window's worth of data in flight so a DRAIN phase would remain
	// observable and the sender never looks application-limited here.
	e.c.OnSent(delivered)
	e.c.OnSample(RateSample{
		Now:            e.now,
		RTT:            e.rtt,
		BytesDelivered: delivered,
		BytesAcked:     delivered,
		Interval:       interval,
	})
}

// appLimitedSample injects an application-limited delivery sample.
func appLimitedSample(e *simEnv, rateBps uint64) {
	interval := 25 * time.Millisecond
	delivered := rateBps * uint64(interval.Nanoseconds()) / 1_000_000_000
	e.now = e.now.Add(interval)
	// Deliberately do not refill the pipe: the sender is application-limited.
	e.c.OnSample(RateSample{
		Now:            e.now,
		RTT:            e.rtt,
		BytesDelivered: delivered,
		BytesAcked:     delivered,
		Interval:       interval,
		AppLimited:     true,
	})
}

// TestAppLimitedLowSampleIgnored verifies invariant (1): an app-limited low
// bandwidth sample neither lowers BtlBw nor drives STARTUP full-pipe exit.
func TestAppLimitedLowSampleIgnored(t *testing.T) {
	e := newSimEnv(t, testConfig())

	// Prime a stable BtlBw of 1 MB/s with enough healthy rounds to cross
	// round boundaries while remaining in STARTUP. Rates grow >25% per round
	// so the full-pipe detector never trips during setup.
	const baseRate uint64 = 1_000_000
	rate := uint64(200_000)
	for i := 0; i < 8; i++ {
		healthySample(e, rate)
		rate = rate * 3 / 2
	}
	bwBefore := e.c.BtlBw()
	if bwBefore == 0 {
		t.Fatal("expected non-zero primed BtlBw")
	}
	if e.c.State() != StateStartup {
		t.Fatalf("setup expected STARTUP, got %v", e.c.State())
	}

	// App-limited samples at 10% of the estimate must be ignored.
	for i := 0; i < 12; i++ {
		appLimitedSample(e, bwBefore/10)
		if e.c.BtlBw() != bwBefore {
			t.Fatalf("app-limited low sample changed BtlBw: %d -> %d", bwBefore, e.c.BtlBw())
		}
		if e.c.State() != StateStartup {
			t.Fatalf("app-limited low sample forced exit to %v", e.c.State())
		}
	}
}

// TestAppLimitedHighSampleAdopted verifies the second half of invariant (1):
// an app-limited sample above the current max must still be adopted, even
// though it must not count toward full-pipe detection.
func TestAppLimitedHighSampleAdopted(t *testing.T) {
	e := newSimEnv(t, testConfig())
	rate := uint64(200_000)
	for i := 0; i < 8; i++ {
		healthySample(e, rate)
		rate = rate * 3 / 2
	}
	bwBefore := e.c.BtlBw()

	// An app-limited burst measured at 4x the current max must be adopted.
	appLimitedSample(e, bwBefore*4)
	if e.c.BtlBw() <= bwBefore {
		t.Fatalf("app-limited high sample not adopted: before=%d after=%d", bwBefore, e.c.BtlBw())
	}
	if e.c.State() != StateStartup {
		t.Fatalf("unexpected state after app-limited high sample: %v", e.c.State())
	}
}
