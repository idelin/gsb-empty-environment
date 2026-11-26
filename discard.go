package recovery

// DiscardSpace abandons all state for a packet-number space, as when Initial
// or Handshake keys are discarded. Discarded packets reduce in-flight bytes
// but are not reported as newly lost.
func (m *Machine) DiscardSpace(time int64, space Space) Result {
	if err := m.begin(time); err != nil {
		return m.errResult(err)
	}
	if !space.Valid() {
		return m.errResult(ErrInvalidSpace)
	}

	state := &m.spaces[space]
	for _, pkt := range state.packets {
		if pkt.outstanding {
			pkt.outstanding = false
			m.bytesInFlight -= pkt.bytes
			if pkt.ackEliciting {
				state.ackElicitingInFlight--
			}
		}
	}
	state.packets = make(map[int64]*sentPacket)
	state.largestAcked = -1
	state.lossTime = 0
	state.lastAckElicitingSentAt = -1
	state.ackElicitingInFlight = 0

	m.timerManaged = true
	m.resetTimer()
	return m.result([]LostPacket{}, space)
}

// DiscardKeys is an RFC-vocabulary alias for DiscardSpace.
func (m *Machine) DiscardKeys(time int64, space Space) Result {
	return m.DiscardSpace(time, space)
}
