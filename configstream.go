// Package configstream implements the receiving side of a chunked
// configuration update stream.
//
// A sender emits the changes of one revision as an ordered series of
// chunk Events, terminated by a commit-marker Event. The receiver stages
// every chunk and only applies the staged changes to its committed
// snapshot when the commit marker arrives, so each revision is applied
// atomically. Chunks that arrive out of order are buffered until the
// missing sequence numbers show up; nothing is applied partially.
//
// Re-sending an already committed revision is allowed (e.g. after a
// reconnect): the receiver validates that the replayed changes digest
// matches what was originally committed and reports a conflict
// otherwise. Calling Disconnect drops all uncommitted staged state.
package configstream

import (
	"encoding/binary"
	"errors"
	"hash/fnv"
	"sync"
)

// ChangeType identifies the kind of a single configuration mutation.
type ChangeType byte

const (
	// ChangeUpsert inserts a key or replaces its value.
	ChangeUpsert ChangeType = iota
	// ChangeDelete removes a key. Value is ignored.
	ChangeDelete
)

// Change is a single mutation inside a revision.
type Change struct {
	Type  ChangeType
	Key   string
	Value string
}

// Event is one chunk on the wire. Seq numbers chunks monotonically
// within a stream session, starting at 0 and restarting after a
// reconnect. The chunk carrying Commit=true is the commit marker: it
// terminates its revision and triggers the atomic apply.
type Event struct {
	Revision uint64
	Seq      uint64
	Changes  []Change
	Commit   bool
}

var (
	// ErrSeqRegression is returned when a chunk carries a sequence
	// number that was already consumed in this session.
	ErrSeqRegression = errors.New("configstream: sequence number already consumed")
	// ErrRevisionInterleaved is returned when a chunk for a different
	// revision arrives while another revision is still uncommitted.
	ErrRevisionInterleaved = errors.New("configstream: chunk interleaves revisions before commit")
	// ErrInconsistentReplay is returned when a re-sent, already
	// committed revision does not match what was originally applied.
	ErrInconsistentReplay = errors.New("configstream: replayed revision conflicts with committed state")
	// ErrInvalidChange is returned when a change carries an unknown type.
	ErrInvalidChange = errors.New("configstream: unknown change type")
)

type stagingState struct {
	active   bool
	revision uint64
	changes  []Change
}

// Stream receives events for one stream session and maintains the
// committed configuration snapshot.
type Stream struct {
	mu        sync.Mutex
	committed map[string]string
	revision  uint64
	digests   map[uint64]uint64 // committed revision -> digest of its changes

	staging stagingState
	nextSeq uint64
	pending map[uint64]Event // out-of-order chunks waiting for missing seqs
}

// NewStream returns an empty stream receiver at revision 0.
func NewStream() *Stream {
	return &Stream{
		committed: make(map[string]string),
		digests:   make(map[uint64]uint64),
		pending:   make(map[uint64]Event),
	}
}

// Handle consumes one event. Out-of-order chunks are buffered and Handle
// returns nil; buffered chunks are drained in order once the missing
// sequence numbers arrive.
func (s *Stream) Handle(ev Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if ev.Seq < s.nextSeq {
		return ErrSeqRegression
	}
	if ev.Seq > s.nextSeq {
		if _, dup := s.pending[ev.Seq]; dup {
			return ErrSeqRegression
		}
		s.pending[ev.Seq] = ev
		return nil
	}
	if err := s.processLocked(ev); err != nil {
		return err
	}
	for {
		next, ok := s.pending[s.nextSeq]
		if !ok {
			return nil
		}
		delete(s.pending, s.nextSeq)
		if err := s.processLocked(next); err != nil {
			return err
		}
	}
}

func (s *Stream) processLocked(ev Event) error {
	s.nextSeq = ev.Seq + 1

	if !s.staging.active {
		s.staging.active = true
		s.staging.revision = ev.Revision
	} else if ev.Revision != s.staging.revision {
		return ErrRevisionInterleaved
	}
	for _, c := range ev.Changes {
		if c.Type != ChangeUpsert && c.Type != ChangeDelete {
			return ErrInvalidChange
		}
	}
	s.staging.changes = append(s.staging.changes, ev.Changes...)

	if !ev.Commit {
		return nil
	}
	defer func() { s.staging = stagingState{} }()

	digest := digestChanges(ev.Revision, s.staging.changes)
	if ev.Revision > s.revision {
		applyChanges(s.committed, s.staging.changes)
		s.revision = ev.Revision
		s.digests[ev.Revision] = digest
		return nil
	}
	// Replayed old revision: only a byte-identical repeat of what was
	// committed is acceptable; anything else is a conflict.
	recorded, ok := s.digests[ev.Revision]
	if !ok || recorded != digest {
		return ErrInconsistentReplay
	}
	return nil
}

func applyChanges(state map[string]string, changes []Change) {
	for _, c := range changes {
		if c.Type == ChangeDelete {
			delete(state, c.Key)
		} else {
			state[c.Key] = c.Value
		}
	}
}

// Disconnect drops every uncommitted staged change and all buffered
// out-of-order chunks, and resets the sequence expectation so the
// sender can restart the session from sequence 0.
func (s *Stream) Disconnect() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.staging = stagingState{}
	s.pending = make(map[uint64]Event)
	s.nextSeq = 0
}

// Snapshot returns a copy of the committed configuration and the
// revision it belongs to.
func (s *Stream) Snapshot() (map[string]string, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.committed))
	for k, v := range s.committed {
		out[k] = v
	}
	return out, s.revision
}

// Pending reports how many chunks are currently buffered waiting for
// missing sequence numbers.
func (s *Stream) Pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending)
}

func digestChanges(revision uint64, changes []Change) uint64 {
	h := fnv.New64a()
	var num [8]byte
	binary.BigEndian.PutUint64(num[:], revision)
	h.Write(num[:])
	for _, c := range changes {
		h.Write([]byte{byte(c.Type)})
		writeString(h, c.Key)
		writeString(h, c.Value)
	}
	return h.Sum64()
}

func writeString(h interface{ Write([]byte) (int, error) }, s string) {
	var num [4]byte
	binary.BigEndian.PutUint32(num[:], uint32(len(s)))
	h.Write(num[:])
	h.Write([]byte(s))
}
