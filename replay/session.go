package replay

import (
	"errors"
	"fmt"
	"time"
)

// RejectReason classifies why a packet was refused.
type RejectReason int

const (
	// RejectNone means the packet was accepted.
	RejectNone RejectReason = iota
	// RejectNoSession means no live session slot matches the index.
	RejectNoSession
	// RejectForge means the packet failed cryptographic authentication.
	RejectForge
	// RejectExpired means the session is older than RejectAfterTime.
	RejectExpired
	// RejectMessageCap means RejectAfterMessages has been reached.
	RejectMessageCap
	// RejectReplay means the counter is below the window floor or its
	// bitmap bit was already set.
	RejectReplay
)

// String returns a stable diagnostic label.
func (r RejectReason) String() string {
	switch r {
	case RejectNone:
		return "none"
	case RejectNoSession:
		return "no-session"
	case RejectForge:
		return "forged"
	case RejectExpired:
		return "expired"
	case RejectMessageCap:
		return "message-cap"
	case RejectReplay:
		return "replay"
	default:
		return fmt.Sprintf("reject-%d", int(r))
	}
}

// Sentinel errors, one per reject reason. A zero-reason result carries a
// nil error.
var (
	ErrNoSession  = errors.New("replay: no session for receive index")
	ErrForge      = errors.New("replay: packet failed authentication")
	ErrExpired    = errors.New("replay: session past RejectAfterTime")
	ErrMessageCap = errors.New("replay: session reached RejectAfterMessages")
	ErrReplay     = errors.New("replay: duplicate or out-of-window sequence number")
	reasonToErr   = [...]error{RejectNoSession: ErrNoSession, RejectForge: ErrForge, RejectExpired: ErrExpired, RejectMessageCap: ErrMessageCap, RejectReplay: ErrReplay}
)

// SessionConfig parameterizes a session generation.
type SessionConfig struct {
	// Index is the 32-bit receive index identifying this generation.
	Index uint32
	// RejectAfterMessages caps the number of messages that may be
	// accepted on the session. Zero means no count limit; the sequence
	// number ceiling of 2^64-RejectAfterMessages is also enforced.
	RejectAfterMessages uint64
	// RejectAfterTime is the lifetime of the session measured from its
	// creation time. Zero means sessions never time out.
	RejectAfterTime time.Duration
	// WindowWidth is the anti-replay bitmap width W in bits; it must be
	// a positive multiple of 64.
	WindowWidth uint64
	// Now supplies the clock. If nil, time.Now is used.
	Now func() time.Time
}

// Session is one key generation with its own independent replay window.
// A Session is not safe for concurrent use; [KeyRing] serializes access.
type Session struct {
	index               uint32
	rejectAfterMessages uint64
	rejectAfterTime     time.Duration
	createdAt           time.Time
	window              *Window
	accepted            uint64
	now                 func() time.Time
}

// NewSession constructs a session and its empty replay window.
func NewSession(cfg SessionConfig) (*Session, error) {
	win, err := NewWindow(cfg.WindowWidth)
	if err != nil {
		return nil, err
	}
	if cfg.RejectAfterTime < 0 {
		return nil, fmt.Errorf("replay: RejectAfterTime must be non-negative, got %s", cfg.RejectAfterTime)
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Session{
		index:               cfg.Index,
		rejectAfterMessages: cfg.RejectAfterMessages,
		rejectAfterTime:     cfg.RejectAfterTime,
		createdAt:           now(),
		window:              win,
		now:                 now,
	}, nil
}

// Index returns the 32-bit receive index of the session.
func (s *Session) Index() uint32 { return s.index }

// Window exposes the independent anti-replay window for inspection.
func (s *Session) Window() *Window { return s.window }

// Accepted returns the number of messages accepted on this session.
func (s *Session) Accepted() uint64 { return s.accepted }

// CreatedAt returns the session creation timestamp.
func (s *Session) CreatedAt() time.Time { return s.createdAt }

// Expired reports whether the session age has reached RejectAfterTime.
func (s *Session) Expired() bool {
	if s.rejectAfterTime == 0 {
		return false
	}
	return s.now().Sub(s.createdAt) >= s.rejectAfterTime
}

// Process applies every receive-side gate to seq in order: expiry,
// message cap (including the 2^64 sequence-number ceiling), then the
// anti-replay window. The window is only consulted for fresh packets, so
// rejected packets never mutate replay state.
func (s *Session) Process(seq uint64) RejectReason {
	if s.Expired() {
		return RejectExpired
	}

	// RejectAfterMessages counts accepted messages; the same limit also
	// defines the sequence-number ceiling 2^64-limit when non-zero.
	if s.rejectAfterMessages != 0 {
		if s.accepted >= s.rejectAfterMessages || seq >= ^uint64(0)-s.rejectAfterMessages+1 {
			return RejectMessageCap
		}
	}

	if !s.window.Check(seq) {
		return RejectReplay
	}

	s.accepted++
	return RejectNone
}
