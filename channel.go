// Package netsec implements a recovery-channel (for example, recovery email)
// rebinding transaction for an identity host.
//
// A replacement never grants recovery power to an unverified new channel: a
// change must be confirmed separately through both the old channel and the new
// channel, and the two confirmations must bind the same change digest and both
// occur before the same deadline. Only when both confirmations are present does
// the active recovery channel switch atomically.
package netsec

import "errors"

// Side identifies which channel a confirmation token is bound to.
type Side string

const (
	// SideOld is the currently active recovery channel.
	SideOld Side = "old"
	// SideNew is the proposed replacement channel.
	SideNew Side = "new"
)

// Channel is an addressable recovery channel, such as a recovery email.
type Channel struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// Valid reports whether the channel is fully specified.
func (c Channel) Valid() bool {
	return c.Kind != "" && c.Value != ""
}

// Equal reports whether two channels are identical.
func (c Channel) Equal(other Channel) bool {
	return c == other
}

// Outcome records how a transaction reached its terminal state. Archived
// outcomes let late, lost-and-replayed proofs return precise errors instead of
// being silently treated as unknown requests.
type Outcome string

const (
	OutcomeSwitched   Outcome = "switched"
	OutcomeCanceled   Outcome = "canceled"
	OutcomeSuperseded Outcome = "superseded"
)

// Transaction is a single pending rebinding change.
type Transaction struct {
	// Digest is the change summary every proof (old and new) must bind.
	Digest string  `json:"digest"`
	Old    Channel `json:"old"`
	New    Channel `json:"new"`
	// ExpiresNano is the single deadline shared by both confirmations.
	ExpiresNano  int64         `json:"expires_nano"`
	OldConfirmed *Confirmation `json:"old_confirmed,omitempty"`
	NewConfirmed *Confirmation `json:"new_confirmed,omitempty"`
}

// Confirmation records one side's proof, including the digest it is bound to.
type Confirmation struct {
	Digest     string `json:"digest"`
	Side       Side   `json:"side"`
	AtNano     int64  `json:"at_nano"`
	ExpireNano int64  `json:"expire_nano"`
}

// Archive is a terminal transaction retained so stale proofs stay interpretable.
type Archive struct {
	Digest      string  `json:"digest"`
	Old         Channel `json:"old"`
	New         Channel `json:"new"`
	Outcome     Outcome `json:"outcome"`
	CompletedAt int64   `json:"completed_at_nano"`
	ExpiresNano int64   `json:"expires_nano"`
}

// State is the full durable state of one user's recovery channel.
//
// Invariant: Active is always a usable recovery channel. While Pending is
// non-nil, Active remains the old channel until the transaction switches, so no
// intermediate state ever leaves the user without a definite recovery channel.
type State struct {
	Version uint64       `json:"version"`
	Active  Channel      `json:"active"`
	Pending *Transaction `json:"pending,omitempty"`
	Past    []Archive    `json:"past,omitempty"`
}

// Domain events emitted as the state machine advances. Each command produces at
// most one event; tests can observe them to reason about event orderings.
type Event struct {
	Type        string
	AtNano      int64
	Superseded  string // digest replaced by ChangeBegun, empty otherwise
	Digest      string
	Old         Channel
	New         Channel
	ExpiresNano int64
	Outcome     Outcome
	// Switch is set on the confirm event that completes the two-sided proof.
	Switch *Event
}

const (
	EventChangeBegun     = "change_begun"
	EventChangeConfirmed = "change_confirmed"
	EventChannelSwitched = "channel_switched"
	EventChangeCanceled  = "change_canceled"
)

// Sentinel errors returned by the state machine and manager.
var (
	// ErrNoState indicates the user has no provisioned recovery state.
	ErrNoState = errors.New("netsec: recovery state not provisioned")
	// ErrNoPending indicates there is no in-flight change to act on.
	ErrNoPending = errors.New("netsec: no pending channel change")
	// ErrProofRevoked indicates the proof's transaction was canceled or
	// superseded by a later change, so the proof must never be honored.
	ErrProofRevoked = errors.New("netsec: confirmation proof revoked")
	// ErrProofExpired indicates the proof is past its deadline.
	ErrProofExpired = errors.New("netsec: confirmation proof expired")
	// ErrDigestMismatch indicates the proof binds a different change digest.
	ErrDigestMismatch = errors.New("netsec: change digest mismatch")
	// ErrSideMismatch indicates a token was presented through the wrong side.
	ErrSideMismatch = errors.New("netsec: confirmation side mismatch")
	// ErrAlreadyComplete indicates the proof's change already finished.
	ErrAlreadyComplete = errors.New("netsec: change already complete")
	// ErrInvalidChannel indicates a malformed or identical channel argument.
	ErrInvalidChannel = errors.New("netsec: invalid recovery channel")
	// ErrConflict indicates the optimistic write lost a concurrent race.
	ErrConflict = errors.New("netsec: concurrent state conflict")
	// ErrStateVersion indicates an unexpected storage version was observed.
	ErrStateVersion = errors.New("netsec: storage version mismatch")
)
