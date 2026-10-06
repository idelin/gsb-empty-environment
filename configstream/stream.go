// Package configstream implements an atomic, revisioned configuration
// stream receiver.
//
// A configuration update is identified by a revision number and is
// transported as one or more sequenced chunks of changes followed by a
// commit marker carrying the expected chunk count. Staged changes only
// take effect when the commit marker has arrived and every sequence
// number in [0, chunks) is present; a missing sequence number causes
// the receiver to wait instead of applying a partial update.
//
// Replayed events for an already committed revision are validated for
// consistency against a digest of the originally applied change set
// instead of being re-applied. Disconnecting discards all uncommitted
// staging.
package configstream

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"sync"
)

// Op is the kind of a single configuration change.
type Op int

const (
	OpPut Op = iota
	OpDelete
)

func (o Op) String() string {
	if o == OpDelete {
		return "delete"
	}
	return "put"
}

// Change is a single key-level mutation within a revision.
type Change struct {
	Op    Op
	Key   string
	Value string // OpPut only
}

// InconsistentReplayError is returned when events replayed for an
// already committed revision do not match the change set that was
// originally applied for that revision.
type InconsistentReplayError struct {
	Revision uint64
}

func (e *InconsistentReplayError) Error() string {
	return fmt.Sprintf("configstream: replayed changes for revision %d are inconsistent with the committed revision", e.Revision)
}

// UnknownRevisionError is returned when a commit marker references a
// revision at or below the committed revision for which no digest was
// recorded (for example because a snapshot superseded it).
type UnknownRevisionError struct {
	Revision uint64
}

func (e *UnknownRevisionError) Error() string {
	return fmt.Sprintf("configstream: no committed record for replayed revision %d", e.Revision)
}

// Receiver consumes a configuration stream and maintains the committed
// configuration state. It is safe for concurrent use.
type Receiver struct {
	mu sync.Mutex

	state    map[string]string
	revision uint64

	// staged holds not-yet-committed chunks: revision -> seq -> changes.
	staged map[uint64]map[int][]Change
	// commits holds received commit markers: revision -> chunk count.
	commits map[uint64]int
	// digests records the change-set digest of every committed
	// revision so replays can be validated instead of re-applied.
	digests map[uint64][32]byte
}

// NewReceiver returns an empty Receiver at revision 0.
func NewReceiver() *Receiver {
	return &Receiver{
		state:   make(map[string]string),
		staged:  make(map[uint64]map[int][]Change),
		commits: make(map[uint64]int),
		digests: make(map[uint64][32]byte),
	}
}

// Revision returns the currently committed revision.
func (r *Receiver) Revision() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.revision
}

// State returns a copy of the currently committed configuration.
func (r *Receiver) State() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneMap(r.state)
}

// ApplyChunk stages one sequenced chunk of changes for a revision. If
// the commit marker for the revision has already arrived and the chunk
// completes the sequence, the revision is finalized immediately.
func (r *Receiver) ApplyChunk(rev uint64, seq int, changes []Change) error {
	if seq < 0 {
		return fmt.Errorf("configstream: negative sequence number %d", seq)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	chunks, ok := r.staged[rev]
	if !ok {
		chunks = make(map[int][]Change)
		r.staged[rev] = chunks
	}
	chunks[seq] = append([]Change(nil), changes...)
	return r.finalizeLocked(rev)
}

// Commit records the commit marker for a revision. chunks is the total
// number of chunks (sequence numbers 0..chunks-1) the revision consists
// of. The revision is finalized once every chunk is staged; until then
// the receiver waits and nothing is applied.
func (r *Receiver) Commit(rev uint64, chunks int) error {
	if chunks <= 0 {
		return fmt.Errorf("configstream: commit for revision %d declares %d chunks", rev, chunks)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commits[rev] = chunks
	return r.finalizeLocked(rev)
}

// ApplySnapshot atomically replaces the whole configuration with a
// snapshot taken at revision rev. It is a resynchronization point:
// staging for revisions at or below rev is discarded. Stale snapshots
// (rev <= current revision) are ignored.
func (r *Receiver) ApplySnapshot(rev uint64, snapshot map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rev <= r.revision {
		return nil
	}
	r.state = cloneMap(snapshot)
	r.revision = rev
	for stagedRev := range r.staged {
		if stagedRev <= rev {
			delete(r.staged, stagedRev)
		}
	}
	for commitRev := range r.commits {
		if commitRev <= rev {
			delete(r.commits, commitRev)
		}
	}
	return nil
}

// Disconnect drops all uncommitted staging and pending commit markers.
// The committed state and revision are left untouched.
func (r *Receiver) Disconnect() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.staged = make(map[uint64]map[int][]Change)
	r.commits = make(map[uint64]int)
}

// finalizeLocked applies or validates a revision once its commit marker
// and all of its chunks are present. Otherwise it returns nil and the
// receiver keeps waiting.
func (r *Receiver) finalizeLocked(rev uint64) error {
	chunks, committed := r.commits[rev]
	if !committed {
		return nil
	}
	if rev <= r.revision {
		// Replayed old revision: validate consistency, never re-apply.
		want, ok := r.digests[rev]
		if !ok {
			delete(r.staged, rev)
			delete(r.commits, rev)
			return &UnknownRevisionError{Revision: rev}
		}
		staged := r.staged[rev]
		for seq := 0; seq < chunks; seq++ {
			if _, ok := staged[seq]; !ok {
				// Wait for the replayed chunks to complete.
				return nil
			}
		}
		var changes []Change
		for seq := 0; seq < chunks; seq++ {
			changes = append(changes, staged[seq]...)
		}
		delete(r.staged, rev)
		delete(r.commits, rev)
		if want != digestChanges(changes) {
			return &InconsistentReplayError{Revision: rev}
		}
		return nil
	}
	staged := r.staged[rev]
	for seq := 0; seq < chunks; seq++ {
		if _, ok := staged[seq]; !ok {
			// Missing sequence number: wait, never apply partially.
			return nil
		}
	}
	var changes []Change
	for seq := 0; seq < chunks; seq++ {
		changes = append(changes, staged[seq]...)
	}
	digest := digestChanges(changes)
	delete(r.staged, rev)
	delete(r.commits, rev)
	for _, c := range changes {
		if c.Op == OpDelete {
			delete(r.state, c.Key)
		} else {
			r.state[c.Key] = c.Value
		}
	}
	r.revision = rev
	r.digests[rev] = digest
	// Anything staged at or below the new revision is superseded.
	for stagedRev := range r.staged {
		if stagedRev <= rev {
			delete(r.staged, stagedRev)
		}
	}
	for commitRev := range r.commits {
		if commitRev <= rev {
			delete(r.commits, commitRev)
		}
	}
	return nil
}

// digestChanges computes a canonical digest of a change set that is
// independent of how the changes were split into chunks.
func digestChanges(changes []Change) [32]byte {
	sorted := append([]Change(nil), changes...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Key != sorted[j].Key {
			return sorted[i].Key < sorted[j].Key
		}
		if sorted[i].Op != sorted[j].Op {
			return sorted[i].Op < sorted[j].Op
		}
		return sorted[i].Value < sorted[j].Value
	})
	h := sha256.New()
	var buf [8]byte
	writeString := func(s string) {
		binary.BigEndian.PutUint64(buf[:], uint64(len(s)))
		h.Write(buf[:])
		h.Write([]byte(s))
	}
	for _, c := range sorted {
		binary.BigEndian.PutUint64(buf[:], uint64(c.Op))
		h.Write(buf[:])
		writeString(c.Key)
		writeString(c.Value)
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

func cloneMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// ApplyChanges is the snapshot-model reference: it applies a complete
// change set to a state map in one shot and returns the result. It is
// used to cross-check that streamed, chunked application of a revision
// (including deletes and adds in the same revision) matches the
// snapshot model.
func ApplyChanges(state map[string]string, changes []Change) map[string]string {
	out := cloneMap(state)
	for _, c := range changes {
		if c.Op == OpDelete {
			delete(out, c.Key)
		} else {
			out[c.Key] = c.Value
		}
	}
	return out
}
