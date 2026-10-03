// Package paxos implements a Multi-Paxos majority-consensus replicated log
// with leader leases, hole repair, and fine-grained metrics instrumentation.
package paxos

import "fmt"

// ErrCode is a machine-readable classification of every failure path in the
// consensus engine. All error codes are counted in the node metrics.
type ErrCode int

const (
	ErrNone ErrCode = iota
	// ErrBallotTooLow: a Prepare/Accept was rejected because the acceptor has
	// already promised a higher ballot.
	ErrBallotTooLow
	// ErrNotLeader: a propose fast-path was attempted without leadership.
	ErrNotLeader
	// ErrNoQuorum: a phase failed to collect a majority of responses.
	ErrNoQuorum
	// ErrLeaseExpired: the leader lease was not valid when consulted.
	ErrLeaseExpired
	// ErrPartition: the transport could not deliver a message (network cut).
	ErrPartition
	// ErrStaleCommit: an Accept targeted an already-committed slot.
	ErrStaleCommit
	// ErrConflictCommit: a Commit tried to overwrite a different committed value.
	ErrConflictCommit
	// ErrTimeout: an operation exceeded its deadline.
	ErrTimeout
)

var errNames = map[ErrCode]string{
	ErrNone:           "NONE",
	ErrBallotTooLow:   "BALLOT_TOO_LOW",
	ErrNotLeader:      "NOT_LEADER",
	ErrNoQuorum:       "NO_QUORUM",
	ErrLeaseExpired:   "LEASE_EXPIRED",
	ErrPartition:      "PARTITION",
	ErrStaleCommit:    "STALE_COMMIT",
	ErrConflictCommit: "CONFLICT_COMMIT",
	ErrTimeout:        "TIMEOUT",
}

func (c ErrCode) String() string {
	if s, ok := errNames[c]; ok {
		return s
	}
	return fmt.Sprintf("UNKNOWN(%d)", int(c))
}

// Error is a consensus failure carrying an ErrCode.
type Error struct {
	Code ErrCode
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Msg) }

func errf(code ErrCode, format string, args ...interface{}) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// Ballot is a totally-ordered proposal number: (Epoch, Node).
type Ballot struct {
	Epoch uint64 `json:"epoch"`
	Node  int    `json:"node"`
}

func (b Ballot) Less(o Ballot) bool {
	return b.Epoch < o.Epoch || (b.Epoch == o.Epoch && b.Node < o.Node)
}

func (b Ballot) Equal(o Ballot) bool { return b.Epoch == o.Epoch && b.Node == o.Node }

func (b Ballot) String() string { return fmt.Sprintf("(%d,%d)", b.Epoch, b.Node) }

// Entry is a single log record. Noop entries fill holes so the committed
// prefix is always contiguous.
type Entry struct {
	Slot   uint64 `json:"slot"`
	Value  []byte `json:"value,omitempty"`
	Noop   bool   `json:"noop,omitempty"`
	Ballot Ballot `json:"ballot"` // ballot under which the value was accepted
}

func (e Entry) equalValue(o Entry) bool {
	if e.Noop != o.Noop || len(e.Value) != len(o.Value) {
		return false
	}
	for i := range e.Value {
		if e.Value[i] != o.Value[i] {
			return false
		}
	}
	return true
}

// slot is the per-slot acceptor state.
type slot struct {
	accepted    Entry
	hasAccepted bool
	committed   bool
}

// Promise is the Phase-1b reply: it carries everything a new leader needs to
// safely repair the log.
type Promise struct {
	Ballot      Ballot           `json:"ballot"`
	From        int              `json:"from"`
	CommitIndex uint64           `json:"commitIndex"`
	Committed   []Entry          `json:"committed,omitempty"` // committed entries the leader may lack
	Accepted    map[uint64]Entry `json:"accepted,omitempty"`  // uncommitted accepted entries
}
