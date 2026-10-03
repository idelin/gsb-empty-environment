// Package paxos implements a Multi-Paxos majority-consensus replicated log
// engine with a no-hole commit rule.
//
// Core semantics:
//   - Phase 1 (Prepare/Promise): a candidate gathers a majority of promises
//     and merges the highest-ballot accepted entry for every log slot.
//   - Phase 2 (Accept/Accepted): the leader replicates entries and commits a
//     slot only after a majority accepted it in the leader's current ballot.
//   - Leader lease: while the lease is valid the leader bypasses Phase 1 and
//     appends consecutive log entries directly with Phase 2.
//   - Gap repair: a new leader back-fills every uncommitted slot discovered
//     during Phase 1 (recovered value or no-op) before the commit index may
//     advance past it, so the committed prefix never contains holes.
//
// All time is driven by a deterministic, per-node tick clock; there are no
// goroutines or wall-clock dependencies inside the package.
package paxos

// Role is the protocol role of a node.
type Role int

const (
	Follower Role = iota
	Candidate
	Leader
)

func (r Role) String() string {
	switch r {
	case Follower:
		return "Follower"
	case Candidate:
		return "Candidate"
	case Leader:
		return "Leader"
	}
	return "Unknown"
}

// Ballot is a totally-ordered proposal number. The node id breaks ties so
// that two nodes never generate the same ballot.
type Ballot struct {
	N  uint64
	ID int
}

// Greater reports whether b is strictly greater than o.
func (b Ballot) Greater(o Ballot) bool {
	if b.N != o.N {
		return b.N > o.N
	}
	return b.ID > o.ID
}

// Less reports whether b is strictly smaller than o.
func (b Ballot) Less(o Ballot) bool { return o.Greater(b) }

// Entry is a single replicated log slot. A zero Ballot (N == 0) marks an
// uncommitted placeholder hole inside an acceptor log; it is never committed.
// A nil Cmd with a non-zero Ballot is a committed no-op filler.
type Entry struct {
	Ballot Ballot
	Cmd    []byte
}

func (e Entry) isHole() bool { return e.Ballot.N == 0 }

func cloneLog(log []Entry) []Entry {
	out := make([]Entry, len(log))
	copy(out, log)
	return out
}
