package rfc9002

import "time"

// LossDelay is the time threshold:
// max(kTimeThreshold * max(latest_rtt, smoothed_rtt), kGranularity).
func (ld *LossDetector) LossDelay() time.Duration {
	ld.ensure()
	rtt := ld.LatestRTT
	if ld.SmoothedRTT > rtt {
		rtt = ld.SmoothedRTT
	}
	d := time.Duration(ld.TimeThreshold * float64(rtt))
	if d < ld.Granularity {
		d = ld.Granularity
	}
	return d
}

// DetectLostPackets runs packet-threshold (kPacketThreshold = 3) and
// time-threshold detection for one space (RFC 9002 A.10). It updates
// loss_time[space], removes lost bytes from in-flight accounting and invokes
// congestion / persistent-congestion handling.
func (ld *LossDetector) DetectLostPackets(space PacketNumberSpace, now time.Time) []LostPacket {
	ld.ensure()
	st := &ld.Spaces[space]
	if st.KeysDiscarded || !st.LargestAckedValid {
		st.LossTime = time.Time{}
		return nil
	}

	largest := st.LargestAcked
	lossDelay := ld.LossDelay()
	threshold := ld.PacketThreshold

	var lost []LostPacket
	var earliestDeadline time.Time

	for _, p := range ld.sortedSentPackets(space) {
		if p.Status != StatusSent {
			continue
		}

		packetThresholdLost := largest >= threshold && p.Number <= largest-threshold
		timeThresholdLost := now.Sub(p.SentAt) >= lossDelay

		var reason LossReason
		switch {
		case packetThresholdLost:
			reason = LossReasonPacketThreshold
		case timeThresholdLost:
			reason = LossReasonTimeThreshold
		}

		if reason != LossReasonNone {
			p.Status = StatusLost
			p.LostAt = now
			p.LossReason = reason
			lp := LostPacket{
				Packet: p,
				Number: p.Number,
				Space:  space,
				Size:   p.Size,
				SentAt: p.SentAt,
				Reason: reason,
			}
			ld.removeInFlight(p)
			lost = append(lost, lp)
			continue
		}

		deadline := p.SentAt.Add(lossDelay)
		if earliestDeadline.IsZero() || deadline.Before(earliestDeadline) {
			earliestDeadline = deadline
		}
	}

	st.LossTime = earliestDeadline

	if len(lost) > 0 {
		ld.onPacketsLost(lost, now)
	}
	return lost
}

func (ld *LossDetector) onPacketsLost(lost []LostPacket, now time.Time) {
	space := lost[0].Space
	if !ld.DisableAutomaticPersistentCongestion && ld.DetectPersistentCongestion(space) {
		ld.OnPersistentCongestion(now)
		return
	}
	// Use the most recently sent lost packet as the epoch trigger.
	var triggerSentAt time.Time
	for _, lp := range lost {
		if lp.SentAt.After(triggerSentAt) {
			triggerSentAt = lp.SentAt
		}
	}
	ld.applyCongestionEvent(now, triggerSentAt)
}

// DetectPersistentCongestion implements the quantitative RFC 9002 7.6.2
// test: two lost ack-eliciting packets whose send times are at least
// PersistentCongestionDuration apart with nothing acknowledged between them.
// It scans retained packet state for the space.
func (ld *LossDetector) DetectPersistentCongestion(space PacketNumberSpace) bool {
	ld.ensure()
	st := &ld.Spaces[space]
	if st.KeysDiscarded {
		return false
	}
	period := ld.PersistentCongestionDuration()

	lost := make([]*SentPacket, 0)
	for _, p := range ld.sortedSentPackets(space) {
		if p.Status == StatusLost && p.AckEliciting {
			lost = append(lost, p)
		}
	}

	for i := 0; i < len(lost); i++ {
		for j := i + 1; j < len(lost); j++ {
			lo, hi := lost[i], lost[j]
			if hi.SentAt.Sub(lo.SentAt) < period {
				continue
			}
			if ld.nothingAckedBetween(space, lo.Number, hi.Number) {
				return true
			}
		}
	}
	return false
}

// nothingAckedBetween reports whether every tracked packet within [lo, hi] is
// lost (and none acknowledged / still outstanding). Untracked numbers do not
// invalidate the interval.
func (ld *LossDetector) nothingAckedBetween(space PacketNumberSpace, lo, hi PacketNumber) bool {
	st := &ld.Spaces[space]
	for n, p := range st.SentPackets {
		if n < lo || n > hi || p == nil {
			continue
		}
		if p.Status == StatusAcked || p.Status == StatusSent {
			return false
		}
	}
	return true
}
