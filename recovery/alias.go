// Package recovery re-exports the root RFC 9002 recovery package under the
// conventional import path suffix "/recovery".
package recovery

import (
	root "prescreen.local/ps-4b0c066dfd"
)

type (
	// Space is a packet-number space.
	Space = root.Space
	// PacketNumberSpace aliases Space.
	PacketNumberSpace = root.PacketNumberSpace
	// TimerKind identifies loss or PTO.
	TimerKind = root.TimerKind
	// EventKind identifies an event-envelope type.
	EventKind = root.EventKind
	// LossReason identifies packet or time loss.
	LossReason = root.LossReason

	// AckRange is an inclusive ACK range.
	AckRange = root.AckRange
	// SendEvent is a send event.
	SendEvent = root.SendEvent
	// AckEvent is an ACK event.
	AckEvent = root.AckEvent
	// PathEvent is a path-validation event.
	PathEvent = root.PathEvent
	// ClockEvent is a clock event.
	ClockEvent = root.ClockEvent
	// Event is a unified event.
	Event = root.Event
	// PacketRef identifies a packet.
	PacketRef = root.PacketRef
	// LostPacket describes a loss.
	LostPacket = root.LostPacket
	// RTTStats contains RTT estimator state.
	RTTStats = root.RTTStats
	// Timer describes the next timer.
	Timer = root.Timer
	// Result is an event result.
	Result = root.Result

	// Machine is the state machine.
	Machine = root.Machine
	// Recovery aliases Machine.
	Recovery = root.Recovery
	// QUICRecovery aliases Machine.
	QUICRecovery = root.QUICRecovery
	// RecoveryStateMachine aliases Machine.
	RecoveryStateMachine = root.RecoveryStateMachine
	// StateMachine aliases Machine.
	StateMachine = root.StateMachine
)

const (
	Initial         = root.Initial
	Handshake       = root.Handshake
	ApplicationData = root.ApplicationData

	InitialSpace         = root.InitialSpace
	HandshakeSpace       = root.HandshakeSpace
	ApplicationDataSpace = root.ApplicationDataSpace
	AppDataSpace         = root.AppDataSpace

	NoTimer   = root.NoTimer
	LossTimer = root.LossTimer
	PTOTimer  = root.PTOTimer

	EventSend  = root.EventSend
	EventACK   = root.EventACK
	EventPath  = root.EventPath
	EventClock = root.EventClock

	PacketThresholdLoss = root.PacketThresholdLoss
	TimeThresholdLoss   = root.TimeThresholdLoss
	TimerLoss           = root.TimerLoss

	Microsecond        = root.Microsecond
	Millisecond        = root.Millisecond
	TimerGranularity   = root.TimerGranularity
	InitialRTT         = root.InitialRTT
	PacketThreshold    = root.PacketThreshold
	DefaultMaxACKDelay = root.DefaultMaxACKDelay
)

var (
	ErrNonMonotonicTime = root.ErrNonMonotonicTime
	ErrInvalidSpace     = root.ErrInvalidSpace
	ErrDuplicatePacket  = root.ErrDuplicatePacket
)

// New creates a machine.
func New(maxACKDelayMicros ...int64) *Machine { return root.New(maxACKDelayMicros...) }

// NewDefault creates a machine with 25ms max_ack_delay.
func NewDefault() *Machine { return root.NewDefault() }

// NewMachine aliases New.
func NewMachine(maxACKDelayMicros ...int64) *Machine { return root.NewMachine(maxACKDelayMicros...) }

// NewRecovery aliases New.
func NewRecovery(maxACKDelayMicros ...int64) *Recovery { return root.NewRecovery(maxACKDelayMicros...) }

// NewQUICRecovery aliases New.
func NewQUICRecovery(maxACKDelayMicros ...int64) *QUICRecovery {
	return root.NewQUICRecovery(maxACKDelayMicros...)
}

// NewRecoveryStateMachine aliases New.
func NewRecoveryStateMachine(maxACKDelayMicros ...int64) *RecoveryStateMachine {
	return root.NewRecoveryStateMachine(maxACKDelayMicros...)
}

// Range creates an inclusive ACK range.
func Range(first, last int64) AckRange { return root.Range(first, last) }

// TimeThresholdDelay forwards the RFC time-threshold formula.
func TimeThresholdDelay(smoothedRTT, latestRTT int64) int64 {
	return root.TimeThresholdDelay(smoothedRTT, latestRTT)
}

// ClampACKDelay forwards max_ack_delay clamping.
func ClampACKDelay(ackDelay, maxACKDelay int64) int64 {
	return root.ClampACKDelay(ackDelay, maxACKDelay)
}

// ACKAdjustmentAllowed forwards the min_rtt plausibility test.
func ACKAdjustmentAllowed(latestRTT, minRTT, cappedDelay int64) bool {
	return root.ACKAdjustmentAllowed(latestRTT, minRTT, cappedDelay)
}

// AdjustRTTSample forwards RTT estimation.
func AdjustRTTSample(current RTTStats, latestRTT, ackDelay, maxACKDelay int64) RTTStats {
	return root.AdjustRTTSample(current, latestRTT, ackDelay, maxACKDelay)
}

// PTOBaseDuration forwards PTO calculation.
func PTOBaseDuration(smoothedRTT, rttVar, maxACKDelay int64, space Space) int64 {
	return root.PTOBaseDuration(smoothedRTT, rttVar, maxACKDelay, space)
}

// Send creates a send envelope.
func Send(event SendEvent) Event { return root.Send(event) }

// ACK creates an ACK envelope.
func ACK(event AckEvent) Event { return root.ACK(event) }

// Clock creates a clock envelope.
func Clock(time int64) Event { return root.Clock(time) }

// Path creates a path envelope.
func Path(event PathEvent) Event { return root.Path(event) }
