// Package paxos implements Multi-Paxos majority consensus with a gap-free
// log commit engine.
//
// The protocol runs the classic two phases:
//
//	Phase 1 (Prepare/Promise): a would-be leader picks a ballot higher than
//	any it has seen and asks a majority to promise not to accept older
//	ballots. Promises carry the acceptor's accepted values so the leader can
//	re-propose any value that may already be chosen (safety) and fill holes
//	with no-ops (gap repair).
//
//	Phase 2 (Accept/Accepted): the leader proposes values for concrete log
//	slots. A slot is committed once a majority of acceptors has accepted it.
//
// While a leader holds a valid lease it skips Phase 1 entirely and commits
// consecutive log entries with Phase 2 only.
package paxos

// Ballot is a Paxos proposal number. Ballots are totally ordered: first by
// round N, then by the node ID that minted them.
type Ballot struct {
	N  uint64
	ID int
}

// Less reports whether b is strictly lower than o.
func (b Ballot) Less(o Ballot) bool {
	return b.N < o.N || (b.N == o.N && b.ID < o.ID)
}

// Greater reports whether b is strictly higher than o.
func (b Ballot) Greater(o Ballot) bool { return o.Less(b) }

// EntryState is the lifecycle state of a log entry.
type EntryState uint8

const (
	// StateAccepted means the entry was accepted by this node but is not
	// known to be chosen by a majority.
	StateAccepted EntryState = iota
	// StateCommitted means the entry is chosen by a majority and will never
	// change.
	StateCommitted
)

// LogEntry is a single slot in the replicated log. A nil Value is a no-op
// entry used to plug holes during gap repair.
type LogEntry struct {
	Slot   uint64
	Ballot Ballot
	Value  []byte
	State  EntryState
}

// IsNoop reports whether the entry is a no-op hole filler.
func (e *LogEntry) IsNoop() bool { return e.Value == nil }

// PrepareReq is a Phase 1 request covering every slot >= From.
type PrepareReq struct {
	Ballot Ballot
	From   uint64
}

// SlotInfo reports the acceptor state of one slot inside a promise.
type SlotInfo struct {
	Slot      uint64
	Ballot    Ballot
	Value     []byte
	Committed bool
}

// PromiseResp is a Phase 1 response.
type PromiseResp struct {
	OK          bool
	Promised    Ballot
	CommitIndex uint64
	MaxSlot     uint64
	Slots       []SlotInfo
}

// AcceptReq is a Phase 2 request for a single slot.
type AcceptReq struct {
	Ballot Ballot
	Slot   uint64
	Value  []byte
}

// AcceptedResp is a Phase 2 response.
type AcceptedResp struct {
	OK       bool
	Promised Ballot
}

// CommitReq informs a follower that Slot is committed and reports the
// leader's current commit index so lagging followers can catch up.
type CommitReq struct {
	Ballot      Ballot
	Slot        uint64
	CommitIndex uint64
}

// SyncReq pulls committed entries [From, ...] from a peer.
type SyncReq struct {
	From uint64
}

// SyncResp carries committed entries for log catch-up.
type SyncResp struct {
	Ballot      Ballot
	CommitIndex uint64
	Entries     []LogEntry
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	c := make([]byte, len(b))
	copy(c, b)
	return c
}
