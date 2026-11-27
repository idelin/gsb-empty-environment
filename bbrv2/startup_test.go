package bbrv2

import "testing"

// TestStartupThreeRoundsDrainToProbeBW verifies invariant (2): exactly three
// consecutive <25% growth rounds trigger DRAIN, and the machine only enters
// PROBE_BW once in-flight drops to/below the BDP.
func TestStartupThreeRoundsDrainToProbeBW(t *testing.T) {
	e := newSimEnv(t, testConfig())

	// Two high-growth STARTUP rounds.
	e.sendRound(t, 0, 0)
	e.sendRound(t, 0, 0)

	rounds := 0
	for e.c.State() == StateStartup && rounds < 8 {
		e.sendRound(t, 0, 0)
		rounds++
	}
	if e.c.State() != StateDrain {
		t.Fatalf("expected DRAIN after low-growth rounds, got %v", e.c.State())
	}
	if !e.c.FullPipe() {
		t.Fatal("expected full pipe flag set")
	}
	if e.c.fullBwCnt != uint32(e.c.cfg.FullBwRounds) {
		t.Fatalf("expected %d low-growth rounds, got %d", e.c.cfg.FullBwRounds, e.c.fullBwCnt)
	}

	// DRAIN must not switch state while in-flight still exceeds the BDP:
	// keep bytes outstanding and feed a zero-delivery sample.
	bdp := e.c.BDP()
	e.c.OnSent(bdp * 3)
	e.now = e.now.Add(e.rtt / 4)
	e.c.OnSample(RateSample{Now: e.now, RTT: e.rtt, Interval: e.rtt / 4})
	if e.c.State() != StateDrain {
		t.Fatalf("DRAIN must persist while inflight > BDP, got %v", e.c.State())
	}

	// Drain the queue; once inflight <= BDP, PROBE_BW starts in DOWN.
	entryPhase := e.drainUntilState(t, StateProbeBW)
	if e.c.State() != StateProbeBW {
		t.Fatalf("expected PROBE_BW after drain, got %v", e.c.State())
	}
	if entryPhase != PhaseDown {
		t.Fatalf("expected initial DOWN phase on PROBE_BW entry, got %v", entryPhase)
	}
	if e.c.InflightHi() == 0 {
		t.Fatal("expected inflight_hi initialised on PROBE_BW entry")
	}
}

// TestStartupLossExitsEarly verifies the loss/ECN branch of invariant (2):
// a significant loss round leaves STARTUP immediately without waiting for
// three low-growth rounds.
func TestStartupLossExitsEarly(t *testing.T) {
	e := newSimEnv(t, testConfig())
	e.sendRound(t, 0, 0)
	e.sendRound(t, 0.1, 0) // 10% loss > 2% threshold
	if e.c.State() != StateDrain {
		t.Fatalf("expected DRAIN after loss round, got %v", e.c.State())
	}
	if e.c.FullPipe() || int(e.c.fullBwCnt) >= e.c.cfg.FullBwRounds {
		t.Fatalf("congestion must exit STARTUP before the %d-round full-pipe threshold, cnt=%d",
			e.c.cfg.FullBwRounds, e.c.fullBwCnt)
	}
}
