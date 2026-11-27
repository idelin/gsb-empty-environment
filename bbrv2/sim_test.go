package bbrv2

import (
	"testing"
	"time"
)

// simEnv is a deterministic network harness driving the Controller one
// delivery sample at a time. A simulated round sends the current cwnd,
// delivers it at the configured bottleneck rate across four samples, and
// advances the clock by one RTT so that Round-Trip boundaries line up with
// round boundaries.
type simEnv struct {
	c       *Controller
	now     time.Time
	rtt     time.Duration
	samples int
	bwBps   uint64 // fixed bottleneck capacity in bytes/sec; 0 = uncapped
}

// setBottleneck reconfigures the simulated bottleneck capacity.
func (e *simEnv) setBottleneck(bps uint64) {
	e.bwBps = bps
}

func newSimEnv(t *testing.T, cfg Config) *simEnv {
	t.Helper()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return &simEnv{
		c:       New(cfg),
		now:     base,
		rtt:     100 * time.Millisecond,
		samples: 4,
		bwBps:   1_000_000, // 1 MB/s bottleneck => BDP ~100 KB at 100 ms RTT
	}
}

// sendRound models a continuously sending transport: the current window is
// put in flight, while the bytes outstanding from the previous round are
// delivered across four samples. In-flight therefore stays at roughly cwnd,
// mirroring a real sender so DRAIN/DOWN remain observable until an explicit
// drainRound. lossFraction/ecnFraction apply to the bytes delivered this round.
func (e *simEnv) sendRound(t *testing.T, lossFraction, ecnFraction float64) {
	t.Helper()
	previouslyOutstanding := e.c.Inflight()
	e.c.OnSent(e.c.Cwnd())

	// The bottleneck can deliver at most bwBps*RTT bytes per round; any
	// excess offered load remains queued in the pipe (not lost) until DRAIN.
	capacity := e.bwBps * uint64(e.rtt.Nanoseconds()) / 1_000_000_000
	deliverable := previouslyOutstanding
	if e.bwBps > 0 && capacity < deliverable {
		deliverable = capacity
	}
	lost := uint64(float64(deliverable) * lossFraction)
	acked := previouslyOutstanding - lost
	if deliverable < acked {
		acked = deliverable - lost
	}
	ecn := uint64(float64(acked) * ecnFraction)

	perSample := acked / uint64(e.samples)
	interval := e.rtt / time.Duration(e.samples)
	deliveredTotal := uint64(0)
	for i := 0; i < e.samples; i++ {
		var d, a, l, ec uint64
		if i == e.samples-1 {
			d = acked - deliveredTotal
			a = d
			l = lost
			ec = ecn
		} else {
			d = perSample
			a = perSample
			ec = 0
		}
		deliveredTotal += d
		e.now = e.now.Add(interval)
		e.c.OnSample(RateSample{
			Now:            e.now,
			RTT:            e.rtt,
			BytesDelivered: d,
			BytesAcked:     a,
			BytesLost:      l,
			BytesECN:       ec,
			Interval:       interval,
		})
	}
}

// ackRound delivers the outstanding bytes from the prior round without
// refilling the pipe, so congestion adaptation freezes the correct peak.
func (e *simEnv) ackRound(t *testing.T, lossFraction, ecnFraction float64) {
	t.Helper()
	previouslyOutstanding := e.c.Inflight()
	capacity := e.bwBps * uint64(e.rtt.Nanoseconds()) / 1_000_000_000
	deliverable := previouslyOutstanding
	if e.bwBps > 0 && capacity < deliverable {
		deliverable = capacity
	}
	lost := uint64(float64(deliverable) * lossFraction)
	acked := deliverable - lost
	ecn := uint64(float64(acked) * ecnFraction)

	perSample := acked / uint64(e.samples)
	interval := e.rtt / time.Duration(e.samples)
	deliveredTotal := uint64(0)
	for i := 0; i < e.samples; i++ {
		var d, a uint64
		if i == e.samples-1 {
			d = acked - deliveredTotal
			a = d
		} else {
			d = perSample
			a = perSample
		}
		deliveredTotal += d
		e.now = e.now.Add(interval)
		// Spread the congestion signal across every delivery sample so the
		// controller excludes the whole round from BtlBw. Bytes are counted
		// once, on the final sample; the non-zero markers only flag signals.
		var l, ec uint64
		if lost > 0 {
			l = 1
		}
		if ecn > 0 {
			ec = 1
		}
		if i == e.samples-1 {
			l = lost
			ec = ecn
		}
		e.c.OnSample(RateSample{
			Now:            e.now,
			RTT:            e.rtt,
			BytesDelivered: d,
			BytesAcked:     a,
			BytesLost:      l,
			BytesECN:       ec,
			Interval:       interval,
		})
	}
}

// sendRoundAppLimited sends fewer bytes than the window, so the controller is
// application-limited for the whole round.
func (e *simEnv) sendRoundAppLimited(t *testing.T, bytes uint64) {
	t.Helper()
	e.c.SetAppLimited()
	e.c.OnSent(bytes)

	perSample := bytes / uint64(e.samples)
	interval := e.rtt / time.Duration(e.samples)
	deliveredTotal := uint64(0)
	for i := 0; i < e.samples; i++ {
		var d uint64
		if i == e.samples-1 {
			d = bytes - deliveredTotal
		} else {
			d = perSample
		}
		deliveredTotal += d
		e.now = e.now.Add(interval)
		e.c.OnSample(RateSample{
			Now:            e.now,
			RTT:            e.rtt,
			BytesDelivered: d,
			BytesAcked:     d,
			Interval:       interval,
		})
	}
}

// drainRound sends nothing and acknowledges all outstanding in-flight bytes,
// simulating the queue draining in DRAIN/PROBE_RTT.
func (e *simEnv) drainRound(t *testing.T) {
	t.Helper()
	outstanding := e.c.Inflight()
	if outstanding == 0 {
		e.now = e.now.Add(e.rtt)
		// Zero-delivery samples carry no RTT measurement; they only advance
		// the clock so timers (minRTT expiry, dwell) remain testable.
		e.c.OnSample(RateSample{Now: e.now, Interval: e.rtt / 4})
		return
	}
	perSample := outstanding / uint64(e.samples)
	interval := e.rtt / time.Duration(e.samples)
	deliveredTotal := uint64(0)
	for i := 0; i < e.samples; i++ {
		var d uint64
		if i == e.samples-1 {
			d = outstanding - deliveredTotal
		} else {
			d = perSample
		}
		deliveredTotal += d
		e.now = e.now.Add(interval)
		e.c.OnSample(RateSample{
			Now:            e.now,
			RTT:            e.rtt,
			BytesDelivered: d,
			BytesAcked:     d,
			Interval:       interval,
		})
	}
}

// drainUntilState drains one sample at a time until the controller reaches
// the wanted top-level state, returning the phase observed on entry. This
// catches transitions that occur mid-round rather than only at round end.
func (e *simEnv) drainUntilState(t *testing.T, want State) ProbePhase {
	t.Helper()
	interval := e.rtt / time.Duration(e.samples)
	for n := 0; n < 64 && e.c.State() != want; n++ {
		outstanding := e.c.Inflight()
		var d uint64
		if outstanding > 0 {
			step := outstanding / uint64(e.samples)
			if step == 0 {
				step = outstanding
			}
			if step > outstanding {
				step = outstanding
			}
			d = step
		}
		e.now = e.now.Add(interval)
		e.c.OnSample(RateSample{
			Now:            e.now,
			RTT:            e.rtt,
			BytesDelivered: d,
			BytesAcked:     d,
			Interval:       interval,
		})
	}
	if e.c.State() != want {
		t.Fatalf("drainUntilState never reached %v, ended in %v", want, e.c.State())
	}
	return e.c.ProbePhase()
}

// runUntilPhase advances zero-delivery clock samples (keeping the pipe
// loaded by re-sending the cwnd each sample) until the wanted PROBE_BW
// sub-phase is entered, returning the phase actually observed.
func (e *simEnv) runUntilPhase(t *testing.T, want ProbePhase) ProbePhase {
	t.Helper()
	interval := e.rtt / time.Duration(e.samples)
	for n := 0; n < 128; n++ {
		if e.c.ProbePhase() == want {
			return want
		}
		outstanding := e.c.Inflight()
		var d uint64
		if outstanding > 0 {
			step := outstanding / uint64(e.samples)
			if step == 0 {
				step = outstanding
			}
			d = step
		}
		e.c.OnSent(d)
		e.now = e.now.Add(interval)
		e.c.OnSample(RateSample{
			Now:            e.now,
			RTT:            e.rtt,
			BytesDelivered: d,
			BytesAcked:     d,
			Interval:       interval,
		})
	}
	t.Fatalf("runUntilPhase never reached %v, ended in %v", want, e.c.ProbePhase())
	return e.c.ProbePhase()
}

// advanceRound feeds loaded samples until the Round-Trip counter advances,
// guaranteeing that round-boundary logic has run exactly once.
func (e *simEnv) advanceRound(t *testing.T) {
	t.Helper()
	start := e.c.RoundCount()
	interval := e.rtt / time.Duration(e.samples)
	for n := 0; e.c.RoundCount() == start && n < 32; n++ {
		// Offer enough load to saturate the simulated bottleneck so the
		// measured delivery rate tracks capacity changes during UP probing.
		capPerSample := e.bwBps * uint64(interval.Nanoseconds()) / 1_000_000_000
		d := capPerSample
		if d == 0 {
			d = e.c.cfg.MSS
		}
		e.c.OnSent(d)
		e.now = e.now.Add(interval)
		e.c.OnSample(RateSample{
			Now:            e.now,
			RTT:            e.rtt,
			BytesDelivered: d,
			BytesAcked:     d,
			Interval:       interval,
		})
	}
	if e.c.RoundCount() == start {
		t.Fatalf("advanceRound failed to cross a round boundary")
	}
}

// advance moves the clock and feeds a zero-delivery sample (no data in flight).
func (e *simEnv) advance(d time.Duration) {
	e.now = e.now.Add(d)
	e.c.OnSample(RateSample{Now: e.now, Interval: d})
}

func (e *simEnv) elapsed() time.Duration {
	return e.now.Sub(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
}
