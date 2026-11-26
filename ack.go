package rfc9002

import "time"

// OnAckReceived processes an ACK frame for one space (RFC 9002 A.7): it finds
// newly acknowledged packets, updates RTT only when the largest acknowledged
// packet is newly acked and the ACK newly acks an ack-eliciting packet, runs
// congestion-window growth, ECN handling and loss detection, and finally
// recomputes the unified timer.
func (ld *LossDetector) OnAckReceived(ack AckFrame, now time.Time) *AckOutcome {
	ld.ensure()
	out := &AckOutcome{Space: ack.Space}
	if !ack.Space.Valid() {
		return out
	}
	st := &ld.Spaces[ack.Space]
	if st.KeysDiscarded {
		out.Timer = ld.timerState(now)
		return out
	}

	// Largest acknowledged bookkeeping.
	if !st.LargestAckedValid || ack.LargestAcked > st.LargestAcked {
		st.LargestAcked = ack.LargestAcked
		st.LargestAckedValid = true
	}

	targets := ld.enumerateAckTargets(&ack, st)

	newlyAcked := make([]*SentPacket, 0, len(targets))
	largestIsNew := false
	anyNewAckEliciting := false

	for number := range targets {
		p, ok := st.SentPackets[number]
		if !ok || p == nil {
			continue
		}
		if p.Status == StatusAcked {
			continue
		}
		// Newly acknowledged (including a previously declared-lost packet,
		// which becomes a spurious recovery).
		p.Status = StatusAcked
		p.AckedAt = now
		ld.removeInFlight(p)
		newlyAcked = append(newlyAcked, p)
		out.NewlyAcked = append(out.NewlyAcked, number)
		if number == ack.LargestAcked {
			largestIsNew = true
		}
		if p.AckEliciting {
			anyNewAckEliciting = true
		}
		// Congestion-window growth only for packets that were genuinely
		// outstanding and not already removed as lost.
		if p.LossReason == LossReasonNone {
			ld.growWindowOnAckedPacket(p.Size, p.SentAt)
		}
	}

	// RTT sample: largest acked must be newly acked AND the ACK must newly
	// ack at least one ack-eliciting packet.
	if largestIsNew && anyNewAckEliciting {
		if largest, ok := st.SentPackets[ack.LargestAcked]; ok && largest != nil {
			latest := now.Sub(largest.SentAt)
			if latest >= 0 {
				subtractAckDelay := ack.Space == ApplicationData && ld.HandshakeConfirmed
				ld.RTTEstimator.Update(latest, ack.AckDelay, ld.Config.effectiveMaxAckDelay(), subtractAckDelay)
				out.RTTUpdated = true
			}
		}
	}

	// Any forward progress resets PTO backoff.
	if len(out.NewlyAcked) > 0 && ld.PTOCount != 0 {
		ld.PTOCount = 0
		out.PTOCountReset = true
	}

	// ECN-CE growth is a congestion signal equivalent to loss.
	reductionsBefore := ld.CongestionReductions
	pcBefore := ld.PersistentCongestionCount
	if ack.ECNCEDelta > 0 {
		ld.OnECNCE(ack.ECNCEDelta, now)
		out.ECNCEIncreased = true
	} else if ack.ECNCECount > ld.LastECNCECount {
		out.ECNCEIncreased = true
		ld.OnECNCECount(ack.ECNCECount, now)
	}

	// Packet/time threshold loss detection.
	out.Lost = ld.DetectLostPackets(ack.Space, now)
	if ld.CongestionReductions > reductionsBefore {
		out.CongestionReduced = true
	}
	out.PersistentCongestion = ld.PersistentCongestionCount > pcBefore

	ld.refreshTimer(now)
	out.Timer = ld.Timer
	return out
}

// AckOne acknowledges exactly one packet number in a space.
func (ld *LossDetector) AckOne(space PacketNumberSpace, number PacketNumber, now time.Time) *AckOutcome {
	return ld.OnAckReceived(AckFrame{
		Space:        space,
		LargestAcked: number,
		Numbers:      []PacketNumber{number},
	}, now)
}

// AckNumbers acknowledges an explicit set of packet numbers; largest is
// derived as the maximum unless supplied.
func (ld *LossDetector) AckNumbers(space PacketNumberSpace, numbers []PacketNumber, now time.Time) *AckOutcome {
	var largest PacketNumber
	first := true
	for _, n := range numbers {
		if first || n > largest {
			largest = n
			first = false
		}
	}
	return ld.OnAckReceived(AckFrame{
		Space:        space,
		LargestAcked: largest,
		Numbers:      numbers,
	}, now)
}

// AckUpTo cumulatively acknowledges every outstanding packet <= largest.
func (ld *LossDetector) AckUpTo(space PacketNumberSpace, largest PacketNumber, now time.Time) *AckOutcome {
	return ld.OnAckReceived(AckFrame{
		Space:        space,
		LargestAcked: largest,
		Cumulative:   true,
	}, now)
}

func (ld *LossDetector) enumerateAckTargets(ack *AckFrame, st *PacketNumberSpaceState) map[PacketNumber]struct{} {
	targets := make(map[PacketNumber]struct{})

	if ack.Cumulative {
		for n := range st.SentPackets {
			if n <= ack.LargestAcked {
				targets[n] = struct{}{}
			}
		}
	}

	for _, n := range ack.Numbers {
		targets[n] = struct{}{}
	}

	for _, r := range ack.Ranges {
		lo, hi := r.Smallest, r.Largest
		if lo > hi {
			lo, hi = hi, lo
		}
		// Guard against pathological ranges in tests.
		if hi-lo > 1_000_000 {
			for n := range st.SentPackets {
				if n >= lo && n <= hi {
					targets[n] = struct{}{}
				}
			}
			continue
		}
		for n := lo; n <= hi; n++ {
			targets[n] = struct{}{}
			if n == hi {
				break // avoid overflow wrap
			}
		}
	}

	if len(ack.Ranges) == 0 && len(ack.Numbers) == 0 && !ack.Cumulative {
		targets[ack.LargestAcked] = struct{}{}
	}
	// Largest Acknowledged is always part of an ACK frame's first range.
	targets[ack.LargestAcked] = struct{}{}
	return targets
}
