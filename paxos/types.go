// Package paxos implements a Multi-Paxos majority-consensus replicated log
// with leader leases, hole repair, and an adaptive load-aware controller.
package paxos

// Ballot is a totally-ordered proposal number. Ties on N are broken by node ID.
type Ballot struct {
	N  uint64
	ID int
}

func (b Ballot) GreaterThan(o Ballot) bool {
	if b.N != o.N {
		return b.N > o.N
	}
	return b.ID > o.ID
}

func (b Ballot) LessThan(o Ballot) bool { return o.GreaterThan(b) }

// Entry is a single replicated log record.
type Entry struct {
	Slot      uint64
	Ballot    Ballot
	Value     string
	Committed bool
}

// NoopValue fills slots that no majority ever accepted, keeping the log
// contiguous (no holes) without inventing client data.
const NoopValue = "\x00noop"

type PrepareRequest struct {
	Ballot Ballot
}

// PrepareResponse carries the acceptor's uncommitted tail so a new leader can
// reconstruct the log: committed prefix length plus every accepted-but-
// uncommitted entry beyond it.
type PrepareResponse struct {
	OK          bool
	Promised    Ballot
	CommitIndex uint64
	Entries     []Entry
}

type AcceptRequest struct {
	Entry Entry
}

type AcceptResponse struct {
	OK       bool
	Promised Ballot
}

type CommitRequest struct {
	Entry Entry
}

type HeartbeatRequest struct {
	Ballot      Ballot
	CommitIndex uint64
}

type HeartbeatResponse struct {
	OK          bool
	CommitIndex uint64
}
