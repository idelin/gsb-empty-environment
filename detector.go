package rfc9002

import (
	"sort"
	"time"
)

// PacketNumberSpaceState is the per-space sent-packet table and RFC state.
type PacketNumberSpaceState struct {
	// Space is this space.
	Space PacketNumberSpace

	// SentPackets is sent_packets[space], keyed by packet number. Packets are
	// retained after ack/loss with their Status for observability and
	// persistent-congestion detection.
	SentPackets map[PacketNumber]*SentPacket

	// LargestAcked is largest_acked_packet[space].
	LargestAcked PacketNumber
	// LargestAckedValid reports whether an ACK has ever updated LargestAcked.
	LargestAckedValid bool

	// LossTime is loss_time[space], the earliest time-threshold deadline.
	LossTime time.Time

	// LastAckElicitingSentAt is time_of_last_ack_eliciting_packet[space].
	LastAckElicitingSentAt time.Time

	// InFlightBytes is the per-space bytes currently counted in flight.
	InFlightBytes int

	// NextPacketNumber is an optional allocator counter.
	NextPacketNumber PacketNumber

	// KeysDiscarded reports that Initial/Handshake keys were dropped.
	KeysDiscarded bool
}

// LossDetector is the RFC 9002 loss-detection state machine together with the
// Appendix B congestion event state and RFC 9000 anti-amplification bookkeeping.
//
// All time-dependent methods take an explicit `now`, making the machine
// deterministic and easy to test with a fake clock.
type LossDetector struct {
	// Config is the tunable configuration.
	Config

	initDone bool

	// Spaces holds the three independent packet-number spaces.
	Spaces [NumSpaces]PacketNumberSpaceState

	// RTTEstimator is embedded, promoting LatestRTT, MinRTT, SmoothedRTT,
	// RTTVar and Samples directly onto LossDetector.
	RTTEstimator

	// PTOCount is pto_count: the consecutive PTO backoff exponent.
	PTOCount int
	// PTOExpirations counts raw PTO timer expirations (not reset by ACK).
	PTOExpirations uint64

	// Timer is the last resolved unified timer state.
	Timer TimerState

	// BytesInFlight is the connection-wide bytes_in_flight.
	BytesInFlight int

	// CongestionWindow is the live congestion window (shadows the optional
	// Config seed of the same name).
	CongestionWindow int
	// SlowStartThreshold is the live ssthresh (shadows the Config seed).
	SlowStartThreshold int

	// RecoveryStartTime is congestion_recovery_start_time.
	RecoveryStartTime time.Time
	// InRecovery reports whether a recovery epoch is open.
	InRecovery bool
	// RecoveryEpoch increments on every new halving / persistent event.
	RecoveryEpoch uint64
	// CongestionReductions counts loss/ECN window halvings.
	CongestionReductions uint64
	// SuppressedCongestionEvents counts signals folded into an open epoch.
	SuppressedCongestionEvents uint64
	// LastCongestionReductionAt is the last halving time.
	LastCongestionReductionAt time.Time

	// PersistentCongestion reports the current persistent-congestion latch.
	PersistentCongestion bool
	// PersistentCongestionCount counts persistent-congestion entries.
	PersistentCongestionCount uint64
	// LastPersistentCongestionAt is the last reset time.
	LastPersistentCongestionAt time.Time

	// BytesSent / BytesReceived drive server anti-amplification. They count
	// datagram/packet bytes handed to / received from the network.
	BytesSent     int
	BytesReceived int

	// LastECNCECount is the last absolute ECN-CE counter observed.
	LastECNCECount uint64
	// ECNCEIncrements is the sum of all observed ECN-CE deltas.
	ECNCEIncrements uint64
	// ECNCEEvents counts ACKs carrying a positive ECN-CE delta.
	ECNCEEvents uint64
}

// Option configures a LossDetector at construction. It is implemented by the
// With* option helpers and by Config itself, so both
// NewLossDetector(WithServer(true)) and NewLossDetector(Config{...}) compile.
type Option interface {
	applyLossOption(ld *LossDetector)
}

type optionFunc func(*LossDetector)

func (f optionFunc) applyLossOption(ld *LossDetector) { f(ld) }

// applyLossOption lets a bare Config act as a constructor Option.
func (c Config) applyLossOption(ld *LossDetector) { ld.Config = c }

// WithConfig applies a whole Config value.
func WithConfig(c Config) Option {
	return optionFunc(func(ld *LossDetector) { ld.Config = c })
}

// WithMaxAckDelay sets the peer max_ack_delay.
func WithMaxAckDelay(d time.Duration) Option {
	return optionFunc(func(ld *LossDetector) { ld.Config.MaxAckDelay = d })
}

// WithServer marks the endpoint as a server.
func WithServer(isServer bool) Option {
	return optionFunc(func(ld *LossDetector) { ld.Config.IsServer = isServer })
}

// WithHandshakeConfirmed sets the handshake confirmation latch.
func WithHandshakeConfirmed(v bool) Option {
	return optionFunc(func(ld *LossDetector) { ld.Config.HandshakeConfirmed = v })
}

// WithAddressValidated sets peer address validation.
func WithAddressValidated(v bool) Option {
	return optionFunc(func(ld *LossDetector) { ld.Config.AddressValidated = v })
}

// NewLossDetector constructs a state machine with RFC RECOMMENDED defaults,
// then applies opts.
func NewLossDetector(opts ...Option) *LossDetector {
	ld := &LossDetector{Config: DefaultConfig()}
	for _, opt := range opts {
		if opt != nil {
			opt.applyLossOption(ld)
		}
	}
	ld.ensure()
	return ld
}

// New is an alias for NewLossDetector.
func New(opts ...Option) *LossDetector { return NewLossDetector(opts...) }

// NewRecovery is an alias for NewLossDetector.
func NewRecovery(opts ...Option) *LossDetector { return NewLossDetector(opts...) }

// NewStateMachine is an alias for NewLossDetector.
func NewStateMachine(opts ...Option) *LossDetector { return NewLossDetector(opts...) }

// NewWithConfig constructs a state machine from an explicit Config.
func NewWithConfig(c Config) *LossDetector {
	ld := &LossDetector{Config: c}
	ld.ensure()
	return ld
}

// NewServer constructs a server-side state machine (anti-amplification on).
func NewServer(opts ...Option) *LossDetector {
	opts = append(opts, WithServer(true))
	return NewLossDetector(opts...)
}

// NewClient constructs a client-side state machine.
func NewClient(opts ...Option) *LossDetector {
	opts = append(opts, WithServer(false))
	return NewLossDetector(opts...)
}

// ensure lazily initializes a possibly zero-value LossDetector.
func (ld *LossDetector) ensure() {
	if ld == nil {
		return
	}
	if ld.initDone {
		return
	}
	ld.Config.ensureDefaults()
	for i := range ld.Spaces {
		if ld.Spaces[i].SentPackets == nil {
			ld.Spaces[i].SentPackets = make(map[PacketNumber]*SentPacket)
		}
		ld.Spaces[i].Space = Space(i)
	}
	if ld.CongestionWindow == 0 {
		if ld.Config.CongestionWindow > 0 {
			ld.CongestionWindow = ld.Config.CongestionWindow
		} else {
			ld.CongestionWindow = ld.InitialWindow
		}
	}
	if ld.SlowStartThreshold == 0 {
		if ld.Config.SlowStartThreshold > 0 {
			ld.SlowStartThreshold = ld.Config.SlowStartThreshold
		} else {
			ld.SlowStartThreshold = InfiniteWindow
		}
	}
	// RFC 9002 Section 5.3 initialization. Samples stays zero so the first
	// real sample resets the estimator; MinRTT stays zero until then.
	// Explicitly pinned values (SmoothedRTT set by the caller) are preserved.
	if ld.SmoothedRTT == 0 && ld.RTTVar == 0 {
		ld.SmoothedRTT = ld.InitialRTT
		ld.RTTVar = ld.InitialRTT / 2
	}
	ld.initDone = true
}

// AllocPacketNumber returns the next unused packet number for space and
// advances the space allocator.
func (ld *LossDetector) AllocPacketNumber(space PacketNumberSpace) PacketNumber {
	ld.ensure()
	s := &ld.Spaces[space]
	n := s.NextPacketNumber
	s.NextPacketNumber++
	return n
}

func (ld *LossDetector) addInFlight(p *SentPacket) {
	if p == nil || !p.InFlight || p.BytesAccounted {
		return
	}
	ld.BytesInFlight += p.Size
	ld.Spaces[p.Space].InFlightBytes += p.Size
	p.BytesAccounted = true
}

func (ld *LossDetector) removeInFlight(p *SentPacket) {
	if p == nil || !p.BytesAccounted {
		return
	}
	ld.BytesInFlight -= p.Size
	st := &ld.Spaces[p.Space]
	st.InFlightBytes -= p.Size
	if st.InFlightBytes < 0 {
		st.InFlightBytes = 0
	}
	if ld.BytesInFlight < 0 {
		ld.BytesInFlight = 0
	}
	p.BytesAccounted = false
}

// OnPacketSent records a sent packet (RFC A.5). The packet's Space, Number,
// SentAt, Size, AckEliciting and InFlight should be populated. When
// AckEliciting is true but InFlight is left false it is promoted to in flight.
// Wire bytes also feed anti-amplification BytesSent accounting.
func (ld *LossDetector) OnPacketSent(pkt SentPacket) *SentPacket {
	ld.ensure()
	if !pkt.Space.Valid() {
		pkt.Space = ApplicationData
	}
	st := &ld.Spaces[pkt.Space]
	if st.KeysDiscarded {
		// Space is gone; do not track new state.
		return nil
	}
	if pkt.AckEliciting {
		pkt.InFlight = true
	}
	if pkt.Status == 0 {
		pkt.Status = StatusSent
	}
	if pkt.SentAt.IsZero() {
		pkt.SentAt = time.Now()
	}
	if existing, ok := st.SentPackets[pkt.Number]; ok && existing != nil {
		// Retransmission/reuse of a packet number is a caller bug; overwrite
		// but first detach the old accounting.
		ld.removeInFlight(existing)
	}
	p := pkt
	st.SentPackets[p.Number] = &p
	if p.Number >= st.NextPacketNumber {
		st.NextPacketNumber = p.Number + 1
	}
	ld.addInFlight(&p)
	if p.AckEliciting {
		st.LastAckElicitingSentAt = p.SentAt
	}
	if p.Size > 0 {
		ld.BytesSent += p.Size
	}
	// Sending an ack-eliciting packet restarts the timer per RFC; callers
	// that want the cached timer can invoke SetLossDetectionTimer with the
	// send time. We deliberately do not take a now argument here beyond
	// pkt.SentAt, so refresh using that.
	ld.refreshTimer(p.SentAt)
	return &p
}

// SendAckEliciting is a convenience wrapper around OnPacketSent for a normal
// ack-eliciting, in-flight packet.
func (ld *LossDetector) SendAckEliciting(space PacketNumberSpace, number PacketNumber, at time.Time, size int) *SentPacket {
	return ld.OnPacketSent(SentPacket{
		Space:        space,
		Number:       number,
		SentAt:       at,
		Size:         size,
		AckEliciting: true,
		InFlight:     true,
	})
}

// SendPureAck records a pure ACK packet: not ack-eliciting and not in flight,
// therefore it contributes no bytes_in_flight and never arms PTO. A positive
// size still counts as wire bytes for anti-amplification.
func (ld *LossDetector) SendPureAck(space PacketNumberSpace, number PacketNumber, at time.Time, size int) *SentPacket {
	return ld.OnPacketSent(SentPacket{
		Space:        space,
		Number:       number,
		SentAt:       at,
		Size:         size,
		AckEliciting: false,
		InFlight:     false,
	})
}

// InFlightForSpace returns the per-space in-flight byte count.
func (ld *LossDetector) InFlightForSpace(space PacketNumberSpace) int {
	ld.ensure()
	return ld.Spaces[space].InFlightBytes
}

// LargestAckedFor returns largest_acked and its validity for a space.
func (ld *LossDetector) LargestAckedFor(space PacketNumberSpace) (PacketNumber, bool) {
	ld.ensure()
	st := &ld.Spaces[space]
	return st.LargestAcked, st.LargestAckedValid
}

// SentPacketsFor returns a space's sent packet table.
func (ld *LossDetector) SentPacketsFor(space PacketNumberSpace) map[PacketNumber]*SentPacket {
	ld.ensure()
	return ld.Spaces[space].SentPackets
}

// sortedSentPackets returns packets in a space sorted by packet number.
func (ld *LossDetector) sortedSentPackets(space PacketNumberSpace) []*SentPacket {
	st := &ld.Spaces[space]
	out := make([]*SentPacket, 0, len(st.SentPackets))
	for _, p := range st.SentPackets {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out
}
