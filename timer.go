package recovery

import "math"

func (m *Machine) resetTimer() {
	if !m.timerManaged {
		m.timer = Timer{Kind: NoTimer, Deadline: -1}
		return
	}

	best := Timer{Kind: NoTimer, Deadline: -1}
	consider := func(candidate Timer) {
		if candidate.Deadline <= 0 {
			return
		}
		if best.Kind == NoTimer || candidate.Deadline < best.Deadline {
			best = candidate
		}
	}

	for space := Initial; space <= ApplicationData; space++ {
		if lossTime := m.spaces[space].lossTime; lossTime > 0 {
			consider(Timer{Kind: LossTimer, Deadline: lossTime, Space: space})
		}
	}

	var ptoBest Timer
	if !m.antiAmplificationLimited {
		for space := Initial; space <= ApplicationData; space++ {
			if space == ApplicationData && !m.handshakeConfirmed {
				continue
			}
			state := &m.spaces[space]
			if state.ackElicitingInFlight == 0 {
				continue
			}
			if state.lastAckElicitingSentAt < 0 {
				continue
			}
			period, factor := m.ptoPeriod(space)
			deadline := saturatingAdd(state.lastAckElicitingSentAt, period)
			candidate := Timer{
				Kind:          PTOTimer,
				Deadline:      deadline,
				Space:         space,
				PTOCount:      m.ptoCount,
				BackoffFactor: factor,
			}
			if ptoBest.Kind == NoTimer || candidate.Deadline < ptoBest.Deadline {
				ptoBest = candidate
			}
		}

		if ptoBest.Kind == NoTimer {
			// RFC 9002 arms a handshake/deadlock-recovery PTO when no
			// ack-eliciting packet remains in flight and address validation
			// has not completed. The same fallback covers ack-eliciting
			// 0-RTT/1-RTT packets that must be ignored before handshake
			// confirmation.
			noEligibleHandshakePTO := m.ackElicitingInFlightTotal() == 0 || !m.handshakeConfirmed
			if noEligibleHandshakePTO && !m.peerValidationComplete() {
				space, basis := m.noInFlightPTOBasis()
				period, factor := m.ptoPeriod(space)
				ptoBest = Timer{
					Kind:          PTOTimer,
					Deadline:      saturatingAdd(basis, period),
					Space:         space,
					PTOCount:      m.ptoCount,
					BackoffFactor: factor,
				}
			}
		}
	}
	if ptoBest.Kind != NoTimer {
		consider(ptoBest)
	}

	m.timer = best
}

func (m *Machine) fireDueTimers(now int64) []LostPacket {
	var lost []LostPacket

	for iteration := 0; iteration < 64; iteration++ {
		if !m.timer.Armed() || m.timer.Deadline > now {
			break
		}

		if m.timer.Kind == LossTimer {
			space := m.timer.Space
			more := m.detectLostPackets(space, now)
			lost = append(lost, more...)
			if len(more) == 0 {
				m.spaces[space].lossTime = 0
			}
		} else {
			space := m.timer.Space
			m.ptoCount++
			spaceCopy := space
			m.lastPTOFired = &PacketRef{Space: spaceCopy}
		}

		m.resetTimer()
	}

	return lost
}

func (m *Machine) ptoPeriod(space Space) (period int64, factor int64) {
	base := PTOBaseDuration(m.smoothedRTT, m.rttVar, m.maxACKDelay, space)

	shift := m.ptoCount
	if shift > 62 {
		shift = 62
	}
	factor = int64(1) << shift
	if shift > 0 && base > math.MaxInt64>>shift {
		return math.MaxInt64, factor
	}
	return base << shift, factor
}

func (m *Machine) ackElicitingInFlightTotal() int64 {
	var total int64
	for space := Initial; space <= ApplicationData; space++ {
		total += m.spaces[space].ackElicitingInFlight
	}
	return total
}

func (m *Machine) peerValidationComplete() bool {
	if m.peerValidationSet {
		return m.peerCompletedAddressValidation
	}
	return m.handshakeConfirmed
}

func (m *Machine) noInFlightPTOBasis() (Space, int64) {
	space := Initial
	basis := m.now
	found := false
	for candidate := Initial; candidate <= Handshake; candidate++ {
		lastSent := m.spaces[candidate].lastAckElicitingSentAt
		if lastSent < 0 {
			continue
		}
		if !found || lastSent < basis {
			space = candidate
			basis = lastSent
			found = true
		}
	}
	if !found {
		basis = m.now
	}
	return space, basis
}

func saturatingAdd(a, b int64) int64 {
	if b > 0 && a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}
