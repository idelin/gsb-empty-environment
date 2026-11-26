package rfc9002

import "time"

// PacketNumber is a QUIC packet number; numbers are independent per space.
type PacketNumber uint64

// PacketStatus is the recovery status of a tracked sent packet.
type PacketStatus uint8

const (
	// StatusSent means the packet is outstanding (or at least tracked).
	StatusSent PacketStatus = iota
	// StatusAcked means an ACK newly acknowledged the packet.
	StatusAcked
	// StatusLost means loss detection declared the packet lost.
	StatusLost
)

// LossReason explains why DetectLostPackets marked a packet lost.
type LossReason uint8

const (
	// LossReasonNone is the zero value.
	LossReasonNone LossReason = iota
	// LossReasonPacketThreshold is kPacketThreshold gap loss.
	LossReasonPacketThreshold
	// LossReasonTimeThreshold is the 9/8 * max(SRTT, latest) timer loss.
	LossReasonTimeThreshold
)

func (r LossReason) String() string {
	switch r {
	case LossReasonPacketThreshold:
		return "packet_threshold"
	case LossReasonTimeThreshold:
		return "time_threshold"
	default:
		return "none"
	}
}

// SentPacket is the RFC 9002 A.1.1 "Sent Packet Fields" record plus
// bookkeeping needed by the state machine.
type SentPacket struct {
	// Number is the packet number within Space.
	Number PacketNumber
	// Space is the packet number space.
	Space PacketNumberSpace
	// SentAt is time_sent.
	SentAt time.Time
	// Size is sent_bytes, used for bytes_in_flight / congestion control.
	Size int
	// AckEliciting is the ack_eliciting flag. Pure ACK packets are false.
	AckEliciting bool
	// InFlight is the in_flight flag. Pure ACK packets are not in flight.
	InFlight bool

	// Status is the current recovery status.
	Status PacketStatus
	// BytesAccounted reports whether Size is currently counted in
	// BytesInFlight / the per-space in-flight counter. It makes removal
	// idempotent across ack, loss and key discard.
	BytesAccounted bool

	// LostAt is when loss detection marked the packet lost.
	LostAt time.Time
	// LossReason records the triggering threshold.
	LossReason LossReason
	// AckedAt is when the packet was acknowledged.
	AckedAt time.Time
}

// AckRange is an inclusive ACK range [Smallest, Largest].
type AckRange struct {
	Smallest PacketNumber
	Largest  PacketNumber
}

// Range is a convenience constructor for an inclusive AckRange.
func Range(smallest, largest PacketNumber) AckRange {
	if smallest > largest {
		smallest, largest = largest, smallest
	}
	return AckRange{Smallest: smallest, Largest: largest}
}

// AckFrame is the subset of a QUIC ACK frame consumed by loss detection.
type AckFrame struct {
	// Space is the packet number space the ACK belongs to.
	Space PacketNumberSpace
	// LargestAcked is the ACK frame's Largest Acknowledged.
	LargestAcked PacketNumber
	// AckDelay is the decoded ACK Delay as a duration.
	AckDelay time.Duration

	// Ranges are inclusive acknowledged ranges. When both Ranges and
	// Numbers are empty the ACK is treated as acknowledging exactly
	// LargestAcked.
	Ranges []AckRange
	// Numbers lists additional/explicit acknowledged packet numbers. It is
	// merged with Ranges and LargestAcked.
	Numbers []PacketNumber

	// Cumulative, when true, acknowledges every outstanding packet with
	// number <= LargestAcked (a TCP-style cumulative ACK). This is a
	// convenience for tests; real QUIC ACKs use Ranges.
	Cumulative bool

	// ECNCECount is the absolute ECN-CE counter carried by the ACK frame.
	ECNCECount uint64
	// ECNCEDelta optionally supplies an explicit ECN-CE increment. When
	// non-zero it takes precedence over ECNCECount diffing.
	ECNCEDelta uint64
}

// LostPacket pairs a lost sent packet with its loss reason.
type LostPacket struct {
	Packet *SentPacket
	Number PacketNumber
	Space  PacketNumberSpace
	Size   int
	SentAt time.Time
	Reason LossReason
}

// TimerKind identifies which single timer is currently armed.
type TimerKind uint8

const (
	// TimerNone means no timer should be armed.
	TimerNone TimerKind = iota
	// TimerLoss means the earliest loss_time deadline is armed.
	TimerLoss
	// TimerPTO means the PTO deadline is armed.
	TimerPTO
)

func (k TimerKind) String() string {
	switch k {
	case TimerLoss:
		return "loss"
	case TimerPTO:
		return "pto"
	default:
		return "none"
	}
}

// TimerState is the resolved unified loss-detection/PTO timer.
type TimerState struct {
	// Armed reports whether the transport should run a timer.
	Armed bool
	// Kind is TimerLoss, TimerPTO or TimerNone.
	Kind TimerKind
	// Deadline is the absolute deadline when Armed.
	Deadline time.Time
	// Space is the space owning the deadline.
	Space PacketNumberSpace
	// PTOCount is the current PTO backoff counter.
	PTOCount int
	// AmplificationBlocked reports that a server has hit the 3x
	// anti-amplification limit and therefore the PTO timer is disabled.
	AmplificationBlocked bool
}

// AckOutcome summarizes OnAckReceived so tests and callers can assert on
// every state transition without reaching into internals.
type AckOutcome struct {
	Space          PacketNumberSpace
	NewlyAcked     []PacketNumber
	Lost           []LostPacket
	RTTUpdated     bool
	ECNCEIncreased bool

	CongestionReduced    bool
	PersistentCongestion bool
	PTOCountReset        bool

	Timer TimerState
}

// TimeoutOutcome summarizes OnLossDetectionTimeout.
type TimeoutOutcome struct {
	Kind                 TimerKind
	Space                PacketNumberSpace
	Now                  time.Time
	Lost                 []LostPacket
	PTOFired             bool
	ProbeSpace           PacketNumberSpace
	ProbePackets         int
	PTOCount             int
	AmplificationBlocked bool
	Timer                TimerState
}

// DiscardOutcome summarizes DiscardKeys.
type DiscardOutcome struct {
	Space             PacketNumberSpace
	Discarded         bool
	PacketsRemoved    int
	BytesRemoved      int
	InFlightRemaining int
	PTOCountReset     bool
	Timer             TimerState
}
