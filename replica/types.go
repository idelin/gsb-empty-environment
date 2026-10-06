package replica

// Entry is a single replicated log record. Index is implicit (1-based
// position in the log); Term identifies the leader epoch that created it.
type Entry struct {
	Term    uint64 `json:"term"`
	Command string `json:"command"`
}

// AppendRequest is the leader -> follower append message ( Raft
// AppendEntries analogue for a single-node simulator).
type AppendRequest struct {
	Term         uint64  `json:"term"`         // leader's current term
	PrevIndex    uint64  `json:"prevIndex"`    // index of entry immediately before Entries
	PrevTerm     uint64  `json:"prevTerm"`     // term of entry at PrevIndex
	Entries      []Entry `json:"entries"`      // entries to append (may be empty = heartbeat)
	LeaderCommit uint64  `json:"leaderCommit"` // leader's commit index
}

// AppendReply is the follower's acknowledgement. Success is only ever true
// after the new state has been durably persisted.
type AppendReply struct {
	Term        uint64 `json:"term"`
	Success     bool   `json:"success"`
	Reason      string `json:"reason,omitempty"`
	LastIndex   uint64 `json:"lastIndex"`
	CommitIndex uint64 `json:"commitIndex"`
}

// Rejection reasons.
const (
	ReasonStaleTerm          = "stale-term"
	ReasonMissingPredecessor = "missing-predecessor"
	ReasonPrevTermMismatch   = "prev-term-mismatch"
	ReasonPersistFailed      = "persist-failed"
)
