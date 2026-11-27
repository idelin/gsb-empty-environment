package bbrv2

import (
	"testing"
	"time"
)

// TestProbeRTTTenSecondTriggerAndDwell verifies invariant (3): a stale minRTT
// (>=10s) enters PROBE_RTT, cwnd is reduced to min(cwnd, max(4MSS, 0.5 BDP)),
// the dwell timer starts only after real in-flight falls below the target,
// and the state persists for max(200ms, 1 RTT).
func TestProbeRTTTenSecondTriggerAndDwell(t *testing.T) {
	cfg := testConfig()
	e := newSimEnv(t, cfg)

	// Reach PROBE_BW quickly via plateau + drain.
	e.sendRound(t, 0, 0)
	e.sendRound(t, 0, 0)
	for e.c.State() == StateStartup {
		e.sendRound(t, 0, 0)
	}
	for e.c.State() == StateDrain {
		e.drainRound(t)
	}
	if e.c.State() != StateProbeBW {
		t.Fatalf("setup expected PROBE_BW, got %v", e.c.State())
	}

	cwndBefore := e.c.Cwnd()
	bdp := e.c.BDP()
	minPipe := uint64(cfg.MinCwndPackets) * cfg.MSS
	wantTarget := bdp / 2
	if minPipe > wantTarget {
		wantTarget = minPipe
	}
	if cwndBefore < wantTarget {
		wantTarget = cwndBefore
	}

	// No fresh lower RTT: advance just under the expiry, must stay in PROBE_BW.
	e.advance(cfg.MinRTTExpiry - time.Millisecond)
	if e.c.State() != StateProbeBW {
		t.Fatalf("PROBE_RTT must not trigger before 10s, got %v", e.c.State())
	}

	// Crossing the 10 second boundary enters PROBE_RTT immediately.
	e.advance(2 * time.Millisecond)
	if e.c.State() != StateProbeRTT {
		t.Fatalf("expected PROBE_RTT after 10s stale minRTT, got %v", e.c.State())
	}
	if e.c.Cwnd() != wantTarget {
		t.Fatalf("PROBE_RTT cwnd=%d want %d (bdp=%d)", e.c.Cwnd(), wantTarget, bdp)
	}

	// Keep bytes outstanding above the target: the dwell timer must stay zero.
	e.c.OnSent(wantTarget * 3)
	e.advance(150 * time.Millisecond)
	if e.c.ProbeRTTDwell() != 0 {
		t.Fatalf("dwell timer must not start while inflight > target, got %v", e.c.ProbeRTTDwell())
	}

	// Drain below the target: now the timer starts and requires
	// max(200ms, 1 RTT) before leaving.
	for e.c.Inflight() > wantTarget {
		e.drainRound(t)
	}
	if e.c.State() != StateProbeRTT {
		t.Fatalf("expected to remain in PROBE_RTT at timer start, got %v", e.c.State())
	}
	// The timer starts on the sample that first brings in-flight below the
	// target, so the initial dwell is at most one delivery interval.
	if e.c.ProbeRTTDwell() > e.rtt/time.Duration(e.samples) {
		t.Fatalf("dwell should begin only after inflight drops, got %v", e.c.ProbeRTTDwell())
	}

	minDwell := e.c.cfg.ProbeRTTMinDuration
	if e.c.MinRTT() > minDwell {
		minDwell = e.c.MinRTT()
	}
	// Advance based on the real dwell start recorded inside drainRound.
	e.advance(minDwell / 2)
	if e.c.State() != StateProbeRTT {
		t.Fatalf("PROBE_RTT ended before min dwell, got %v", e.c.State())
	}
	remaining := minDwell - e.c.ProbeRTTDwell() + time.Millisecond
	e.advance(remaining)
	if e.c.State() != StateProbeBW {
		t.Fatalf("expected PROBE_BW after dwell, got %v", e.c.State())
	}
	if e.c.MinRTTAge() >= cfg.MinRTTExpiry {
		t.Fatal("minRTT expiry window should restart on PROBE_RTT exit")
	}
}
