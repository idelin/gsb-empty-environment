package netsec

import (
	"crypto/rand"
	"encoding/binary"
	"net/netip"
	"sync"
)

// DropReason explains why a packet was not accepted.
type DropReason uint8

const (
	DropNone DropReason = iota
	DropInvalidSource
	DropSourceMismatch
	DropSequenceJump
	DropUnexpectedSSRC
	DropBYESourceMismatch
	DropBYESSRCMismatch
	DropSessionEnded
	DropSSRCCollision
)

func (r DropReason) String() string {
	switch r {
	case DropNone:
		return "accepted"
	case DropInvalidSource:
		return "invalid source"
	case DropSourceMismatch:
		return "source does not match locked endpoint"
	case DropSequenceJump:
		return "sequence jump awaiting resynchronization"
	case DropUnexpectedSSRC:
		return "new SSRC was not declared by signaling"
	case DropBYESourceMismatch:
		return "RTCP BYE source does not match locked endpoint"
	case DropBYESSRCMismatch:
		return "RTCP BYE SSRC does not match the active stream"
	case DropSessionEnded:
		return "session ended"
	case DropSSRCCollision:
		return "SSRC collides with the local SSRC"
	default:
		return "unknown drop reason"
	}
}

// Decision is the result of passing one RTP or RTCP packet through a
// SourceBinder.
type Decision struct {
	Accepted          bool
	Reason            DropReason
	SSRC              uint32
	Source            netip.AddrPort
	LocalSSRC         uint32
	SourceBound       bool
	Resynchronized    bool
	SSRCChanged       bool
	LocalSSRCChanged  bool
	SSRCCollision     bool
	RTCPBYE           bool
	SessionTerminated bool
}

// Stats contains cumulative counters for a receive session. The counters are
// safe to read while packets are being processed.
type Stats struct {
	RTPReceived         uint64
	RTPAccepted         uint64
	RTCPReceived        uint64
	RTCPBYEsAccepted    uint64
	Dropped             uint64
	SourceMismatchDrops uint64
	SequenceJumpDrops   uint64
	UnexpectedSSRCDrops uint64
	BYEDrops            uint64
	ForgedBYEDrops      uint64
	SessionEndedDrops   uint64
	SSRCCollisionDrops  uint64
	Resynchronizations  uint64
	LocalSSRCChanges    uint64
}

// SSRCGenerator returns a replacement local SSRC. It must not return either
// argument; the binder also rejects a result equal to the active SSRC and
// falls back to a deterministic unused value if necessary.
type SSRCGenerator func(oldSSRC, collidingSSRC uint32) uint32

// Option configures a SourceBinder.
type Option func(*SourceBinder)

// WithSSRCChangeHandler registers the callback used to re-announce a new local
// SSRC after collision. The callback runs outside the binder lock.
func WithSSRCChangeHandler(handler func(oldSSRC, newSSRC uint32)) Option {
	return func(b *SourceBinder) {
		b.handler = handler
	}
}

// WithSSRCGenerator overrides the random local SSRC generator. It is intended
// for deterministic tests or deployments with their own SSRC allocation policy.
func WithSSRCGenerator(generator SSRCGenerator) Option {
	return func(b *SourceBinder) {
		b.generator = generator
	}
}

// SourceBinder protects one receive session's RTP/RTCP source binding.
//
// The first accepted RTP packet locks the remote IP and UDP port. All later
// packets must come from that exact endpoint. The active SSRC can only change
// after DeclareSSRCChange is called by signaling.
type SourceBinder struct {
	mu sync.Mutex

	localSSRC uint32
	generator SSRCGenerator
	handler   func(oldSSRC, newSSRC uint32)

	boundSource netip.AddrPort
	bound       bool
	terminated  bool

	activeSSRC uint32
	active     bool
	sequence   *SequenceState

	declared     bool
	declaredSSRC uint32
	blockedSSRC  map[uint32]struct{}
	stats        Stats
}

// NewSourceBinder creates a receive guard for a session with localSSRC as its
// own RTP SSRC.
func NewSourceBinder(localSSRC uint32, opts ...Option) *SourceBinder {
	b := &SourceBinder{localSSRC: localSSRC}
	b.generator = randomSSRC
	for _, opt := range opts {
		if opt != nil {
			opt(b)
		}
	}
	return b
}

// HandleRTP validates the transport source, SSRC, and sequence number of one
// RTP packet.
func (b *SourceBinder) HandleRTP(ssrc uint32, seq uint16, source netip.AddrPort) Decision {
	b.mu.Lock()
	b.stats.RTPReceived++

	decision := Decision{
		SSRC:      ssrc,
		Source:    source,
		LocalSSRC: b.localSSRC,
	}

	if !source.IsValid() {
		b.drop(&decision, DropInvalidSource)
		b.mu.Unlock()
		return decision
	}

	if b.terminated {
		b.stats.SessionEndedDrops++
		b.drop(&decision, DropSessionEnded)
		b.mu.Unlock()
		return decision
	}

	// A transport mismatch takes precedence over SSRC handling. In particular,
	// an off-path packet cannot force a local SSRC change or a stream switch.
	if b.bound && source != b.boundSource {
		b.stats.SourceMismatchDrops++
		b.drop(&decision, DropSourceMismatch)
		b.mu.Unlock()
		return decision
	}

	if b.ssrcBlocked(ssrc) {
		decision.SSRCCollision = true
		b.stats.SSRCCollisionDrops++
		b.drop(&decision, DropSSRCCollision)
		b.mu.Unlock()
		return decision
	}

	oldLocal, newLocal, collision := b.resolveLocalCollision(ssrc)
	decision.LocalSSRC = b.localSSRC
	if collision {
		decision.SSRCCollision = true
		decision.LocalSSRCChanged = true
		b.blockSSRC(ssrc)
		b.stats.SSRCCollisionDrops++
		b.stats.LocalSSRCChanges++
		b.drop(&decision, DropSSRCCollision)
		b.mu.Unlock()
		if b.handler != nil {
			b.handler(oldLocal, newLocal)
		}
		return decision
	}

	if !b.bound {
		b.bound = true
		b.boundSource = source
		b.active = true
		b.activeSSRC = ssrc
		b.sequence = NewSequenceState(seq)
		b.acceptRTP(&decision)
		decision.SourceBound = true
		b.mu.Unlock()
		return decision
	}

	if !b.active || ssrc != b.activeSSRC {
		if !b.declared || ssrc != b.declaredSSRC {
			b.stats.UnexpectedSSRCDrops++
			b.drop(&decision, DropUnexpectedSSRC)
			b.mu.Unlock()
			return decision
		}

		b.active = true
		b.activeSSRC = ssrc
		b.declared = false
		b.declaredSSRC = 0
		b.sequence = NewSequenceState(seq)
		b.acceptRTP(&decision)
		decision.SSRCChanged = true
		b.mu.Unlock()
		return decision
	}

	update := b.sequence.Update(seq)
	if !update.Accepted {
		if update.LargeJump {
			b.stats.SequenceJumpDrops++
		}
		b.drop(&decision, DropSequenceJump)
		b.mu.Unlock()
		return decision
	}

	b.acceptRTP(&decision)
	decision.Resynchronized = update.Resynchronized
	if update.Resynchronized {
		b.stats.Resynchronizations++
	}
	b.mu.Unlock()
	return decision
}

// HandleRTCPBYE applies an RTCP BYE. It is effective only when its transport
// source exactly matches the locked RTP endpoint and its SSRC is the active
// stream.
func (b *SourceBinder) HandleRTCPBYE(ssrc uint32, source netip.AddrPort) Decision {
	b.mu.Lock()
	b.stats.RTCPReceived++

	decision := Decision{
		SSRC:      ssrc,
		Source:    source,
		LocalSSRC: b.localSSRC,
	}

	if !source.IsValid() {
		b.stats.BYEDrops++
		b.drop(&decision, DropInvalidSource)
		b.mu.Unlock()
		return decision
	}

	if b.terminated {
		b.stats.BYEDrops++
		b.stats.SessionEndedDrops++
		b.drop(&decision, DropSessionEnded)
		b.mu.Unlock()
		return decision
	}

	if !b.bound || source != b.boundSource {
		b.stats.BYEDrops++
		b.stats.ForgedBYEDrops++
		b.drop(&decision, DropBYESourceMismatch)
		b.mu.Unlock()
		return decision
	}

	oldLocal, newLocal, collision := b.resolveLocalCollision(ssrc)
	decision.LocalSSRC = b.localSSRC
	if collision {
		decision.SSRCCollision = true
		decision.LocalSSRCChanged = true
		b.stats.BYEDrops++
		b.stats.SSRCCollisionDrops++
		b.stats.LocalSSRCChanges++
		b.drop(&decision, DropSSRCCollision)
		b.mu.Unlock()
		if b.handler != nil {
			b.handler(oldLocal, newLocal)
		}
		return decision
	}

	if !b.active || ssrc != b.activeSSRC {
		b.stats.BYEDrops++
		b.drop(&decision, DropBYESSRCMismatch)
		b.mu.Unlock()
		return decision
	}

	b.active = false
	b.terminated = true
	b.declared = false
	b.declaredSSRC = 0
	b.stats.RTCPBYEsAccepted++
	decision.Accepted = true
	decision.RTCPBYE = true
	decision.SessionTerminated = true
	b.mu.Unlock()
	return decision
}

// DeclareSSRCChange records an SSRC change asserted by trusted signaling.
// The next RTP packet from the already locked endpoint may switch to newSSRC.
// It returns false if there is no bound stream, the SSRC is already active, or
// the requested SSRC collides with the local SSRC.
func (b *SourceBinder) DeclareSSRCChange(newSSRC uint32) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.bound || !b.active || b.terminated ||
		newSSRC == b.localSSRC || newSSRC == b.activeSSRC || b.ssrcBlocked(newSSRC) {
		return false
	}

	b.declared = true
	b.declaredSSRC = newSSRC
	return true
}

// LocalSSRC returns the current local SSRC. It may change after a collision.
func (b *SourceBinder) LocalSSRC() uint32 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.localSSRC
}

// LockedSource returns the endpoint locked by the first accepted RTP packet.
func (b *SourceBinder) LockedSource() (netip.AddrPort, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.boundSource, b.bound
}

// ActiveSSRC returns the SSRC currently allowed to contribute media.
func (b *SourceBinder) ActiveSSRC() (uint32, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.activeSSRC, b.active
}

// Terminated reports whether an authenticated RTCP BYE ended the session.
func (b *SourceBinder) Terminated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.terminated
}

// Stats returns a snapshot of the session counters.
func (b *SourceBinder) Stats() Stats {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stats
}

func (b *SourceBinder) acceptRTP(decision *Decision) {
	b.stats.RTPAccepted++
	decision.Accepted = true
	decision.Reason = DropNone
}

func (b *SourceBinder) drop(decision *Decision, reason DropReason) {
	decision.Accepted = false
	decision.Reason = reason
	b.stats.Dropped++
}

func (b *SourceBinder) resolveLocalCollision(ssrc uint32) (oldSSRC, newSSRC uint32, collision bool) {
	if ssrc != b.localSSRC {
		return 0, 0, false
	}

	oldSSRC = b.localSSRC
	newSSRC = b.allocateSSRC(ssrc)
	b.localSSRC = newSSRC
	return oldSSRC, newSSRC, true
}

func (b *SourceBinder) allocateSSRC(colliding uint32) uint32 {
	for i := 0; i < 32; i++ {
		candidate := b.generator(b.localSSRC, colliding)
		if b.validSSRC(candidate, colliding) {
			return candidate
		}
	}

	candidate := b.localSSRC + 1
	for !b.validSSRC(candidate, colliding) {
		candidate++
	}
	return candidate
}

func (b *SourceBinder) validSSRC(candidate, colliding uint32) bool {
	return candidate != 0 &&
		candidate != b.localSSRC &&
		candidate != colliding &&
		(!b.active || candidate != b.activeSSRC) &&
		!b.ssrcBlocked(candidate)
}

func (b *SourceBinder) blockSSRC(ssrc uint32) {
	if b.blockedSSRC == nil {
		b.blockedSSRC = make(map[uint32]struct{})
	}
	b.blockedSSRC[ssrc] = struct{}{}
}

func (b *SourceBinder) ssrcBlocked(ssrc uint32) bool {
	if b.blockedSSRC == nil {
		return false
	}
	_, blocked := b.blockedSSRC[ssrc]
	return blocked
}

func randomSSRC(_, _ uint32) uint32 {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err == nil {
		return binary.BigEndian.Uint32(buf[:])
	}
	return 0
}
