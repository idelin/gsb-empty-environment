package rfc9002

import "time"

func optionalNow(now []time.Time) time.Time {
	if len(now) > 0 {
		return now[0]
	}
	return time.Time{}
}

func ptoBackoffFactor(count int) int64 {
	if count < 0 {
		count = 0
	}
	if count > 30 {
		count = 30
	}
	return int64(1) << uint(count)
}

// GetLossTimeAndSpace returns the earliest loss_time across the three spaces
// (RFC 9002 A.8). A zero deadline means no time-threshold loss timer is set.
func (ld *LossDetector) GetLossTimeAndSpace() (time.Time, PacketNumberSpace) {
	ld.ensure()
	var earliest time.Time
	space := Initial
	for _, sp := range AllSpaces {
		lt := ld.Spaces[sp].LossTime
		if lt.IsZero() {
			continue
		}
		if earliest.IsZero() || lt.Before(earliest) {
			earliest = lt
			space = sp
		}
	}
	return earliest, space
}

// lastOutstandingAckElicitingSentAt returns the PTO anchor for a space: the
// send time of the most recent outstanding ack-eliciting packet.
func (ld *LossDetector) lastOutstandingAckElicitingSentAt(space PacketNumberSpace) time.Time {
	var latest time.Time
	for _, p := range ld.Spaces[space].SentPackets {
		if p == nil || p.Status != StatusSent || !p.AckEliciting {
			continue
		}
		if latest.IsZero() || p.SentAt.After(latest) {
			latest = p.SentAt
		}
	}
	return latest
}

// GetPtoTimeAndSpace computes the earliest PTO deadline across eligible
// spaces (RFC 9002 A.8). Initial/Handshake never add max_ack_delay.
// Application Data is only considered after handshake confirmation and does
// add max_ack_delay. A zero deadline means no PTO should be armed (for
// example when only pure ACKs are outstanding).
func (ld *LossDetector) GetPtoTimeAndSpace() (time.Time, PacketNumberSpace) {
	ld.ensure()
	factor := ptoBackoffFactor(ld.PTOCount)
	basePTO := time.Duration(int64(ld.ptoBase()) * factor)

	var ptoTime time.Time
	ptoSpace := Initial
	found := false

	for _, sp := range AllSpaces {
		st := &ld.Spaces[sp]
		if st.KeysDiscarded {
			continue
		}
		// Skip Application Data until the handshake is confirmed.
		if sp == ApplicationData && !ld.HandshakeConfirmed {
			continue
		}
		anchor := ld.lastOutstandingAckElicitingSentAt(sp)
		if anchor.IsZero() {
			continue
		}
		duration := basePTO
		if sp == ApplicationData {
			duration += time.Duration(int64(ld.Config.effectiveMaxAckDelay()) * factor)
		}
		deadline := anchor.Add(duration)
		if !found || deadline.Before(ptoTime) {
			found = true
			ptoTime = deadline
			ptoSpace = sp
		}
	}

	if !found {
		return time.Time{}, Initial
	}
	return ptoTime, ptoSpace
}

// GetEffectivePtoTimeAndSpace applies the anti-amplification gate to the raw
// RFC PTO calculation. When blocked it returns a zero deadline and blocked=true.
func (ld *LossDetector) GetEffectivePtoTimeAndSpace(_ time.Time) (deadline time.Time, space PacketNumberSpace, blocked bool) {
	ld.ensure()
	if ld.AntiAmplificationLimited() {
		return time.Time{}, Initial, true
	}
	d, s := ld.GetPtoTimeAndSpace()
	return d, s, false
}

// AntiAmplificationLimited reports the RFC 9000 Section 8.1 server condition:
// the peer address is not validated and sent bytes have reached
// factor * received bytes (factor defaults to 3). A fresh endpoint that has
// not received anything is not reported as actively limited.
func (ld *LossDetector) AntiAmplificationLimited() bool {
	ld.ensure()
	if ld.AddressValidated {
		return false
	}
	if !ld.IsServer && !ld.EnforceAmplification {
		return false
	}
	if ld.BytesReceived <= 0 {
		return false
	}
	return ld.BytesSent >= ld.AntiAmplificationFactor*ld.BytesReceived
}

// IsAmplificationLimited is an alias for AntiAmplificationLimited.
func (ld *LossDetector) IsAmplificationLimited() bool { return ld.AntiAmplificationLimited() }

// AtAntiAmplificationLimit is an alias for AntiAmplificationLimited.
func (ld *LossDetector) AtAntiAmplificationLimit() bool { return ld.AntiAmplificationLimited() }

// IsPTOBlockedByAmplification reports why the unified timer may be disarmed.
func (ld *LossDetector) IsPTOBlockedByAmplification() bool { return ld.AntiAmplificationLimited() }

func (ld *LossDetector) timerState(_ time.Time) TimerState {
	ld.ensure()
	if deadline, space := ld.GetLossTimeAndSpace(); !deadline.IsZero() {
		return TimerState{
			Armed:    true,
			Kind:     TimerLoss,
			Deadline: deadline,
			Space:    space,
			PTOCount: ld.PTOCount,
		}
	}

	if ld.AntiAmplificationLimited() {
		// At the anti-amplification limit the server cannot send probes, so
		// the PTO timer must be disabled rather than fire in a loop.
		return TimerState{
			Armed:                false,
			Kind:                 TimerNone,
			PTOCount:             ld.PTOCount,
			AmplificationBlocked: true,
		}
	}

	if deadline, space := ld.GetPtoTimeAndSpace(); !deadline.IsZero() {
		return TimerState{
			Armed:    true,
			Kind:     TimerPTO,
			Deadline: deadline,
			Space:    space,
			PTOCount: ld.PTOCount,
		}
	}

	return TimerState{Armed: false, Kind: TimerNone, PTOCount: ld.PTOCount}
}

// SetLossDetectionTimer resolves and caches the unified single timer
// (RFC 9002 A.8): loss deadline wins, otherwise the PTO deadline, unless the
// server is anti-amplification blocked (timer disabled).
func (ld *LossDetector) SetLossDetectionTimer(now time.Time) TimerState {
	ld.Timer = ld.timerState(now)
	return ld.Timer
}

func (ld *LossDetector) refreshTimer(now time.Time) TimerState {
	return ld.SetLossDetectionTimer(now)
}

// NextDeadline returns the single next timer deadline and its kind/space.
// armed is false when no timer should run.
func (ld *LossDetector) NextDeadline(now time.Time) (deadline time.Time, kind TimerKind, space PacketNumberSpace, armed bool) {
	ts := ld.SetLossDetectionTimer(now)
	if !ts.Armed {
		return time.Time{}, ts.Kind, ts.Space, false
	}
	return ts.Deadline, ts.Kind, ts.Space, true
}

// TimerDeadline is a convenience returning the armed deadline and ok.
func (ld *LossDetector) TimerDeadline(now time.Time) (time.Time, bool) {
	ts := ld.SetLossDetectionTimer(now)
	return ts.Deadline, ts.Armed
}

// OnLossDetectionTimeout advances the state machine when the unified timer
// expires (RFC 9002 A.9). A loss deadline runs time-threshold detection;
// otherwise PTO emits a probe intent and backs off. An anti-amplification
// block is a no-op that leaves the timer disabled, preventing a PTO loop.
func (ld *LossDetector) OnLossDetectionTimeout(now time.Time) *TimeoutOutcome {
	ld.ensure()
	out := &TimeoutOutcome{Now: now}

	if deadline, space := ld.GetLossTimeAndSpace(); !deadline.IsZero() {
		out.Kind = TimerLoss
		out.Space = space
		out.Lost = ld.DetectLostPackets(space, now)
		ld.refreshTimer(now)
		out.Timer = ld.Timer
		return out
	}

	if ld.AntiAmplificationLimited() {
		ld.refreshTimer(now)
		out.Kind = TimerNone
		out.AmplificationBlocked = true
		out.Timer = ld.Timer
		return out
	}

	deadline, space := ld.GetPtoTimeAndSpace()
	if deadline.IsZero() {
		ld.refreshTimer(now)
		out.Kind = TimerNone
		out.Timer = ld.Timer
		return out
	}

	ld.PTOExpirations++
	out.PTOFired = true
	out.Kind = TimerPTO
	out.Space = space
	out.ProbeSpace = space
	out.ProbePackets = ld.PTOProbePackets

	ld.PTOCount++
	out.PTOCount = ld.PTOCount

	ld.refreshTimer(now)
	out.Timer = ld.Timer
	return out
}

// RecordBytesSent adds wire bytes sent for anti-amplification accounting.
func (ld *LossDetector) RecordBytesSent(n int) TimerState {
	ld.ensure()
	if n > 0 {
		ld.BytesSent += n
	}
	return ld.refreshTimer(time.Time{})
}

// RecordBytesReceived adds bytes received and re-evaluates the unified timer.
// A new client packet that lifts the 3x limit re-arms the PTO timer.
func (ld *LossDetector) RecordBytesReceived(n int, now ...time.Time) TimerState {
	ld.ensure()
	if n > 0 {
		ld.BytesReceived += n
	}
	return ld.refreshTimer(optionalNow(now))
}

// OnDatagramSent records a sent datagram size.
func (ld *LossDetector) OnDatagramSent(size int) TimerState {
	return ld.RecordBytesSent(size)
}

// OnDatagramReceived records a received datagram size and re-arms the timer.
func (ld *LossDetector) OnDatagramReceived(size int, now ...time.Time) TimerState {
	return ld.RecordBytesReceived(size, now...)
}

// ReceiveClientPacket is the semantic anti-amplification release hook: the
// server received a new client packet, so byte credit increases and the timer
// is reset/re-armed if the limit no longer applies.
func (ld *LossDetector) ReceiveClientPacket(size int, now ...time.Time) TimerState {
	return ld.RecordBytesReceived(size, now...)
}

// ValidateAddress marks the peer address validated, permanently clearing
// anti-amplification limiting, and resets the timer.
func (ld *LossDetector) ValidateAddress(now ...time.Time) TimerState {
	ld.ensure()
	ld.AddressValidated = true
	return ld.refreshTimer(optionalNow(now))
}

// SetHandshakeConfirmed updates the handshake-confirmation latch and timer.
func (ld *LossDetector) SetHandshakeConfirmed(v bool, now ...time.Time) TimerState {
	ld.ensure()
	ld.HandshakeConfirmed = v
	return ld.refreshTimer(optionalNow(now))
}

// ConfirmHandshake marks the handshake confirmed (enables max_ack_delay
// subtraction and Application Data PTO) and recomputes the timer.
func (ld *LossDetector) ConfirmHandshake(now ...time.Time) TimerState {
	return ld.SetHandshakeConfirmed(true, now...)
}

// DiscardKeys drops Initial or Handshake state (RFC 9002 / TLS key discard):
// it removes the space's in-flight bytes, clears its loss timer/table and
// resets pto_count, then recomputes the unified timer.
func (ld *LossDetector) DiscardKeys(space PacketNumberSpace, now ...time.Time) *DiscardOutcome {
	ld.ensure()
	t := optionalNow(now)
	out := &DiscardOutcome{Space: space}
	st := &ld.Spaces[space]

	if space != Initial && space != Handshake {
		out.InFlightRemaining = st.InFlightBytes
		out.Timer = ld.timerState(t)
		return out
	}
	if st.KeysDiscarded {
		out.Discarded = false
		out.InFlightRemaining = 0
		out.Timer = ld.timerState(t)
		return out
	}

	for _, p := range st.SentPackets {
		out.PacketsRemoved++
		if p != nil && p.BytesAccounted {
			out.BytesRemoved += p.Size
			ld.removeInFlight(p)
		}
	}

	st.SentPackets = make(map[PacketNumber]*SentPacket)
	st.InFlightBytes = 0
	st.LossTime = time.Time{}
	st.LargestAcked = 0
	st.LargestAckedValid = false
	st.LastAckElicitingSentAt = time.Time{}
	st.NextPacketNumber = 0
	st.KeysDiscarded = true

	if ld.PTOCount != 0 {
		ld.PTOCount = 0
		out.PTOCountReset = true
	}

	ld.refreshTimer(t)
	out.Timer = ld.Timer
	out.Discarded = true
	out.InFlightRemaining = st.InFlightBytes
	return out
}
