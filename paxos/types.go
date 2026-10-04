// Package paxos implements Multi-Paxos majority consensus with a hole-free
// log commit engine: Prepare/Promise + Accept/Accepted two-phase protocol,
// leader leases that allow skipping Phase 1, and hole repair on leader change.
package paxos

import "errors"

// Ballot is a totally-ordered proposal number: (Term, Node).
type Ballot struct {
	Term uint64
	Node int
}

// Compare returns -1, 0 or 1.
func (b Ballot) Compare(o Ballot) int {
	switch {
	case b.Term != o.Term:
		if b.Term < o.Term {
			return -1
		}
		return 1
	case b.Node != o.Node:
		if b.Node < o.Node {
			return -1
		}
		return 1
	default:
		return 0
	}
}

func (b Ballot) IsZero() bool { return b.Term == 0 && b.Node == 0 }

// Entry is a single log slot.
type Entry struct {
	Slot   uint64
	Ballot Ballot
	Value  []byte
}

// Noop is the reserved value used to fill holes during repair.
var Noop = []byte("\x00paxos-noop")

// IsNoop reports whether v is a repair no-op value.
func IsNoop(v []byte) bool { return string(v) == string(Noop) }

// PrepareRequest is Phase 1a. FromSlot limits the accepted entries the
// promisee reports back (entries below FromSlot are already committed).
type PrepareRequest struct {
	Ballot   Ballot
	FromSlot uint64
}

// Promise is Phase 1b.
type Promise struct {
	Ok       bool
	Promised Ballot  // current promised ballot (also on rejection)
	Accepted []Entry // accepted entries with Slot >= request.FromSlot
}

// AcceptRequest is Phase 2a.
type AcceptRequest struct {
	Entry Entry
}

// Accepted is Phase 2b.
type Accepted struct {
	Ok       bool
	Promised Ballot
}

// CommitNotice tells a follower that Entry is committed (chosen by a majority).
type CommitNotice struct {
	Entry Entry
}

var (
	// ErrNotLeader is returned by Propose on non-leaders or expired leases.
	ErrNotLeader = errors.New("paxos: not leader (or lease expired)")
	// ErrNoQuorum is returned when a majority cannot be reached.
	ErrNoQuorum = errors.New("paxos: no quorum")
	// ErrUnreachable is returned by the network for partitioned links.
	ErrUnreachable = errors.New("paxos: peer unreachable")
)
