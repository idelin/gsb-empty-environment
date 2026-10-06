package replica

import (
	"errors"
	"fmt"
	"sync"
)

// ErrCorruption marks protocol violations that indicate data corruption:
// overwriting a committed entry, or two different commands recorded under
// the same (index, term).
var ErrCorruption = errors.New("log corruption")

// Replica is a single-node log replica (follower). Indices are 1-based.
type Replica struct {
	mu      sync.Mutex
	store   *Store
	term    uint64
	commit  uint64
	entries []Entry
	rec     *Recorder
}

// OpenReplica recovers a replica from its durable store.
func OpenReplica(store *Store) (*Replica, error) {
	meta, entries, err := store.Load()
	if err != nil {
		return nil, err
	}
	return &Replica{store: store, term: meta.Term, commit: meta.Commit, entries: entries}, nil
}

// SetRecorder attaches a trace recorder (optional).
func (r *Replica) SetRecorder(rec *Recorder) { r.rec = rec }

// State returns a snapshot of the replica's in-memory state.
func (r *Replica) State() (term, commit, lastIndex uint64, entries []Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]Entry, len(r.entries))
	copy(cp, r.entries)
	return r.term, r.commit, uint64(len(r.entries)), cp
}

// HandleAppend processes one AppendRequest. It never acknowledges (returns
// Success=true) before the resulting state is durably persisted.
func (r *Replica) HandleAppend(req AppendRequest) (rep AppendReply, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	defer func() {
		if r.rec != nil {
			errStr := ""
			if err != nil {
				errStr = err.Error()
			}
			r.rec.Add(StepAppend, &req, &rep, errStr)
		}
	}()

	rep.Term = r.term
	rep.LastIndex = uint64(len(r.entries))
	rep.CommitIndex = r.commit

	// 1. Reject messages from old terms.
	if req.Term < r.term {
		rep.Reason = ReasonStaleTerm
		return rep, nil
	}

	// 2. Predecessor must exist and match.
	if req.PrevIndex > uint64(len(r.entries)) {
		rep.Reason = ReasonMissingPredecessor
		return rep, nil
	}
	if req.PrevIndex >= 1 {
		prevTerm := r.entries[req.PrevIndex-1].Term
		if prevTerm != req.PrevTerm {
			if req.PrevIndex <= r.commit {
				return rep, fmt.Errorf("%w: committed entry %d (term %d) contradicted by prevTerm %d",
					ErrCorruption, req.PrevIndex, prevTerm, req.PrevTerm)
			}
			rep.Reason = ReasonPrevTermMismatch
			return rep, nil
		}
	}

	// 3. Merge entries: skip duplicates, truncate conflicting suffix.
	newEntries := r.entries
	dirty := req.Term != r.term
	for i, e := range req.Entries {
		idx := req.PrevIndex + 1 + uint64(i)
		if idx <= uint64(len(newEntries)) {
			exist := newEntries[idx-1]
			switch {
			case exist.Term == e.Term && exist.Command == e.Command:
				continue // duplicate delivery, already have it
			case exist.Term == e.Term:
				return rep, fmt.Errorf("%w: index %d term %d holds %q, cannot become %q",
					ErrCorruption, idx, e.Term, exist.Command, e.Command)
			default:
				if idx <= r.commit {
					return rep, fmt.Errorf("%w: cannot truncate committed entry %d (term %d -> %d)",
						ErrCorruption, idx, exist.Term, e.Term)
				}
				// Drop conflicting uncommitted suffix. Copy so a failed
				// save cannot mutate the live log's backing array.
				newEntries = append([]Entry{}, newEntries[:idx-1]...)
			}
		}
		newEntries = append(newEntries, e)
		dirty = true
	}

	// 4. Advance commit (never regresses, never past last index).
	newCommit := r.commit
	lastIndex := uint64(len(newEntries))
	if req.LeaderCommit > newCommit {
		newCommit = min(req.LeaderCommit, lastIndex)
	}
	if newCommit != r.commit {
		dirty = true
	}

	// 5. Persist before acknowledging. On failure no ack is sent and the
	//    in-memory state is left untouched.
	if dirty {
		meta := Meta{Term: req.Term, Commit: newCommit}
		if err := r.store.Save(meta, newEntries); err != nil {
			rep.Reason = ReasonPersistFailed
			return rep, err
		}
		r.term = req.Term
		r.entries = newEntries
		r.commit = newCommit
	}

	rep.Term = r.term
	rep.Success = true
	rep.LastIndex = uint64(len(r.entries))
	rep.CommitIndex = r.commit
	return rep, nil
}
