package paxos

import "errors"

// Errors returned by Propose.
var (
	// ErrNotLeader is returned when this node is not the leader and could
	// not become one (no quorum reachable).
	ErrNotLeader = errors.New("paxos: not leader")
	// ErrNoQuorum is returned when a majority could not be reached.
	ErrNoQuorum = errors.New("paxos: no quorum")
	// ErrBackpressure is returned when the proposal intake queue is full.
	ErrBackpressure = errors.New("paxos: proposal queue full")
	// ErrClosed is returned when the node has been closed.
	ErrClosed = errors.New("paxos: node closed")
)

// Entry is a single log record. Ballot records the ballot under which the
// entry was accepted; Noop marks filler entries used to repair holes.
type Entry struct {
	Ballot uint64
	Value  []byte
	Noop   bool
}

// IndexedEntry pairs an Entry with its log index, used in Commit messages
// so lagging followers can catch up and dirty minority slots can be
// safely overwritten.
type IndexedEntry struct {
	Index uint64
	Entry Entry
}

// AcceptedSlot is the acceptor state a Promise reports for one log index.
type AcceptedSlot struct {
	Ballot uint64 // ballot under which the entry was accepted
	Entry  Entry
}

// PrepareMsg is a Phase 1a request.
type PrepareMsg struct {
	From   int
	Ballot uint64
}

// PromiseMsg is a Phase 1b response. Accepted contains every slot the
// acceptor has ever accepted (keyed by 1-based log index).
type PromiseMsg struct {
	From     int
	OK       bool
	Ballot   uint64 // acceptor's current promised ballot
	MaxIndex uint64
	Accepted map[uint64]AcceptedSlot
}

// AcceptMsg is a Phase 2a request.
type AcceptMsg struct {
	From   int
	Ballot uint64
	Index  uint64
	Entry  Entry
}

// AcceptedMsg is a Phase 2b response.
type AcceptedMsg struct {
	From   int
	OK     bool
	Ballot uint64 // acceptor's current promised ballot
}

// CommitMsg notifies followers of the leader's commit index and carries
// the committed prefix so partitioned followers can repair and apply.
type CommitMsg struct {
	From        int
	Ballot      uint64
	CommitIndex uint64
	Entries     []IndexedEntry // committed entries 1..CommitIndex
}
