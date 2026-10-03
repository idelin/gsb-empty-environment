// Package paxos implements a Multi-Paxos majority-consensus replicated log
// with leader leases (Phase-1 bypass) and hole repair.
package paxos

import "fmt"

// ProposalID is a Paxos ballot. Ordered by (Number, NodeID).
type ProposalID struct {
	Number uint64
	NodeID string
}

// Compare returns -1, 0 or 1.
func (p ProposalID) Compare(o ProposalID) int {
	if p.Number != o.Number {
		if p.Number < o.Number {
			return -1
		}
		return 1
	}
	switch {
	case p.NodeID < o.NodeID:
		return -1
	case p.NodeID > o.NodeID:
		return 1
	}
	return 0
}

func (p ProposalID) String() string { return fmt.Sprintf("{%d %s}", p.Number, p.NodeID) }

// NoOpValue fills log holes left by uncommitted minority proposals.
var NoOpValue = []byte("\x00noop")

// Slot is a single log position.
type Slot struct {
	Index     uint64
	Ballot    ProposalID
	Value     []byte
	Committed bool
}

// Request / Response are the wire messages exchanged through the Network.
type Request interface{ isRequest() }
type Response interface{ isResponse() }

// PrepareRequest is Phase 1a. It covers the whole log (Multi-Paxos).
type PrepareRequest struct {
	Ballot ProposalID
	From   string
}

// Promise is Phase 1b. Entries carries the acceptor's full local log so the
// new leader can repair holes and adopt the highest-ballot value per slot.
type Promise struct {
	From        string
	Ballot      ProposalID
	OK          bool
	Entries     map[uint64]Slot
	CommitIndex uint64
}

// AcceptRequest is Phase 2a.
type AcceptRequest struct {
	Slot   uint64
	Ballot ProposalID
	Value  []byte
	From   string
}

// Accepted is Phase 2b.
type Accepted struct {
	From   string
	Slot   uint64
	Ballot ProposalID
	OK     bool
}

// CommitRequest notifies followers that a slot is committed.
type CommitRequest struct {
	Slot        uint64
	CommitIndex uint64
	From        string
}

func (*PrepareRequest) isRequest() {}
func (*AcceptRequest) isRequest()  {}
func (*CommitRequest) isRequest()  {}
func (Promise) isResponse()        {}
func (Accepted) isResponse()       {}
