package bbrv2

import "testing"

// TestECNAndLossShrinkInflightHi verifies invariant (4): crossing either the
// loss or the ECN threshold shrinks inflight_hi to the in-flight / safe
// bound and enforces a 0.85 cruise headroom.
func TestECNAndLossShrinkInflightHi(t *testing.T) {
	cfg := testConfig()

	run := func(name string, lossFraction, ecnFraction float64) {
		t.Run(name, func(t *testing.T) {
			e := newSimEnv(t, cfg)
			e.sendRound(t, 0, 0)
			e.sendRound(t, 0, 0)
			for e.c.State() == StateStartup {
				e.sendRound(t, 0, 0)
			}
			for e.c.State() == StateDrain {
				e.drainRound(t)
			}
			// One healthy PROBE_BW round exercises the DOWN->CRUISE path.
			e.sendRound(t, 0, 0)
			hiBefore := e.c.InflightHi()

			// Build a known in-flight level, then deliver it without
			// refilling so the round that ends is the congested one.
			knownInflight := e.c.Cwnd()
			e.c.OnSent(knownInflight)
			e.ackRound(t, lossFraction, ecnFraction)

			if e.c.State() != StateProbeBW || e.c.ProbePhase() != PhaseDown {
				t.Fatalf("expected DOWN after congestion, got %v/%v", e.c.State(), e.c.ProbePhase())
			}
			hiAfter := e.c.InflightHi()
			if hiAfter >= hiBefore {
				t.Fatalf("inflight_hi not reduced: before=%d after=%d", hiBefore, hiAfter)
			}
			if hiAfter > knownInflight {
				t.Fatalf("inflight_hi=%d exceeds in-flight at congestion=%d", hiAfter, knownInflight)
			}
			if hiAfter < e.c.BDP() {
				t.Fatalf("inflight_hi=%d fell below BDP=%d", hiAfter, e.c.BDP())
			}

			wantHeadroom := scaleFloat(hiAfter, e.c.cfg.HeadroomFactor)
			if got := e.c.HeadroomTarget(); got != wantHeadroom {
				t.Fatalf("headroom=%d want 0.85*inflight_hi=%d", got, wantHeadroom)
			}
			// While in DOWN the cwnd target equals the headroom ceiling.
			if e.c.Cwnd() != wantHeadroom {
				t.Fatalf("DOWN cwnd=%d want headroom %d", e.c.Cwnd(), wantHeadroom)
			}

			// Drain to the headroom level: DOWN must then give way to CRUISE.
			for e.c.ProbePhase() == PhaseDown {
				e.drainRound(t)
			}
			if e.c.ProbePhase() != PhaseCruise {
				t.Fatalf("expected CRUISE below headroom, got %v", e.c.ProbePhase())
			}
			if e.c.Cwnd() != wantHeadroom {
				t.Fatalf("cruise cwnd=%d want headroom %d", e.c.Cwnd(), wantHeadroom)
			}
		})
	}

	run("loss", 0.1, 0) // 10% loss > 2% threshold
	run("ecn", 0, 0.6)  // 60% ECN > 50% threshold
	run("loss_and_ecn", 0.05, 0.55)
}

// TestProbeBWPhaseCycleAndUpExponential verifies the four-phase cycle and the
// per-round exponential UP probing that breaks through the old ceiling.
func TestProbeBWPhaseCycleAndUpExponential(t *testing.T) {
	cfg := testConfig()
	e := newSimEnv(t, cfg)

	e.sendRound(t, 0, 0)
	e.sendRound(t, 0, 0)
	for e.c.State() == StateStartup {
		e.sendRound(t, 0, 0)
	}
	for e.c.State() == StateDrain {
		e.drainRound(t)
	}

	// Drain through DOWN into CRUISE.
	for e.c.ProbePhase() == PhaseDown {
		e.drainRound(t)
	}
	if e.c.ProbePhase() != PhaseCruise {
		t.Fatalf("expected CRUISE, got %v", e.c.ProbePhase())
	}

	// Cruise dwell then REFILL.
	e.advance(cfg.CruiseMaxDuration)
	if e.c.ProbePhase() != PhaseRefill {
		t.Fatalf("expected REFILL after cruise dwell, got %v", e.c.ProbePhase())
	}

	// Raise the available bandwidth before the probe so UP rounds observe
	// BtlBw growth and the three-round plateau detector does not trip while
	// exponential probing is being exercised.
	e.setBottleneck(e.bwBps * 8)

	// REFILL transitions to UP at a round boundary; catch the exact sample.
	e.runUntilPhase(t, PhaseUp)
	if e.c.ProbePhase() != PhaseUp {
		t.Fatalf("expected UP after refill round, got %v", e.c.ProbePhase())
	}

	// UP starts with a one-MSS probe allowance and a cwnd target that breaks
	// above the old inflight_hi ceiling.
	first := e.c.ProbeInflight()
	if first != e.c.cfg.MSS {
		t.Fatalf("initial UP probe=%d want MSS=%d", first, e.c.cfg.MSS)
	}
	if e.c.Cwnd() <= e.c.InflightHi() {
		t.Fatalf("UP cwnd=%d must probe above inflight_hi=%d", e.c.Cwnd(), e.c.InflightHi())
	}
	prevCwnd := e.c.Cwnd()
	prevProbe := e.c.ProbeInflight()

	// Keep growing the bottleneck so BtlBw keeps responding each UP round;
	// the probe allowance doubles every round (exponential probing).
	for i := 0; i < 3; i++ {
		e.setBottleneck(e.bwBps * 2)
		e.advanceRound(t)
		if e.c.ProbePhase() != PhaseUp {
			t.Fatalf("expected to remain in UP during growth, got %v", e.c.ProbePhase())
		}
		if e.c.ProbeInflight() <= prevProbe {
			t.Fatalf("probe allowance must grow exponentially: %d -> %d", prevProbe, e.c.ProbeInflight())
		}
		if e.c.Cwnd() <= prevCwnd {
			t.Fatalf("UP cwnd must grow: %d -> %d", prevCwnd, e.c.Cwnd())
		}
		prevProbe = e.c.ProbeInflight()
		prevCwnd = e.c.Cwnd()
	}

	// Plateau behaviour is covered by TestUPPlateauReturnsToDown.
}

// TestPacingSmoothing checks that the exported pacing rate is smoothed toward
// the gain-scaled target rather than jumping directly to it.
func TestPacingSmoothing(t *testing.T) {
	e := newSimEnv(t, testConfig())
	e.sendRound(t, 0, 0)
	if e.c.PacingRate() == 0 || e.c.PacingRateTarget() == 0 {
		t.Fatal("expected non-zero pacing output after first sample")
	}
	if e.c.PacingRate() > e.c.PacingRateTarget() && e.c.pacingGain() >= 1 {
		t.Fatalf("smoothed rate %d overshot target %d", e.c.PacingRate(), e.c.PacingRateTarget())
	}
	// Repeated identical samples converge the smoothed rate onto the target.
	for i := 0; i < 10; i++ {
		e.sendRound(t, 0, 0)
	}
	if e.c.PacingRate() != e.c.PacingRateTarget() {
		t.Fatalf("pacing did not converge: smoothed=%d target=%d", e.c.PacingRate(), e.c.PacingRateTarget())
	}
}
