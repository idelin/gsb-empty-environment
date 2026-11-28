package bbr

import (
	"testing"
	"time"
)

const testMSS = 1448

// driveRound delivers exactly the packets currently in flight over the given
// RTT, closing one BBR round. loss/ecn let a test inject explicit signals.
func driveRound(t *testing.T, b *BBR, now int64, rtt time.Duration, loss, ecn int) {
	t.Helper()
	inflight := b.Inflight()
	acked := inflight - loss
	if acked < 0 {
		acked = 0
	}
	b.HandleAck(AckEvent{
		Now:            now,
		RTT:            rtt,
		AckedPackets:   acked,
		LostPackets:    loss,
		ECNEchoes:      ecn,
		BytesDelivered: acked * testMSS,
		Interval:       rtt,
	})
	// Refill the pipe at the new window so subsequent rounds have data.
	if b.CanSend() {
		b.OnSent(b.Cwnd()-b.Inflight(), now+rtt.Nanoseconds()/2)
	}
}

// TestStartupEarlyExitLocksInflightHi verifies that heavy explicit loss (or a
// high ECN fraction) within a single STARTUP round forces an immediate exit to
// DRAIN and pins the long-term inflight_hi at the observed flight minus
// headroom, rather than waiting for three low-growth rounds.
func TestStartupEarlyExitLocksInflightHi(t *testing.T) {
	cfg := DefaultConfig()
	var enteredDrain bool
	cfg.OnStateChange = func(_, n State) {
		if n == Drain {
			enteredDrain = true
		}
	}
	b := New(cfg)
	rtt := 40 * time.Millisecond
	now := int64(0)

	// Fill an initial window and let the first round deliver cleanly so a
	// delivery-rate (and thus BDP) sample exists.
	b.OnSent(b.Cwnd(), now)
	now += rtt.Nanoseconds()
	driveRound(t, b, now, rtt, 0, 0)
	if b.State() != Startup {
		t.Fatalf("expected STARTUP after clean round, got %s", b.State())
	}

	// Inject a burst large enough to overfill a shallow buffer, then report a
	// loss fraction above loss_thresh within one round.
	flight := b.Inflight()
	loss := flight/20 + 2 // >= 2% and >= MinLossEvents
	now += rtt.Nanoseconds()
	driveRound(t, b, now, rtt, loss, 0)

	if !b.ExitedStartupEarly {
		t.Fatalf("expected early STARTUP exit from explicit loss")
	}
	if !enteredDrain {
		t.Fatalf("expected transition through DRAIN after early exit, state=%s", b.State())
	}
	if !b.StartupHiLocked || b.InflightHi() <= 0 {
		t.Fatalf("expected inflight_hi locked at early exit, got hi=%d locked=%v",
			b.InflightHi(), b.StartupHiLocked)
	}
	// The locked ceiling must reflect beta/headroom back-off, i.e. be below the
	// flight size that proved unsafe.
	if b.InflightHi() >= flight {
		t.Fatalf("inflight_hi=%d should be below unsafe flight=%d", b.InflightHi(), flight)
	}
}

// TestStartupECNEarlyExit verifies the ECN-driven variant of the early exit.
func TestStartupECNEarlyExit(t *testing.T) {
	cfg := DefaultConfig()
	var enteredDrain bool
	cfg.OnStateChange = func(_, n State) {
		if n == Drain {
			enteredDrain = true
		}
	}
	b := New(cfg)
	rtt := 40 * time.Millisecond
	now := int64(0)
	b.OnSent(b.Cwnd(), now)
	now += rtt.Nanoseconds()
	driveRound(t, b, now, rtt, 0, 0)

	// In STARTUP even a small number of ECN echoes trips the sensitive marker.
	flight := b.Inflight()
	now += rtt.Nanoseconds()
	driveRound(t, b, now, rtt, 0, cfg.MinLossEvents)

	if !enteredDrain || !b.ExitedStartupEarly || !b.StartupHiLocked {
		t.Fatalf("ECN early exit failed: drain=%v early=%v locked=%v flight=%d",
			enteredDrain, b.ExitedStartupEarly, b.StartupHiLocked, flight)
	}
}

// TestProbeBWSubphaseOrderAndHeadroom walks the full DOWN -> CRUISE -> REFILL
// -> UP sequence and asserts CRUISE is capped at inflight_hi*(1-headroom).
func TestProbeBWSubphaseOrderAndHeadroom(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CruiseDuration = 100 * time.Millisecond
	var phases []ProbePhase
	cfg.OnPhaseChange = func(_, n ProbePhase) { phases = append(phases, n) }
	b := New(cfg)
	rtt := 40 * time.Millisecond
	now := int64(0)

	// Isolate the ProbeBW sub-phase machine by entering it directly with valid
	// estimates; the STARTUP/DRAIN transitions are covered by dedicated tests.
	b.state = ProbeBW
	b.phase = ProbeDown
	b.phaseEnter = now
	b.minRTT = rtt
	b.sampler.update(1_000_000, 0, false)
	b.cwnd = 30
	b.inflight = 30
	b.OnSent(30, now)
	// The machine starts in DOWN; seed the observed sequence with it.
	phases = append(phases, ProbeDown)

	// Establish an explicit long-term ceiling for the headroom assertion.
	if b.InflightHi() == 0 {
		b.inflightHi = 40
	}
	hi := b.InflightHi()

	// Advance virtual time across several phase boundaries (REFILL/UP are RTT
	// gated; CRUISE is gated by CruiseDuration).
	deadline := now + int64(2*time.Second)
	for now < deadline {
		now += rtt.Nanoseconds()
		b.Tick(now)
		if b.Phase() == ProbeCruise {
			cap := scaleInt(hi, 1.0-cfg.Headroom)
			if b.Cwnd() > cap {
				t.Fatalf("CRUISE cwnd=%d exceeds headroom cap=%d (hi=%d)", b.Cwnd(), cap, hi)
			}
		}
	}

	want := []ProbePhase{ProbeDown, ProbeCruise, ProbeRefill, ProbeUp}
	if !containsPrefix(phases, want) {
		t.Fatalf("expected sub-phase order %v, got %v", want, phases)
	}
}

func containsPrefix(got []ProbePhase, want []ProbePhase) bool {
	if len(got) < len(want) {
		return false
	}
	for i, p := range want {
		if got[i] != p {
			return false
		}
	}
	return true
}

// TestECNBacksOffLowerBounds drives a ProbeUP encounter with ECN above the
// threshold and asserts the short-term inflight_lo and bw_lo are beta-decayed
// while the long-term inflight_hi shrinks and the phase falls back to DOWN.
func TestECNBacksOffLowerBounds(t *testing.T) {
	cfg := DefaultConfig()
	var sawDown bool
	cfg.OnPhaseChange = func(_, n ProbePhase) {
		if n == ProbeDown {
			sawDown = true
		}
	}
	b := New(cfg)
	b.state = ProbeBW
	b.phase = ProbeUp
	b.minRTT = 40 * time.Millisecond
	// Seed bw_hi so operatingBw/BDP are defined.
	b.sampler.update(1_000_000, 0, false)
	b.inflightHi = 40

	// Build a round where >= 50% of ACKs carry ECN echoes.
	b.OnSent(20, 0)
	now := int64(40 * time.Millisecond)
	b.HandleAck(AckEvent{
		Now:            now,
		RTT:            b.minRTT,
		AckedPackets:   20,
		ECNEchoes:      12,
		BytesDelivered: 20 * testMSS,
		Interval:       b.minRTT,
	})

	if b.InflightLo() <= 0 {
		t.Fatalf("expected inflight_lo back-off after ECN, got 0")
	}
	if b.BwLo() <= 0 {
		t.Fatalf("expected bw_lo back-off after ECN, got 0")
	}
	if b.InflightLo() > scaleInt(20, cfg.Beta)+1 {
		t.Fatalf("inflight_lo=%d not beta-decayed around latest flight", b.InflightLo())
	}
	if !sawDown {
		t.Fatalf("expected fallback through DOWN after ECN in UP, phase=%s", b.Phase())
	}
	if b.InflightHi() >= 40 {
		t.Fatalf("expected inflight_hi to shrink from 40, got %d", b.InflightHi())
	}
}

// TestAppLimitedDoesNotPolluteBwHi feeds high-rate full-utilization samples
// followed by very low-rate app-limited samples and asserts bw_hi never drops.
func TestAppLimitedDoesNotPolluteBwHi(t *testing.T) {
	cfg := DefaultConfig()
	b := New(cfg)

	// Establish a high bw_hi from application-backlogged samples.
	for round := uint64(0); round < 3; round++ {
		b.sampler.update(2_000_000, round, false)
	}
	hi := b.BwHi()
	if hi != 2_000_000 {
		t.Fatalf("expected bw_hi=2000000, got %d", hi)
	}

	// Idle/application-limited intervals report far lower delivery rates.
	for round := uint64(3); round < 20; round++ {
		b.sampler.update(50_000, round, true)
	}
	if got := b.BwHi(); got != hi {
		t.Fatalf("app-limited samples polluted bw_hi: want %d got %d", hi, got)
	}

	// A new genuine full-utilization sample may still raise the estimate.
	b.sampler.update(3_000_000, 21, false)
	if b.BwHi() != 3_000_000 {
		t.Fatalf("real sample should be able to raise bw_hi, got %d", b.BwHi())
	}
}

// TestProbeRTTTimingAndRecovery forces min_rtt to go stale for 10s, verifies
// entry into PROBE_RTT with the tiny window, then asserts recovery back to
// PROBE_BW after max(200ms, 1 RTT) with a refreshed min_rtt timestamp.
func TestProbeRTTTimingAndRecovery(t *testing.T) {
	cfg := DefaultConfig()
	b := New(cfg)
	rtt := 40 * time.Millisecond

	// Reach PROBE_BW with valid estimates.
	b.state = ProbeBW
	b.phase = ProbeCruise
	b.minRTT = rtt
	b.minRTTStamp = 0
	b.sampler.update(1_000_000, 0, false)
	b.cwnd = 30

	now := int64(cfg.ProbeRTTInterval) // exactly the staleness threshold
	b.Tick(now)
	if b.State() != ProbeRTT {
		t.Fatalf("expected PROBE_RTT after 10s stale min_rtt, got %s", b.State())
	}

	// The window must be squeezed to half a BDP but never below 4 packets.
	wantCwnd := scaleInt(b.BDP(), cfg.ProbeRTTCwndFraction)
	if wantCwnd < cfg.MinCwnd {
		wantCwnd = cfg.MinCwnd
	}
	if b.Cwnd() != wantCwnd {
		t.Fatalf("PROBE_RTT cwnd=%d want %d", b.Cwnd(), wantCwnd)
	}

	// Drain the pipe to the probe window so the dwell/exit guard can pass.
	b.inflight = b.cwnd
	stampBefore := b.minRTTStamp

	// Advance past max(200ms, 1 RTT).
	now += cfg.ProbeRTTMinDuration.Nanoseconds() + rtt.Nanoseconds()
	b.Tick(now)
	if b.State() != ProbeBW {
		t.Fatalf("expected recovery to PROBE_BW, got %s", b.State())
	}
	if b.Phase() != ProbeDown {
		t.Fatalf("expected to resume in DOWN, got %s", b.Phase())
	}
	if b.minRTTStamp <= stampBefore {
		t.Fatalf("min_rtt timestamp should be refreshed on exit")
	}
}
