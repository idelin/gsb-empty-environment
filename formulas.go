package recovery

// TimeThresholdDelay returns max(9/8 * max(smoothed_rtt, latest_rtt), 1ms)
// in microseconds.
func TimeThresholdDelay(smoothedRTT, latestRTT int64) int64 {
	reference := smoothedRTT
	if latestRTT > reference {
		reference = latestRTT
	}
	delay := (9 * reference) / 8
	if delay < TimerGranularity {
		delay = TimerGranularity
	}
	return delay
}

// ClampACKDelay applies the peer max_ack_delay cap.
func ClampACKDelay(ackDelay, maxACKDelay int64) int64 {
	if ackDelay < 0 {
		ackDelay = 0
	}
	if maxACKDelay < 0 {
		maxACKDelay = 0
	}
	if ackDelay > maxACKDelay {
		return maxACKDelay
	}
	return ackDelay
}

// ACKAdjustmentAllowed reports whether subtracting cappedDelay would leave an
// RTT sample no smaller than minRTT.
func ACKAdjustmentAllowed(latestRTT, minRTT, cappedDelay int64) bool {
	return latestRTT >= minRTT+cappedDelay
}

// AdjustRTTSample performs one RFC 9002/RFC 6298 update using microseconds.
// The caller is responsible for application-space and ack-eliciting checks.
func AdjustRTTSample(current RTTStats, latestRTT, ackDelay, maxACKDelay int64) RTTStats {
	next := current
	next.LatestRTT = latestRTT
	if !current.HasRTTSample {
		next.MinRTT = latestRTT
	} else if latestRTT < current.MinRTT {
		next.MinRTT = latestRTT
	}

	adjusted := latestRTT
	delay := ClampACKDelay(ackDelay, maxACKDelay)
	if ACKAdjustmentAllowed(latestRTT, next.MinRTT, delay) {
		adjusted = latestRTT - delay
	}

	if !current.HasRTTSample {
		next.HasRTTSample = true
		next.SmoothedRTT = adjusted
		next.RTTVar = adjusted / 2
		return next
	}

	diff := current.SmoothedRTT - adjusted
	if diff < 0 {
		diff = -diff
	}
	next.RTTVar = (3*current.RTTVar + diff) / 4
	next.SmoothedRTT = (7*current.SmoothedRTT + adjusted) / 8
	return next
}

// PTOBaseDuration returns the unbacked-off PTO duration in microseconds.
// ApplicationData includes maxACKDelay; Initial and Handshake do not.
func PTOBaseDuration(smoothedRTT, rttVar, maxACKDelay int64, space Space) int64 {
	varianceTerm := 4 * rttVar
	if varianceTerm < TimerGranularity {
		varianceTerm = TimerGranularity
	}
	pto := smoothedRTT + varianceTerm
	if space == ApplicationData {
		pto += maxACKDelay
	}
	return pto
}
