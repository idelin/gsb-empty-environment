package netsec

import (
	"math"
	"net/netip"
	"sync"
)

const (
	// MinInitialDatagramSize is the smallest UDP payload that can carry a
	// client Initial packet (RFC 9000, Section 14.1).
	MinInitialDatagramSize = 1200

	// AmplificationFactor is the maximum number of bytes a server may send
	// on an unvalidated path for each byte received on that path
	// (RFC 9000, Section 8.1).
	AmplificationFactor = 3
)

// SendKind classifies an outbound datagram for anti-amplification
// bookkeeping and path-validation ordering.
type SendKind uint8

const (
	// SendData is an ordinary outbound datagram. It is blocked on a migrated
	// path until a PATH_CHALLENGE has been sent on that path.
	SendData SendKind = iota

	// SendPathChallenge is a datagram that carries a PATH_CHALLENGE frame.
	// It is the only kind of datagram allowed before a newly migrated path
	// has had its challenge sent.
	SendPathChallenge

	// SendPTOProbe is a probe sent after a PTO expires. It is subject to the
	// same anti-amplification limit as ordinary data.
	SendPTOProbe
)

// PathStats is a snapshot of one path's anti-amplification state.
type PathStats struct {
	Path              netip.AddrPort
	Received          uint64
	Sent              uint64
	Limit             uint64
	Remaining         uint64
	Validated         bool
	ChallengeRequired bool
}

type pathState struct {
	received          uint64
	sent              uint64
	validated         bool
	challengeRequired bool
}

// Budget tracks the RFC 9000 Section 8.1 anti-amplification state for a QUIC
// server connection. It is safe for concurrent use and must not be copied
// after first use.
//
// Anti-amplification accounting is per path. A path is initially unvalidated
// and may send at most three times the UDP payload bytes received on that
// path. Calling a validation method removes the limit for that path.
//
// A newly migrated path must send a PATH_CHALLENGE before any other datagram.
// The challenge itself remains subject to the anti-amplification limit.
type Budget struct {
	mu         sync.Mutex
	paths      map[netip.AddrPort]*pathState
	current    netip.AddrPort
	hasCurrent bool
}

// NewBudget creates an empty anti-amplification budget.
func NewBudget() *Budget {
	return &Budget{paths: make(map[netip.AddrPort]*pathState)}
}

// New creates an empty anti-amplification budget.
func New() *Budget {
	return NewBudget()
}

// NewBudgetForPath creates a budget and makes path the connection's initial
// path. No PATH_CHALLENGE is required for the initial path.
func NewBudgetForPath(path netip.AddrPort) *Budget {
	b := NewBudget()
	b.current = path
	b.hasCurrent = path.IsValid()
	if b.hasCurrent {
		b.paths[path] = &pathState{}
	}
	return b
}

// ReceiveDatagram records the UDP payload size of a non-Initial datagram
// received on path. The full UDP payload size counts toward the path's
// anti-amplification allowance.
func (b *Budget) ReceiveDatagram(path netip.AddrPort, payloadSize int) bool {
	return b.receive(path, payloadSize, false)
}

// ReceiveInitial records a client Initial datagram received on path.
//
// An Initial carried in a UDP datagram shorter than
// MinInitialDatagramSize is discarded and is not counted toward the
// anti-amplification budget. The bool result reports whether the datagram was
// accepted for accounting.
func (b *Budget) ReceiveInitial(path netip.AddrPort, payloadSize int) bool {
	return b.receive(path, payloadSize, true)
}

func (b *Budget) receive(path netip.AddrPort, payloadSize int, initial bool) bool {
	if payloadSize < 0 || !path.IsValid() {
		return false
	}
	if initial && payloadSize < MinInitialDatagramSize {
		return false
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	p := b.pathLocked(path)
	p.received = saturatingAdd(p.received, uint64(payloadSize))
	if !b.hasCurrent {
		b.current = path
		b.hasCurrent = true
	}
	return true
}

// MarkValidated records that the client address on path has been validated.
func (b *Budget) MarkValidated(path netip.AddrPort) bool {
	return b.validate(path)
}

// OnHandshakeDecrypted records successful decryption of a Handshake packet
// from the client on path, which validates the client address for that path.
func (b *Budget) OnHandshakeDecrypted(path netip.AddrPort) bool {
	return b.validate(path)
}

// OnValidToken records receipt of a valid address-validation token on path.
// Valid Retry and NEW_TOKEN tokens both validate the path.
func (b *Budget) OnValidToken(path netip.AddrPort) bool {
	return b.validate(path)
}

// OnPathResponse records a successful PATH_RESPONSE on path, which validates
// the path during connection migration.
func (b *Budget) OnPathResponse(path netip.AddrPort) bool {
	return b.validate(path)
}

func (b *Budget) validate(path netip.AddrPort) bool {
	if !path.IsValid() {
		return false
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	p := b.pathLocked(path)
	p.validated = true
	p.challengeRequired = false
	return true
}

// MigrateTo makes path the connection's current path and requires a
// PATH_CHALLENGE on it before any non-challenge datagram may be sent. Existing
// accounting for the path is retained, but the path's budget is independent of
// every other path's budget.
func (b *Budget) MigrateTo(path netip.AddrPort) bool {
	if !path.IsValid() {
		return false
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	p := b.pathLocked(path)
	b.current = path
	b.hasCurrent = true
	p.challengeRequired = !p.validated
	return true
}

// CurrentPath returns the connection's current path.
func (b *Budget) CurrentPath() (netip.AddrPort, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.current, b.hasCurrent
}

// CanSend reports whether a datagram of size bytes of kind kind may be sent on
// path. It does not reserve budget; use Reserve for an atomic check and
// reservation.
func (b *Budget) CanSend(path netip.AddrPort, size int, kind SendKind) bool {
	if size < 0 || !path.IsValid() {
		return false
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	p := b.paths[path]
	if p == nil {
		p = &pathState{}
	}
	return canSend(p, size, kind)
}

// Reserve reports whether a datagram of size bytes of kind kind may be sent on
// path. If allowed, it immediately accounts for the bytes.
//
// Bytes are charged when reserved so a scheduler cannot overcommit a path.
// Call Refund if the datagram is discarded before it is written to the
// network.
func (b *Budget) Reserve(path netip.AddrPort, size int, kind SendKind) bool {
	if size < 0 || !path.IsValid() {
		return false
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	p := b.pathLocked(path)
	if !canSend(p, size, kind) {
		return false
	}

	p.sent = saturatingAdd(p.sent, uint64(size))
	if kind == SendPathChallenge {
		p.challengeRequired = false
	}
	return true
}

// TrySend is an alias for Reserve.
func (b *Budget) TrySend(path netip.AddrPort, size int, kind SendKind) bool {
	return b.Reserve(path, size, kind)
}

// Refund removes bytes previously reserved for path, for example when the
// datagram was not actually sent. kind must match the kind passed to Reserve
// so that a failed PATH_CHALLENGE restores the path's ordering requirement.
func (b *Budget) Refund(path netip.AddrPort, size int, kind SendKind) {
	if size <= 0 {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	p := b.paths[path]
	if p == nil {
		return
	}
	n := uint64(size)
	if n >= p.sent {
		p.sent = 0
	} else {
		p.sent -= n
	}
	if kind == SendPathChallenge && !p.validated {
		p.challengeRequired = true
	}
}

// PathStats returns a snapshot of path's accounting and validation state.
func (b *Budget) PathStats(path netip.AddrPort) (PathStats, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	p := b.paths[path]
	if p == nil {
		return PathStats{}, false
	}
	limit := limitFor(p)
	return PathStats{
		Path:              path,
		Received:          p.received,
		Sent:              p.sent,
		Limit:             limit,
		Remaining:         remainingFor(p),
		Validated:         p.validated,
		ChallengeRequired: p.challengeRequired,
	}, true
}

// ReceivedBytes returns the UDP payload bytes counted on path.
func (b *Budget) ReceivedBytes(path netip.AddrPort) uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	if p := b.paths[path]; p != nil {
		return p.received
	}
	return 0
}

// SentBytes returns the bytes reserved for sending on path.
func (b *Budget) SentBytes(path netip.AddrPort) uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	if p := b.paths[path]; p != nil {
		return p.sent
	}
	return 0
}

// RemainingBytes returns how many more bytes may be sent on path before the
// anti-amplification limit is reached. Validated paths are unlimited and
// return math.MaxUint64.
func (b *Budget) RemainingBytes(path netip.AddrPort) uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	if p := b.paths[path]; p != nil {
		return remainingFor(p)
	}
	return 0
}

// IsValidated reports whether path has been validated.
func (b *Budget) IsValidated(path netip.AddrPort) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.paths[path]
	return p != nil && p.validated
}

// ChallengeRequired reports whether path must send a PATH_CHALLENGE before
// ordinary data or PTO probes.
func (b *Budget) ChallengeRequired(path netip.AddrPort) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.paths[path]
	return p != nil && p.challengeRequired
}

func (b *Budget) pathLocked(path netip.AddrPort) *pathState {
	if b.paths == nil {
		b.paths = make(map[netip.AddrPort]*pathState)
	}
	p := b.paths[path]
	if p == nil {
		p = &pathState{}
		b.paths[path] = p
	}
	return p
}

func canSend(p *pathState, size int, kind SendKind) bool {
	if p.challengeRequired && kind != SendPathChallenge {
		return false
	}
	if p.validated {
		return true
	}
	limit := limitFor(p)
	return p.sent <= limit && uint64(size) <= limit-p.sent
}

func limitFor(p *pathState) uint64 {
	if p.validated {
		return math.MaxUint64
	}
	return saturatingMul(p.received, AmplificationFactor)
}

func remainingFor(p *pathState) uint64 {
	if p.validated {
		return math.MaxUint64
	}
	limit := limitFor(p)
	if p.sent >= limit {
		return 0
	}
	return limit - p.sent
}

func saturatingAdd(a, b uint64) uint64 {
	if b > math.MaxUint64-a {
		return math.MaxUint64
	}
	return a + b
}

func saturatingMul(a uint64, factor uint64) uint64 {
	if a > math.MaxUint64/factor {
		return math.MaxUint64
	}
	return a * factor
}
