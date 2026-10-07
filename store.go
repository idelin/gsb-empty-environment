// Package statestore implements a digest-chained state library.
//
// State is recovered by combining a snapshot with a chain of operation
// deltas. Every delta carries the digest of the state it applies to
// (PreDigest) and the digest of the state it produces (PostDigest):
//
//   - Re-applying an already applied delta is an idempotent no-op.
//   - A delta whose PreDigest/Seq does not match the current head is
//     rejected (broken chain), so a delta from one branch can never be
//     attached to the baseline of another.
//   - A delta whose ops do not reproduce PostDigest is rejected as
//     corrupt, and recovery is atomic: a failed Restore leaves the
//     store untouched.
package statestore

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"sort"
	"sync"
)

// Digest is the content hash of a state at a given sequence number.
type Digest [sha256.Size]byte

// OpKind identifies the mutation performed by an Op.
type OpKind uint8

const (
	OpSet OpKind = iota // set Key to Value
	OpAdd               // add Value to Key
	OpDel               // delete Key
)

// Op is a single deterministic state mutation.
type Op struct {
	Kind  OpKind
	Key   string
	Value int64
}

// Delta transitions the state from Seq to Seq+1.
type Delta struct {
	Seq        uint64
	PreDigest  Digest // digest of the state this delta applies to
	PostDigest Digest // digest of the state this delta produces
	Ops        []Op
}

// Snapshot captures the full state at a sequence number.
type Snapshot struct {
	Seq    uint64
	Digest Digest
	State  map[string]int64
}

var (
	// ErrChainBroken is returned when a delta does not extend the
	// current chain head (wrong sequence, wrong baseline digest, or a
	// conflicting delta for an already committed position).
	ErrChainBroken = errors.New("statestore: delta does not extend the current chain head")
	// ErrDigestMismatch is returned when replaying a delta's ops does
	// not reproduce its PostDigest (corrupt delta).
	ErrDigestMismatch = errors.New("statestore: post-state digest mismatch")
	// ErrBadSnapshot is returned when a snapshot's digest does not
	// match the state it carries.
	ErrBadSnapshot = errors.New("statestore: snapshot digest does not match its state")
)

// digestOf computes the canonical digest of a state at a sequence number.
func digestOf(seq uint64, state map[string]int64) Digest {
	h := sha256.New()
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], seq)
	h.Write(buf[:])
	keys := make([]string, 0, len(state))
	for k := range state {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0})
		binary.BigEndian.PutUint64(buf[:], uint64(state[k]))
		h.Write(buf[:])
	}
	var d Digest
	copy(d[:], h.Sum(nil))
	return d
}

func cloneState(state map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(state))
	for k, v := range state {
		out[k] = v
	}
	return out
}

func applyOp(state map[string]int64, op Op) {
	switch op.Kind {
	case OpSet:
		state[op.Key] = op.Value
	case OpAdd:
		state[op.Key] += op.Value
	case OpDel:
		delete(state, op.Key)
	}
}

// Store is a digest-chained state store. It is safe for concurrent use.
type Store struct {
	mu      sync.Mutex
	state   map[string]int64
	seq     uint64
	digest  Digest
	history map[uint64]Digest // seq -> head digest at that seq, for idempotent replay
}

// NewStore returns an empty store at sequence 0.
func NewStore() *Store {
	state := make(map[string]int64)
	d := digestOf(0, state)
	return &Store{
		state:   state,
		digest:  d,
		history: map[uint64]Digest{0: d},
	}
}

// Seq returns the current sequence number.
func (s *Store) Seq() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seq
}

// Digest returns the digest of the current head state.
func (s *Store) Digest() Digest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.digest
}

// Get returns the value stored under key.
func (s *Store) Get(key string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.state[key]
	return v, ok
}

// Snapshot returns a deep copy of the current state, its sequence
// number and its digest.
func (s *Store) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Snapshot{Seq: s.seq, Digest: s.digest, State: cloneState(s.state)}
}

// NewDelta builds the delta that applies ops on top of the current head.
func (s *Store) NewDelta(ops []Op) Delta {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneState(s.state)
	for _, op := range ops {
		applyOp(next, op)
	}
	cp := make([]Op, len(ops))
	copy(cp, ops)
	return Delta{
		Seq:        s.seq,
		PreDigest:  s.digest,
		PostDigest: digestOf(s.seq+1, next),
		Ops:        cp,
	}
}

// Apply applies d to the store. It reports whether the delta was
// actually applied; re-applying an already applied delta is an
// idempotent no-op and reports applied == false.
func (s *Store) Apply(d Delta) (applied bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d.Seq < s.seq {
		// Already committed position: accept only the exact same
		// delta as an idempotent replay; anything else conflicts
		// with the committed chain.
		pre, okPre := s.history[d.Seq]
		post, okPost := s.history[d.Seq+1]
		if okPre && okPost && pre == d.PreDigest && post == d.PostDigest {
			return false, nil
		}
		return false, ErrChainBroken
	}
	if d.Seq > s.seq || d.PreDigest != s.digest {
		return false, ErrChainBroken
	}
	next := cloneState(s.state)
	for _, op := range d.Ops {
		applyOp(next, op)
	}
	post := digestOf(s.seq+1, next)
	if post != d.PostDigest {
		return false, ErrDigestMismatch
	}
	s.state = next
	s.seq++
	s.digest = post
	s.history[s.seq] = post
	return true, nil
}

// Restore atomically replaces the store with the state recovered from
// snap followed by deltas. On any error the store is left untouched.
func (s *Store) Restore(snap Snapshot, deltas []Delta) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if digestOf(snap.Seq, snap.State) != snap.Digest {
		return ErrBadSnapshot
	}
	state := cloneState(snap.State)
	seq := snap.Seq
	digest := snap.Digest
	history := map[uint64]Digest{seq: digest}
	for _, d := range deltas {
		if d.Seq != seq || d.PreDigest != digest {
			return ErrChainBroken
		}
		next := cloneState(state)
		for _, op := range d.Ops {
			applyOp(next, op)
		}
		post := digestOf(seq+1, next)
		if post != d.PostDigest {
			return ErrDigestMismatch
		}
		state = next
		seq++
		digest = post
		history[seq] = post
	}
	s.state = state
	s.seq = seq
	s.digest = digest
	s.history = history
	return nil
}
