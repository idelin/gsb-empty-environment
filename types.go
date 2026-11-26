// Package recovery implements the RFC 9002 QUIC loss detection and probe
// timeout state machine for the Initial, Handshake, and ApplicationData
// packet-number spaces.
//
// All timestamps and durations exchanged by this package are signed 64-bit
// microseconds. A zero or negative timer deadline means that no timer is
// armed.
package recovery

import "fmt"

// Space is a QUIC packet-number space.
type Space uint8

// PacketNumberSpace is an alias for callers using RFC terminology.
type PacketNumberSpace = Space

const (
	// Initial is the Initial packet-number space.
	Initial Space = iota
	// Handshake is the Handshake packet-number space.
	Handshake
	// ApplicationData is the ApplicationData (1-RTT) packet-number space.
	ApplicationData
)

// Valid reports whether s is one of the three RFC 9002 spaces.
func (s Space) Valid() bool { return s <= ApplicationData }

// String returns the RFC name of the packet-number space.
func (s Space) String() string {
	switch s {
	case Initial:
		return "Initial"
	case Handshake:
		return "Handshake"
	case ApplicationData:
		return "ApplicationData"
	default:
		return fmt.Sprintf("UnknownSpace(%d)", uint8(s))
	}
}

// TimerKind identifies the armed timer.
type TimerKind uint8

const (
	// NoTimer indicates that no timer is armed.
	NoTimer TimerKind = iota
	// LossTimer indicates a time-threshold loss deadline.
	LossTimer
	// PTOTimer indicates a probe-timeout deadline.
	PTOTimer
)

// String returns a stable timer name.
func (k TimerKind) String() string {
	switch k {
	case LossTimer:
		return "loss"
	case PTOTimer:
		return "pto"
	default:
		return "none"
	}
}

// AckRange is an inclusive ACK interval [First, Last].
type AckRange struct {
	First int64
	Last  int64
}

// Range constructs an inclusive ACK interval and normalizes its endpoints.
func Range(a, b int64) AckRange {
	if a > b {
		a, b = b, a
	}
	return AckRange{First: a, Last: b}
}

// SendEvent records one transmitted packet.
//
// Unless InFlight explicitly says otherwise, an ack-eliciting packet is
// counted as in flight and a non-ack-eliciting packet is not. In particular,
// a pure ACK packet never contributes to BytesInFlight.
type SendEvent struct {
	Time              int64
	At                int64
	Timestamp         int64
	Space             Space
	PacketNumberSpace Space
	PacketNumber      int64
	Number            int64
	PN                int64
	Bytes             int64
	SentBytes         int64
	Size              int64
	Length            int64
	AckEliciting      bool
	AckElicits        bool
	IsAckEliciting    bool
	PureACK           bool
	IsPureACK         bool
	InFlight          *bool
}

// AckEvent is an ACK frame in one packet-number space.
//
// Ranges are inclusive intervals. If Ranges is empty, the ACK is treated as
// acknowledging only LargestAcked.
type AckEvent struct {
	Time                int64
	At                  int64
	Timestamp           int64
	Space               Space
	PacketNumberSpace   Space
	LargestAcked        int64
	LargestAcknowledged int64
	Largest             int64
	AckDelay            int64
	AckDelayMicros      int64
	Ranges              []AckRange

	// AckRanges accepts inclusive [first,last] pairs as an alternative to
	// Ranges. AcknowledgedNumbers creates singleton ranges.
	AckRanges           [][2]int64
	AcknowledgedNumbers []int64
}

// PathEvent changes path validation / anti-amplification state. A nil pointer
// leaves the corresponding state unchanged.
type PathEvent struct {
	Time                           int64
	At                             int64
	HandshakeConfirmed             *bool
	PeerCompletedAddressValidation *bool
	AntiAmplificationLimited       *bool

	// Convenience true-only switches.
	PathValidated              bool
	Validated                  bool
	AntiAmplificationUnblocked bool
}

// ClockEvent advances the state machine clock. Due loss and PTO deadlines are
// processed before the result is returned.
type ClockEvent struct {
	Time int64
}

// EventKind selects the event carried by Event.
type EventKind string

const (
	EventSend  EventKind = "send"
	EventACK   EventKind = "ack"
	EventPath  EventKind = "path"
	EventClock EventKind = "clock"
)

// Event is the unified event-envelope entry point.
type Event struct {
	Kind  EventKind
	Type  string
	Name  string
	Send  *SendEvent
	ACK   *AckEvent
	Path  *PathEvent
	Clock *ClockEvent
}

// Send constructs a send event envelope.
func Send(e SendEvent) Event { return Event{Kind: EventSend, Send: &e} }

// ACK constructs an ACK event envelope.
func ACK(e AckEvent) Event { return Event{Kind: EventACK, ACK: &e} }

// Clock constructs a clock-advance event envelope.
func Clock(time int64) Event {
	return Event{Kind: EventClock, Clock: &ClockEvent{Time: time}}
}

// Path constructs a path-state event envelope.
func Path(e PathEvent) Event { return Event{Kind: EventPath, Path: &e} }

// PacketRef identifies a packet in a packet-number space.
type PacketRef struct {
	Space        Space
	PacketNumber int64
}

// LostPacket describes one newly declared lost packet.
type LostPacket struct {
	Space        Space
	PacketNumber int64
	SentAt       int64
	Bytes        int64
	Reason       LossReason
}

// Number returns the packet number.
func (p LostPacket) Number() int64 { return p.PacketNumber }

// LossReason identifies packet- or time-threshold loss detection.
type LossReason uint8

const (
	PacketThresholdLoss LossReason = iota + 1
	TimeThresholdLoss
	TimerLoss
)

// String returns a short loss reason.
func (r LossReason) String() string {
	switch r {
	case PacketThresholdLoss:
		return "packet_threshold"
	case TimeThresholdLoss:
		return "time_threshold"
	case TimerLoss:
		return "timer"
	default:
		return "unknown"
	}
}

// RTTStats is the RFC 9002 RTT estimator state. Values are microseconds.
type RTTStats struct {
	LatestRTT    int64 `json:"latest_rtt"`
	SmoothedRTT  int64 `json:"smoothed_rtt"`
	RTTVar       int64 `json:"rttvar"`
	MinRTT       int64 `json:"min_rtt"`
	HasRTTSample bool  `json:"has_rtt_sample"`
}

// Triple returns latest_rtt, smoothed_rtt, and rttvar in microseconds.
func (r RTTStats) Triple() (latestRTT, smoothedRTT, rttVar int64) {
	return r.LatestRTT, r.SmoothedRTT, r.RTTVar
}

// Timer is the next loss or PTO deadline.
type Timer struct {
	Kind     TimerKind
	Deadline int64
	Space    Space
	// PTOCount is the RFC pto_count after processing the event.
	PTOCount int64
	// BackoffFactor is 2^PTOCount when Kind is PTOTimer.
	BackoffFactor int64
}

// Armed reports whether a deadline exists.
func (t Timer) Armed() bool { return t.Kind != NoTimer && t.Deadline > 0 }

// Result is returned for every event.
type Result struct {
	Time int64
	// Space is the event space, or the timer space for clock events.
	Space Space

	// LostPackets contains losses discovered while handling this event.
	LostPackets []LostPacket
	// NewlyLost is an alias for LostPackets.
	NewlyLost []LostPacket
	// LostPacketNumbers is the numeric form of LostPackets.
	LostPacketNumbers []int64

	// BytesInFlight is the live RFC bytes_in_flight counter.
	BytesInFlight int64
	// InFlightBytes is an alias for BytesInFlight.
	InFlightBytes int64

	// RTT is the current RTT estimator triple and min_rtt.
	RTT RTTStats
	// Scalar mirrors of RTT for direct field access.
	LatestRTT   int64
	SmoothedRTT int64
	RTTVar      int64
	MinRTT      int64

	// Timer is the next loss/PTO deadline.
	Timer Timer
	// NextTimeoutAt and its aliases are -1 when no timer is armed.
	NextTimeoutAt int64
	NextTimerAt   int64
	Deadline      int64

	// LargestAcked is the non-rewinding maximum ACK number per event space.
	LargestAcked int64

	// PTOFired is non-nil if this event processed a PTO expiration.
	PTOFired *PacketRef

	// Err is set by the unified Handle entry point for invalid events.
	Err error
}

// LostNumbers returns just the newly lost packet numbers.
func (r Result) LostNumbers() []int64 {
	if len(r.LostPacketNumbers) == len(r.LostPackets) {
		return append([]int64(nil), r.LostPacketNumbers...)
	}
	out := make([]int64, len(r.LostPackets))
	for i, pkt := range r.LostPackets {
		out[i] = pkt.PacketNumber
	}
	return out
}

// RTTTriple returns latest_rtt, smoothed_rtt, and rttvar in microseconds.
func (r Result) RTTTriple() (latestRTT, smoothedRTT, rttVar int64) {
	return r.RTT.Triple()
}

// NextTimeout returns the next absolute timer time, or -1 when unarmed.
func (r Result) NextTimeout() int64 {
	if !r.Timer.Armed() {
		return -1
	}
	return r.Timer.Deadline
}

// NextTimerTime mirrors NextTimeout for callers using that vocabulary.
func (r Result) NextTimerTime() int64 { return r.NextTimeout() }
