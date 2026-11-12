package netsec

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

const (
	// MaxPIVBytes is the maximum size of an OSCORE Partial IV (a 40-bit
	// counter).
	MaxPIVBytes = 5

	// MaxPIVNumber is the largest value representable by a 5-byte PIV.
	MaxPIVNumber uint64 = (1 << (8 * MaxPIVBytes)) - 1

	// WindowSize is the number of sequence numbers covered by the replay
	// window, including the highest sequence number seen.
	WindowSize = 32

	// EchoOptionNumber is the CoAP option number assigned to Echo by RFC 9175.
	EchoOptionNumber = 252

	// MaxEchoBytes is the maximum length of the Echo option value (RFC 9175
	// permits values from 1 through 40 bytes).
	MaxEchoBytes = 40
)

const (
	defaultEchoBytes = 16
	defaultEchoTTL   = 60 * time.Second
)

var (
	// ErrPIVMissing reports an empty Partial IV.
	ErrPIVMissing = errors.New("netsec: PIV is empty")

	// ErrPIVTooLong reports a Partial IV longer than MaxPIVBytes.
	ErrPIVTooLong = errors.New("netsec: PIV exceeds 5 bytes")

	// ErrPIVOutOfRange reports a numeric PIV outside the 40-bit range.
	ErrPIVOutOfRange = errors.New("netsec: PIV exceeds the 40-bit range")

	// ErrReplay reports a request whose sequence number is already present in
	// the replay window.
	ErrReplay = errors.New("netsec: replayed sequence number")

	// ErrBelowWindow reports a request older than the replay window.
	ErrBelowWindow = errors.New("netsec: sequence number below replay window")

	// ErrEchoRequired reports that the window is unknown, so the caller must
	// return 4.01 Unauthorized with the supplied Echo option.
	ErrEchoRequired = errors.New("netsec: Echo challenge required")

	// ErrEchoInvalid reports an Echo value that does not match the current
	// challenge. The returned challenge is the one to send to the client.
	ErrEchoInvalid = errors.New("netsec: invalid Echo value")

	// ErrSequenceExhausted reports that no new PIV can be issued or accepted.
	// The security context is disabled when this error is returned.
	ErrSequenceExhausted = errors.New("netsec: sequence number space exhausted")

	// ErrEchoUnavailable reports a failure to generate randomness for an Echo
	// challenge.
	ErrEchoUnavailable = errors.New("netsec: unable to generate Echo challenge")
)

// Status is the outcome of a replay check.
type Status uint8

const (
	// StatusAccepted means the request passed the freshness check and its
	// sequence number was recorded.
	StatusAccepted Status = iota

	// StatusEchoRequired means the request was not processed. The caller must
	// send a 4.01 (Unauthorized) response containing the Echo option in the
	// returned Decision.
	StatusEchoRequired

	// StatusRejected means the request must not be processed.
	StatusRejected
)

func (s Status) String() string {
	switch s {
	case StatusAccepted:
		return "accepted"
	case StatusEchoRequired:
		return "echo required"
	case StatusRejected:
		return "rejected"
	default:
		return fmt.Sprintf("Status(%d)", uint8(s))
	}
}

// Decision is the result of Guard.Check.
type Decision struct {
	Status Status
	// Echo is the current RFC 9175 Echo challenge. It is non-nil only when
	// Status is StatusEchoRequired (and, for an invalid Echo value, it can be
	// used as the replacement challenge).
	Echo []byte
	// Err explains a rejection. It is nil for StatusAccepted. For
	// StatusEchoRequired it is ErrEchoRequired or ErrEchoInvalid.
	Err error
}

// Accepted reports whether the request may be processed.
func (d Decision) Accepted() bool { return d.Status == StatusAccepted }

// Option configures a Guard.
type Option func(*Guard)

// WithEchoTTL sets how long an Echo challenge remains valid. A non-positive
// duration restores the default of 60 seconds.
func WithEchoTTL(ttl time.Duration) Option {
	return func(g *Guard) {
		if ttl > 0 {
			g.echoTTL = ttl
		}
	}
}

// WithEchoReader sets the entropy source used for Echo challenges. This is
// primarily useful for deterministic tests; production callers should use the
// default crypto/rand.Reader.
func WithEchoReader(r io.Reader) Option {
	return func(g *Guard) {
		if r != nil {
			g.echoRand = r
		}
	}
}

// WithClock sets the clock used for Echo challenge expiry. This is primarily
// useful for tests.
func WithClock(now func() time.Time) Option {
	return func(g *Guard) {
		if now != nil {
			g.now = now
		}
	}
}

// WithMaxPIV overrides the highest sequence number accepted by this guard.
// Values above the 40-bit PIV maximum are clamped to MaxPIVNumber. This option
// is intended for constrained contexts and tests; the default is the full
// 40-bit range.
func WithMaxPIV(max uint64) Option {
	return func(g *Guard) {
		if max < MaxPIVNumber {
			g.maxPIV = max
		}
	}
}

// Guard enforces OSCORE replay protection for one security context. It is
// safe for concurrent use by multiple CoAP handler goroutines.
//
// A new Guard represents a process that has no knowledge of the previous
// replay window (for example, after a restart). Until a request proves
// freshness by returning the Echo challenge from an RFC 9175 4.01 response,
// Guard refuses to process requests and returns StatusEchoRequired. The caller
// must decrypt and authenticate the OSCORE request before calling Check; the
// Echo value supplied here must be the one from the protected request, not an
// outer, unauthenticated option.
type Guard struct {
	mu sync.Mutex

	known     bool
	exhausted bool
	highest   uint64
	floor     uint64
	bitmap    uint64
	maxPIV    uint64

	challenge        []byte
	challengeExpires time.Time

	echoTTL  time.Duration
	echoRand io.Reader
	now      func() time.Time
}

// NewGuard creates a guard whose replay window is unknown. The first request
// is answered with an Echo challenge rather than processed.
func NewGuard(opts ...Option) *Guard {
	g := &Guard{
		maxPIV:   MaxPIVNumber,
		echoTTL:  defaultEchoTTL,
		echoRand: rand.Reader,
		now:      time.Now,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(g)
		}
	}
	return g
}

// DecodePIV converts a 1- to 5-byte Partial IV into its numeric value. Bytes
// are interpreted as an unsigned big-endian integer, as required by OSCORE.
func DecodePIV(piv []byte) (uint64, error) {
	if len(piv) == 0 {
		return 0, ErrPIVMissing
	}
	if len(piv) > MaxPIVBytes {
		return 0, ErrPIVTooLong
	}
	var n uint64
	for _, b := range piv {
		n = (n << 8) | uint64(b)
	}
	return n, nil
}

// EncodePIV converts a numeric PIV into its shortest non-empty big-endian
// representation.
func EncodePIV(n uint64) ([]byte, error) {
	if n > MaxPIVNumber {
		return nil, ErrPIVOutOfRange
	}
	if n == 0 {
		return []byte{0}, nil
	}
	out := make([]byte, 0, MaxPIVBytes)
	for n > 0 {
		out = append([]byte{byte(n)}, out...)
		n >>= 8
	}
	return out, nil
}

// Check validates a request's PIV and, when required, its Echo option.
//
// piv is the Partial IV from the authenticated OSCORE request. echo is the
// value of the Echo option from that same protected request, or nil if the
// option is absent.
//
// Once the window is known, a fresh PIV at or above the lower window edge is
// recorded and accepted; a PIV already seen in the window, or one lower than
// the window, is rejected. After a restart, the PIV of the request that
// carried the fresh Echo value becomes the window's lower limit (RFC 8613
// Section 7.5), so requests older than that proof cannot be replayed even if
// they would otherwise fit in the 32-entry window.
//
// The guard never reuses a sequence number after reaching its configured
// maximum: the request carrying the final PIV is accepted, after which the
// context is disabled and all later requests are rejected with
// ErrSequenceExhausted.
func (g *Guard) Check(piv, echo []byte) Decision {
	seq, err := DecodePIV(piv)
	if err != nil {
		return Decision{Status: StatusRejected, Err: err}
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.exhausted {
		return Decision{Status: StatusRejected, Err: ErrSequenceExhausted}
	}
	if seq > g.maxPIV {
		g.exhausted = true
		g.challenge = nil
		return Decision{Status: StatusRejected, Err: ErrSequenceExhausted}
	}

	if !g.known {
		return g.checkUnknownWindow(seq, echo)
	}

	if seq < g.floor {
		return Decision{Status: StatusRejected, Err: ErrBelowWindow}
	}
	if seq > g.highest {
		g.advance(seq)
		if seq == g.maxPIV {
			g.exhausted = true
			g.challenge = nil
		}
		return Decision{Status: StatusAccepted}
	}

	age := g.highest - seq
	if age >= WindowSize {
		return Decision{Status: StatusRejected, Err: ErrBelowWindow}
	}
	mask := uint64(1) << age
	if g.bitmap&mask != 0 {
		return Decision{Status: StatusRejected, Err: ErrReplay}
	}
	g.bitmap |= mask
	return Decision{Status: StatusAccepted}
}

func (g *Guard) checkUnknownWindow(seq uint64, echo []byte) Decision {
	challenge, err := g.currentChallenge()
	if err != nil {
		return Decision{Status: StatusRejected, Err: ErrEchoUnavailable}
	}
	if len(echo) == 0 {
		return Decision{Status: StatusEchoRequired, Echo: cloneBytes(challenge), Err: ErrEchoRequired}
	}
	if len(echo) > MaxEchoBytes || subtle.ConstantTimeCompare(echo, challenge) != 1 {
		return Decision{Status: StatusEchoRequired, Echo: cloneBytes(challenge), Err: ErrEchoInvalid}
	}

	// The client proved freshness. The protected Echo value cannot be copied
	// into an old captured OSCORE ciphertext by an attacker, so this request
	// establishes the new window.
	g.challenge = nil
	g.challengeExpires = time.Time{}
	g.known = true
	g.highest = seq
	g.floor = seq
	g.bitmap = 1
	if seq == g.maxPIV {
		g.exhausted = true
	}
	return Decision{Status: StatusAccepted}
}

func (g *Guard) advance(seq uint64) {
	delta := seq - g.highest
	if delta >= WindowSize {
		g.bitmap = 1
	} else {
		g.bitmap = (g.bitmap << delta) | 1
	}
	g.highest = seq
	if seq >= WindowSize {
		lower := seq - (WindowSize - 1)
		if lower > g.floor {
			g.floor = lower
		}
	}
}

func (g *Guard) currentChallenge() ([]byte, error) {
	now := g.now()
	if len(g.challenge) > 0 && now.Before(g.challengeExpires) {
		return g.challenge, nil
	}

	challenge := make([]byte, defaultEchoBytes)
	if _, err := io.ReadFull(g.echoRand, challenge); err != nil {
		return nil, err
	}
	g.challenge = challenge
	g.challengeExpires = now.Add(g.echoTTL)
	return challenge, nil
}

// ForgetWindow discards the replay window and all outstanding Echo
// challenges, returning the guard to the unknown-window state. It may be used
// when state cannot be recovered, but callers should normally restore a
// persisted window instead. An exhausted context stays disabled.
func (g *Guard) ForgetWindow() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.known = false
	g.highest = 0
	g.floor = 0
	g.bitmap = 0
	g.challenge = nil
	g.challengeExpires = time.Time{}
}

// WindowUnknown reports whether the guard still needs an Echo proof.
func (g *Guard) WindowUnknown() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return !g.known
}

// Exhausted reports whether the context has been disabled because its
// sequence-number space is used up.
func (g *Guard) Exhausted() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.exhausted
}

// HighestSeen returns the highest sequence number recorded in the window. The
// second result is false while the window is unknown.
func (g *Guard) HighestSeen() (uint64, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.highest, g.known
}

// LowerLimit returns the lowest sequence number that may still be accepted.
// After a post-restart Echo proof this is the PIV of the request that carried
// the fresh Echo value; as the window advances it follows the window's lower
// edge. The second result is false while the window is unknown.
func (g *Guard) LowerLimit() (uint64, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.floor, g.known
}

func cloneBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return append([]byte(nil), b...)
}
