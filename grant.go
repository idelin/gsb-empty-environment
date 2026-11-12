package netsec

import (
	"sync"
	"time"
)

// GrantState is the lifecycle state of a device-authorization grant.
//
// A grant has exactly one terminal state. Once it is approved, denied or
// expired it never leaves that state, and no subsequent polling or user
// interaction can change it.
type GrantState int

const (
	// StatePending: the user has not finished the authorization interaction.
	StatePending GrantState = iota
	// StateClaimed: the user presented the short code and may still decide.
	StateClaimed
	// StateApproved is terminal: the user allowed the client.
	StateApproved
	// StateDenied is terminal: the user rejected the client.
	StateDenied
	// StateExpired is terminal: the grant's lifetime elapsed before approval.
	StateExpired
)

// String returns the RFC 8628 style name of the state.
func (s GrantState) String() string {
	switch s {
	case StatePending:
		return "pending"
	case StateClaimed:
		return "claimed"
	case StateApproved:
		return "approved"
	case StateDenied:
		return "denied"
	case StateExpired:
		return "expired"
	default:
		return "unknown"
	}
}

// IsTerminal reports whether the state is a final state.
func (s GrantState) IsTerminal() bool {
	switch s {
	case StateApproved, StateDenied, StateExpired:
		return true
	default:
		return false
	}
}

// GrantView is the immutable snapshot exposed to callers.
type GrantView struct {
	DeviceCode string
	UserCode   string
	State      GrantState
	CreatedAt  time.Time
	ExpiresAt  time.Time
	NextPollAt time.Time
	PollStep   time.Duration
}

// grant is the protected mutable record. Every field must be read and
// written while holding grant.mu. The manager's own lock is only used to
// protect its indexes, never to substitute for this per-grant lock.
type grant struct {
	mu sync.Mutex

	deviceCode string
	userCode   string
	state      GrantState

	createdAt  time.Time
	expiresAt  time.Time
	nextPollAt time.Time
	pollStep   time.Duration
}

// snapshot must be called with g.mu held.
func (g *grant) snapshot() GrantView {
	return GrantView{
		DeviceCode: g.deviceCode,
		UserCode:   g.userCode,
		State:      g.state,
		CreatedAt:  g.createdAt,
		ExpiresAt:  g.expiresAt,
		NextPollAt: g.nextPollAt,
		PollStep:   g.pollStep,
	}
}
