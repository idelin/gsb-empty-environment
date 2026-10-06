// Package replsim implements a single-node log replication simulator.
//
// The node accepts AppendRequests from leaders of different terms. It
// matches the predecessor (PrevIndex/PrevTerm), truncates any conflicting
// uncommitted suffix, and appends new entries. A committed prefix is never
// truncated, requests from older terms are rejected, and an entry that
// arrives with the same index and term but different content is reported
// as corruption. An append is acknowledged only after the new state has
// been persisted to Storage.
package replsim

import "sync"

// Entry is a single log record. Its index is implicit: log[i] has index i+1.
type Entry struct {
	Term uint64
	Data string
}

// AppendRequest is the leader-to-follower append message.
type AppendRequest struct {
	Term         uint64
	PrevIndex    uint64
	PrevTerm     uint64
	Entries      []Entry
	LeaderCommit uint64
}

// RejectReason explains why an AppendRequest was not acknowledged.
type RejectReason string

const (
	RejectNone               RejectReason = ""
	RejectStaleTerm          RejectReason = "stale-term"
	RejectMissingPredecessor RejectReason = "missing-predecessor"
	RejectPrevTermMismatch   RejectReason = "prev-term-mismatch"
	RejectCommittedConflict  RejectReason = "committed-conflict"
	RejectCorruption         RejectReason = "corruption"
	RejectStorageFailure     RejectReason = "storage-failure"
)

// AppendResponse is the follower reply. Success is true only after the
// state implied by the request has been persisted.
type AppendResponse struct {
	Term       uint64
	Success    bool
	Reason     RejectReason
	MatchIndex uint64
}

// Node is a single replicated log follower.
type Node struct {
	mu          sync.Mutex
	storage     Storage
	term        uint64
	log         []Entry
	commitIndex uint64
}

// NewNode creates a Node whose initial state is loaded from storage.
func NewNode(storage Storage) (*Node, error) {
	state, err := storage.Load()
	if err != nil {
		return nil, err
	}
	return &Node{
		storage:     storage,
		term:        state.CurrentTerm,
		log:         cloneEntries(state.Log),
		commitIndex: state.CommitIndex,
	}, nil
}

// Term returns the node's current term.
func (n *Node) Term() uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.term
}

// CommitIndex returns the node's commit index.
func (n *Node) CommitIndex() uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.commitIndex
}

// Log returns a copy of the node's log.
func (n *Node) Log() []Entry {
	n.mu.Lock()
	defer n.mu.Unlock()
	return cloneEntries(n.log)
}

func (n *Node) lastIndex() uint64 { return uint64(len(n.log)) }

func (n *Node) reject(reason RejectReason) AppendResponse {
	return AppendResponse{Term: n.term, Reason: reason}
}

func (n *Node) persist() error {
	return n.storage.Store(State{
		CurrentTerm: n.term,
		Log:         n.log,
		CommitIndex: n.commitIndex,
	})
}

// HandleAppend processes one AppendRequest. It never acknowledges an
// append whose state has not been persisted.
func (n *Node) HandleAppend(req AppendRequest) AppendResponse {
	n.mu.Lock()
	defer n.mu.Unlock()

	if req.Term < n.term {
		return n.reject(RejectStaleTerm)
	}
	if req.Term > n.term {
		n.term = req.Term
		if err := n.persist(); err != nil {
			return n.reject(RejectStorageFailure)
		}
	}

	if req.PrevIndex > n.lastIndex() {
		return n.reject(RejectMissingPredecessor)
	}
	if req.PrevIndex > 0 && n.log[req.PrevIndex-1].Term != req.PrevTerm {
		return n.reject(RejectPrevTermMismatch)
	}

	for i, entry := range req.Entries {
		idx := req.PrevIndex + uint64(i) + 1
		if idx > n.lastIndex() {
			n.log = append(n.log, req.Entries[i:]...)
			break
		}
		existing := n.log[idx-1]
		if existing.Term == entry.Term {
			if existing.Data != entry.Data {
				return n.reject(RejectCorruption)
			}
			continue
		}
		if idx <= n.commitIndex {
			return n.reject(RejectCommittedConflict)
		}
		n.log = append(n.log[:idx-1:idx-1], req.Entries[i:]...)
		break
	}

	if req.LeaderCommit > n.commitIndex {
		n.commitIndex = minUint64(req.LeaderCommit, n.lastIndex())
	}

	if err := n.persist(); err != nil {
		return n.reject(RejectStorageFailure)
	}
	return AppendResponse{
		Term:       n.term,
		Success:    true,
		MatchIndex: req.PrevIndex + uint64(len(req.Entries)),
	}
}

func minUint64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

func cloneEntries(src []Entry) []Entry {
	if src == nil {
		return nil
	}
	dst := make([]Entry, len(src))
	copy(dst, src)
	return dst
}
