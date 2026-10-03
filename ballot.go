package paxos

import "fmt"

// Ballot is a totally ordered proposal number. The node ID breaks ties so
// that two concurrent leaders can never mint the same ballot.
type Ballot struct {
	N  int
	ID int
}

func (b Ballot) Less(o Ballot) bool {
	if b.N != o.N {
		return b.N < o.N
	}
	return b.ID < o.ID
}

func (b Ballot) Greater(o Ballot) bool { return o.Less(b) }

func (b Ballot) String() string { return fmt.Sprintf("(%d,%d)", b.N, b.ID) }

// Entry is a single log slot. Noop entries are used by a new leader to fill
// holes (slots that were never chosen by any majority) during Phase 1 repair.
type Entry struct {
	Index  int
	Ballot Ballot
	Value  string
	Noop   bool
}

// --- RPC messages -------------------------------------------------------

type PrepareRequest struct {
	Ballot Ballot
}

type PrepareResponse struct {
	OK       bool
	Promised Ballot
	Accepted []Entry // everything this acceptor has accepted, for hole repair
}

type AcceptRequest struct {
	Ballot  Ballot
	Entries []Entry
}

type AcceptResponse struct {
	OK       bool
	Promised Ballot
}

type CommitRequest struct {
	Ballot Ballot
	Entry  Entry
}

type CommitResponse struct {
	OK bool
}
