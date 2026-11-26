package rfc9002

import "time"

// RTTEstimator holds the four RFC 9002 RTT observables.
//
// The zero value is ready to use; SmoothedRTT == 0 means "no sample yet".
type RTTEstimator struct {
	// LatestRTT is latest_rtt from the most recent eligible ACK.
	LatestRTT time.Duration
	// MinRTT is min_rtt across all samples (ack delay ignored).
	MinRTT time.Duration
	// SmoothedRTT is smoothed_rtt.
	SmoothedRTT time.Duration
	// RTTVar is rttvar.
	RTTVar time.Duration

	// Samples counts applied RTT samples.
	Samples int
}

// HasSample reports whether at least one RTT sample was applied.
func (r *RTTEstimator) HasSample() bool { return r.Samples > 0 }

// Update applies one RTT sample following RFC 9002 Section 5.3 / A.6.
//
// ackDelay is the decoded peer ack delay. maxAckDelay is the peer transport
// limit. subtractAckDelay must be true only for Application Data after the
// handshake is confirmed. When subtraction is enabled the effective delay is
// capped by maxAckDelay and additionally bounded by latest-min_rtt so the
// adjusted RTT can never drop below min_rtt.
func (r *RTTEstimator) Update(latest time.Duration, ackDelay time.Duration, maxAckDelay time.Duration, subtractAckDelay bool) {
	r.LatestRTT = latest

	if r.SmoothedRTT == 0 || r.Samples == 0 {
		// First RTT sample.
		r.MinRTT = latest
		r.SmoothedRTT = latest
		r.RTTVar = latest / 2
		r.Samples = 1
		return
	}

	// min_rtt ignores acknowledgment delay.
	if latest < r.MinRTT || r.MinRTT == 0 {
		r.MinRTT = latest
	}

	adjusted := latest
	if subtractAckDelay {
		delay := ackDelay
		if delay < 0 {
			delay = 0
		}
		// Limit ack_delay by max_ack_delay after handshake confirmation.
		if maxAckDelay > 0 && delay > maxAckDelay {
			delay = maxAckDelay
		}
		// Bound the subtraction by (latest_rtt - min_rtt). This is the
		// explicit "do not adjust below min_rtt" plausibility rule.
		if bound := latest - r.MinRTT; bound >= 0 && delay > bound {
			delay = bound
		}
		if delay > 0 && delay <= latest {
			adjusted = latest - delay
		}
	}

	// rttvar = 3/4*rttvar + 1/4*|smoothed_rtt - adjusted_rtt|
	diff := r.SmoothedRTT - adjusted
	if diff < 0 {
		diff = -diff
	}
	r.RTTVar = (3*r.RTTVar + diff) / 4
	// smoothed_rtt = 7/8*smoothed_rtt + 1/8*adjusted_rtt
	r.SmoothedRTT = (7*r.SmoothedRTT + adjusted) / 8
	r.Samples++
}

// PTOBase returns smoothed_rtt (or kInitialRtt) plus max(4*rttvar, gran),
// without max_ack_delay and without PTO backoff.
func (ld *LossDetector) ptoBase() time.Duration {
	rtt := ld.SmoothedRTT
	if rtt == 0 {
		rtt = ld.InitialRTT
	}
	varianceTerm := 4 * ld.RTTVar
	if varianceTerm < ld.Granularity {
		varianceTerm = ld.Granularity
	}
	return rtt + varianceTerm
}

// PersistentCongestionDuration is
// (smoothed_rtt + max(4*rttvar, kGranularity) + max_ack_delay)
// * kPersistentCongestionThreshold (RFC 9002 Section 7.6.2).
func (ld *LossDetector) PersistentCongestionDuration() time.Duration {
	rtt := ld.SmoothedRTT
	if rtt == 0 {
		rtt = ld.InitialRTT
	}
	varianceTerm := 4 * ld.RTTVar
	if varianceTerm < ld.Granularity {
		varianceTerm = ld.Granularity
	}
	ptoLike := rtt + varianceTerm + ld.Config.effectiveMaxAckDelay()
	return ptoLike * time.Duration(ld.PersistentCongestionThreshold)
}
