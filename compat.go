package recovery

// Recovery is a vocabulary alias for the RFC 9002 state machine.
type Recovery = Machine

// RecoveryStateMachine is a vocabulary alias for Machine.
type RecoveryStateMachine = Machine

// StateMachine is a vocabulary alias for Machine.
type StateMachine = Machine

// LossDetector is a vocabulary alias for Machine.
type LossDetector = Machine

// QuicRecovery is a vocabulary alias for Machine.
type QuicRecovery = Machine

// QUICRecovery is the idiomatic-capitalization alias for Machine.
type QUICRecovery = Machine

// NewRecovery creates an RFC 9002 machine.
func NewRecovery(maxACKDelayMicros ...int64) *Recovery { return New(maxACKDelayMicros...) }

// NewRecoveryStateMachine creates an RFC 9002 machine.
func NewRecoveryStateMachine(maxACKDelayMicros ...int64) *RecoveryStateMachine {
	return New(maxACKDelayMicros...)
}

// NewStateMachine creates an RFC 9002 machine.
func NewStateMachine(maxACKDelayMicros ...int64) *StateMachine { return New(maxACKDelayMicros...) }

// NewLossDetector creates an RFC 9002 machine.
func NewLossDetector(maxACKDelayMicros ...int64) *LossDetector { return New(maxACKDelayMicros...) }

// NewQuicRecovery creates an RFC 9002 machine.
func NewQuicRecovery(maxACKDelayMicros ...int64) *QuicRecovery { return New(maxACKDelayMicros...) }

// NewQUICRecovery creates an RFC 9002 machine.
func NewQUICRecovery(maxACKDelayMicros ...int64) *QUICRecovery { return New(maxACKDelayMicros...) }

// NewMachine is a concise constructor alias.
func NewMachine(maxACKDelayMicros ...int64) *Machine { return New(maxACKDelayMicros...) }

// RFC-named space aliases.
const (
	InitialSpace         = Initial
	HandshakeSpace       = Handshake
	ApplicationDataSpace = ApplicationData
	AppDataSpace         = ApplicationData
)

// SetMaxACKDelay updates the peer max_ack_delay in microseconds.
func (m *Machine) SetMaxACKDelay(maxACKDelayMicros int64) {
	if maxACKDelayMicros < 0 {
		maxACKDelayMicros = 0
	}
	m.maxACKDelay = maxACKDelayMicros
}

// HandleEvent is an alias for Handle.
func (m *Machine) HandleEvent(event Event) Result { return m.Handle(event) }

// ApplyEvent is an alias for Handle.
func (m *Machine) ApplyEvent(event Event) Result { return m.Handle(event) }

// Step is an alias for Handle.
func (m *Machine) Step(event Event) Result { return m.Handle(event) }

// Process is an alias for Handle.
func (m *Machine) Process(event Event) Result { return m.Handle(event) }

// Dispatch is an alias for Handle.
func (m *Machine) Dispatch(event Event) Result { return m.Handle(event) }

// OnPacketSent records a send using positional arguments.
func (m *Machine) OnPacketSent(time int64, space Space, packetNumber, packetBytes int64, ackEliciting bool) Result {
	return m.Send(SendEvent{
		Time:         time,
		Space:        space,
		PacketNumber: packetNumber,
		Bytes:        packetBytes,
		AckEliciting: ackEliciting,
	})
}

// SendPacket is an alias for OnPacketSent.
func (m *Machine) SendPacket(time int64, space Space, packetNumber, packetBytes int64, ackEliciting bool) Result {
	return m.OnPacketSent(time, space, packetNumber, packetBytes, ackEliciting)
}

// OnAckReceived processes an ACK using a struct event.
func (m *Machine) OnAckReceived(event AckEvent) Result { return m.Ack(event) }

// OnAck is an alias for Ack.
func (m *Machine) OnAck(event AckEvent) Result { return m.Ack(event) }

// OnACK is an alias for Ack.
func (m *Machine) OnACK(event AckEvent) Result { return m.Ack(event) }

// ProcessAck is an alias for Ack.
func (m *Machine) ProcessAck(event AckEvent) Result { return m.Ack(event) }

// RecordAck is an alias for Ack.
func (m *Machine) RecordAck(event AckEvent) Result { return m.Ack(event) }

// Acknowledge is an alias for Ack.
func (m *Machine) Acknowledge(event AckEvent) Result { return m.Ack(event) }

// Tick advances the clock and fires due timers.
func (m *Machine) Tick(time int64) Result { return m.AdvanceClock(time) }

// OnTick is an alias for AdvanceClock.
func (m *Machine) OnTick(time int64) Result { return m.AdvanceClock(time) }

// ClockAdvance is an alias for AdvanceClock.
func (m *Machine) ClockAdvance(time int64) Result { return m.AdvanceClock(time) }

// AdvanceTime is an alias for AdvanceClock.
func (m *Machine) AdvanceTime(time int64) Result { return m.AdvanceClock(time) }

// OnClock is an alias for AdvanceClock.
func (m *Machine) OnClock(time int64) Result { return m.AdvanceClock(time) }

// OnTimer is an RFC-vocabulary alias for AdvanceClock.
func (m *Machine) OnTimer(time int64) Result { return m.AdvanceClock(time) }

// OnPathValidation is an alias for PathValidation.
func (m *Machine) OnPathValidation(event PathEvent) Result { return m.PathValidation(event) }

// SetPathValidated completes peer address validation at the given time.
func (m *Machine) SetPathValidated(time int64, validated bool) Result {
	return m.SetPeerAddressValidated(time, validated)
}
