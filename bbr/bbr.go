package bbr

import "time"

// AckEvent is the per-ACK feedback consumed by the state machine. The
// transport (or the netsim engine) is responsible for detecting losses and
// echoing ECN counts; BBR only reacts to the explicit signals.
type AckEvent struct {
	// Now is the virtual (or real) arrival time of the ACK, in nanoseconds.
	Now int64
	// RTT is the latest round-trip-time sample for this ACK.
	RTT time.Duration
	// AckedPackets / LostPackets are newly S/ACKed and declared-lost packets.
	AckedPackets int
	LostPackets  int
	// ECNEchoes is the number of packets reported ECN-marked by this ACK train.
	ECNEchoes int
	// BytesDelivered over Interval form the delivery-rate sample.
	BytesDelivered int
	Interval       time.Duration
	// AppLimited reports whether the application was unable to fill the pipe
	// during the sample interval.
	AppLimited bool
}

// BBR is a BBRv2 congestion control instance. It is intended for
// single-goroutine use driven by an external clock; all transitions are
// deterministic functions of the AckEvent stream.
type BBR struct {
	cfg   *Config
	state State
	phase ProbePhase

	// round bookkeeping
	round     uint64
	delivered int // cumulative S/ACKed packets
	// roundGoal is the number of packets (acked or declared lost) that must
	// leave the pipe to end the current round.
	roundGoal     int
	roundObserved int // packets observed leaving the pipe this round
	roundAcks     int
	roundLoss     int
	roundECN      int
	roundStartBw  uint64
	fullRounds    int // consecutive low-growth rounds in STARTUP
	startupRounds int // STARTUP rounds completed, drives cwnd doubling

	// flight-size bookkeeping (packets)
	inflight       int
	inflightLatest int // flight size observed at the latest ACK
	cwnd           int

	// dual bounds
	sampler      *rateSampler // bw_hi window filter
	bwLo         uint64       // short-term lower-bound rate estimate (bytes/sec)
	inflightHi   int          // long-term upper bound on safe flight size
	inflightLo   int          // short-term beta-decayed flight-size bound
	probePackets int          // exponential UP probe allowance (1,2,4,...)

	// min-RTT bookkeeping
	minRTT      time.Duration
	minRTTStamp int64

	// phase / state timing
	stateEnter  int64
	phaseEnter  int64
	cruiseEnter int64

	pacingRate uint64

	// ExitedStartupEarly is set when loss/ECN force STARTUP to exit before
	// the three-round growth criterion fires.
	ExitedStartupEarly bool
	// StartupHiLocked reports that inflight_hi was pinned at the early exit.
	StartupHiLocked bool
}

// New constructs a BBRv2 sender in STARTUP. A nil config selects defaults.
func New(cfg *Config) *BBR {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	return &BBR{
		cfg:     cfg,
		state:   Startup,
		phase:   ProbeDown,
		cwnd:    cfg.InitialCwnd,
		sampler: newRateSampler(cfg.MaxBwFilterWindow),
	}
}

// State returns the current main state.
func (b *BBR) State() State { return b.state }

// Phase returns the current ProbeBW sub-phase (undefined outside PROBE_BW).
func (b *BBR) Phase() ProbePhase { return b.phase }

// Cwnd returns the congestion window in packets.
func (b *BBR) Cwnd() int { return b.cwnd }

// Inflight returns the current number of packets in flight.
func (b *BBR) Inflight() int { return b.inflight }

// InflightHi / InflightLo expose the flight-size dual bounds (packets).
func (b *BBR) InflightHi() int { return b.inflightHi }
func (b *BBR) InflightLo() int { return b.inflightLo }

// BwHi returns the filtered maximum delivery rate (bytes/sec).
func (b *BBR) BwHi() uint64 { return b.sampler.bwHi() }

// BwLo returns the short-term lower-bound rate estimate (bytes/sec).
func (b *BBR) BwLo() uint64 { return b.bwLo }

// MinRTT returns the current minimum RTT estimate.
func (b *BBR) MinRTT() time.Duration { return b.minRTT }

// PacingRate returns the current pacing rate in bytes/second.
func (b *BBR) PacingRate() uint64 { return b.pacingRate }

// CanSend reports whether one more packet may be sent under the cwnd limit.
// Pacing is enforced separately via PacingRate (the netsim engine applies a
// token bucket; a real transport wires it into its pacer).
func (b *BBR) CanSend() bool { return b.inflight < b.cwnd }

// OnSent accounts for freshly transmitted packets and starts a new BBR round
// when the pipe was idle.
func (b *BBR) OnSent(packets int, now int64) {
	if packets <= 0 {
		return
	}
	b.inflight += packets
	// A new round opens when a send happens with no active round. Its goal is
	// fixed at the flight size present at that instant and is NOT extended by
	// later sends, so each round lasts about one RTT. Loss counts toward the
	// goal so a dropped head-of-line packet cannot stall the round boundary.
	if b.roundGoal == 0 {
		b.roundGoal = b.inflight
		b.roundObserved = 0
	}
}

// HandleAck processes one ACK/feedback event.
func (b *BBR) HandleAck(e AckEvent) {
	if e.RTT > 0 {
		b.updateMinRTT(e.RTT, e.Now)
	}

	// inflight_latest is the flight size observed when the ACK arrives,
	// i.e. before this ACK's packets leave the pipe.
	b.inflightLatest = b.inflight
	if e.AckedPackets+e.LostPackets > b.inflight {
		b.inflight = 0
	} else {
		b.inflight -= e.AckedPackets + e.LostPackets
	}
	b.delivered += e.AckedPackets
	b.roundAcks += e.AckedPackets
	b.roundLoss += e.LostPackets
	b.roundECN += e.ECNEchoes

	if e.Interval > 0 && e.BytesDelivered > 0 {
		rate := uint64(e.BytesDelivered) * uint64(time.Second) / uint64(e.Interval.Nanoseconds())
		b.sampler.update(rate, b.round, e.AppLimited)
	}

	// Explicit loss / ECN reaction (dual-bound adaptation + early STARTUP exit).
	if b.isCongested() {
		b.adaptCongestion(e.Now)
	}

	// Round completion: the flight present at the round opening has drained.
	b.roundObserved += e.AckedPackets + e.LostPackets
	if b.roundGoal != 0 && b.roundObserved >= b.roundGoal {
		b.endRound(e.Now)
		// Packets still in flight immediately start the next round over the
		// remaining flight, so ACKs arriving before the next burst still fall
		// inside an active round instead of being orphaned.
		if b.inflight > 0 {
			b.roundGoal = b.inflight
			b.roundObserved = 0
		}
	}

	b.updateState(e.Now)
	b.recomputeControl(e.Now)
}

// Tick drives time-based transitions without an ACK (required for PROBE_RTT
// entry/exit while the sender is idle or window-limited).
func (b *BBR) Tick(now int64) {
	b.updateState(now)
	b.recomputeControl(now)
}

// updateMinRTT tracks the smallest RTT sample; the timestamp is only refreshed
// inside PROBE_RTT (see exitProbeRTT), so a 10s-stale min_rtt forces a probe.
func (b *BBR) updateMinRTT(rtt time.Duration, now int64) {
	if b.minRTT == 0 || rtt < b.minRTT {
		b.minRTT = rtt
		if b.minRTTStamp == 0 {
			b.minRTTStamp = now
		}
	}
}

// isCongested implements the BBRv2 explicit-signal thresholds evaluated over
// the current round: loss ratio >= loss_thresh (with an absolute loss floor)
// or ECN-echo fraction >= ecn_thresh.
func (b *BBR) isCongested() bool {
	total := b.roundAcks + b.roundLoss
	if b.roundLoss >= b.cfg.MinLossEvents && total > 0 {
		if float64(b.roundLoss)/float64(total) >= b.cfg.LossThresh {
			return true
		}
	}
	// During STARTUP a handful of ECN marks already prove the shallow buffer
	// is overfilling, so exit early instead of waiting for the steady-state
	// 50% threshold; the long-term inflight_hi is locked at the same instant.
	ecnThresh := b.cfg.ECNThresh
	if b.state == Startup && b.roundECN >= b.cfg.MinLossEvents {
		ecnThresh = 0.0
	}
	if b.roundAcks > 0 && float64(b.roundECN)/float64(b.roundAcks) >= ecnThresh {
		return true
	}
	return false
}

// adaptCongestion applies the beta back-off of the short-term bounds and,
// depending on the current state, pins inflight_hi or aborts the UP probe.
func (b *BBR) adaptCongestion(now int64) {
	// BBRv2 only reacts to explicit loss/ECN while probing upward or while
	// still in STARTUP. Signals observed in DOWN/CRUISE/REFILL are expected
	// (the sender is already inside the known safe envelope) and must not keep
	// decaying the short-term bounds, otherwise the window collapses.
	if b.state != Startup && !(b.state == ProbeBW && b.phase == ProbeUp) {
		return
	}

	bw := b.operatingBw()

	// bw_lo: multiplicative decay of the short-term rate lower bound.
	bwCandidate := scaleU64(bw, b.cfg.Beta)
	switch {
	case b.bwLo == 0:
		b.bwLo = bwCandidate
	case scaleU64(b.bwLo, b.cfg.Beta) > bwCandidate:
		b.bwLo = scaleU64(b.bwLo, b.cfg.Beta)
	default:
		b.bwLo = bwCandidate
	}

	// inflight_lo: beta decay around the latest safely-delivered flight size.
	loCandidate := scaleInt(b.inflightLatest, b.cfg.Beta)
	switch {
	case b.inflightLo == 0:
		b.inflightLo = loCandidate
	case scaleInt(b.inflightLo, b.cfg.Beta) > loCandidate:
		b.inflightLo = scaleInt(b.inflightLo, b.cfg.Beta)
	default:
		b.inflightLo = loCandidate
	}
	if b.inflightLo < b.cfg.MinCwnd {
		b.inflightLo = b.cfg.MinCwnd
	}

	switch {
	case b.state == Startup:
		// Early STARTUP exit: immediately lock the long-term upper bound at
		// the flight size that just proved unsafe, minus headroom.
		hi := scaleInt(b.inflightLatest, 1.0-b.cfg.Headroom)
		if hi < b.cfg.MinCwnd {
			hi = b.cfg.MinCwnd
		}
		b.inflightHi = hi
		b.ExitedStartupEarly = true
		b.StartupHiLocked = true
		b.exitStartup(now)
	case b.state == ProbeBW && b.phase == ProbeUp:
		// The probe hit the ceiling: shrink inflight_hi toward the safe
		// flight size and fall back to DOWN, keeping the short-term bounds.
		hi := scaleInt(b.inflightLatest, b.cfg.Beta)
		if hi < b.cfg.MinCwnd {
			hi = b.cfg.MinCwnd
		}
		if b.inflightHi == 0 || hi < b.inflightHi {
			b.inflightHi = hi
		}
		b.enterPhase(now, ProbeDown)
	}
}

func scaleU64(v uint64, f float64) uint64 {
	return uint64(float64(v) * f)
}

func scaleInt(v int, f float64) int {
	return int(float64(v) * f)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
