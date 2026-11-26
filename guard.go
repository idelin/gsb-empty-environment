package quicguard

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"net/netip"
	"sync"
	"time"
)

// MinInitialDatagramSize is the smallest UDP datagram allowed to carry a
// client Initial (RFC 9000 section 14.1).
const MinInitialDatagramSize = 1200

// AmplificationFactor is the strict egress-to-ingress multiplier allowed for
// an unvalidated source address (RFC 9000 section 8.1.4).
const AmplificationFactor = 3

// Default limits used when Config leaves a field unset.
const (
	defaultTokenTTL           = 10 * time.Second
	defaultMaxReplayEntries   = 65536
	defaultMaxQueuedDatagrams = 64
	minResetHMACKeyLen        = 32
)

// Config configures a Guard. TokenAEADKey must be exactly 32 random bytes for
// AES-256-GCM. ResetHMACKey must be at least 32 random bytes and must never be
// shared with any other service.
type Config struct {
	TokenAEADKey       []byte
	ResetHMACKey       []byte
	TokenTTL           time.Duration
	MaxReplayEntries   int
	MaxQueuedDatagrams int
	// Now and Random default to time.Now and crypto/rand.Reader. Override them
	// only in deterministic tests.
	Now    func() time.Time
	Random interface{ Read(p []byte) (int, error) }
}

// pathState is the per-source-accounting record. The source address is the
// full UDP address (IP and port); a migrated address gets a fresh record and
// therefore a fresh 3x budget.
type pathState struct {
	validated  bool
	rxBytes    uint64
	txBytes    uint64
	pending    [8]byte
	hasPending bool
	queue      [][]byte
	reason     string
}

func (p *pathState) amplificationLimit() uint64 {
	return p.rxBytes * AmplificationFactor
}

// QueuedPacket is an egress datagram held while the path is over budget.
type QueuedPacket struct {
	Data []byte
}

// Guard is the concurrency-safe QUIC anti-amplification and validation state
// machine. It owns the Retry replay table and all per-path counters.
type Guard struct {
	config    Config
	tokenAEAD cipher.AEAD
	resetHMAC []byte
	mu        sync.Mutex
	paths     map[netip.AddrPort]*pathState
	replay    map[string]int64 // nonce -> expiry in Unix nanos
}

// NewGuard validates the configuration and constructs a Guard.
func NewGuard(config Config) (*Guard, error) {
	if len(config.TokenAEADKey) != 32 {
		return nil, fmt.Errorf("%w: TokenAEADKey must be 32 bytes for AES-256-GCM", ErrMalformed)
	}
	block, err := aes.NewCipher(config.TokenAEADKey)
	if err != nil {
		return nil, err
	}
	tokenAEAD, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(config.ResetHMACKey) < minResetHMACKeyLen {
		return nil, fmt.Errorf("%w: ResetHMACKey must be at least %d bytes", ErrMalformed, minResetHMACKeyLen)
	}
	if config.TokenTTL <= 0 {
		config.TokenTTL = defaultTokenTTL
	}
	if config.MaxReplayEntries <= 0 {
		config.MaxReplayEntries = defaultMaxReplayEntries
	}
	if config.MaxQueuedDatagrams <= 0 {
		config.MaxQueuedDatagrams = defaultMaxQueuedDatagrams
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	return &Guard{
		config:    config,
		tokenAEAD: tokenAEAD,
		resetHMAC: append([]byte(nil), config.ResetHMACKey...),
		paths:     make(map[netip.AddrPort]*pathState),
		replay:    make(map[string]int64),
	}, nil
}

func (g *Guard) now() time.Time { return g.config.Now() }

func (g *Guard) readRandom(p []byte) (int, error) { return g.config.Random.Read(p) }

// pathLocked returns the accounting record for src, creating an unvalidated
// record with a zeroed 3x budget on first use.
func (g *Guard) pathLocked(src netip.AddrPort) *pathState {
	p, ok := g.paths[src]
	if !ok {
		p = &pathState{}
		g.paths[src] = p
	}
	return p
}

// PathView is an immutable snapshot of a source address record.
type PathView struct {
	Source             netip.AddrPort
	Validated          bool
	RXBytes            uint64
	TXBytes            uint64
	AmplificationLimit uint64
	QueuedDatagrams    int
	PendingChallenge   bool
}

// Snapshot returns the current accounting state of a source address.
func (g *Guard) Snapshot(src netip.AddrPort) PathView {
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.paths[src]
	if !ok {
		return PathView{Source: src, AmplificationLimit: 0}
	}
	return PathView{
		Source:             src,
		Validated:          p.validated,
		RXBytes:            p.rxBytes,
		TXBytes:            p.txBytes,
		AmplificationLimit: p.amplificationLimit(),
		QueuedDatagrams:    len(p.queue),
		PendingChallenge:   p.hasPending,
	}
}

// IngressResult describes the guard's evaluation of one received datagram.
type IngressResult struct {
	Source      netip.AddrPort
	Packets     []*Header
	RXBytes     uint64
	TXBytes     uint64
	Limit       uint64
	Validated   bool
	ValidatedBy string
	RetryClaims *RetryTokenClaims
	TokenErr    error
	// Drained holds datagrams released from the suspension queue because this
	// ingress event validated the address. The host must send them in order.
	Drained []QueuedPacket
}

// OnDatagram accounts a received UDP datagram and runs the validation state
// machine. The full datagram length (including every coalesced packet) counts
// toward rxBytes. A first-packet Initial shorter than 1200 bytes is rejected
// before any accounting occurs.
func (g *Guard) OnDatagram(src netip.AddrPort, datagram []byte) (*IngressResult, error) {
	packets, err := SplitDatagram(datagram)
	if err != nil {
		return nil, err
	}
	first := packets[0]
	if first.IsInitial() && len(datagram) < MinInitialDatagramSize {
		return nil, fmt.Errorf("%w: %d bytes", ErrInitialTooSmall, len(datagram))
	}

	g.mu.Lock()
	p := g.pathLocked(src)
	p.rxBytes += uint64(len(datagram))
	g.mu.Unlock()

	result := &IngressResult{
		Source:  src,
		Packets: packets,
		RXBytes: p.rxBytes,
	}

	switch {
	case first.IsInitial() && len(first.Token) > 0:
		claims, verifyErr := g.VerifyRetryToken(first.Token, src.Addr())
		result.TokenErr = verifyErr
		if verifyErr == nil {
			if string(claims.RetrySCID) != string(first.DCID) {
				result.TokenErr = ErrTokenBinding
			} else {
				result.RetryClaims = claims
				result.Drained = g.markValidated(src, "retry-token")
			}
		}
	case first.Type == TypeHandshake:
		// A Handshake packet proves the client derived the Initial secrets and
		// therefore owns the claimed source address (RFC 9000 section 8.1).
		result.Drained = g.markValidated(src, "handshake")
	}

	g.mu.Lock()
	result.Validated = p.validated
	result.ValidatedBy = p.reason
	result.TXBytes = p.txBytes
	result.Limit = p.amplificationLimit()
	g.mu.Unlock()
	return result, nil
}

// SendResult reports what happened to an attempted egress datagram.
type SendResult struct {
	Sent    bool
	Queued  bool
	TXBytes uint64
	RXBytes uint64
	Limit   uint64
	Drained []QueuedPacket
}

// Send applies the 3x anti-amplification check to one egress datagram. On an
// unvalidated path the datagram is sent only when txBytes+len(payload) does not
// exceed 3*rxBytes; otherwise it is suspended whole in a FIFO queue and never
// truncated. A validated path sends immediately and ignores the multiplier.
func (g *Guard) Send(src netip.AddrPort, payload []byte) (*SendResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	p := g.pathLocked(src)
	result := &SendResult{RXBytes: p.rxBytes}
	if p.validated {
		p.txBytes += uint64(len(payload))
		result.Sent = true
		result.TXBytes = p.txBytes
		result.Limit = p.amplificationLimit()
		result.Drained = g.drainQueueLocked(p)
		return result, nil
	}

	limit := p.amplificationLimit()
	projected := p.txBytes + uint64(len(payload))
	if projected <= limit {
		p.txBytes = projected
		result.Sent = true
		result.TXBytes = p.txBytes
		result.Limit = limit
		return result, nil
	}

	if len(p.queue) >= g.config.MaxQueuedDatagrams {
		return nil, ErrQueueFull
	}
	stored := append([]byte(nil), payload...)
	p.queue = append(p.queue, stored)
	result.Queued = true
	result.TXBytes = p.txBytes
	result.Limit = limit
	return result, fmt.Errorf("%w: tx=%d rx=%d limit=%d want=%d",
		ErrAmplificationLimited, p.txBytes, p.rxBytes, limit, projected)
}

// drainQueueLocked releases every suspended datagram in FIFO order.
func (g *Guard) drainQueueLocked(p *pathState) []QueuedPacket {
	if len(p.queue) == 0 {
		return nil
	}
	drained := make([]QueuedPacket, len(p.queue))
	for i, data := range p.queue {
		p.txBytes += uint64(len(data))
		drained[i] = QueuedPacket{Data: data}
	}
	p.queue = nil
	return drained
}

// markValidated transitions a path to validated and releases its queue.
func (g *Guard) markValidated(src netip.AddrPort, reason string) []QueuedPacket {
	g.mu.Lock()
	defer g.mu.Unlock()
	p := g.pathLocked(src)
	p.validated = true
	p.reason = reason
	return g.drainQueueLocked(p)
}

// ConfirmHandshake explicitly validates a source address once the transport
// completes the handshake, even if no Handshake packet was seen by the guard.
func (g *Guard) ConfirmHandshake(src netip.AddrPort) []QueuedPacket {
	return g.markValidated(src, "handshake")
}

// Invalidate returns the path to the unvalidated state with zeroed counters,
// discarding any suspended datagrams and pending challenge.
func (g *Guard) Invalidate(src netip.AddrPort) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.paths[src] = &pathState{}
}
