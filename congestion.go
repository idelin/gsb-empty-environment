package rfc9002

import "time"

// IsInRecovery reports whether a recovery epoch is currently open.
func (ld *LossDetector) IsInRecovery() bool { return ld.InRecovery }

// InCongestionRecovery is the RFC B.3 predicate: a packet belongs to the
// current recovery epoch when it was sent at or before recovery_start_time.
func (ld *LossDetector) InCongestionRecovery(sentAt time.Time) bool {
	return ld.InRecovery && !sentAt.After(ld.RecoveryStartTime)
}

// EndRecovery closes the current recovery epoch. The next loss/ECN event then
// performs another window halving. It returns whether an epoch was open.
func (ld *LossDetector) EndRecovery() bool {
	was := ld.InRecovery
	ld.InRecovery = false
	ld.RecoveryStartTime = time.Time{}
	return was
}

// ExitRecovery is an alias for EndRecovery.
func (ld *LossDetector) ExitRecovery() bool { return ld.EndRecovery() }

// CompleteRecovery is an alias for EndRecovery.
func (ld *LossDetector) CompleteRecovery() bool { return ld.EndRecovery() }

// applyCongestionEvent performs at most one multiplicative decrease per
// recovery epoch. triggerSentAt is the send time of the packet that triggered
// the event; a zero time means "no specific packet". Per RFC 9002 B.5 a
// trigger packet sent after the epoch start may open a new epoch, while any
// event belonging to the open epoch is suppressed.
//
// It returns true when the congestion window was actually halved.
func (ld *LossDetector) applyCongestionEvent(now time.Time, triggerSentAt time.Time) bool {
	ld.ensure()
	if ld.InRecovery {
		if triggerSentAt.IsZero() || !triggerSentAt.After(ld.RecoveryStartTime) {
			ld.SuppressedCongestionEvents++
			return false
		}
		// Triggering packet was sent after the previous epoch start:
		// this starts a fresh epoch.
	}

	ld.InRecovery = true
	ld.RecoveryStartTime = now
	ld.RecoveryEpoch++

	decreased := int(float64(ld.CongestionWindow) * ld.LossReductionFactor)
	if decreased < ld.MinimumWindow {
		decreased = ld.MinimumWindow
	}
	ld.CongestionWindow = decreased
	ld.SlowStartThreshold = decreased
	ld.CongestionReductions++
	ld.LastCongestionReductionAt = now
	return true
}

// CongestionEvent is the parameter-less entry point for a loss/ECN signal at
// now. It halves at most once per open recovery epoch and reports whether it
// actually reduced the window.
func (ld *LossDetector) CongestionEvent(now time.Time) bool {
	return ld.applyCongestionEvent(now, time.Time{})
}

// OnLossOrECN is an alias for CongestionEvent.
func (ld *LossDetector) OnLossOrECN(now time.Time) bool { return ld.CongestionEvent(now) }

// OnECNCE processes an ECN-CE counter increase. Each positive increment is a
// congestion signal equivalent to loss; the window is halved at most once per
// recovery epoch. It reports whether the window was halved.
func (ld *LossDetector) OnECNCE(delta uint64, now time.Time) bool {
	ld.ensure()
	if delta == 0 {
		return false
	}
	ld.ECNCEIncrements += delta
	ld.ECNCEEvents++
	return ld.applyCongestionEvent(now, time.Time{})
}

// OnECNCECount ingests an absolute ECN-CE counter value (as carried in an ACK
// frame), diffing it against the last observed value.
func (ld *LossDetector) OnECNCECount(counter uint64, now time.Time) bool {
	ld.ensure()
	var delta uint64
	if counter > ld.LastECNCECount {
		delta = counter - ld.LastECNCECount
	}
	ld.LastECNCECount = counter
	if delta == 0 {
		return false
	}
	return ld.OnECNCE(delta, now)
}

// OnPersistentCongestion resets the congestion window to kMinimumWindow
// (RFC 9002 Section 7.6.2 / B.7) and opens a recovery epoch. After this, ACKs
// grow the window in slow start because cwnd <= ssthresh.
func (ld *LossDetector) OnPersistentCongestion(now time.Time) bool {
	ld.ensure()
	ld.PersistentCongestion = true
	ld.PersistentCongestionCount++
	ld.LastPersistentCongestionAt = now
	ld.CongestionWindow = ld.MinimumWindow
	if ld.SlowStartThreshold < ld.MinimumWindow {
		ld.SlowStartThreshold = ld.MinimumWindow
	}
	// Collapse any open epoch and start a fresh one so stale loss/ECN
	// signals cannot immediately decrease the window again.
	ld.InRecovery = true
	ld.RecoveryStartTime = now
	ld.RecoveryEpoch++
	return true
}

// MarkPersistentCongestion is an alias for OnPersistentCongestion.
func (ld *LossDetector) MarkPersistentCongestion(now time.Time) bool {
	return ld.OnPersistentCongestion(now)
}

// DeclarePersistentCongestion is an alias for OnPersistentCongestion.
func (ld *LossDetector) DeclarePersistentCongestion(now time.Time) bool {
	return ld.OnPersistentCongestion(now)
}

// growWindowOnAckedPacket applies RFC B.4 congestion window growth for a
// newly acknowledged packet that was still in flight.
func (ld *LossDetector) growWindowOnAckedPacket(size int, sentAt time.Time) {
	if size <= 0 {
		return
	}
	// No growth for packets belonging to the current recovery epoch.
	if ld.InCongestionRecovery(sentAt) {
		return
	}
	mss := ld.MaxDatagramSize
	if ld.CongestionWindow < ld.SlowStartThreshold {
		// Slow start: exponential growth (one MSS per ACKed MSS-ish bytes).
		ld.CongestionWindow += size
		return
	}
	// Congestion avoidance: cwnd += MSS * bytes_acked / cwnd.
	if ld.CongestionWindow > 0 {
		ld.CongestionWindow += mss * size / ld.CongestionWindow
	}
}
