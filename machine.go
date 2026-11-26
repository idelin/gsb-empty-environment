package recovery

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	// Microsecond constants from RFC 9002 Appendix A.
	Microsecond      int64 = 1
	Millisecond      int64 = 1000 * Microsecond
	TimerGranularity int64 = Millisecond
	InitialRTT       int64 = 333 * Millisecond
	PacketThreshold  int64 = 3

	// DefaultMaxACKDelay is the RFC transport-parameter default.
	DefaultMaxACKDelay int64 = 25 * Millisecond
)

// ErrNonMonotonicTime is returned when an event goes backwards.
var ErrNonMonotonicTime = errors.New("recovery: event time is not monotonic")

// ErrInvalidSpace is returned for an unknown packet-number space.
var ErrInvalidSpace = errors.New("recovery: invalid packet number space")

// ErrDuplicatePacket is returned when a packet number is reused in a space.
var ErrDuplicatePacket = errors.New("recovery: duplicate packet number in space")

type sentPacket struct {
	ref          PacketRef
	sentAt       int64
	bytes        int64
	ackEliciting bool
	inFlight     bool
	outstanding  bool
	acked        bool
	lost         bool
}

type spaceState struct {
	largestAcked           int64
	lossTime               int64
	lastAckElicitingSentAt int64
	ackElicitingInFlight   int64
	packets                map[int64]*sentPacket
}

// Machine is a three-space RFC 9002 recovery state machine.
type Machine struct {
	maxACKDelay int64
	now         int64
	started     bool

	latestRTT     int64
	smoothedRTT   int64
	rttVar        int64
	minRTT        int64
	haveRTTSample bool

	handshakeConfirmed             bool
	peerCompletedAddressValidation bool
	peerValidationSet              bool
	antiAmplificationLimited       bool

	ptoCount      int64
	bytesInFlight int64
	timer         Timer
	timerManaged  bool
	lastPTOFired  *PacketRef

	spaces [3]spaceState
}

// New creates a machine using the peer max_ack_delay in microseconds. If the
// argument is omitted, the RFC transport-parameter default of 25ms is used.
func New(maxACKDelayMicros ...int64) *Machine {
	maxACKDelay := DefaultMaxACKDelay
	if len(maxACKDelayMicros) > 0 {
		maxACKDelay = maxACKDelayMicros[0]
	}
	if maxACKDelay < 0 {
		maxACKDelay = 0
	}
	m := &Machine{
		maxACKDelay: maxACKDelay,
		smoothedRTT: InitialRTT,
		rttVar:      InitialRTT / 2,
		timer:       Timer{Kind: NoTimer, Deadline: -1},
	}
	for i := range m.spaces {
		m.spaces[i].largestAcked = -1
		m.spaces[i].lastAckElicitingSentAt = -1
		m.spaces[i].packets = make(map[int64]*sentPacket)
	}
	return m
}

// NewDefault creates a machine with the RFC default 25ms max_ack_delay.
func NewDefault() *Machine { return New(DefaultMaxACKDelay) }

// Handle processes one unified event envelope and returns its result.
func (m *Machine) Handle(event Event) Result {
	kind := event.Kind
	if kind == "" {
		if event.Type != "" {
			kind = EventKind(event.Type)
		} else if event.Name != "" {
			kind = EventKind(event.Name)
		}
	}
	if kind == "" {
		switch {
		case event.Send != nil:
			kind = EventSend
		case event.ACK != nil:
			kind = EventACK
		case event.Path != nil:
			kind = EventPath
		case event.Clock != nil:
			kind = EventClock
		}
	}
	normalizedKind := EventKind(strings.ReplaceAll(strings.ToLower(string(kind)), "-", "_"))
	switch normalizedKind {
	case EventSend, "packet_sent", "send_packet", "sent":
		if event.Send == nil {
			return m.errorResult("recovery: nil send event")
		}
		return m.Send(*event.Send)
	case EventACK, "ack_received", "acknowledgment", "acknowledgement":
		if event.ACK == nil {
			return m.errorResult("recovery: nil ack event")
		}
		return m.Ack(*event.ACK)
	case EventPath, "path_validation", "path_validation_switch", "validation":
		if event.Path == nil {
			return m.errorResult("recovery: nil path event")
		}
		return m.PathValidation(*event.Path)
	case EventClock, "clock_advance", "advance_clock", "tick", "time", "timer":
		if event.Clock == nil {
			return m.errorResult("recovery: nil clock event")
		}
		return m.AdvanceClock(event.Clock.Time)
	default:
		return m.errorResult(fmt.Sprintf("recovery: unknown event kind %q", event.Kind))
	}
}

// Send records a transmitted packet.
func (m *Machine) Send(event SendEvent) Result {
	event = normalizeSendEvent(event)
	if err := m.begin(event.Time); err != nil {
		return m.errResult(err)
	}
	if !event.Space.Valid() {
		return m.errResult(ErrInvalidSpace)
	}
	space := &m.spaces[event.Space]
	if _, exists := space.packets[event.PacketNumber]; exists {
		return m.errResult(fmt.Errorf("%w: %s/%d", ErrDuplicatePacket, event.Space, event.PacketNumber))
	}

	ackEliciting := event.AckEliciting && !event.PureACK
	inFlight := ackEliciting
	if event.InFlight != nil {
		inFlight = *event.InFlight
	}
	if event.PureACK {
		inFlight = false
	}
	if event.Bytes < 0 {
		return m.errResult(fmt.Errorf("recovery: negative packet bytes %d", event.Bytes))
	}

	pkt := &sentPacket{
		ref:          PacketRef{Space: event.Space, PacketNumber: event.PacketNumber},
		sentAt:       event.Time,
		bytes:        event.Bytes,
		ackEliciting: ackEliciting,
		inFlight:     inFlight,
		outstanding:  inFlight,
	}
	space.packets[event.PacketNumber] = pkt

	if inFlight {
		m.bytesInFlight += event.Bytes
	}
	if ackEliciting && inFlight {
		space.ackElicitingInFlight++
		space.lastAckElicitingSentAt = event.Time
	}

	// RFC 9002/x-net re-arms when an in-flight datagram is sent. A pure ACK
	// has inFlight=false by construction and therefore does not reach here.
	if inFlight {
		m.timerManaged = true
		m.resetTimer()
	}
	return m.result(nil, event.Space)
}

// Ack processes an ACK frame, including reordered older ACKs and ranges.
func (m *Machine) Ack(event AckEvent) Result {
	event = normalizeAckEvent(event)
	if err := m.begin(event.Time); err != nil {
		return m.errResult(err)
	}
	if !event.Space.Valid() {
		return m.errResult(ErrInvalidSpace)
	}
	if event.LargestAcked < 0 {
		return m.errResult(fmt.Errorf("recovery: negative largest_acked %d", event.LargestAcked))
	}

	ranges, err := normalizeRanges(event)
	if err != nil {
		return m.errResult(err)
	}
	space := &m.spaces[event.Space]
	previousLargest := space.largestAcked
	largestAdvanced := event.LargestAcked > previousLargest
	if largestAdvanced {
		space.largestAcked = event.LargestAcked
	}

	acked := m.markAcknowledged(event.Space, ranges)
	if len(acked) > 0 {
		m.ptoCount = 0
	}

	if largestAdvanced && event.Space == ApplicationData {
		if pkt := space.packets[event.LargestAcked]; pkt != nil && pkt.ackEliciting {
			latest := event.Time - pkt.sentAt
			if latest < 0 {
				return m.errResult(fmt.Errorf("recovery: RTT sample went backwards for %d", event.LargestAcked))
			}
			m.updateRTT(latest, event.AckDelay)
		}
	}

	lost := m.detectLostPackets(event.Space, event.Time)
	m.timerManaged = true
	m.resetTimer()
	result := m.result(lost, event.Space)
	return result
}

// PathValidation changes handshake/path/anti-amplification state and re-arms
// the timer as required after path-recovery events.
func (m *Machine) PathValidation(event PathEvent) Result {
	event = normalizePathEvent(event)
	if err := m.begin(event.Time); err != nil {
		return m.errResult(err)
	}
	if event.HandshakeConfirmed != nil {
		m.handshakeConfirmed = *event.HandshakeConfirmed
	}
	if event.PeerCompletedAddressValidation != nil {
		m.peerCompletedAddressValidation = *event.PeerCompletedAddressValidation
		m.peerValidationSet = true
	}
	if event.AntiAmplificationLimited != nil {
		m.antiAmplificationLimited = *event.AntiAmplificationLimited
	}
	m.timerManaged = true
	m.resetTimer()
	lost := m.fireDueTimers(event.Time)
	return m.result(lost, ApplicationData)
}

// SetHandshakeConfirmed is a concise path-event helper.
func (m *Machine) SetHandshakeConfirmed(time int64, confirmed bool) Result {
	return m.PathValidation(PathEvent{Time: time, HandshakeConfirmed: &confirmed})
}

// SetPeerAddressValidated is a concise path-event helper.
func (m *Machine) SetPeerAddressValidated(time int64, validated bool) Result {
	return m.PathValidation(PathEvent{Time: time, PeerCompletedAddressValidation: &validated})
}

// SetAntiAmplificationLimited is a concise anti-amplification helper.
func (m *Machine) SetAntiAmplificationLimited(time int64, limited bool) Result {
	return m.PathValidation(PathEvent{Time: time, AntiAmplificationLimited: &limited})
}

// AdvanceClock advances time and processes due loss/PTO deadlines.
func (m *Machine) AdvanceClock(time int64) Result {
	if err := m.begin(time); err != nil {
		return m.errResult(err)
	}
	lost := m.fireDueTimers(time)
	return m.result(lost, m.timer.Space)
}

// Now returns the last event time.
func (m *Machine) Now() int64 { return m.now }

// BytesInFlight returns the live in-flight byte counter.
func (m *Machine) BytesInFlight() int64 { return m.bytesInFlight }

// RTT returns a copy of the current RTT estimator state.
func (m *Machine) RTT() RTTStats {
	return RTTStats{
		LatestRTT:    m.latestRTT,
		SmoothedRTT:  m.smoothedRTT,
		RTTVar:       m.rttVar,
		MinRTT:       m.minRTT,
		HasRTTSample: m.haveRTTSample,
	}
}

// PTOCount returns the current RFC pto_count.
func (m *Machine) PTOCount() int64 { return m.ptoCount }

// LargestAcked returns the non-rewinding largest ACK number for space.
func (m *Machine) LargestAcked(space Space) (int64, error) {
	if !space.Valid() {
		return -1, ErrInvalidSpace
	}
	return m.spaces[space].largestAcked, nil
}

// NextTimeout returns the current next absolute deadline, or -1 if unarmed.
func (m *Machine) NextTimeout() int64 {
	if !m.timer.Armed() {
		return -1
	}
	return m.timer.Deadline
}

func (m *Machine) begin(time int64) error {
	m.lastPTOFired = nil
	if m.started && time < m.now {
		return fmt.Errorf("%w: new=%d current=%d", ErrNonMonotonicTime, time, m.now)
	}
	m.started = true
	m.now = time
	return nil
}

func (m *Machine) markAcknowledged(space Space, ranges []AckRange) []PacketRef {
	state := &m.spaces[space]
	var acked []PacketRef
	for pn, pkt := range state.packets {
		if pkt.acked || !numberInRanges(pn, ranges) {
			continue
		}
		pkt.acked = true
		acked = append(acked, pkt.ref)
		if pkt.outstanding {
			pkt.outstanding = false
			m.bytesInFlight -= pkt.bytes
			if pkt.ackEliciting {
				state.ackElicitingInFlight--
			}
		}
	}
	sort.Slice(acked, func(i, j int) bool { return acked[i].PacketNumber < acked[j].PacketNumber })
	return acked
}

func normalizeRanges(event AckEvent) ([]AckRange, error) {
	var ranges []AckRange
	if len(event.Ranges) > 0 {
		ranges = append(ranges, event.Ranges...)
	}
	for _, pair := range event.AckRanges {
		ranges = append(ranges, Range(pair[0], pair[1]))
	}
	for _, pn := range event.AcknowledgedNumbers {
		ranges = append(ranges, Range(pn, pn))
	}
	if len(ranges) == 0 {
		ranges = []AckRange{Range(event.LargestAcked, event.LargestAcked)}
	} else {
		ranges = append(ranges, Range(event.LargestAcked, event.LargestAcked))
	}
	for i := range ranges {
		if ranges[i].First > ranges[i].Last {
			return nil, fmt.Errorf("recovery: invalid ack range [%d,%d]", ranges[i].First, ranges[i].Last)
		}
		if ranges[i].First < 0 {
			return nil, fmt.Errorf("recovery: negative ack range endpoint %d", ranges[i].First)
		}
	}
	return ranges, nil
}

func numberInRanges(number int64, ranges []AckRange) bool {
	for _, r := range ranges {
		if number >= r.First && number <= r.Last {
			return true
		}
	}
	return false
}

func (m *Machine) updateRTT(latest, ackDelay int64) {
	updated := AdjustRTTSample(m.RTT(), latest, ackDelay, m.maxACKDelay)
	m.latestRTT = updated.LatestRTT
	m.smoothedRTT = updated.SmoothedRTT
	m.rttVar = updated.RTTVar
	m.minRTT = updated.MinRTT
	m.haveRTTSample = updated.HasRTTSample
}

func (m *Machine) result(lost []LostPacket, space Space) Result {
	if lost == nil {
		lost = []LostPacket{}
	}
	numbers := make([]int64, len(lost))
	for i, pkt := range lost {
		numbers[i] = pkt.PacketNumber
	}
	deadline := m.NextTimeout()
	rtt := m.RTT()
	r := Result{
		Time:              m.now,
		Space:             space,
		LostPackets:       lost,
		NewlyLost:         lost,
		LostPacketNumbers: numbers,
		BytesInFlight:     m.bytesInFlight,
		InFlightBytes:     m.bytesInFlight,
		RTT:               rtt,
		LatestRTT:         rtt.LatestRTT,
		SmoothedRTT:       rtt.SmoothedRTT,
		RTTVar:            rtt.RTTVar,
		MinRTT:            rtt.MinRTT,
		Timer:             m.timer,
		NextTimeoutAt:     deadline,
		NextTimerAt:       deadline,
		Deadline:          deadline,
		LargestAcked:      m.spaces[space].largestAcked,
	}
	r.PTOFired = m.lastPTOFired
	return r
}

func (m *Machine) errorResult(message any) Result {
	var err error
	switch v := message.(type) {
	case error:
		err = v
	case string:
		err = errors.New(v)
	default:
		err = fmt.Errorf("%v", v)
	}
	rtt := m.RTT()
	deadline := m.NextTimeout()
	return Result{
		Time:              m.now,
		LostPackets:       []LostPacket{},
		NewlyLost:         []LostPacket{},
		LostPacketNumbers: []int64{},
		BytesInFlight:     m.bytesInFlight,
		InFlightBytes:     m.bytesInFlight,
		RTT:               rtt,
		LatestRTT:         rtt.LatestRTT,
		SmoothedRTT:       rtt.SmoothedRTT,
		RTTVar:            rtt.RTTVar,
		MinRTT:            rtt.MinRTT,
		Timer:             m.timer,
		NextTimeoutAt:     deadline,
		NextTimerAt:       deadline,
		Deadline:          deadline,
		Err:               err,
	}
}

func (m *Machine) errResult(err error) Result { return m.errorResult(err) }
