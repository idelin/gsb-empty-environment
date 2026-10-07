package statedb

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

// Sentinel error kinds returned inside Error.Kind.
var (
	// ErrGap: the delta's sequence number is not the next expected one.
	ErrGap = errors.New("statedb: delta sequence gap")
	// ErrBaseMismatch: the delta's base digest does not match the head,
	// i.e. a broken chain or a delta from another branch.
	ErrBaseMismatch = errors.New("statedb: base digest mismatch")
	// ErrDigestMismatch: replaying the ops does not reproduce the
	// delta's claimed post-state digest (corrupted delta).
	ErrDigestMismatch = errors.New("statedb: post-state digest mismatch")
	// ErrConflict: a different delta is already recorded at this
	// sequence number (branch delta replayed onto the wrong baseline).
	ErrConflict = errors.New("statedb: conflicting delta at sequence")
	// ErrBadSnapshot: the snapshot's digest does not match its content.
	ErrBadSnapshot = errors.New("statedb: snapshot digest mismatch")
)

// Error is a rejection with a machine-checkable Kind and context detail.
type Error struct {
	Kind error
	Msg  string
}

func (e *Error) Error() string { return e.Kind.Error() + ": " + e.Msg }

// KindOf extracts the sentinel kind from an error, or nil if err is nil.
func KindOf(err error) error {
	if err == nil {
		return nil
	}
	if e, ok := err.(*Error); ok {
		return e.Kind
	}
	return err
}

func reject(kind error, format string, args ...interface{}) error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

// Delta is an ordered batch of ops linking two state digests.
type Delta struct {
	Seq        uint64 // 1-based position in the chain
	BaseDigest Digest // digest of the state the ops apply to
	Digest     Digest // digest of the state the ops produce
	Ops        []Op
}

// id is a content hash used to detect conflicting deltas at the same seq.
func (d Delta) id() Digest {
	h := sha256.New()
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], d.Seq)
	h.Write(buf[:])
	h.Write(d.BaseDigest[:])
	h.Write(d.Digest[:])
	for _, op := range d.Ops {
		h.Write([]byte{byte(op.Kind)})
		h.Write([]byte(op.Key))
		h.Write([]byte{0})
		binary.BigEndian.PutUint64(buf[:], uint64(op.Val))
		h.Write(buf[:])
	}
	var id Digest
	copy(id[:], h.Sum(nil))
	return id
}

// Snapshot captures a state at a given chain position.
type Snapshot struct {
	Seq    uint64
	Digest Digest
	State  *State
}

// Store is a state chain: a head state plus the deltas that produced it.
type Store struct {
	seq     uint64
	head    Digest
	state   *State
	applied map[uint64]Digest // seq -> delta id, for idempotent replay
}

// NewStore returns an empty store at sequence 0.
func NewStore() *Store {
	return &Store{
		head:    ZeroDigest,
		state:   NewState(),
		applied: make(map[uint64]Digest),
	}
}

// Seq returns the sequence number of the last applied delta.
func (s *Store) Seq() uint64 { return s.seq }

// Head returns the digest of the current state.
func (s *Store) Head() Digest { return s.head }

// State returns a copy of the current state.
func (s *Store) State() *State { return s.state.Clone() }

// Snapshot captures the current state.
func (s *Store) Snapshot() Snapshot {
	return Snapshot{Seq: s.seq, Digest: s.head, State: s.state.Clone()}
}

// Apply commits a delta. Re-applying a delta that is already recorded is
// an idempotent no-op. Any inconsistency (gap, wrong base, corrupted
// content, conflicting branch) is rejected and leaves the store unchanged.
func (s *Store) Apply(d Delta) error {
	if d.Seq <= s.seq {
		if id, ok := s.applied[d.Seq]; ok && id == d.id() {
			return nil // already applied: idempotent no-op
		}
		return reject(ErrConflict, "seq %d", d.Seq)
	}
	if d.Seq != s.seq+1 {
		return reject(ErrGap, "have %d, got %d", s.seq, d.Seq)
	}
	if d.BaseDigest != s.head {
		return reject(ErrBaseMismatch, "seq %d base %s, head %s",
			d.Seq, d.BaseDigest, s.head)
	}
	next := s.state.Clone()
	for _, op := range d.Ops {
		next.apply(op)
	}
	if got := DigestOf(next); got != d.Digest {
		return reject(ErrDigestMismatch, "seq %d declares %s, replay gives %s",
			d.Seq, d.Digest, got)
	}
	s.state = next
	s.head = d.Digest
	s.seq = d.Seq
	s.applied[d.Seq] = d.id()
	return nil
}

// Restore rebuilds a store from a snapshot plus the deltas that follow
// it. Restoration is atomic: on any error it returns nil and no
// partially-applied state is observable.
func Restore(snap Snapshot, deltas []Delta) (*Store, error) {
	if got := DigestOf(snap.State); got != snap.Digest {
		return nil, reject(ErrBadSnapshot, "declares %s, content gives %s",
			snap.Digest, got)
	}
	s := &Store{
		seq:     snap.Seq,
		head:    snap.Digest,
		state:   snap.State.Clone(),
		applied: make(map[uint64]Digest),
	}
	for _, d := range deltas {
		if err := s.Apply(d); err != nil {
			return nil, err
		}
	}
	return s, nil
}
