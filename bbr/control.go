package bbr

import "time"

// endRound handles per-round bookkeeping: STARTUP growth detection and the
// exponential UP probe schedule.
func (b *BBR) endRound(now int64) {
	b.round++

	if b.state == Startup {
		b.startupRounds++
		bw := b.sampler.bwHi()
		if b.roundStartBw > 0 {
			growth := float64(bw) / float64(b.roundStartBw)
			if growth < 1.0+b.cfg.StartupFullGrowth {
				b.fullRounds++
			} else {
				b.fullRounds = 0
			}
		}
		b.roundStartBw = bw
		if b.fullRounds >= b.cfg.StartupFullRounds {
			b.exitStartup(now)
		}
	}

	if b.state == ProbeBW && b.phase == ProbeUp {
		// A clean UP round (loss/ECN would have left the phase via
		// adaptCongestion) grows inflight_hi and doubles the probe allowance.
		b.inflightHi = maxInt(b.inflightHi, b.inflightLatest)
		next := b.probePackets * 2
		if b.inflightHi > 0 && next > b.inflightHi {
			// Probe allowance exceeds the known ceiling: end the cycle and
			// reset the short-term bounds while retaining long-term inflight_hi.
			b.resetShortBounds()
			b.enterPhase(now, ProbeDown)
		} else {
			b.probePackets = next
		}
	}

	b.roundAcks, b.roundLoss, b.roundECN = 0, 0, 0
	// Clear the goal; the next OnSent opens a fresh round, keeping each round
	// roughly one RTT long even under continuous back-to-back transmission.
	b.roundGoal = 0
	b.roundObserved = 0
}

// exitStartup leaves STARTUP for DRAIN.
func (b *BBR) exitStartup(now int64) {
	b.setState(now, Drain)
}

// resetShortBounds clears the per-cycle short-term estimates at the start of a
// fresh ProbeBW cycle; the long-term inflight_hi is deliberately preserved.
func (b *BBR) resetShortBounds() {
	b.bwLo = 0
	b.inflightLo = 0
}

// updateState runs all non-congestion state/phase transitions.
func (b *BBR) updateState(now int64) {
	// PROBE_RTT entry: min_rtt has not been refreshed for 10s.
	if b.state != ProbeRTT && b.minRTT != 0 &&
		now-b.minRTTStamp >= b.cfg.ProbeRTTInterval.Nanoseconds() {
		b.enterProbeRTT(now)
		return
	}

	switch b.state {
	case Drain:
		if b.BDP() > 0 && b.inflight <= b.BDP() {
			b.setState(now, ProbeBW)
			b.enterPhase(now, ProbeDown)
		}

	case ProbeRTT:
		dwell := time.Duration(now - b.stateEnter)
		minDwell := b.cfg.ProbeRTTMinDuration
		if b.minRTT > minDwell {
			minDwell = b.minRTT
		}
		// Hold the tiny window for at least max(200ms, 1 RTT) and until the
		// pipe has drained to it, so the queue fully empties.
		if dwell >= minDwell && b.inflight <= b.cwnd {
			b.exitProbeRTT(now)
		}

	case ProbeBW:
		switch b.phase {
		case ProbeDown:
			rtt := b.effectiveRTT()
			if b.BDP() > 0 && (b.inflight <= b.BDP() ||
				(rtt > 0 && now-b.phaseEnter >= rtt.Nanoseconds())) {
				b.enterPhase(now, ProbeCruise)
				b.cruiseEnter = now
			}
		case ProbeCruise:
			if now-b.cruiseEnter >= b.cfg.CruiseDuration.Nanoseconds() {
				b.enterPhase(now, ProbeRefill)
			}
		case ProbeRefill:
			rtt := b.effectiveRTT()
			if rtt > 0 && now-b.phaseEnter >= rtt.Nanoseconds() {
				// A fresh upward probe begins: clear the previous cycle's
				// short-term bounds so they do not decay forever, while the
				// long-term inflight_hi ceiling is retained.
				b.resetShortBounds()
				b.enterPhase(now, ProbeUp)
				b.probePackets = 1
			}
		case ProbeUp:
			// UP transitions are driven by round-end and congestion callbacks.
		}
	}
}

func (b *BBR) enterProbeRTT(now int64) {
	b.setState(now, ProbeRTT)
}

func (b *BBR) exitProbeRTT(now int64) {
	// The drained measurement refreshed the true propagation RTT.
	b.minRTTStamp = now
	b.setState(now, ProbeBW)
	b.resetShortBounds()
	b.enterPhase(now, ProbeDown)
}

func (b *BBR) setState(now int64, s State) {
	if b.state == s {
		return
	}
	old := b.state
	b.state = s
	b.stateEnter = now
	if b.cfg.OnStateChange != nil {
		b.cfg.OnStateChange(old, s)
	}
}

func (b *BBR) enterPhase(now int64, p ProbePhase) {
	if b.phase == p && b.state == ProbeBW {
		return
	}
	old := b.phase
	b.phase = p
	b.phaseEnter = now
	if b.cfg.OnPhaseChange != nil {
		b.cfg.OnPhaseChange(old, p)
	}
}

// effectiveRTT falls back to 1ms when no sample exists yet so phase timers
// remain well-defined during the very first RTT.
func (b *BBR) effectiveRTT() time.Duration {
	if b.minRTT > 0 {
		return b.minRTT
	}
	return time.Millisecond
}

// operatingBw returns the bw_lo/bw_hi clamped estimate. Before any lower
// bound exists it is simply bw_hi.
func (b *BBR) operatingBw() uint64 {
	hi := b.sampler.bwHi()
	if b.bwLo == 0 {
		return hi
	}
	if b.bwLo < hi {
		return b.bwLo
	}
	return hi
}

// BDP is the bandwidth-delay product in packets, using the operating
// bandwidth and min RTT. It returns 0 until the first valid sample exists.
func (b *BBR) BDP() int {
	bw := b.operatingBw()
	if bw == 0 || b.minRTT == 0 {
		return 0
	}
	bdpBytes := float64(bw) * b.minRTT.Seconds()
	return int(bdpBytes / float64(b.cfg.MSS))
}

// startupCwnd grows the STARTUP window one round at a time: it doubles on each
// round boundary (the classic slow-start exponential) but is capped by the
// 2.89x-gain BDP target so a single saturated rate sample cannot inflate the
// window to the full gain in one RTT and overflow a shallow buffer.
func (b *BBR) startupCwnd() int {
	bdp := b.BDP()
	if bdp == 0 {
		return maxInt(b.cwnd, b.cfg.InitialCwnd)
	}
	capGain := scaleInt(bdp, b.cfg.StartupGain)
	// One doubling per completed round, anchored at the initial window: each
	// RTT at most doubles the window rather than jumping to the full gain cap.
	target := b.cfg.InitialCwnd
	for r := 0; r < b.startupRounds && target < capGain; r++ {
		target *= 2
	}
	if target > capGain {
		target = capGain
	}
	if target < b.cfg.MinCwnd {
		target = b.cfg.MinCwnd
	}
	return target
}

// recomputeControl updates cwnd and the pacing rate after every event.
func (b *BBR) recomputeControl(now int64) {
	_ = now
	switch b.state {
	case ProbeRTT:
		// min(0.5 * BDP, max(4 MSS, ...)): drain to at most half a BDP but
		// never below the 4-packet floor.
		cwnd := scaleInt(b.BDP(), b.cfg.ProbeRTTCwndFraction)
		if cwnd < b.cfg.MinCwnd {
			cwnd = b.cfg.MinCwnd
		}
		b.cwnd = cwnd

	case Startup:
		b.cwnd = b.startupCwnd()

	case Drain:
		b.cwnd = maxInt(b.cfg.MinCwnd, scaleInt(b.BDP(), b.cfg.DrainGain))

	case ProbeBW:
		b.cwnd = b.probeBwCwnd()
	}

	gain := b.pacingGain()
	bw := b.operatingBw()
	if bw > 0 {
		b.pacingRate = scaleU64(bw, gain)
	}
}

// probeBwCwnd applies the per-sub-phase flight-size caps. inflight_lo (the
// beta-decayed short-term bound) and inflight_hi (with 15% headroom) both act
// as ceilings; the UP phase may exceed them by its exponential probe allowance.
func (b *BBR) probeBwCwnd() int {
	bdp := b.BDP()
	base := bdp
	if base == 0 {
		base = b.cfg.InitialCwnd
	}

	var cwnd int
	switch b.phase {
	case ProbeUp:
		// 1.25x probing, capped at inflight_hi plus the probe allowance
		// (1, 2, 4, ... packets per round).
		cwnd = scaleInt(base, b.cfg.UpGain)
		if b.inflightHi > 0 {
			capHi := b.inflightHi + b.probePackets
			if cwnd > capHi {
				cwnd = capHi
			}
		}
		if b.inflightLo > 0 && cwnd > b.inflightLo {
			cwnd = b.inflightLo
		}
	case ProbeDown:
		cwnd = scaleInt(base, b.cfg.DownGain)
		cwnd = b.applyBounds(cwnd)
	case ProbeCruise:
		// Steady state capped at inflight_hi * (1 - headroom).
		cwnd = base
		if b.inflightHi > 0 {
			ceiling := scaleInt(b.inflightHi, 1.0-b.cfg.Headroom)
			if cwnd > ceiling {
				cwnd = ceiling
			}
		}
		cwnd = b.applyBounds(cwnd)
	default: // REFILL: refill toward inflight_hi over one RTT.
		cwnd = base
		if b.inflightHi > 0 && cwnd > b.inflightHi {
			cwnd = b.inflightHi
		}
		cwnd = b.applyBounds(cwnd)
	}

	if cwnd < b.cfg.MinCwnd {
		cwnd = b.cfg.MinCwnd
	}
	return cwnd
}

// applyBounds intersects a target window with the dual-bound ceilings.
func (b *BBR) applyBounds(target int) int {
	if b.inflightHi > 0 && target > b.inflightHi {
		target = b.inflightHi
	}
	if b.inflightLo > 0 && target > b.inflightLo {
		target = b.inflightLo
	}
	return target
}

// pacingGain returns the pacing gain of the active state/sub-phase.
func (b *BBR) pacingGain() float64 {
	switch b.state {
	case Startup:
		return b.cfg.StartupGain
	case Drain:
		return b.cfg.DrainGain
	case ProbeRTT:
		return b.cfg.CruiseGain
	case ProbeBW:
		switch b.phase {
		case ProbeDown:
			return b.cfg.DownGain
		case ProbeUp:
			return b.cfg.UpGain
		default: // CRUISE / REFILL
			return b.cfg.CruiseGain
		}
	default:
		return 1.0
	}
}
