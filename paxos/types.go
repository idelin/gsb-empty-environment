// Package paxos implements Multi-Paxos majority consensus with a gap-free
// log commit engine: Prepare/Promise + Accept/Accepted two-phase protocol,
// a leader lease that lets a stable leader bypass Phase 1 for consecutive
// log entries, and hole repair for uncommitted slots.
package paxos

import (
	"fmt"

	"multipaxos/store"
)

// Ballot is a totally ordered proposal number: (Term, Node).
type Ballot struct {
	Term uint64
	Node int
}

// Compare returns -1, 0 or 1 as b is less than, equal to, or greater than o.
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
	}
	return 0
}

func (b Ballot) String() string { return fmt.Sprintf("(%d,%d)", b.Term, b.Node) }

// FlagNoop marks entries that a new leader writes to fill log holes.
const FlagNoop uint32 = 1

// MsgType enumerates protocol messages.
type MsgType int

const (
	MsgPrepare MsgType = iota
	MsgPromise
	MsgAccept
	MsgAccepted
	MsgCommit
)

func (t MsgType) String() string {
	switch t {
	case MsgPrepare:
		return "Prepare"
	case MsgPromise:
		return "Promise"
	case MsgAccept:
		return "Accept"
	case MsgAccepted:
		return "Accepted"
	case MsgCommit:
		return "Commit"
	}
	return "?"
}

// Message is the single envelope for all protocol traffic.
type Message struct {
	Type MsgType
	From int
	To   int

	Ballot Ballot // proposal number of the round
	Slot   uint64 // Accept/Accepted: slot being accepted

	MinSlot uint64        // Prepare: send accepted entries >= MinSlot
	Entry   store.Entry   // Accept/Commit: the value
	Entries []store.Entry // Promise: accepted entries >= MinSlot

	CommitIndex uint64 // Commit: leader's commit index
	OK          bool   // Promise/Accepted: false = rejection (Ballot = promised)
}
