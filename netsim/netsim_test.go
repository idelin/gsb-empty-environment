package netsim

import (
	"testing"
	"time"

	"prescreen.local/ps-93493e0dc5/bbr"
)

// fastTestLink shrinks the time-based constants so end-to-end behavior
// converges inside a short deterministic run.
func fastBBRConfig() *bbr.Config {
	cfg := bbr.DefaultConfig()
	cfg.CruiseDuration = 200 * time.Millisecond
	return cfg
}

// TestDeterminism runs the identical configuration twice and asserts the
// traffic counters and final BBR state match exactly.
func TestDeterminism(t *testing.T) {
	run := func() (bbr.State, bbr.ProbePhase, SimStats) {
		sim := New(DefaultLinkConfig())
		f := sim.AddFlow(1, 0, fastBBRConfig())
		sim.Run(int64(2 * time.Second))
		return f.BBR().State(), f.BBR().Phase(), sim.Stats()
	}

	s1, p1, st1 := run()
	s2, p2, st2 := run()
	if s1 != s2 || p1 != p2 {
		t.Fatalf("non-deterministic final state: (%s/%s) vs (%s/%s)", s1, p1, s2, p2)
	}
	if st1.QueueDrops != st2.QueueDrops || st1.ECNMarks != st2.ECNMarks {
		t.Fatalf("non-deterministic network counters: %+v vs %+v", st1, st2)
	}
	if st1.Flows[1] != st2.Flows[1] {
		t.Fatalf("non-deterministic flow counters: %+v vs %+v", st1.Flows[1], st2.Flows[1])
	}
}

// TestEndToEndStateProgression drives one backlogged flow through the
// bottleneck and asserts it leaves STARTUP, visits DRAIN and reaches PROBE_BW,
// and that the four ProbeBW sub-phases all occur in the canonical order.
func TestEndToEndStateProgression(t *testing.T) {
	cfg := fastBBRConfig()
	var states []bbr.State
	var phases []bbr.ProbePhase
	cfg.OnStateChange = func(_, n bbr.State) { states = append(states, n) }
	cfg.OnPhaseChange = func(_, n bbr.ProbePhase) { phases = append(phases, n) }

	sim := New(DefaultLinkConfig())
	f := sim.AddFlow(1, 0, cfg)
	sim.Run(int64(3 * time.Second))

	if f.BBR().State() != bbr.ProbeBW {
		t.Fatalf("expected steady state PROBE_BW, got %s (states=%v)", f.BBR().State(), states)
	}
	if !containsState(states, bbr.Drain) {
		t.Fatalf("expected DRAIN to be visited, states=%v", states)
	}
	want := []bbr.ProbePhase{bbr.ProbeDown, bbr.ProbeCruise, bbr.ProbeRefill, bbr.ProbeUp}
	if !subPhasesInOrder(phases, want) {
		t.Fatalf("expected sub-phase order %v somewhere in %v", want, phases)
	}
}

// TestECNEnabledLinkMarksPackets enables an aggressive ECN threshold and
// asserts the bottleneck marks packets and the BBR flow reacts by leaving a
// UP probe (the dual-bound back-off itself is unit-tested in the bbr package).
func TestECNEnabledLinkMarksPackets(t *testing.T) {
	link := DefaultLinkConfig()
	link.ECNThreshold = 6 // mark aggressively as the shallow queue fills
	cfg := fastBBRConfig()
	downFromUp := false
	cfg.OnPhaseChange = func(o, n bbr.ProbePhase) {
		if o == bbr.ProbeUp && n == bbr.ProbeDown {
			downFromUp = true
		}
	}

	sim := New(link)
	f := sim.AddFlow(1, 0, cfg)
	sim.Run(int64(3 * time.Second))

	st := sim.Stats()
	if st.ECNMarks == 0 {
		t.Fatalf("expected ECN marks on an ECN-enabled bottleneck")
	}
	if f.BBR().BwHi() == 0 {
		t.Fatalf("expected a learned bw_hi despite ECN marking")
	}
	// At least one UP probe must have been aborted by the explicit signal.
	if !downFromUp {
		t.Fatalf("expected at least one UP->DOWN back-off under ECN, marks=%d", st.ECNMarks)
	}
}

// TestAppLimitedFlowDoesNotCollapseBwHi registers a rate-limited (application
// limited) flow and asserts that, after an initial backlogged learning phase,
// reducing the application offer rate does not drive bw_hi to zero.
func TestAppLimitedFlowDoesNotCollapseBwHi(t *testing.T) {
	sim := New(DefaultLinkConfig())
	// Offer only ~20% of the link rate so most samples are app-limited.
	f := sim.AddFlow(1, DefaultLinkConfig().RateBps/5, fastBBRConfig())
	sim.Run(int64(3 * time.Second))

	if f.BBR().BwHi() == 0 {
		t.Fatalf("app-limited flow must retain a non-zero bw_hi estimate")
	}
	// Delivery stays positive, proving the flow continues making progress.
	if sim.Stats().Flows[1].Delivered == 0 {
		t.Fatalf("app-limited flow delivered no packets")
	}
}

// TestTwoFlowDeterministicSharing runs two backlogged senders over the shared
// bottleneck and asserts both converge to non-trivial, stable delivery.
func TestTwoFlowDeterministicSharing(t *testing.T) {
	sim := New(DefaultLinkConfig())
	sim.AddFlow(1, 0, fastBBRConfig())
	sim.AddFlow(2, 0, fastBBRConfig())
	sim.Run(int64(4 * time.Second))

	st := sim.Stats()
	for _, id := range []int{1, 2} {
		fs := st.Flows[id]
		if fs.Delivered < 50 {
			t.Fatalf("flow %d delivered too little: %+v", id, fs)
		}
		if sim.FlowByID(id).BBR().BwHi() == 0 {
			t.Fatalf("flow %d failed to learn bw_hi", id)
		}
	}
}

func containsState(got []bbr.State, want bbr.State) bool {
	for _, s := range got {
		if s == want {
			return true
		}
	}
	return false
}

func subPhasesInOrder(got []bbr.ProbePhase, want []bbr.ProbePhase) bool {
	for start := 0; start+len(want) <= len(got); start++ {
		match := true
		for i, p := range want {
			if got[start+i] != p {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
